package com.checknetwork.app.core;

import java.util.ArrayList;
import java.util.List;

/** Literal-only mirror of diagnostic.IsPublicDiagnosticIP; never performs DNS/network I/O. */
final class GeoAddressPolicy {
    private static final String[] BLOCKED = {
            "0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
            "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.31.196.0/24",
            "192.52.193.0/24", "192.88.99.0/24", "192.168.0.0/16", "192.175.48.0/24",
            "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4",
            "240.0.0.0/4", "100.100.100.200/32", "::/128", "::1/128", "64:ff9b::/96",
            "64:ff9b:1::/48", "100::/64", "100:0:0:1::/64", "2001::/23", "fc00::/7",
            "fe80::/10", "ff00::/8", "2001:db8::/32", "2002::/16", "2620:4f:8000::/48",
            "3fff::/20", "5f00::/16"
    };
    private static final List<Prefix> PREFIXES = prefixes();

    private GeoAddressPolicy() {}

    static boolean isCanonicalPublic(String address) {
        if (address == null || address.length() > 39) return false;
        byte[] bytes = parse(address);
        if (bytes == null) return false;
        if (bytes.length == 16) {
            if (!canonicalV6(bytes).equals(address)) return false;
            boolean mapped = true;
            for (int i = 0; i < 10; i++) if (bytes[i] != 0) mapped = false;
            if (mapped && bytes[10] == (byte) 255 && bytes[11] == (byte) 255) return false;
        }
        for (Prefix prefix : PREFIXES) if (prefix.contains(bytes)) return false;
        return true;
    }

    private static byte[] parse(String address) {
        if (address.indexOf(':') < 0) {
            String[] parts = address.split("\\.", -1);
            if (parts.length != 4) return null;
            byte[] bytes = new byte[4];
            for (int i = 0; i < 4; i++) {
                if (!parts[i].matches("0|[1-9][0-9]{0,2}")) return null;
                int value = Integer.parseInt(parts[i]);
                if (value > 255) return null;
                bytes[i] = (byte) value;
            }
            return bytes;
        }
        if (!address.matches("[0-9a-f:]+")) return null;
        int gap = address.indexOf("::");
        if (gap >= 0 && address.indexOf("::", gap + 2) >= 0) return null;
        String left = gap < 0 ? address : address.substring(0, gap);
        String right = gap < 0 ? "" : address.substring(gap + 2);
        String[] start = left.isEmpty() ? new String[0] : left.split(":", -1);
        String[] end = right.isEmpty() ? new String[0] : right.split(":", -1);
        int size = start.length + end.length;
        if (gap < 0 ? size != 8 : size >= 8) return null;
        byte[] bytes = new byte[16];
        for (int i = 0; i < size; i++) {
            String part = i < start.length ? start[i] : end[i - start.length];
            if (!part.matches("[0-9a-f]{1,4}")) return null;
            int value = Integer.parseInt(part, 16);
            int at = i < start.length ? i : 8 - end.length + i - start.length;
            bytes[2 * at] = (byte) (value >> 8);
            bytes[2 * at + 1] = (byte) value;
        }
        return bytes;
    }

    private static String canonicalV6(byte[] bytes) {
        int[] words = new int[8];
        for (int i = 0; i < 8; i++) words[i] = (bytes[2 * i] & 255) * 256 + (bytes[2 * i + 1] & 255);
        int best = -1, longest = 1;
        for (int i = 0; i < 8;) {
            if (words[i] != 0) { i++; continue; }
            int start = i;
            while (i < 8 && words[i] == 0) i++;
            if (i - start > longest) { best = start; longest = i - start; }
        }
        StringBuilder result = new StringBuilder();
        for (int i = 0; i < 8; i++) {
            if (i == best) { result.append("::"); i += longest - 1; continue; }
            if (result.length() > 0 && result.charAt(result.length() - 1) != ':') result.append(':');
            result.append(Integer.toHexString(words[i]));
        }
        return result.toString();
    }

    private static List<Prefix> prefixes() {
        List<Prefix> result = new ArrayList<>();
        for (String cidr : BLOCKED) {
            String[] parts = cidr.split("/");
            result.add(new Prefix(parse(parts[0]), Integer.parseInt(parts[1])));
        }
        return result;
    }

    private static final class Prefix {
        private final byte[] bytes;
        private final int bits;
        Prefix(byte[] bytes, int bits) { this.bytes = bytes; this.bits = bits; }
        boolean contains(byte[] address) {
            if (bytes.length != address.length) return false;
            for (int i = 0; i < bits; i += 8) {
                int mask = 255 << (8 - Math.min(8, bits - i));
                if ((bytes[i / 8] & mask) != (address[i / 8] & mask)) return false;
            }
            return true;
        }
    }
}
