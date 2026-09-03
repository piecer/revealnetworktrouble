package com.checknetwork.app;

import com.checknetwork.app.core.CheckCapabilities;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.network.ApiConnectionConfig;
import com.checknetwork.app.network.CapabilitiesTransport;
import com.checknetwork.app.network.OperationDeadline;
import com.checknetwork.app.network.ReportTransport;
import com.checknetwork.app.network.TransportException;
import com.checknetwork.app.state.RequestCoordinator;
import com.checknetwork.app.state.RequestState;
import java.util.Objects;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

/** Retainable lifecycle owner for one diagnostics lane; never holds an Activity reference. */
public final class DiagnosticsSession {
    public interface Listener { void onStateChanged(RequestState state); }
    public interface Dispatcher { void dispatch(Runnable runnable); }
    interface ProductionTransports {
        CapabilitiesTransport capabilities(ApiConnectionConfig config, OperationDeadline deadline);
        ReportTransport reports(ApiConnectionConfig config, OperationDeadline deadline);
    }
    private final RequestCoordinator coordinator;
    private final Dispatcher dispatcher;
    private final ExecutorService executor;
    private final ProductionFactory productionFactory;
    private Object owner;
    private Listener listener;
    private boolean destroyed;

    DiagnosticsSession(RequestCoordinator coordinator, Dispatcher dispatcher, ExecutorService executor) {
        this(coordinator, dispatcher, executor, null);
    }

    private DiagnosticsSession(RequestCoordinator coordinator, Dispatcher dispatcher,
            ExecutorService executor, ProductionFactory factory) {
        this.coordinator = Objects.requireNonNull(coordinator, "coordinator");
        this.dispatcher = Objects.requireNonNull(dispatcher, "dispatcher");
        this.executor = executor;
        this.productionFactory = factory;
        coordinator.setStateListener(this::publish);
    }

    public static DiagnosticsSession create(Dispatcher dispatcher) {
        ExecutorService executor = Executors.newSingleThreadExecutor(runnable -> {
            Thread thread = new Thread(runnable, "diagnostics-session");
            thread.setDaemon(true);
            return thread;
        });
        ReportTransport.Clock clock = new ReportTransport.Clock() {
            @Override public long nanoTime() { return System.nanoTime(); }
            @Override public java.time.Instant now() { return java.time.Instant.now(); }
        };
        ProductionFactory factory = new ProductionFactory(executor, new ProductionTransports() {
            @Override public CapabilitiesTransport capabilities(ApiConnectionConfig config,
                    OperationDeadline deadline) {
                return new CapabilitiesTransport(config, clock, deadline);
            }
            @Override public ReportTransport reports(ApiConnectionConfig config,
                    OperationDeadline deadline) {
                return new ReportTransport(config, clock, deadline);
            }
        }, clock);
        return new DiagnosticsSession(new RequestCoordinator(factory), dispatcher, executor, factory);
    }

    static DiagnosticsSession createProductionForTests(Dispatcher dispatcher, ExecutorService executor,
            ProductionTransports transports, ReportTransport.Clock clock) {
        ProductionFactory factory = new ProductionFactory(executor, transports, clock);
        return new DiagnosticsSession(new RequestCoordinator(factory), dispatcher, executor, factory);
    }

    public synchronized void attach(Object newOwner, Listener newListener) {
        if (destroyed) throw new IllegalStateException("session is destroyed");
        owner = Objects.requireNonNull(newOwner, "owner");
        listener = Objects.requireNonNull(newListener, "listener");
        deliver(newOwner, newListener, coordinator.state());
    }

    public synchronized void detach(Object oldOwner) {
        if (owner == oldOwner) {
            owner = null;
            listener = null;
        }
    }

    public long start(ReportRequest request) { return coordinator.start(request); }

    public synchronized long start(ApiConnectionConfig config, ReportRequest request) {
        Objects.requireNonNull(config, "config");
        if (productionFactory != null) productionFactory.setNextConfig(config);
        try { return coordinator.start(request); }
        finally { if (productionFactory != null) productionFactory.clearUnconsumedConfig(); }
    }

    public void invalidateInput(String signature) { coordinator.invalidateInput(signature); }
    public void cancel() { coordinator.cancel(); }
    public RequestState state() { return coordinator.state(); }
    public synchronized boolean isDestroyed() { return destroyed; }

    public void destroy() {
        synchronized (this) {
            if (destroyed) return;
            destroyed = true;
            owner = null;
            listener = null;
        }
        coordinator.setStateListener(null);
        coordinator.destroy();
        if (productionFactory != null) productionFactory.clearUnconsumedConfig();
        if (executor != null) {
            try { executor.shutdownNow(); }
            catch (RuntimeException ignored) { }
        }
    }

    private void publish(RequestState state) {
        final Object expectedOwner;
        final Listener expectedListener;
        synchronized (this) {
            if (destroyed || owner == null || listener == null) return;
            expectedOwner = owner;
            expectedListener = listener;
        }
        deliver(expectedOwner, expectedListener, state);
    }

    private void deliver(Object expectedOwner, Listener expectedListener, RequestState state) {
        try {
            dispatcher.dispatch(() -> {
                synchronized (DiagnosticsSession.this) {
                    if (destroyed || owner != expectedOwner || listener != expectedListener) return;
                }
                try { expectedListener.onStateChanged(state); }
                catch (RuntimeException ignored) { }
            });
        } catch (RuntimeException ignored) { }
    }

    private static final class ProductionFactory implements RequestCoordinator.CallFactory {
        private final ExecutorService executor;
        private final ProductionTransports transports;
        private final ReportTransport.Clock clock;
        private final AtomicReference<ApiConnectionConfig> nextConfig = new AtomicReference<>();

        ProductionFactory(ExecutorService executor, ProductionTransports transports,
                ReportTransport.Clock clock) {
            this.executor = Objects.requireNonNull(executor, "executor");
            this.transports = Objects.requireNonNull(transports, "transports");
            this.clock = Objects.requireNonNull(clock, "clock");
        }

        void setNextConfig(ApiConnectionConfig config) {
            if (!nextConfig.compareAndSet(null, config))
                throw new IllegalStateException("connection config is already pending");
        }

        void clearUnconsumedConfig() { nextConfig.set(null); }

        @Override public RequestCoordinator.CancellableCall create(ReportRequest request) {
            ApiConnectionConfig snapshot = Objects.requireNonNull(nextConfig.getAndSet(null), "connection config");
            OperationDeadline operationDeadline =
                    new OperationDeadline(clock, ReportTransport.MAX_DEADLINE_MILLIS);
            return new RequestCoordinator.CancellableCall() {
                interface Cancellation { void cancel(); }
                final AtomicBoolean cancelled = new AtomicBoolean();
                final AtomicReference<Cancellation> activeCall = new AtomicReference<>();
                volatile Future<?> future;

                @Override public void start(RequestCoordinator.Callback callback) {
                    future = executor.submit(() -> {
                        try {
                            CapabilitiesTransport.Call capabilitiesCall =
                                    transports.capabilities(snapshot, operationDeadline).newCall();
                            activate(capabilitiesCall::cancel);
                            CheckCapabilities capabilities = capabilitiesCall.execute();
                            throwIfCancelled();
                            if (operationDeadline.remainingMillis() == 0L)
                                throw TransportException.of(TransportException.Kind.TIMEOUT);
                            try { capabilities.validate(request); }
                            catch (CheckCapabilities.CapabilityMismatchException unsupported) {
                                throw TransportException.unsupportedCapability(unsupported.reason());
                            }
                            throwIfCancelled();

                            if (operationDeadline.remainingMillis() == 0L)
                                throw TransportException.of(TransportException.Kind.TIMEOUT);
                            ReportTransport.Call reportCall =
                                    transports.reports(snapshot, operationDeadline).newCall(request);
                            activate(reportCall::cancel);
                            ReportTransport.Response response = reportCall.execute();
                            callback.onSuccess(response.rawJson(), response.report());
                        } catch (TransportException error) {
                            callback.onError(error);
                        } catch (RuntimeException | Error unexpected) {
                            callback.onError(TransportException.of(TransportException.Kind.NETWORK));
                        } finally {
                            activeCall.set(null);
                            callback.onFinally();
                        }
                    });
                }

                @Override public void cancel() {
                    cancelled.set(true);
                    safeCancel(activeCall.get());
                    Future<?> current = future;
                    if (current != null) {
                        try { current.cancel(true); }
                        catch (RuntimeException ignored) { }
                    }
                }

                private void activate(Cancellation call) throws TransportException {
                    activeCall.set(call);
                    if (cancelled.get()) {
                        safeCancel(call);
                        throw TransportException.of(TransportException.Kind.CANCELLED);
                    }
                }

                private void throwIfCancelled() throws TransportException {
                    if (cancelled.get()) throw TransportException.of(TransportException.Kind.CANCELLED);
                }

                private void safeCancel(Cancellation call) {
                    if (call == null) return;
                    try { call.cancel(); }
                    catch (RuntimeException ignored) { }
                }
            };
        }
    }
}
