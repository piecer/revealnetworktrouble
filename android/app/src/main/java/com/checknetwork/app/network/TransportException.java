package com.checknetwork.app.network;

import com.checknetwork.app.core.ApiError;
import com.checknetwork.app.core.CheckCapabilities;
import java.util.Objects;
import java.util.Optional;

/** Stable, non-reflecting failure classification for report calls. */
public final class TransportException extends Exception {
    public enum Kind { NETWORK, TIMEOUT, CANCELLED, RESPONSE_TOO_LARGE, INVALID_RESPONSE, API, UNSUPPORTED_CAPABILITY }

    private final Kind kind;
    private final ApiError apiError;
    private final CheckCapabilities.CapabilityMismatchException.Reason capabilityMismatchReason;

    TransportException(Kind kind, String safeMessage) {
        super(safeMessage);
        this.kind = Objects.requireNonNull(kind, "kind");
        if (kind == Kind.API || kind == Kind.UNSUPPORTED_CAPABILITY)
            throw new IllegalArgumentException("Structured failure kind requires its dedicated factory");
        this.apiError = null;
        this.capabilityMismatchReason = null;
    }

    TransportException(ApiError apiError) {
        super(apiError.safeMessage());
        this.kind = Kind.API;
        this.apiError = apiError;
        this.capabilityMismatchReason = null;
    }

    private TransportException(CheckCapabilities.CapabilityMismatchException.Reason reason) {
        super("The report request is not supported by the server capabilities.");
        this.kind = Kind.UNSUPPORTED_CAPABILITY;
        this.apiError = null;
        this.capabilityMismatchReason = Objects.requireNonNull(reason, "reason");
    }

    public static TransportException of(Kind kind) {
        if (kind == Kind.API) throw new IllegalArgumentException("API failures require a structured ApiError");
        if (kind == Kind.UNSUPPORTED_CAPABILITY)
            throw new IllegalArgumentException("Capability failures require a structured reason");
        String message = switch (kind) {
            case NETWORK -> "The report request failed due to a network error.";
            case TIMEOUT -> "The report request exceeded its deadline.";
            case CANCELLED -> "The report request was cancelled.";
            case RESPONSE_TOO_LARGE -> "The report response exceeded the byte limit.";
            case INVALID_RESPONSE -> "The server returned an invalid response.";
            case API, UNSUPPORTED_CAPABILITY -> throw new AssertionError();
        };
        return new TransportException(kind, message);
    }

    public static TransportException unsupportedCapability(
            CheckCapabilities.CapabilityMismatchException.Reason reason) {
        return new TransportException(reason);
    }

    public Kind kind() { return kind; }
    public Optional<ApiError> apiError() { return Optional.ofNullable(apiError); }
    public Optional<CheckCapabilities.CapabilityMismatchException.Reason> capabilityMismatchReason() {
        return Optional.ofNullable(capabilityMismatchReason);
    }

    @Override public String toString() {
        return "TransportException{kind=" + kind
                + (apiError == null ? "" : ", apiError=" + apiError)
                + (capabilityMismatchReason == null ? "" : ", capabilityMismatchReason=" + capabilityMismatchReason)
                + "}";
    }
}
