package com.checknetwork.app.network;

import com.checknetwork.app.core.ApiError;
import com.checknetwork.app.core.CheckCapabilities;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.SocketTimeoutException;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.Objects;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

/** Bounded synchronous checks discovery transport. Calls are one-shot and externally cancellable. */
public final class CapabilitiesTransport {
    public static final int DEADLINE_MILLIS = 10_000;
    public static final int MAX_ERROR_BODY_BYTES = 64 * 1024;
    private static final ScheduledExecutorService DEFAULT_SCHEDULER =
            Executors.newSingleThreadScheduledExecutor(new DaemonFactory());

    private final ApiConnectionConfig config;
    private final ReportTransport.ConnectionFactory connections;
    private final ReportTransport.Clock clock;
    private final ReportTransport.Scheduler scheduler;

    public CapabilitiesTransport(ApiConnectionConfig config) {
        this(config, url -> (HttpURLConnection) url.openConnection(),
                new ReportTransport.Clock() {
                    @Override public long nanoTime() { return System.nanoTime(); }
                    @Override public java.time.Instant now() { return java.time.Instant.now(); }
                },
                (task, delay) -> {
                    java.util.concurrent.ScheduledFuture<?> future =
                            DEFAULT_SCHEDULER.schedule(task, delay, TimeUnit.MILLISECONDS);
                    return () -> future.cancel(false);
                });
    }

    public CapabilitiesTransport(ApiConnectionConfig config, ReportTransport.ConnectionFactory connections,
            ReportTransport.Clock clock, ReportTransport.Scheduler scheduler) {
        this.config = Objects.requireNonNull(config, "config");
        this.connections = Objects.requireNonNull(connections, "connections");
        this.clock = Objects.requireNonNull(clock, "clock");
        this.scheduler = Objects.requireNonNull(scheduler, "scheduler");
    }

    public Call newCall() { return new Call(); }

    public final class Call {
        private enum Terminal { ACTIVE, SUCCESS, TIMEOUT, CANCELLED }
        private final AtomicReference<Terminal> terminal = new AtomicReference<>(Terminal.ACTIVE);
        private final AtomicReference<HttpURLConnection> connection = new AtomicReference<>();
        private final AtomicBoolean disconnected = new AtomicBoolean();
        private final AtomicBoolean started = new AtomicBoolean();

        public void cancel() { stop(Terminal.CANCELLED); }

        public CheckCapabilities execute() throws TransportException {
            if (!started.compareAndSet(false, true)) throw failure(TransportException.Kind.INVALID_RESPONSE);
            throwIfTerminal();
            long startedNanos = clock.nanoTime();
            long budgetNanos = TimeUnit.MILLISECONDS.toNanos(DEADLINE_MILLIS);
            ReportTransport.Scheduled deadline = null;
            try {
                deadline = scheduler.schedule(() -> stop(Terminal.TIMEOUT), DEADLINE_MILLIS);
                HttpURLConnection opened = connections.open(config.checksEndpoint().toURL());
                connection.set(opened);
                if (terminal.get() != Terminal.ACTIVE) disconnectOnce();
                throwIfStoppedOrExpired(startedNanos, budgetNanos);
                configure(opened);
                int status = opened.getResponseCode();
                throwIfStoppedOrExpired(startedNanos, budgetNanos);
                if (status < 200 || status >= 300) {
                    String body = readErrorBody(opened, startedNanos, budgetNanos);
                    String retryAfter;
                    try { retryAfter = opened.getHeaderField("Retry-After"); }
                    catch (RuntimeException ignored) { retryAfter = null; }
                    throw new TransportException(ApiError.parse(status, body, retryAfter, clock.now()));
                }
                byte[] bytes = readSuccessBody(opened, startedNanos, budgetNanos);
                String json = decodeUtf8(bytes);
                final CheckCapabilities capabilities;
                try { capabilities = CheckCapabilities.parse(json); }
                catch (RuntimeException malformed) { throw failure(TransportException.Kind.INVALID_RESPONSE); }
                throwIfStoppedOrExpired(startedNanos, budgetNanos);
                if (!terminal.compareAndSet(Terminal.ACTIVE, Terminal.SUCCESS)) throwIfTerminal();
                return capabilities;
            } catch (TransportException expected) {
                throw winnerOr(expected);
            } catch (SocketTimeoutException timeout) {
                throw winnerOr(failure(TransportException.Kind.TIMEOUT));
            } catch (IOException network) {
                throw winnerOr(failure(TransportException.Kind.NETWORK));
            } finally {
                safeCancel(deadline);
                disconnectOnce();
            }
        }

        private void configure(HttpURLConnection opened) throws IOException {
            opened.setConnectTimeout(DEADLINE_MILLIS);
            opened.setReadTimeout(DEADLINE_MILLIS);
            opened.setInstanceFollowRedirects(false);
            opened.setRequestMethod("GET");
            opened.setDoOutput(false);
            opened.setRequestProperty("Accept", "application/json");
            config.authorizationHeader().ifPresent(value -> opened.setRequestProperty("Authorization", value));
        }

        private byte[] readSuccessBody(HttpURLConnection opened, long start, long budget)
                throws IOException, TransportException {
            if (opened.getContentLengthLong() > CheckCapabilities.MAX_JSON_BYTES)
                throw failure(TransportException.Kind.RESPONSE_TOO_LARGE);
            InputStream input = opened.getInputStream();
            try { return readBounded(input, CheckCapabilities.MAX_JSON_BYTES, true, start, budget); }
            finally { closeInput(input); }
        }

        private String readErrorBody(HttpURLConnection opened, long start, long budget) throws TransportException {
            InputStream input = null;
            try {
                if (opened.getContentLengthLong() > MAX_ERROR_BODY_BYTES) return "";
                input = opened.getErrorStream();
                if (input == null) return "";
                byte[] bytes = readBounded(input, MAX_ERROR_BODY_BYTES, false, start, budget);
                return bytes == null ? "" : decodeUtf8OrEmpty(bytes);
            } catch (TransportException stopped) {
                throw stopped;
            } catch (IOException | RuntimeException unavailable) {
                return "";
            } finally {
                closeInput(input);
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

        private void throwIfStoppedOrExpired(long start, long budget) throws TransportException {
            if (clock.nanoTime() - start >= budget
                    && terminal.compareAndSet(Terminal.ACTIVE, Terminal.TIMEOUT)) disconnectOnce();
            throwIfTerminal();
        }

        private void throwIfTerminal() throws TransportException {
            Terminal state = terminal.get();
            if (state == Terminal.TIMEOUT) throw failure(TransportException.Kind.TIMEOUT);
            if (state == Terminal.CANCELLED) throw failure(TransportException.Kind.CANCELLED);
        }

        private TransportException winnerOr(TransportException fallback) {
            Terminal state = terminal.get();
            if (state == Terminal.TIMEOUT) return failure(TransportException.Kind.TIMEOUT);
            if (state == Terminal.CANCELLED) return failure(TransportException.Kind.CANCELLED);
            return fallback;
        }

        private void stop(Terminal reason) {
            if (terminal.compareAndSet(Terminal.ACTIVE, reason)) disconnectOnce();
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

    private static void closeInput(InputStream input) {
        if (input == null) return;
        try { input.close(); }
        catch (IOException | RuntimeException ignored) { }
    }

    private static void safeCancel(ReportTransport.Scheduled deadline) {
        if (deadline == null) return;
        try { deadline.cancel(); }
        catch (RuntimeException ignored) { }
    }

    private static TransportException failure(TransportException.Kind kind) {
        return TransportException.of(kind);
    }

    private static final class DaemonFactory implements ThreadFactory {
        @Override public Thread newThread(Runnable runnable) {
            Thread thread = new Thread(runnable, "capabilities-transport-deadline");
            thread.setDaemon(true);
            return thread;
        }
    }
}
