package com.checknetwork.app.core;

public final class ReportParseException extends IllegalArgumentException {
    public ReportParseException(String message) { super(message); }
    public ReportParseException(String message, Throwable cause) { super(message, cause); }
}
