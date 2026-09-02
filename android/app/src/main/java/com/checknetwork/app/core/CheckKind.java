package com.checknetwork.app.core;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public enum CheckKind {
    DNS("dns"), TCP("tcp"), HTTP("http"), HTTPS("https"), SSH("ssh"), SMTP("smtp"),
    SUBMISSION("submission"), SMTPS("smtps"), IMAP("imap"), IMAPS("imaps"),
    POP3("pop3"), POP3S("pop3s"), TRACEROUTE("traceroute");

    private final String wireValue;
    CheckKind(String wireValue) { this.wireValue = wireValue; }
    public String wireValue() { return wireValue; }
    public boolean supportsExpectedStatus() { return this == HTTP || this == HTTPS; }

    public static CheckKind fromWire(String value) {
        if (value != null) for (CheckKind kind : values()) if (kind.wireValue.equals(value)) return kind;
        throw new IllegalArgumentException("unsupported check kind");
    }

    public static List<String> wireValues() {
        List<String> values = new ArrayList<>();
        for (CheckKind kind : CheckKind.values()) values.add(kind.wireValue);
        return Collections.unmodifiableList(values);
    }
}
