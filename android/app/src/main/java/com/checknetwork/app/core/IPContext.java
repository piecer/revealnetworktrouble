package com.checknetwork.app.core;

import java.time.Instant;
import java.util.Map;

/** Separate immutable metadata snapshot. Never attached to a Report or either export. */
public final class IPContext {
    private final Map<String,Object> projection;
    IPContext(Map<String,Object> projection) { this.projection = projection; }
    public Map<String,Object> projection() { return projection; }
    public String address() { return (String) projection.get("address"); }
    public String source() { return (String) projection.get("source"); }
    public Instant fetchedAt() { return Instant.parse((String) projection.get("fetched_at")); }
    public Instant expiresAt() { return Instant.parse((String) projection.get("expires_at")); }
    @Override public String toString() { return "IPContext{metadata}"; }
}
