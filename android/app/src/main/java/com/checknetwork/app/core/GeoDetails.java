package com.checknetwork.app.core;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** Validated supplemental snapshots of raw observations, not coordinate/ASN provenance. */
public final class GeoDetails {
    private final int total, omitted;
    private final List<Entry> entries;

    GeoDetails(int total, int omitted, List<Entry> entries) {
        this.total = total;
        this.omitted = omitted;
        this.entries = Collections.unmodifiableList(new ArrayList<>(entries));
    }

    public int schemaVersion() { return 1; }
    public int total() { return total; }
    public int omitted() { return omitted; }
    public List<Entry> entries() { return entries; }

    public static final class Entry {
        private final Map<String, String> fields;
        Entry(Map<String, String> fields) {
            this.fields = Collections.unmodifiableMap(new LinkedHashMap<>(fields));
        }
        public String address() { return fields.get("address"); }
        public String provider() { return fields.get("provider"); }
        public String source() { return fields.get("source"); }
        /** Exact validated strings, including absent-versus-present optional values. */
        public Map<String, String> fields() { return fields; }
    }
}
