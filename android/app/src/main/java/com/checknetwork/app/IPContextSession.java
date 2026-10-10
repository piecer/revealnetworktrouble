package com.checknetwork.app;

import com.checknetwork.app.core.IPContext;
import com.checknetwork.app.core.IPContextAddresses;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.network.ApiConnectionConfig;
import com.checknetwork.app.network.IPContextTransport;
import com.checknetwork.app.network.OperationDeadline;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Objects;
import java.util.concurrent.Executor;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.function.Supplier;

/** Retained context-only owner. No view, Activity, raw report or persistence reference. */
public final class IPContextSession {
    public record Snapshot(String address, boolean loading, IPContext context, boolean appCache,
                           IPContextTransport.Failure failure) {}
    public interface Listener { void changed(Snapshot snapshot); }
    interface Factory { IPContextTransport.Request create(ApiConnectionConfig config, String address, OperationDeadline deadline); }
    private final DiagnosticsSession.Dispatcher dispatcher;
    private final Executor executor;
    private final Factory factory;
    private final OperationDeadline.Clock clock;
    private final Supplier<Instant> now;
    private final LinkedHashMap<String,IPContext> cache = new LinkedHashMap<>(64, .75f, true);
    private Report report;
    private ApiConnectionConfig config;
    private List<String> eligible = List.of();
    private Object owner;
    private Listener listener;
    private long generation;
    private boolean destroyed;
    private IPContextTransport.Request work;
    private boolean workReturned;
    private OperationDeadline publicationDeadline;
    private Snapshot state = new Snapshot(null, false, null, false, null);
    public static IPContextSession create(DiagnosticsSession.Dispatcher dispatcher) {
        ExecutorService executor = Executors.newSingleThreadExecutor(task -> {
            Thread thread = new Thread(task, "ip-context-session"); thread.setDaemon(true); return thread;
        });
        return new IPContextSession(dispatcher, executor,
                (config, address, deadline) -> new IPContextTransport(config).newCall(address, deadline), System::nanoTime, Instant::now);
    }
    IPContextSession(DiagnosticsSession.Dispatcher dispatcher, Executor executor, Factory factory,
                     OperationDeadline.Clock clock, Supplier<Instant> now) {
        this.dispatcher = Objects.requireNonNull(dispatcher); this.executor = Objects.requireNonNull(executor);
        this.factory = Objects.requireNonNull(factory); this.clock = Objects.requireNonNull(clock); this.now = Objects.requireNonNull(now);
    }
    public synchronized Snapshot state() { expirePublication(); return state; }
    public synchronized void bind(Report value, ApiConnectionConfig authority) {
        if (destroyed || report == value && config == authority) return;
        invalidate(); cache.clear(); report = value; config = authority;
        eligible = IPContextAddresses.from(value).addresses();
        state = new Snapshot(null, false, null, false, null); publish();
    }
    public synchronized void select(String address) {
        expirePublication();
        if (destroyed || Objects.equals(state.address(), address)) return;
        invalidate();
        String selected = eligible.contains(address) ? address : null;
        IPContext hit = cached(selected);
        state = new Snapshot(selected, false, hit, hit != null, null); publish();
    }
    public synchronized void attach(Object token, Listener value) {
        if (destroyed) return;
        owner = Objects.requireNonNull(token); listener = Objects.requireNonNull(value); publish();
    }
    public synchronized void detach(Object token) { if (owner == token) { owner = null; listener = null; } }
    public synchronized void cancel() {
        invalidate(); state = new Snapshot(state.address(), false, null, false, null); publish();
    }
    private void invalidate() {
        generation++; publicationDeadline = null;
        if (work != null) try { work.cancel(); } catch (RuntimeException ignored) { }
    }
    private IPContext cached(String address) {
        IPContext value = cache.get(address);
        if (value != null && !now.get().isBefore(value.expiresAt())) { cache.remove(address); return null; }
        return value;
    }
    public synchronized void lookup() {
        expirePublication();
        if (destroyed || config == null || report == null || state.address() == null || state.loading()) return;
        IPContext hit = cached(state.address());
        if (hit != null) { state = new Snapshot(state.address(), false, hit, true, null); publish(); return; }
        retireQuiescentWork();
        if (work != null) { state = new Snapshot(state.address(), false, null, false, IPContextTransport.Failure.UNAVAILABLE); publish(); return; }
        long expected = ++generation; String address = state.address();
        OperationDeadline deadline = new OperationDeadline(clock, IPContextTransport.DEADLINE_MILLIS);
        final IPContextTransport.Request request;
        try { request = factory.create(config, address, deadline); }
        catch (RuntimeException failure) { state = new Snapshot(address, false, null, false, IPContextTransport.Failure.INVALID); publish(); return; }
        work = request; workReturned = false; state = new Snapshot(address, true, null, false, null); publish();
        try { executor.execute(() -> run(request, expected, address, deadline)); }
        catch (RuntimeException failure) {
            try { request.cancel(); } catch (RuntimeException ignored) { }
            workReturned = true; retireQuiescentWork();
            finish(expected, address, deadline, null, IPContextTransport.Failure.UNAVAILABLE);
        }
    }
    private void run(IPContextTransport.Request request, long expected, String address, OperationDeadline deadline) {
        IPContext context = null; IPContextTransport.Failure error = null;
        try { context = request.execute(); }
        catch (IPContextTransport.ContextException failure) { error = failure.failure(); }
        catch (RuntimeException failure) { error = IPContextTransport.Failure.UNAVAILABLE; }
        finally { synchronized (this) { if (work == request) { workReturned = true; retireQuiescentWork(); } } }
        // A callback may return a value while still owning cleanup. Such a value
        // is not publishable; a failed receipt also leaves admission fail-closed.
        synchronized (this) {
            if (context != null && work == request) { context = null; error = IPContextTransport.Failure.UNAVAILABLE; }
        }
        IPContext result = context; IPContextTransport.Failure failure = error;
        try { dispatcher.dispatch(() -> finish(expected, address, deadline, result, failure)); }
        catch (RuntimeException dispatchFailure) { finish(expected, address, deadline, null, IPContextTransport.Failure.UNAVAILABLE); }
    }
    private void retireQuiescentWork() {
        if (work == null || !workReturned) return;
        try { if (work.isQuiescent()) work = null; }
        catch (RuntimeException unavailableReceipt) { /* Fail closed: ownership is not proved retired. */ }
    }
    private synchronized void finish(long expected, String address, OperationDeadline deadline, IPContext value, IPContextTransport.Failure error) {
        if (destroyed || generation != expected || !Objects.equals(address, state.address())) return;
        if (deadline.remainingMillis() == 0) { value = null; error = IPContextTransport.Failure.TIMEOUT; }
        if (value != null && (!address.equals(value.address()) || config.reflectsCredential(value))) { value = null; error = IPContextTransport.Failure.INVALID; }
        if (value != null && now.get().isBefore(value.expiresAt())) {
            cache.put(address, value);
            while (cache.size() > 64) cache.remove(cache.keySet().iterator().next());
        }
        publicationDeadline = deadline;
        state = new Snapshot(address, false, value, false, error); publish();
    }
    private void expirePublication() {
        if (state.context() != null && !now.get().isBefore(state.context().expiresAt())) {
            cache.remove(state.address());
            state = new Snapshot(state.address(), false, null, false, IPContextTransport.Failure.EXPIRED);
        }
        if (publicationDeadline != null && publicationDeadline.remainingMillis() == 0) {
            cache.remove(state.address());
            state = new Snapshot(state.address(), false, null, false, IPContextTransport.Failure.TIMEOUT);
            publicationDeadline = null;
        }
    }
    private void publish() {
        Snapshot expected = state; Object token = owner;
        if (token == null) return;
        // Delayed messages retain neither the attached listener nor its Activity.
        try { dispatcher.dispatch(() -> {
            synchronized (IPContextSession.this) {
                if (destroyed || owner != token || state != expected || listener == null) return;
                expirePublication();
                publicationDeadline = null;
                listener.changed(state);
            }
        }); } catch (RuntimeException ignored) { }
    }
    public synchronized void destroy() {
        if (destroyed) return;
        destroyed = true; invalidate(); cache.clear(); report = null; config = null; eligible = List.of();
        owner = null; listener = null; state = new Snapshot(null, false, null, false, null);
        if (executor instanceof ExecutorService service) service.shutdown();
    }
    int cacheSizeForTests() { synchronized (this) { return cache.size(); } }
}
