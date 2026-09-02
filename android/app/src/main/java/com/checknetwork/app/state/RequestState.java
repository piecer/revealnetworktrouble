package com.checknetwork.app.state;

import com.checknetwork.app.core.Report;
import com.checknetwork.app.network.TransportException;
import java.util.Objects;
import java.util.Optional;

/** Immutable request lifecycle snapshot. */
public final class RequestState {
    public enum Phase { IDLE, LOADING, READY, ERROR, CANCELLED }

    private final Phase phase;
    private final long ownerId;
    private final String signature;
    private final boolean busy;
    private final String rawJson;
    private final Report report;
    private final TransportException error;

    private RequestState(Phase phase, long ownerId, String signature, boolean busy,
                         String rawJson, Report report, TransportException error) {
        this.phase = Objects.requireNonNull(phase, "phase");
        this.ownerId = ownerId;
        this.signature = Objects.requireNonNull(signature, "signature");
        this.busy = busy;
        this.rawJson = rawJson;
        this.report = report;
        this.error = error;
    }

    static RequestState idle(long ownerId, String signature) {
        return new RequestState(Phase.IDLE, ownerId, signature, false, null, null, null);
    }
    static RequestState loading(long ownerId, String signature) {
        return new RequestState(Phase.LOADING, ownerId, signature, true, null, null, null);
    }
    static RequestState ready(long ownerId, String signature, String rawJson, Report report) {
        return new RequestState(Phase.READY, ownerId, signature, false,
                Objects.requireNonNull(rawJson, "rawJson"), Objects.requireNonNull(report, "report"), null);
    }
    static RequestState error(long ownerId, String signature, TransportException error) {
        return new RequestState(Phase.ERROR, ownerId, signature, false, null, null,
                Objects.requireNonNull(error, "error"));
    }
    static RequestState cancelled(long ownerId, String signature) {
        return new RequestState(Phase.CANCELLED, ownerId, signature, false, null, null, null);
    }

    public Phase phase() { return phase; }
    public long ownerId() { return ownerId; }
    public String signature() { return signature; }
    public boolean busy() { return busy; }
    public Optional<String> rawJson() { return Optional.ofNullable(rawJson); }
    public Optional<Report> report() { return Optional.ofNullable(report); }
    public Optional<TransportException> error() { return Optional.ofNullable(error); }
    public boolean shareEligible() { return phase == Phase.READY && rawJson != null && report != null; }

    @Override public String toString() {
        return "RequestState{phase=" + phase + ", ownerId=" + ownerId
                + ", signature='" + signature + "', busy=" + busy + "}";
    }
}
