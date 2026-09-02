package com.checknetwork.app.state;

import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.network.TransportException;
import java.util.Objects;

/** Owner-fenced reducer for one replaceable report request lane. */
public final class RequestCoordinator {
    public interface StateListener { void onStateChanged(RequestState snapshot); }
    public interface CallFactory { CancellableCall create(ReportRequest request); }
    public interface CancellableCall {
        void start(Callback callback);
        void cancel();
    }
    public interface Callback {
        void onSuccess(String rawJson, Report report);
        void onError(TransportException error);
        void onFinally();
    }

    private final CallFactory factory;
    private long ownerSequence;
    private RequestState state = RequestState.idle(0, "");
    private CancellableCall active;
    private boolean destroyed;
    private volatile StateListener stateListener;

    public RequestCoordinator(CallFactory factory) { this.factory = Objects.requireNonNull(factory, "factory"); }

    public RequestState state() { synchronized (this) { return state; } }

    /** Publishes the current immutable snapshot immediately, then future owned transitions. */
    public void setStateListener(StateListener listener) {
        final RequestState snapshot;
        synchronized (this) {
            stateListener = listener;
            snapshot = state;
        }
        publish(listener, snapshot);
    }

    public long start(ReportRequest request) {
        Objects.requireNonNull(request, "request");
        final long owner;
        final String signature = request.signature();
        final CancellableCall call;
        final CancellableCall previous;
        final RequestState snapshot;
        synchronized (this) {
            if (destroyed) throw new IllegalStateException("request coordinator is destroyed");
            owner = Math.incrementExact(ownerSequence);
            ownerSequence = owner;
            call = Objects.requireNonNull(factory.create(request), "call");
            previous = active;
            active = call;
            state = RequestState.loading(owner, signature);
            snapshot = state;
        }
        safeCancel(previous);
        publish(snapshot);
        try {
            call.start(callback(owner, signature, call));
        } catch (RuntimeException startFailure) {
            completeError(owner, signature, call, TransportException.of(TransportException.Kind.NETWORK));
        }
        return owner;
    }

    public void invalidateInput(String canonicalSignature) {
        Objects.requireNonNull(canonicalSignature, "canonicalSignature");
        final CancellableCall previous;
        final RequestState snapshot;
        synchronized (this) {
            if (destroyed) return;
            previous = active;
            active = null;
            state = RequestState.idle(ownerSequence, canonicalSignature);
            snapshot = state;
        }
        safeCancel(previous);
        publish(snapshot);
    }

    public void cancel() {
        final CancellableCall previous;
        final RequestState snapshot;
        synchronized (this) {
            if (destroyed || state.phase() == RequestState.Phase.CANCELLED) return;
            previous = active;
            active = null;
            state = RequestState.cancelled(ownerSequence, state.signature());
            snapshot = state;
        }
        safeCancel(previous);
        publish(snapshot);
    }

    public void destroy() {
        final CancellableCall previous;
        final RequestState snapshot;
        synchronized (this) {
            if (destroyed) return;
            destroyed = true;
            previous = active;
            active = null;
            state = RequestState.cancelled(ownerSequence, state.signature());
            snapshot = state;
        }
        safeCancel(previous);
        publish(snapshot);
    }

    private Callback callback(long owner, String signature, CancellableCall call) {
        return new Callback() {
            @Override public void onSuccess(String rawJson, Report report) {
                completeSuccess(owner, signature, call, rawJson, report);
            }
            @Override public void onError(TransportException error) {
                completeError(owner, signature, call, error);
            }
            @Override public void onFinally() { finalizeOwned(owner, signature, call); }
        };
    }

    private void completeSuccess(long owner, String signature, CancellableCall call, String rawJson, Report report) {
        final RequestState snapshot;
        synchronized (this) {
            if (!owns(owner, signature, call)) return;
            Objects.requireNonNull(rawJson, "rawJson");
            Objects.requireNonNull(report, "report");
            active = null;
            state = RequestState.ready(owner, signature, rawJson, report);
            snapshot = state;
        }
        publish(snapshot);
    }

    private void completeError(long owner, String signature, CancellableCall call, TransportException error) {
        final RequestState snapshot;
        synchronized (this) {
            if (!owns(owner, signature, call)) return;
            Objects.requireNonNull(error, "error");
            active = null;
            state = error.kind() == TransportException.Kind.CANCELLED
                    ? RequestState.cancelled(owner, signature)
                    : RequestState.error(owner, signature, error);
            snapshot = state;
        }
        publish(snapshot);
    }

    private void finalizeOwned(long owner, String signature, CancellableCall call) {
        final RequestState snapshot;
        synchronized (this) {
            if (!owns(owner, signature, call)) return;
            active = null;
            state = RequestState.error(owner, signature,
                    TransportException.of(TransportException.Kind.INVALID_RESPONSE));
            snapshot = state;
        }
        publish(snapshot);
    }

    private boolean owns(long owner, String signature, CancellableCall call) {
        return !destroyed && active == call && state.phase() == RequestState.Phase.LOADING
                && state.ownerId() == owner && state.signature().equals(signature);
    }

    private static void safeCancel(CancellableCall call) {
        if (call == null) return;
        try { call.cancel(); }
        catch (RuntimeException | Error ignored) { }
    }

    private void publish(RequestState snapshot) { publish(stateListener, snapshot); }

    private static void publish(StateListener listener, RequestState snapshot) {
        if (listener == null) return;
        try { listener.onStateChanged(snapshot); }
        catch (RuntimeException ignored) { }
    }
}
