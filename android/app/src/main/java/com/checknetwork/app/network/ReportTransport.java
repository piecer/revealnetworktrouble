package com.checknetwork.app.network;

import com.checknetwork.app.core.ApiError;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.ContractLimits;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportParseException;
import com.checknetwork.app.core.ReportParser;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.core.TargetInput;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.SocketTimeoutException;
import java.net.URL;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Objects;

import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

/** Bounded synchronous report transport. A Call may be executed once and cancelled from another thread. */
public final class ReportTransport {
    public static final int MAX_ERROR_BODY_BYTES = 64 * 1024;
    public static final long DEADLINE_GRACE_MILLIS = 15_000L;
    public static final long MAX_DEADLINE_MILLIS = 315_000L;

    private static final ScheduledExecutorService DEFAULT_SCHEDULER = Executors.newSingleThreadScheduledExecutor(new DaemonFactory());

    public interface ConnectionFactory { HttpURLConnection open(URL url) throws IOException; }
    public interface Clock extends OperationDeadline.Clock { Instant now(); }
    public interface Scheduler { Scheduled schedule(Runnable task, long delayMillis); }
    public interface Scheduled { void cancel(); }

    private final ApiConnectionConfig config;
    private final ConnectionFactory connections;
    private final Clock clock;
    private final Scheduler scheduler;
    private final OperationDeadline operationDeadline;

    public ReportTransport(ApiConnectionConfig config) {
        this(config, url -> (HttpURLConnection) url.openConnection(),
                new Clock() {
                    @Override public long nanoTime() { return System.nanoTime(); }
                    @Override public Instant now() { return Instant.now(); }
                },
                (task, delay) -> {
                    java.util.concurrent.ScheduledFuture<?> future = DEFAULT_SCHEDULER.schedule(task, delay, TimeUnit.MILLISECONDS);
                    return () -> future.cancel(false);
                });
    }

    public ReportTransport(ApiConnectionConfig config, Clock clock,
            OperationDeadline operationDeadline) {
        this(config, url -> (HttpURLConnection) url.openConnection(), clock,
                (task, delay) -> {
                    java.util.concurrent.ScheduledFuture<?> future =
                            DEFAULT_SCHEDULER.schedule(task, delay, TimeUnit.MILLISECONDS);
                    return () -> future.cancel(false);
                }, operationDeadline);
    }

    public ReportTransport(ApiConnectionConfig config, ConnectionFactory connections, Clock clock, Scheduler scheduler) {
        this(config, connections, clock, scheduler, null, true);
    }

    public ReportTransport(ApiConnectionConfig config, ConnectionFactory connections, Clock clock,
            Scheduler scheduler, OperationDeadline operationDeadline) {
        this(config, connections, clock, scheduler,
                Objects.requireNonNull(operationDeadline, "operationDeadline"), false);
    }

    private ReportTransport(ApiConnectionConfig config, ConnectionFactory connections, Clock clock,
            Scheduler scheduler, OperationDeadline operationDeadline, boolean standalone) {
        this.config = Objects.requireNonNull(config, "config");
        this.connections = Objects.requireNonNull(connections, "connections");
        this.clock = Objects.requireNonNull(clock, "clock");
        this.scheduler = Objects.requireNonNull(scheduler, "scheduler");
        this.operationDeadline = operationDeadline;
        if (!standalone && operationDeadline.clock() != clock)
            throw new IllegalArgumentException("operation deadline must use the transport clock");
    }

    public Call newCall(ReportRequest request) {
        return new Call(Objects.requireNonNull(request, "request"));
    }

    /** Parallel server work is bounded by the longest target, with traceroute attempts multiplying its timeout. */
    public static long deadlineMillis(ReportRequest request) {
        Objects.requireNonNull(request, "request");
        long longest = request.timeoutMs();
        int visited = 0;
        for (TargetInput target : request.targets()) {
            if (++visited > ContractLimits.MAX_TARGETS) return MAX_DEADLINE_MILLIS;
            if (target.kind() != CheckKind.TRACEROUTE) continue;
            int attempts = target.attempts() == null ? ContractLimits.DEFAULT_TRACEROUTE_ATTEMPTS : target.attempts();
            try {
                longest = Math.max(longest, Math.multiplyExact((long) request.timeoutMs(), (long) attempts));
            } catch (ArithmeticException overflow) {
                return MAX_DEADLINE_MILLIS;
            }
        }
        try {
            return Math.min(MAX_DEADLINE_MILLIS, Math.addExact(longest, DEADLINE_GRACE_MILLIS));
        } catch (ArithmeticException overflow) {
            return MAX_DEADLINE_MILLIS;
        }
    }

    /** Returns the unspent, capped deadline without extending it when a monotonic clock regresses. */
    static long boundedDeadlineMillis(long startedNanos, long budgetMillis, long nowNanos) {
        long boundedBudget = Math.max(0L, Math.min(MAX_DEADLINE_MILLIS, budgetMillis));
        if (boundedBudget == 0L || nowNanos == startedNanos) return boundedBudget;
        long elapsedNanos = nowNanos - startedNanos;
        if (elapsedNanos < 0L) return nowNanos < startedNanos ? boundedBudget : 0L;
        long budgetNanos = TimeUnit.MILLISECONDS.toNanos(boundedBudget);
        if (elapsedNanos >= budgetNanos) return 0L;
        long remainingNanos = budgetNanos - elapsedNanos;
        return Math.min(boundedBudget, 1L + (remainingNanos - 1L) / 1_000_000L);
    }

    static int toSocketTimeoutMillis(long remainingMillis) {
        return (int) Math.max(1L, Math.min(Math.min(MAX_DEADLINE_MILLIS, Integer.MAX_VALUE), remainingMillis));
    }

    public final class Call {
        private enum Terminal { ACTIVE, SUCCESS, TIMEOUT, CANCELLED }

        private final ReportRequest request;
        private final AtomicReference<Terminal> terminal = new AtomicReference<>(Terminal.ACTIVE);
        private final AtomicReference<HttpURLConnection> connection = new AtomicReference<>();
        private final AtomicBoolean disconnected = new AtomicBoolean();
        private final AtomicBoolean started = new AtomicBoolean();
        private final AtomicReference<Long> remainingCeilingMillis = new AtomicReference<>(MAX_DEADLINE_MILLIS);

        private Call(ReportRequest request) { this.request = request; }

        public void cancel() { stop(Terminal.CANCELLED); }

        public Response execute() throws TransportException {
            if (!started.compareAndSet(false, true)) throw failure(TransportException.Kind.INVALID_RESPONSE);
            long startedNanos = clock.nanoTime();
            long budgetMillis = deadlineMillis(request);
            throwIfTerminal();
            remainingDeadlineOrThrow(startedNanos, budgetMillis);
            byte[] requestBytes = request.toJson().getBytes(StandardCharsets.UTF_8);
            if (requestBytes.length > ContractLimits.MAX_TRANSPORT_BYTES) throw failure(TransportException.Kind.RESPONSE_TOO_LARGE);
            throwIfTerminal();

            Scheduled deadline = null;
            try {
                remainingDeadlineOrThrow(startedNanos, budgetMillis);
                HttpURLConnection opened = connections.open(config.reportsEndpoint().toURL());
                connection.set(opened);
                if (terminal.get() != Terminal.ACTIVE) disconnectOnce();
                long remainingMillis = remainingDeadlineOrThrow(startedNanos, budgetMillis);
                deadline = scheduler.schedule(() -> stop(Terminal.TIMEOUT), remainingMillis);
                throwIfTerminal();
                int socketTimeoutMillis = toSocketTimeoutMillis(remainingDeadlineOrThrow(startedNanos, budgetMillis));
                configure(opened, requestBytes.length, socketTimeoutMillis);
                remainingDeadlineOrThrow(startedNanos, budgetMillis);
                OutputStream output = opened.getOutputStream();
                remainingDeadlineOrThrow(startedNanos, budgetMillis);
                output.write(requestBytes);
                remainingDeadlineOrThrow(startedNanos, budgetMillis);
                output.close();
                throwIfStoppedOrExpired(startedNanos, budgetMillis);

                remainingDeadlineOrThrow(startedNanos, budgetMillis);
                int status = opened.getResponseCode();
                throwIfStoppedOrExpired(startedNanos, budgetMillis);
                if (status < 200 || status >= 300) {
                    String body = readErrorBody(opened, startedNanos, budgetMillis);
                    remainingDeadlineOrThrow(startedNanos, budgetMillis);
                    String retryAfter;
                    try {
                        remainingDeadlineOrThrow(startedNanos, budgetMillis);
                        retryAfter = opened.getHeaderField("Retry-After");
                    }
                    catch (RuntimeException ignored) { retryAfter = null; }
                    remainingDeadlineOrThrow(startedNanos, budgetMillis);
                    Instant now = clock.now();
                    remainingDeadlineOrThrow(startedNanos, budgetMillis);
                    ApiError error = ApiError.parse(status, body, retryAfter, now);
                    remainingDeadlineOrThrow(startedNanos, budgetMillis);
                    throw new TransportException(error);
                }
                byte[] responseBytes = readSuccessBody(opened, startedNanos, budgetMillis);
                remainingDeadlineOrThrow(startedNanos, budgetMillis);
                String raw = decodeUtf8(responseBytes);
                remainingDeadlineOrThrow(startedNanos, budgetMillis);
                final Report report;
                try { report = ReportParser.parse(raw); }
                catch (RuntimeException malformed) { throw failure(TransportException.Kind.INVALID_RESPONSE); }
                throwIfStoppedOrExpired(startedNanos, budgetMillis);
                if (!terminal.compareAndSet(Terminal.ACTIVE, Terminal.SUCCESS)) throwIfTerminal();
                return new Response(raw, report);
            } catch (TransportException expected) {
                throw winnerOr(expected, startedNanos, budgetMillis);
            } catch (SocketTimeoutException timeout) {
                throw winnerOr(failure(TransportException.Kind.TIMEOUT), startedNanos, budgetMillis);
            } catch (IOException network) {
                throw winnerOr(failure(TransportException.Kind.NETWORK), startedNanos, budgetMillis);
            } finally {
                safeCancelDeadline(deadline);
                disconnectOnce();
            }
        }

        private void configure(HttpURLConnection opened, int requestLength, int remainingMillis) throws IOException {
            opened.setConnectTimeout(remainingMillis);
            opened.setReadTimeout(remainingMillis);
            opened.setInstanceFollowRedirects(false);
            opened.setRequestMethod("POST");
            opened.setDoOutput(true);
            opened.setFixedLengthStreamingMode(requestLength);
            opened.setRequestProperty("Content-Type", "application/json; charset=utf-8");
            opened.setRequestProperty("Accept", "application/json");
            config.authorizationHeader().ifPresent(value -> opened.setRequestProperty("Authorization", value));
        }

        private byte[] readSuccessBody(HttpURLConnection opened, long start, long budget) throws IOException, TransportException {
            remainingDeadlineOrThrow(start, budget);
            long length = opened.getContentLengthLong();
            remainingDeadlineOrThrow(start, budget);
            if (length > ContractLimits.MAX_TRANSPORT_BYTES) throw failure(TransportException.Kind.RESPONSE_TOO_LARGE);
            remainingDeadlineOrThrow(start, budget);
            InputStream input = opened.getInputStream();
            remainingDeadlineOrThrow(start, budget);
            byte[] bytes = readBounded(input, ContractLimits.MAX_TRANSPORT_BYTES, true, start, budget);
            remainingDeadlineOrThrow(start, budget);
            closeInput(input);
            remainingDeadlineOrThrow(start, budget);
            return bytes;
        }

        private String readErrorBody(HttpURLConnection opened, long start, long budget) throws TransportException {
            try {
                remainingDeadlineOrThrow(start, budget);
                long length = opened.getContentLengthLong();
                remainingDeadlineOrThrow(start, budget);
                if (length > MAX_ERROR_BODY_BYTES) return "";
                remainingDeadlineOrThrow(start, budget);
                InputStream input = opened.getErrorStream();
                remainingDeadlineOrThrow(start, budget);
                if (input == null) return "";
                byte[] bytes = readBounded(input, MAX_ERROR_BODY_BYTES, false, start, budget);
                remainingDeadlineOrThrow(start, budget);
                closeInput(input);
                remainingDeadlineOrThrow(start, budget);
                if (bytes == null) return "";
                String body = decodeUtf8OrEmpty(bytes);
                remainingDeadlineOrThrow(start, budget);
                return body;
            } catch (TransportException stopped) {
                throw stopped;
            } catch (IOException | RuntimeException unavailable) {
                return "";
            }
        }

        private byte[] readBounded(InputStream input, int limit, boolean rejectOverflow, long start, long budget)
                throws IOException, TransportException {
            ByteArrayOutputStream output = new ByteArrayOutputStream(Math.min(limit, 16 * 1024));
            byte[] buffer = new byte[16 * 1024];
            int total = 0;
            while (true) {
                throwIfStoppedOrExpired(start, budget);
                int count = input.read(buffer, 0, Math.min(buffer.length, limit - total + 1));
                throwIfStoppedOrExpired(start, budget);
                if (count < 0) break;
                if (count == 0) continue;
                total = Math.addExact(total, count);
                if (total > limit) {
                    if (rejectOverflow) throw failure(TransportException.Kind.RESPONSE_TOO_LARGE);
                    return null;
                }
                output.write(buffer, 0, count);
            }
            throwIfStoppedOrExpired(start, budget);
            return output.toByteArray();
        }

        private long remainingDeadlineOrThrow(long start, long budgetMillis) throws TransportException {
            long measured = boundedDeadlineMillis(start, budgetMillis, clock.nanoTime());
            final long effective = operationDeadline == null ? measured
                    : Math.min(measured, operationDeadline.remainingMillis());
            long remaining = remainingCeilingMillis.updateAndGet(previous -> Math.min(previous, effective));
            if (remaining == 0L && terminal.compareAndSet(Terminal.ACTIVE, Terminal.TIMEOUT)) disconnectOnce();
            throwIfTerminal();
            return remaining;
        }

        private void throwIfStoppedOrExpired(long start, long budgetMillis) throws TransportException {
            remainingDeadlineOrThrow(start, budgetMillis);
        }

        private void throwIfTerminal() throws TransportException {
            Terminal state = terminal.get();
            if (state == Terminal.TIMEOUT) throw failure(TransportException.Kind.TIMEOUT);
            if (state == Terminal.CANCELLED) throw failure(TransportException.Kind.CANCELLED);
        }

        private TransportException winnerOr(TransportException fallback, long start, long budgetMillis) {
            long remaining = boundedDeadlineMillis(start, budgetMillis, clock.nanoTime());
            if (operationDeadline != null)
                remaining = Math.min(remaining, operationDeadline.remainingMillis());
            if (remaining == 0L && terminal.compareAndSet(Terminal.ACTIVE, Terminal.TIMEOUT)) disconnectOnce();
            Terminal state = terminal.get();
            if (state == Terminal.CANCELLED) return failure(TransportException.Kind.CANCELLED);
            if (state == Terminal.TIMEOUT) return failure(TransportException.Kind.TIMEOUT);
            return fallback;
        }

        private void stop(Terminal reason) {
            if (terminal.compareAndSet(Terminal.ACTIVE, reason)) disconnectOnce();
        }

        private void safeCancelDeadline(Scheduled deadline) {
            if (deadline == null) return;
            try { deadline.cancel(); }
            catch (RuntimeException ignored) { }
        }

        private void closeInput(InputStream input) {
            if (input == null) return;
            try { input.close(); }
            catch (IOException | RuntimeException ignored) { }
        }

        private void disconnectOnce() {
            HttpURLConnection current = connection.get();
            if (current == null || !disconnected.compareAndSet(false, true)) return;
            try { current.disconnect(); }
            catch (RuntimeException ignored) { }
        }
    }

    private static String decodeUtf8(byte[] bytes) throws TransportException {
        try {
            return StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(bytes)).toString();
        } catch (CharacterCodingException invalid) {
            throw failure(TransportException.Kind.INVALID_RESPONSE);
        }
    }

    private static String decodeUtf8OrEmpty(byte[] bytes) {
        try { return decodeUtf8(bytes); }
        catch (TransportException invalid) { return ""; }
    }

    private static TransportException failure(TransportException.Kind kind) {
        String message = switch (kind) {
            case NETWORK -> "The report request failed due to a network error.";
            case TIMEOUT -> "The report request exceeded its deadline.";
            case CANCELLED -> "The report request was cancelled.";
            case RESPONSE_TOO_LARGE -> "The report response exceeded the byte limit.";
            case INVALID_RESPONSE -> "The server returned an invalid response.";
            case API -> "The server rejected the report request.";
            case UNSUPPORTED_CAPABILITY -> throw new IllegalArgumentException(
                    "Report transport cannot create capability mismatch failures");
        };
        return new TransportException(kind, message);
    }

    public static final class Response {
        private final String rawJson;
        private final Report report;
        private Response(String rawJson, Report report) { this.rawJson = rawJson; this.report = report; }
        public String rawJson() { return rawJson; }
        public Report report() { return report; }
        @Override public String toString() { return "Response{reportId='" + report.id() + "', bytes=" + rawJson.getBytes(StandardCharsets.UTF_8).length + "}"; }
    }

    private static final class DaemonFactory implements ThreadFactory {
        @Override public Thread newThread(Runnable runnable) {
            Thread thread = new Thread(runnable, "report-transport-deadline");
            thread.setDaemon(true);
            return thread;
        }
    }
}
