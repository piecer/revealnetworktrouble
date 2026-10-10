package com.checknetwork.app.network;

import static org.junit.Assert.*;
import com.checknetwork.app.core.IPContext;
import com.checknetwork.app.core.IPContextParserTest;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.concurrent.*;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class IPContextTransportTest {
    @Test public void actualHttpPostsExactlyOneAddressToContextOnly() throws Exception {
        Class<?> type;
        try { type = Class.forName("com.checknetwork.app.network.IPContextTransport"); }
        catch (ClassNotFoundException absent) { fail("separate IP-context transport missing"); return; }
        byte[] payload = Base64.getDecoder().decode(IPContextParserTest.corpus().getJSONArray("cases").getJSONObject(0).getString("wire_base64"));
        try (ServerSocket server = new ServerSocket(0, 1, InetAddress.getLoopbackAddress())) {
            server.setSoTimeout(3000);
            FutureTask<String> peer = new FutureTask<>(() -> {
                try (Socket socket = server.accept()) {
                    socket.setSoTimeout(3000);
                    BufferedReader input = new BufferedReader(new InputStreamReader(socket.getInputStream(), StandardCharsets.UTF_8));
                    StringBuilder captured = new StringBuilder(); String line; int length = 0;
                    while (!(line = input.readLine()).isEmpty()) {
                        captured.append(line).append('\n');
                        if (line.toLowerCase(java.util.Locale.ROOT).startsWith("content-length:")) length = Integer.parseInt(line.substring(15).trim());
                    }
                    char[] body = new char[length]; int n = 0;
                    while (n < length) n += input.read(body, n, length - n);
                    captured.append('\n').append(body);
                    OutputStream out = socket.getOutputStream();
                    out.write(("HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: " + payload.length + "\r\n\r\n").getBytes(StandardCharsets.US_ASCII));
                    out.write(payload); out.flush();
                    return captured.toString();
                }
            });
            Thread thread = new Thread(peer, "context-loopback-fixture"); thread.start();
            ApiConnectionConfig config = ApiConnectionConfig.create("http://127.0.0.1:" + server.getLocalPort(), true, "secret-not-on-cleartext");
            Object transport = type.getConstructor(ApiConnectionConfig.class).newInstance(config);
            Object call = type.getMethod("newCall", String.class).invoke(transport, "1.1.1.1");
            IPContext result = (IPContext) call.getClass().getMethod("execute").invoke(call);
            assertEquals("1.1.1.1", result.address());
            String request = peer.get(5, TimeUnit.SECONDS); thread.join(3000);
            assertTrue(request, request.startsWith("POST /api/v1/ip-context HTTP/1.1\n"));
            assertTrue(request, request.endsWith("\n\n{\"address\":\"1.1.1.1\"}"));
            assertFalse(request.contains("Authorization:"));
            assertFalse(request.contains("secret-not-on-cleartext"));
            assertFalse(request.contains("/checks")); assertFalse(request.contains("/reports"));
        }
    }
}
