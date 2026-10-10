package com.checknetwork.app.network;

import com.checknetwork.app.core.IPContext;
import com.checknetwork.app.core.IPContextParser;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.SocketTimeoutException;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

/** One explicit metadata POST; independent of checks and report transport. */
public final class IPContextTransport {
    public static final long DEADLINE_MILLIS = 10000;
    public enum Failure { NETWORK, TIMEOUT, CANCELLED, TOO_LARGE, INVALID, UNSUPPORTED, UNAVAILABLE, EXPIRED }
    public static final class ContextException extends Exception {
        private final Failure failure;
        public ContextException(Failure failure) { super("IP context request: " + failure.name()); this.failure = failure; }
        public Failure failure() { return failure; }
    }
    public interface Request {
        IPContext execute() throws ContextException;
        /** Revoke delivery promptly; must not wait for I/O or cleanup. */
        void cancel();
        /**
         * Nonblocking retirement receipt, queried only after execute returned (or was
         * never scheduled). A false/throwing receipt keeps the owner's work charged.
         * Synchronous implementations retire all work before execute returns.
         */
        default boolean isQuiescent() { return true; }
    }
    private final ApiConnectionConfig config;
    private final ReportTransport.ConnectionFactory connections;
    private final OperationDeadline.Clock clock;
    private final ThreadFactory workers;
    public IPContextTransport(ApiConnectionConfig config) {
        this(config, url -> (HttpURLConnection) url.openConnection(), System::nanoTime);
    }
    public IPContextTransport(ApiConnectionConfig config, ReportTransport.ConnectionFactory connections, OperationDeadline.Clock clock) {
        this(config, connections, clock, task -> {
            Thread thread = new Thread(task, "ip-context-io"); thread.setDaemon(true); return thread;
        });
    }
    IPContextTransport(ApiConnectionConfig config, ReportTransport.ConnectionFactory connections,
                       OperationDeadline.Clock clock, ThreadFactory workers) {
        this.config = java.util.Objects.requireNonNull(config);
        this.connections = java.util.Objects.requireNonNull(connections);
        this.clock = java.util.Objects.requireNonNull(clock);
        this.workers = java.util.Objects.requireNonNull(workers);
    }
    public Call newCall(String address) { return newCall(address, new OperationDeadline(clock, DEADLINE_MILLIS)); }
    public Call newCall(String address, OperationDeadline deadline) {
        if (!IPContextParser.isEligible(address)) throw new IllegalArgumentException("Ineligible IP context address");
        return new Call(address, deadline);
    }
    public final class Call implements Request {
        private final String address;
        private final OperationDeadline deadline;
        private final AtomicReference<Failure> stopped = new AtomicReference<>();
        private final AtomicReference<HttpURLConnection> connection = new AtomicReference<>();
        private final AtomicBoolean started = new AtomicBoolean(), disconnected = new AtomicBoolean();
        private Thread worker;
        private boolean completed;
        private IPContext result;
        private ContextException failure;
        private Call(String address, OperationDeadline deadline) { this.address = address; this.deadline = deadline; }
        @Override public void cancel() { stop(Failure.CANCELLED); }
        private synchronized void stop(Failure reason) {
            stopped.compareAndSet(null, reason); notifyAll();
        }
        @Override public synchronized boolean isQuiescent() {
            return worker == null || !worker.isAlive();
        }
        private int check() throws ContextException {
            long remaining = deadline.remainingMillis();
            if (remaining == 0) stop(Failure.TIMEOUT);
            if (stopped.get() != null) throw new ContextException(stopped.get());
            return (int) Math.min(DEADLINE_MILLIS, remaining);
        }
        @Override public IPContext execute() throws ContextException {
            if (!started.compareAndSet(false, true)) throw new ContextException(Failure.INVALID);
            check();
            synchronized (this) {
                check();
                try {
                    worker = java.util.Objects.requireNonNull(workers.newThread(this::runOwned));
                    worker.start();
                } catch (RuntimeException | Error rejected) {
                    throw new ContextException(Failure.UNAVAILABLE);
                }
                // Only this result waiter is released by cancellation. The single
                // I/O worker still owns late-open, read, stream close and disconnect.
                while (!completed) {
                    int remaining = check();
                    try { wait(remaining); }
                    catch (InterruptedException interrupted) {
                        stop(Failure.CANCELLED); Thread.currentThread().interrupt(); check();
                    }
                }
            }
            // Success cannot publish/cache before execution AND teardown really exit.
            while (worker.isAlive()) {
                int remaining = check();
                // Joining does not receive cancel's notifyAll. Bound only this
                // retirement observation wait, never the socket read timeout.
                try { worker.join(Math.min(remaining, 50)); }
                catch (InterruptedException interrupted) {
                    stop(Failure.CANCELLED); Thread.currentThread().interrupt(); check();
                }
            }
            check();
            if (failure != null) throw failure;
            return result;
        }
        private void runOwned() {
            IPContext value = null; ContextException error = null;
            try { value = perform(); }
            catch (ContextException expected) { error = expected; }
            catch (RuntimeException | Error unexpected) { error = new ContextException(Failure.NETWORK); }
            finally {
                synchronized (this) { result = value; failure = error; completed = true; notifyAll(); }
            }
        }
        private IPContext perform() throws ContextException {
            try {
                check();
                HttpURLConnection opened = connections.open(config.contextEndpoint().toURL());
                connection.set(opened);
                opened.setConnectTimeout(check()); opened.setReadTimeout(check());
                opened.setInstanceFollowRedirects(false); opened.setUseCaches(false);
                opened.setRequestMethod("POST"); opened.setDoOutput(true);
                byte[] request = ("{\"address\":\"" + address + "\"}").getBytes(StandardCharsets.UTF_8);
                opened.setFixedLengthStreamingMode(request.length);
                opened.setRequestProperty("Content-Type", "application/json; charset=utf-8");
                opened.setRequestProperty("Accept", "application/json");
                config.authorizationHeader().ifPresent(header -> opened.setRequestProperty("Authorization", header));
                check();
                try (OutputStream output = opened.getOutputStream()) { check(); output.write(request); check(); }
                check(); int status = opened.getResponseCode(); check();
                // Never read or reflect provider/proxy error bodies. In particular, do not drain a redirect.
                if (status != 200) throw new ContextException(status == 404 ? Failure.UNSUPPORTED : Failure.UNAVAILABLE);
                long length = opened.getContentLengthLong(); check();
                if (length > IPContextParser.MAX_BYTES) throw new ContextException(Failure.TOO_LARGE);
                byte[] bytes;
                try (InputStream input = opened.getInputStream()) {
                    check(); ByteArrayOutputStream body = new ByteArrayOutputStream(); byte[] buffer = new byte[4096];
                    while (true) {
                        check(); opened.setReadTimeout(check());
                        int count = input.read(buffer, 0, Math.min(buffer.length, IPContextParser.MAX_BYTES - body.size() + 1));
                        check(); if (count < 0) break;
                        if (count > IPContextParser.MAX_BYTES - body.size()) throw new ContextException(Failure.TOO_LARGE);
                        body.write(buffer, 0, count);
                    }
                    bytes = body.toByteArray();
                }
                check(); IPContext context;
                try { context = IPContextParser.parse(bytes, address); }
                catch (IllegalArgumentException malformed) { throw new ContextException(Failure.INVALID); }
                if (config.reflectsCredential(context)) throw new ContextException(Failure.INVALID);
                check(); return context;
            } catch (SocketTimeoutException failure) {
                check(); throw new ContextException(Failure.TIMEOUT);
            } catch (IOException | RuntimeException failure) {
                check(); throw new ContextException(Failure.NETWORK);
            } finally { disconnect(); }
        }
        private void disconnect() {
            HttpURLConnection current = connection.get();
            if (current != null && disconnected.compareAndSet(false, true)) {
                try { current.disconnect(); } catch (RuntimeException ignored) { }
            }
        }
    }
}
