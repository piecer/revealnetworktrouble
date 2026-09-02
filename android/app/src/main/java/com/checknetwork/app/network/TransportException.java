package com.checknetwork.app.network;

import com.checknetwork.app.core.ApiError;
import java.util.Optional;

/** Stable, non-reflecting failure classification for report calls. */
public final class TransportException extends Exception {
    public enum Kind { NETWORK, TIMEOUT, CANCELLED, RESPONSE_TOO_LARGE, INVALID_RESPONSE, API }

    private final Kind kind;
    private final ApiError apiError;

    TransportException(Kind kind, String safeMessage) {
        super(safeMessage);
        this.kind = kind;
        this.apiError = null;
    }

    TransportException(ApiError apiError) {
        super(apiError.safeMessage());
        this.kind = Kind.API;
        this.apiError = apiError;
    }

    public static TransportException of(Kind kind) {
        if (kind == Kind.API) throw new IllegalArgumentException("API failures require a structured ApiError");
        String message = switch (kind) {
            case NETWORK -> "The report request failed due to a network error.";
            case TIMEOUT -> "The report request exceeded its deadline.";
            case CANCELLED -> "The report request was cancelled.";
            case RESPONSE_TOO_LARGE -> "The report response exceeded the byte limit.";
            case INVALID_RESPONSE -> "The server returned an invalid response.";
            case API -> throw new AssertionError();
        };
        return new TransportException(kind, message);
    }

    public Kind kind() { return kind; }
    public Optional<ApiError> apiError() { return Optional.ofNullable(apiError); }

    @Override public String toString() {
        return "TransportException{kind=" + kind
                + (apiError == null ? "" : ", apiError=" + apiError)
                + "}";
    }
}
