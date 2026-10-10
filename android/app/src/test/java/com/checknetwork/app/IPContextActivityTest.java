package com.checknetwork.app;

import static org.junit.Assert.*;
import android.view.View;
import android.view.ViewGroup;
import android.widget.EditText;
import android.widget.TextView;
import com.checknetwork.app.core.GeoDetailsTest;
import com.checknetwork.app.core.ReportParser;
import com.checknetwork.app.state.RequestCoordinator;
import java.util.ArrayList;
import java.util.List;
import org.junit.After;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class IPContextActivityTest {
    private ActivityController<MainActivity> controller;
    private final List<RequestCoordinator.Callback> diagnostics = new ArrayList<>();
    @After public void cleanup() {
        if (controller != null) controller.pause().stop().destroy();
        MainActivity.resetSessionFactoryForTests();
    }
    @Test public void readyReportExposesExplicitNativeContextActionWithoutRerunningDiagnostics() throws Exception {
        MainActivity activity = ready();
        ViewGroup results = activity.findViewById(R.id.results);
        View toggle = results.findViewWithTag("ip_context_toggle");
        assertNotNull("ready Activity must expose IP-context disclosure", toggle);
        assertNull(results.findViewWithTag("ip_context_query"));
        toggle.performClick();
        View query = results.findViewWithTag("ip_context_query");
        assertNotNull("explicit native lookup must be reachable", query);
        assertTrue(query.isShown());
        assertTrue(text(results).contains("8.8.8.8"));
        assertEquals(1, diagnostics.size());
        assertTrue(activity.findViewById(R.id.share).isEnabled());
    }
    @Test public void reflectedCredentialInContextNeverReachesActivityOrExports() throws Exception {
        com.checknetwork.app.core.IPContext value = IPContextSessionTest.firstContext();
        IPContextSession context = new IPContextSession(Runnable::run, Runnable::run,
                (config,address,deadline) -> new com.checknetwork.app.network.IPContextTransport.Request() {
                    public com.checknetwork.app.core.IPContext execute() { return value; }
                    public void cancel() {}
                }, System::nanoTime, () -> java.time.Instant.parse("2026-01-02T03:04:06.006Z"));
        MainActivity.setContextSessionFactoryForTests(dispatcher -> context);
        MainActivity activity = ready("Organization");
        ViewGroup results = activity.findViewById(R.id.results);
        results.findViewWithTag("ip_context_toggle").performClick();
        results.findViewWithTag("ip_context_next").performClick();
        results.findViewWithTag("ip_context_query").performClick();
        assertFalse("credentials must not be reflected in provider detail", text(results).contains("Organization"));
        assertTrue(activity.findViewById(R.id.share).isEnabled());
        assertTrue(activity.findViewById(R.id.share_raw).isEnabled());
        assertEquals(1, diagnostics.size());
    }
    private MainActivity ready() throws Exception { return ready(null); }
    private MainActivity ready(String credential) throws Exception {
        DiagnosticsSession session = new DiagnosticsSession(new RequestCoordinator(request -> new RequestCoordinator.CancellableCall() {
            public void start(RequestCoordinator.Callback callback) { diagnostics.add(callback); }
            public void cancel() {}
        }), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> session);
        controller = Robolectric.buildActivity(MainActivity.class).setup().visible();
        MainActivity activity = controller.get();
        ((EditText) activity.findViewById(R.id.api_url)).setText("https://api.example.test");
        if (credential != null) {
            ((android.widget.CheckBox) activity.findViewById(R.id.auth_enabled)).setChecked(true);
            ((EditText) activity.findViewById(R.id.bearer)).setText(credential);
        }
        activity.findViewById(R.id.run).performClick();
        String raw = GeoDetailsTest.fixture("geo-details-rich-compact-report.json");
        diagnostics.get(0).onSuccess(raw, ReportParser.parse(raw));
        assertEquals(MainActivity.PresentationPhase.RENDERED, activity.presentationPhaseForTests());
        return activity;
    }
    static String text(View view) {
        if (view.getVisibility() != View.VISIBLE) return "";
        StringBuilder result = new StringBuilder();
        if (view instanceof TextView text) result.append(text.getText()).append('\n');
        if (view instanceof ViewGroup group) for (int i = 0; i < group.getChildCount(); i++) result.append(text(group.getChildAt(i)));
        return result.toString();
    }
}
