package com.checknetwork.app.network;

import java.net.HttpURLConnection;
import java.util.List;
import java.util.Map;

/** Extracts Retry-After only when the complete response header collection exposes exactly one value. */
final class RetryAfterHeaderExtractor {
    private RetryAfterHeaderExtractor() { }

    static String extract(HttpURLConnection connection) {
        try {
            Map<String, List<String>> fields = connection.getHeaderFields();
            if (fields == null) return null;
            String found = null;
            int count = 0;
            for (Map.Entry<String, List<String>> field : fields.entrySet()) {
                String name = field.getKey();
                if (name == null || !"Retry-After".equalsIgnoreCase(name)) continue;
                List<String> values = field.getValue();
                if (values == null) return null;
                for (String value : values) {
                    if (value == null || ++count != 1) return null;
                    found = value;
                }
            }
            return count == 1 ? found : null;
        } catch (RuntimeException unavailableOrMalformed) {
            return null;
        }
    }
}
