package com.checknetwork.app;

import static org.junit.Assert.*;
import com.checknetwork.app.core.*;
import com.checknetwork.app.network.*;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class IPContextOwnershipTest {
    static final Instant NOW = Instant.parse("2026-01-02T03:04:06.006Z");
    static IPContext context(String address) throws Exception {
        JSONObject row = IPContextParserTest.corpus().getJSONArray("cases").getJSONObject(0);
        String wire = new String(Base64.getDecoder().decode(row.getString("wire_base64")), java.nio.charset.StandardCharsets.UTF_8).replace("1.1.1.1", address);
        return IPContextParser.parse(wire.getBytes(java.nio.charset.StandardCharsets.UTF_8), address);
    }
    static Report report() throws Exception { return ReportParser.parse(GeoDetailsTest.fixture("geo-details-rich-compact-report.json")); }
    static final class Harness implements AutoCloseable {
        final ArrayDeque<Runnable> work = new ArrayDeque<>(), deliveries = new ArrayDeque<>();
        final List<String> calls = new ArrayList<>();
        final List<IPContextSession.Snapshot> visible = new ArrayList<>();
        Instant now = NOW; long nanos; int cancels; boolean fail;
        final IPContextSession session = new IPContextSession(deliveries::add, work::add, (config,address,deadline) -> {
            calls.add(address);
            return new IPContextTransport.Request() {
                public IPContext execute() throws IPContextTransport.ContextException {
                    if (fail) throw new IPContextTransport.ContextException(IPContextTransport.Failure.UNSUPPORTED);
                    try { return context(address); } catch (Exception e) { throw new AssertionError(e); }
                }
                public void cancel() { cancels++; }
            };
        }, () -> nanos, () -> now);
        final Object owner = new Object();
        Harness() throws Exception { session.bind(report(), ApiConnectionConfig.create("https://api.example.test", false, null)); session.attach(owner, visible::add); drain(); }
        void drain() { IPContextSessionTest.drain(deliveries); }
        void complete() { work.remove().run(); drain(); }
        public void close() { session.destroy(); }
    }
    @Test public void explicitOnlyCacheAndExpiryReportBaseAuthIsolation() throws Exception {
        try (Harness h = new Harness()) {
            h.session.select("1.1.1.1"); h.drain(); assertTrue(h.calls.isEmpty());
            h.session.lookup(); h.complete(); assertNotNull(h.session.state().context()); assertFalse(h.session.state().appCache());
            h.session.lookup(); h.drain(); assertEquals(1, h.calls.size()); assertTrue(h.session.state().appCache());
            h.now = h.session.state().context().expiresAt(); assertNull("expired metadata is not a current app-cache display",h.session.state().context()); h.session.lookup(); h.complete(); assertEquals(2, h.calls.size());
            for (ApiConnectionConfig config : new ApiConnectionConfig[]{ApiConnectionConfig.create("https://api.example.test", false, "new-auth"), ApiConnectionConfig.create("https://other.example.test", false, "new-auth")}) {
                h.session.bind(report(), config); assertEquals(0, h.session.cacheSizeForTests());
                h.session.select("1.1.1.1"); assertNull(h.session.state().context());
                h.session.lookup(); h.complete();
            }
            assertEquals(4, h.calls.size());
        }
    }
    @Test public void delayedOldSuccessErrorAndFinallyCannotOverwriteNewSelectionOrClearBusy() throws Exception {
        for (boolean fail : new boolean[]{false,true}) try (Harness h = new Harness()) {
            h.fail = fail; h.session.select("1.1.1.1"); h.session.lookup(); h.work.remove().run();
            h.session.select("8.8.8.8"); h.fail = false; h.session.lookup();
            h.drain(); assertTrue(h.session.state().loading()); assertEquals("8.8.8.8", h.session.state().address()); assertNull(h.session.state().context());
            h.complete(); assertEquals("8.8.8.8", h.session.state().context().address()); assertEquals(1, h.session.cacheSizeForTests());
        }
    }
    @Test public void cancellationKeepsPhysicalWorkChargedAndAdmissionDoesNotQueue() throws Exception {
        try (Harness h = new Harness()) {
            h.session.select("1.1.1.1"); h.session.lookup();
            h.session.select("8.8.8.8"); h.session.lookup();
            assertEquals(1, h.calls.size()); assertEquals(1, h.work.size()); assertEquals(1, h.cancels);
            h.complete(); assertNull(h.session.state().context()); assertEquals(0, h.session.cacheSizeForTests());
            h.session.lookup(); h.complete(); assertEquals("8.8.8.8", h.session.state().context().address());
        }
    }
    @Test public void detachDoesNotRetainListenerAndNewAttachmentReceivesOnlyCurrentState() throws Exception {
        try (Harness h = new Harness()) {
            h.session.select("1.1.1.1"); h.session.lookup(); h.work.remove().run();
            h.session.detach(h.owner);
            java.lang.reflect.Field listener = IPContextSession.class.getDeclaredField("listener"); listener.setAccessible(true); assertNull(listener.get(h.session));
            int oldCount = h.visible.size(); List<IPContextSession.Snapshot> current = new ArrayList<>();
            h.session.attach(new Object(), current::add); h.drain();
            assertEquals(oldCount, h.visible.size()); assertEquals("1.1.1.1", current.get(current.size()-1).context().address());
        }
    }
    @Test public void pendingSuccessAfterReportAuthOrBaseReplacementIsRevoked() throws Exception {
        try (Harness h = new Harness()) {
            for (String base : new String[]{"https://api.example.test","https://second.example.test"}) {
                h.session.select("1.1.1.1"); h.session.lookup(); h.work.remove().run();
                h.session.bind(report(), ApiConnectionConfig.create(base, false, "rotated")); h.drain();
                assertNull(h.session.state().context()); assertNull(h.session.state().address()); assertEquals(0, h.session.cacheSizeForTests());
            }
        }
    }
    @Test public void invalidSelectionNeverAcquiresAndDestroyRevokesQueuedDelivery() throws Exception {
        try (Harness h = new Harness()) {
            for (String invalid : new String[]{"127.0.0.1", "unknown", "4.4.4.4", "01.1.1.1"}) { h.session.select(invalid); h.session.lookup(); }
            assertTrue(h.calls.isEmpty());
            h.session.select("1.1.1.1"); h.session.lookup(); h.work.remove().run();
            int before = h.visible.size(); h.session.destroy(); h.drain(); assertEquals(before, h.visible.size()); assertNull(h.session.state().context());
        }
    }
    @Test public void lruKeepsAtMost64AndEvictsOldestWithinOneReport() throws Exception {
        try (Harness h = new Harness()) {
            Report large = ReportParser.parse(GeoDetailsTest.fixture("geo-details-truncated-compact-report.json"));
            h.session.bind(large, ApiConnectionConfig.create("https://api.example.test", false, null));
            List<String> addresses = IPContextAddresses.from(large).addresses(); assertTrue(addresses.size() > 64);
            for (String address : addresses.subList(0,65)) { h.session.select(address); h.session.lookup(); h.complete(); assertTrue(h.session.cacheSizeForTests() <= 64); }
            assertEquals(64, h.session.cacheSizeForTests());
            h.session.select(addresses.get(0)); assertNull(h.session.state().context()); h.session.lookup(); h.complete(); assertEquals(66, h.calls.size());
        }
    }
}
