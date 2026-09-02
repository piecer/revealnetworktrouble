package com.checknetwork.app.network;

import java.net.URI;
import java.net.URISyntaxException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.Locale;
import java.util.Optional;
import java.util.Set;

/** Immutable API origin and ephemeral authentication policy. */
public final class ApiConnectionConfig {
    public static final int MAX_BEARER_BYTES = 4_096;
    private static final Set<String> DEBUG_HTTP_HOSTS = Set.of(
            "localhost", "127.0.0.1", "::1", "10.0.2.2", "10.0.3.2");

    private final URI baseUri;
    private final String bearer;

    private ApiConnectionConfig(URI baseUri, String bearer) {
        this.baseUri = baseUri;
        this.bearer = bearer;
    }

    public static ApiConnectionConfig create(String rawBaseUrl, boolean debugBuild, String rawBearer) {
        URI base = parseBase(rawBaseUrl, debugBuild);
        return new ApiConnectionConfig(base, parseBearer(rawBearer));
    }

    private static URI parseBase(String raw, boolean debugBuild) {
        if (raw == null || raw.isEmpty() || !raw.equals(raw.trim())) {
            throw invalidBase();
        }
        try {
            URI candidate = new URI(raw);
            String scheme = candidate.getScheme();
            String host = candidate.getHost();
            if (scheme == null || host == null || candidate.isOpaque() || candidate.getRawUserInfo() != null
                    || candidate.getRawQuery() != null || candidate.getRawFragment() != null
                    || !(candidate.getRawPath().isEmpty() || candidate.getRawPath().equals("/"))
                    || candidate.getPort() == 0 || candidate.getPort() < -1 || candidate.getPort() > 65_535) {
                throw invalidBase();
            }
            scheme = scheme.toLowerCase(Locale.ROOT);
            host = unbracket(host).toLowerCase(Locale.ROOT);
            if (!scheme.equals("https") && !(debugBuild && scheme.equals("http") && DEBUG_HTTP_HOSTS.contains(host))) {
                throw invalidBase();
            }
            if (scheme.equals("http") && !DEBUG_HTTP_HOSTS.contains(host)) {
                throw invalidBase();
            }
            return new URI(scheme, null, host, candidate.getPort(), null, null, null);
        } catch (URISyntaxException | IllegalArgumentException exception) {
            throw invalidBase();
        }
    }

    private static String unbracket(String host) {
        return host.startsWith("[") && host.endsWith("]") ? host.substring(1, host.length() - 1) : host;
    }

    private static IllegalArgumentException invalidBase() {
        return new IllegalArgumentException("API base URL must be an allowed origin");
    }

    private static String parseBearer(String raw) {
        if (raw == null) return null;
        String token = raw.trim();
        if (token.isEmpty()) return null;
        if (token.getBytes(StandardCharsets.UTF_8).length > MAX_BEARER_BYTES) {
            throw new IllegalArgumentException("Bearer credential exceeds the byte limit");
        }
        for (int i = 0; i < token.length(); i++) {
            if (Character.isISOControl(token.charAt(i))) {
                throw new IllegalArgumentException("Bearer credential contains invalid characters");
            }
        }
        return token;
    }

    public URI baseUri() { return baseUri; }

    public URI reportsEndpoint() { return baseUri.resolve("/api/v1/reports"); }
    public URI checksEndpoint() { return baseUri.resolve("/api/v1/checks"); }

    /** Authorization is deliberately unavailable for cleartext debug origins. */
    public Optional<String> authorizationHeader() {
        return baseUri.getScheme().equals("https") && bearer != null
                ? Optional.of("Bearer " + bearer) : Optional.empty();
    }

    /** Stable origin identity. Credential rotation never changes ordinary request identity. */
    public String signature() {
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256")
                    .digest(baseUri.toASCIIString().getBytes(StandardCharsets.UTF_8));
            StringBuilder out = new StringBuilder(64);
            for (byte value : digest) out.append(String.format(Locale.ROOT, "%02x", value & 0xff));
            return out.toString();
        } catch (NoSuchAlgorithmException impossible) {
            throw new AssertionError(impossible);
        }
    }

    @Override public String toString() {
        return "ApiConnectionConfig{baseUri=" + baseUri + ", authentication="
                + (bearer == null ? "absent" : "present") + "}";
    }
}
