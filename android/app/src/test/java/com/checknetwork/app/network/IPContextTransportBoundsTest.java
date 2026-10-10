package com.checknetwork.app.network;

import static org.junit.Assert.*;
import com.checknetwork.app.core.IPContext;
import com.checknetwork.app.core.IPContextParserTest;
import java.io.*;
import java.net.*;
import java.util.*;
import java.util.concurrent.*;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class IPContextTransportBoundsTest {
    static byte[] wire(String id) throws Exception {
        org.json.JSONArray rows = IPContextParserTest.corpus().getJSONArray("cases");
        for (int i = 0; i < rows.length(); i++) if (id.equals(rows.getJSONObject(i).getString("id"))) return Base64.getDecoder().decode(rows.getJSONObject(i).getString("wire_base64"));
        throw new AssertionError(id);
    }
    @Test public void exactBytesAndLimitPlusOneAndMalformedBytesUseOriginalAdmission() throws Exception {
        for (String id : new String[]{"wire-exact-16384", "wire-over-16384", "invalid-utf8", "duplicate-root-key", "version-rounded-fraction", "unpaired-surrogate", "foreign-root-address"}) {
            Fake c = new Fake(wire(id));
            IPContextTransport.Call call = transport(c, () -> 0).newCall("1.1.1.1");
            if (id.equals("wire-exact-16384")) assertEquals("1.1.1.1", call.execute().address());
            else {
                IPContextTransport.ContextException failure = assertThrows(id, IPContextTransport.ContextException.class, call::execute);
                assertEquals(id.equals("wire-over-16384") ? IPContextTransport.Failure.TOO_LARGE : IPContextTransport.Failure.INVALID, failure.failure());
            }
            assertEquals(1, c.disconnects); assertTrue(c.readBytes <= 16385);
        }
    }
    @Test public void decodedCredentialEchoIsRejectedWithoutErrorBodyReflection() throws Exception {
        byte[] bytes = new String(wire("ipv4"), java.nio.charset.StandardCharsets.UTF_8).replace("Organization", "current-test-token").getBytes(java.nio.charset.StandardCharsets.UTF_8);
        Fake c = new Fake(bytes);
        IPContextTransport.ContextException e = assertThrows(IPContextTransport.ContextException.class, () -> transport(c, () -> 0).newCall("1.1.1.1").execute());
        assertEquals(IPContextTransport.Failure.INVALID,e.failure()); assertFalse(e.toString().contains("current-test-token"));
    }
    @Test public void oversizedContentLengthRejectsBeforeBodyAcquisition() throws Exception {
        Fake c = new Fake(wire("ipv4")); c.length = 16385;
        assertEquals(IPContextTransport.Failure.TOO_LARGE, assertThrows(IPContextTransport.ContextException.class, () -> transport(c, () -> 0).newCall("1.1.1.1").execute()).failure());
        assertEquals(0, c.inputOpens);
    }
    @Test public void redirectAnd404NeverReadBodiesFollowOrRetry() throws Exception {
        for (int code : new int[]{301,302,303,307,308,404,401,429,500,503}) {
            Fake c = new Fake(new byte[0]); c.code = code;
            assertEquals(code == 404 ? IPContextTransport.Failure.UNSUPPORTED : IPContextTransport.Failure.UNAVAILABLE,
                    assertThrows(IPContextTransport.ContextException.class, () -> transport(c, () -> 0).newCall("1.1.1.1").execute()).failure());
            assertFalse(c.getInstanceFollowRedirects()); assertEquals(0, c.inputOpens); assertEquals(0, c.errorOpens); assertEquals(1, c.responses);
        }
    }
    @Test public void exactPostAuthAndNoRequestForPrivateUnknownNoncanonicalTokens() throws Exception {
        Fake c = new Fake(wire("ipv4"));
        IPContextTransport t = transport(c, () -> 0);
        for (String address : new String[]{"local","unknown","Unknown","127.0.0.1","10.1.2.3","192.0.2.1","01.1.1.1","1.1.1.1 ","::ffff:1.1.1.1","FE80::1","example.com"}) assertThrows(IllegalArgumentException.class, () -> t.newCall(address));
        assertEquals(0, c.responses);
        IPContextTransport.Call call = t.newCall("1.1.1.1"); call.execute();
        assertEquals("https://api.example.test/api/v1/ip-context", c.opened.toString());
        assertEquals("POST", c.getRequestMethod()); assertEquals("Bearer current-test-token", c.getRequestProperty("Authorization"));
        assertEquals("{\"address\":\"1.1.1.1\"}", c.output.toString("UTF-8"));
        assertThrows(IPContextTransport.ContextException.class, call::execute); assertEquals(1, c.responses);
    }
    @Test public void capturedTotalDeadlineIncludesAdmissionConnectReadAndParseNotTenSecondsPerStage() throws Exception {
        long[] time = {0}; Fake c = new Fake(wire("ipv4"));
        IPContextTransport t = transport(c, () -> time[0]); IPContextTransport.Call call = t.newCall("1.1.1.1");
        time[0] = TimeUnit.SECONDS.toNanos(4);
        c.onResponse = () -> time[0] = TimeUnit.SECONDS.toNanos(8);
        c.onRead = () -> time[0] = TimeUnit.SECONDS.toNanos(10);
        assertEquals(IPContextTransport.Failure.TIMEOUT, assertThrows(IPContextTransport.ContextException.class, call::execute).failure());
        awaitRetirement(call);
        assertEquals(6000, c.getConnectTimeout()); assertTrue(c.getReadTimeout() <= 2000); assertEquals(1, c.disconnects);
        Fake late = new Fake(wire("ipv4")); time[0] = 0;
        IPContextTransport.Call afterOpen = new IPContextTransport(ApiConnectionConfig.create("https://api.example.test", false, null), url -> {
            time[0] = TimeUnit.SECONDS.toNanos(10); return late;
        }, () -> time[0]).newCall("1.1.1.1");
        assertEquals(IPContextTransport.Failure.TIMEOUT, assertThrows(IPContextTransport.ContextException.class, afterOpen::execute).failure());
        awaitRetirement(afterOpen);
        assertEquals(0, late.responses); assertEquals(1, late.disconnects);
    }
    private static void awaitRetirement(IPContextTransport.Call call) throws Exception {
        long end = System.nanoTime() + TimeUnit.SECONDS.toNanos(3);
        while (!call.isQuiescent() && System.nanoTime() < end) Thread.sleep(2);
        assertTrue(call.isQuiescent());
    }
    @Test public void cancellationDisconnectsActiveStreamExactlyOnce() throws Exception {
        Fake c = new Fake(wire("ipv4")); CountDownLatch entered = new CountDownLatch(1), release = new CountDownLatch(1);
        c.onRead = () -> { entered.countDown(); try { assertTrue(release.await(3, TimeUnit.SECONDS)); } catch (InterruptedException e) { throw new AssertionError(e); } };
        IPContextTransport.Call call = transport(c, System::nanoTime).newCall("1.1.1.1");
        ExecutorService worker = Executors.newSingleThreadExecutor();
        try {
            Future<IPContextTransport.Failure> future = worker.submit(() -> { try { call.execute(); throw new AssertionError(); } catch (IPContextTransport.ContextException e) { return e.failure(); } });
            assertTrue(entered.await(3, TimeUnit.SECONDS));
            long retiredBy = System.nanoTime() + TimeUnit.SECONDS.toNanos(3);
            call.cancel(); call.cancel(); release.countDown();
            assertEquals(IPContextTransport.Failure.CANCELLED, future.get(3, TimeUnit.SECONDS));
            // Cancellation delivery deliberately precedes physical retirement. Observe
            // that separate receipt before reading the cleanup counter, in the same budget.
            while (!call.isQuiescent() && System.nanoTime() < retiredBy) Thread.sleep(2);
            assertTrue(call.isQuiescent()); assertEquals(1, c.disconnects);
        } finally { release.countDown(); worker.shutdownNow(); assertTrue(worker.awaitTermination(3, TimeUnit.SECONDS)); }
    }
    private static IPContextTransport transport(Fake c, OperationDeadline.Clock clock) {
        return new IPContextTransport(ApiConnectionConfig.create("https://api.example.test", false, "current-test-token"), url -> { c.opened = url; return c; }, clock);
    }
    static final class Fake extends HttpURLConnection {
        final byte[] bytes; final ByteArrayOutputStream output = new ByteArrayOutputStream();
        int code = 200, disconnects, responses, inputOpens, errorOpens, readBytes;
        long length = -1; URL opened; Runnable onRead = () -> {}, onResponse = () -> {};
        Fake(byte[] bytes) throws Exception { super(new URL("https://api.example.test")); this.bytes = bytes; }
        @Override public void connect() {}
        @Override public boolean usingProxy() { return false; }
        @Override public void disconnect() { disconnects++; }
        @Override public OutputStream getOutputStream() { return output; }
        @Override public int getResponseCode() { responses++; onResponse.run(); return code; }
        @Override public long getContentLengthLong() { return length; }
        @Override public InputStream getErrorStream() { errorOpens++; throw new AssertionError("error body must not be read"); }
        @Override public InputStream getInputStream() {
            inputOpens++;
            return new ByteArrayInputStream(bytes) {
                @Override public synchronized int read(byte[] buffer, int offset, int count) { int n = super.read(buffer, offset, count); if (n > 0) readBytes += n; onRead.run(); return n; }
            };
        }
    }
}
