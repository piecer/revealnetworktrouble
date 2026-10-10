package com.checknetwork.app;

import static org.junit.Assert.*;
import com.checknetwork.app.core.GeoDetailsTest;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportParser;
import com.checknetwork.app.network.ApiConnectionConfig;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class IPContextSessionTest {
    @Test public void deadlineIncludesDelayedFinalListenerPublicationAndDoesNotCacheLateSuccess() throws Exception {
        java.util.ArrayDeque<Runnable> deliveries = new java.util.ArrayDeque<>();
        long[] nanos = {0};
        java.time.Instant fixed = java.time.Instant.parse("2026-01-02T03:04:06.006Z");
        com.checknetwork.app.core.IPContext value = firstContext();
        IPContextSession session = new IPContextSession(deliveries::add, Runnable::run,
                (config,address,deadline) -> new com.checknetwork.app.network.IPContextTransport.Request() {
                    public com.checknetwork.app.core.IPContext execute() { return value; }
                    public void cancel() {}
                }, () -> nanos[0], () -> fixed);
        try {
            Report report = ReportParser.parse(GeoDetailsTest.fixture("geo-details-rich-compact-report.json"));
            session.bind(report, ApiConnectionConfig.create("https://api.example.test", false, null));
            session.select("1.1.1.1");
            java.util.List<IPContextSession.Snapshot> visible = new java.util.ArrayList<>();
            session.attach(new Object(), visible::add); drain(deliveries);
            session.lookup();
            // Loading and completion are delivered, but the final view publication is deliberately delayed.
            deliveries.remove().run(); deliveries.remove().run();
            assertFalse(deliveries.isEmpty());
            nanos[0] = java.util.concurrent.TimeUnit.SECONDS.toNanos(10);
            drain(deliveries);
            IPContextSession.Snapshot last = visible.get(visible.size() - 1);
            assertNull("post-deadline view publication must not expose success", last.context());
            assertEquals(com.checknetwork.app.network.IPContextTransport.Failure.TIMEOUT, last.failure());
            assertEquals(0, session.cacheSizeForTests());
        } finally { session.destroy(); }
    }
    static com.checknetwork.app.core.IPContext firstContext() throws Exception {
        org.json.JSONObject row = com.checknetwork.app.core.IPContextParserTest.corpus().getJSONArray("cases").getJSONObject(0);
        return com.checknetwork.app.core.IPContextParser.parse(java.util.Base64.getDecoder().decode(row.getString("wire_base64")), "1.1.1.1");
    }
    static void drain(java.util.ArrayDeque<Runnable> queue) { while (!queue.isEmpty()) queue.remove().run(); }
    @Test public void separateOwnerSelectsObservedAddressWithoutDiagnosticStateMutation() throws Exception {
        Class<?> type;
        try { type = Class.forName("com.checknetwork.app.IPContextSession"); }
        catch (ClassNotFoundException missing) { fail("independent IP-context session missing"); return; }
        Report report = ReportParser.parse(GeoDetailsTest.fixture("geo-details-rich-compact-report.json"));
        Object session = type.getMethod("create", DiagnosticsSession.Dispatcher.class).invoke(null, (DiagnosticsSession.Dispatcher) Runnable::run);
        try {
            type.getMethod("bind", Report.class, ApiConnectionConfig.class).invoke(session, report, ApiConnectionConfig.create("https://api.example.test", false, null));
            type.getMethod("select", String.class).invoke(session, "8.8.8.8");
            Object state = type.getMethod("state").invoke(session);
            assertEquals("8.8.8.8", state.getClass().getMethod("address").invoke(state));
            assertEquals(false, state.getClass().getMethod("loading").invoke(state));
            assertNull(state.getClass().getMethod("context").invoke(state));
        } finally { type.getMethod("destroy").invoke(session); }
    }
}
