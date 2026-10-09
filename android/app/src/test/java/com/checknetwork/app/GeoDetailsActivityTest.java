package com.checknetwork.app;

import static org.junit.Assert.*;

import android.content.Intent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.TextView;
import com.checknetwork.app.core.GeoDetails;
import com.checknetwork.app.core.GeoDetailsTest;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportParser;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.state.RequestCoordinator;
import java.util.ArrayList;
import java.util.List;
import org.junit.After;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class GeoDetailsActivityTest {
    private ActivityController<MainActivity> controller;
    private final List<Call> calls = new ArrayList<>();

    @After public void cleanup() {
        if (controller != null) controller.pause().stop().destroy();
        MainActivity.resetSessionFactoryForTests();
    }

    @Test public void explicitDisclosureShowsLocationIspPostalTimezoneAndTruthfulProvenance() throws Exception {
        String raw = GeoDetailsTest.fixture("geo-details-rich-compact-report.json");
        MainActivity activity = ready(raw);
        ViewGroup results = activity.findViewById(R.id.results);
        assertFalse(visibleText(results).contains("Different access ISP"));
        Button toggle = results.findViewWithTag("geo_details_toggle");
        assertNotNull("supplemental metadata must be visibly reachable", toggle);
        assertNull(results.findViewWithTag("geo_details_body"));
        toggle.performClick();
        TextView body = results.findViewWithTag("geo_details_body");
        assertNotNull(body);
        assertEquals(View.VISIBLE, body.getVisibility());
        assertTrue(body.isShown());
        assertTrue(body.getText().toString().contains("Text without coordinates"));
        assertTrue(visibleText(results).contains("does not certify legacy coordinates or ASN"));
        results.findViewWithTag("geo_details_next").performClick();
        assertTrue(body.getText().toString().contains("2606:4700:4700::1111"));
        results.findViewWithTag("geo_details_next").performClick();
        String text = body.getText().toString();
        for (String fact : new String[]{"8.8.8.8", "서울 <>&", "😀", "ISP (not AS organization): Different access ISP",
                "Postal: 04527", "Timezone: Asia/Seoul", "Network domain: example.net", "Provider: ipwho.is",
                "Source: cache", "Fetched at (local lookup): 2026-10-09T01:02:03.456Z",
                "Cache expires at: 2026-10-10T01:02:03.456Z", "Provider database update: not supplied"}) {
            assertTrue(fact, text.contains(fact));
        }
        assertFalse(((Button) results.findViewWithTag("geo_details_next")).isEnabled());
        assertTrue(text.length() <= 4096);
        // Human export remains privacy-minimized even while raw details are expanded.
        activity.findViewById(R.id.share).performClick();
        Intent chooser = Shadows.shadowOf(activity).getNextStartedActivity();
        assertNotNull(chooser);
        Intent send = chooser.getParcelableExtra(Intent.EXTRA_INTENT);
        String human = send.getStringExtra(Intent.EXTRA_TEXT);
        assertNotNull(human);
        for (String identifier : new String[]{"Different access ISP", "04527", "Asia/Seoul", "example.net", "ipwho.is"})
            assertFalse(identifier, human.contains(identifier));
        toggle.performClick();
        assertNull("collapse must unmount entry text", results.findViewWithTag("geo_details_body"));
    }

    @Test public void fiveHundredEntriesStayReachableWithOneBoundedDetailAndNoStaleOwner() throws Exception {
        String raw = GeoDetailsTest.fixture("geo-details-truncated-compact-report.json");
        MainActivity activity = ready(raw);
        ViewGroup results = activity.findViewById(R.id.results);
        int initial = count(results);
        Button toggle = results.findViewWithTag("geo_details_toggle");
        assertNotNull(toggle);
        toggle.performClick();
        Report report = ReportParser.parse(raw);
        assertEquals(500, report.geoDetails().orElseThrow().entries().size());
        for (GeoDetails.Entry entry : report.geoDetails().orElseThrow().entries()) {
            TextView body = results.findViewWithTag("geo_details_body");
            assertTrue(body.isShown());
            assertTrue(body.getText().toString().contains("Address: " + entry.address() + "\n"));
            assertTrue(body.getText().length() <= 4096);
            assertTrue("bounded mounted view count", count(results) <= initial + 6);
            results.findViewWithTag("geo_details_next").performClick();
        }
        assertTrue(visibleText(results).contains("Omitted: 1"));
        View oldNext = results.findViewWithTag("geo_details_next");
        activity.findViewById(R.id.run).performClick();
        String legacy = GeoDetailsTest.fixture("enrichment-upstream-report.json");
        calls.get(1).callback.onSuccess(legacy, ReportParser.parse(legacy));
        String current = visibleText(results);
        oldNext.performClick(); toggle.performClick();
        assertEquals(current, visibleText(results));
        assertNull(results.findViewWithTag("geo_details_toggle"));
    }

    @Test public void longIdentifiersAndUnknownFieldsRemainCompleteAndMissingTimesStayUnknown() throws Exception {
        org.json.JSONObject corpus = new org.json.JSONObject(GeoDetailsTest.fixture("geo-details-corpus.json"));
        String longIsp = "Long-ISP-" + "x".repeat(247);
        String sidecar = corpus.getJSONObject("bases").getString("minimal")
                .replace("\"city\":\"city\"", "\"isp\":\"" + longIsp + "\",\"timezone\":\"" + "t".repeat(256) + "\"");
        MainActivity activity = ready(corpus.getString("report_prefix") + sidecar + corpus.getString("report_suffix"));
        ViewGroup results = activity.findViewById(R.id.results);
        results.findViewWithTag("geo_details_toggle").performClick();
        TextView body = results.findViewWithTag("geo_details_body");
        assertTrue(body.isShown());
        String text = body.getText().toString();
        assertTrue(text.contains(longIsp));
        assertTrue(text.contains("t".repeat(256)));
        assertTrue(text.contains("City: not supplied"));
        assertTrue(text.contains("Fetched at (local lookup): not supplied"));
        assertTrue(text.contains("Cache expires at: not supplied"));
        assertFalse(text.contains("Latitude"));
        assertFalse(text.contains("Longitude"));
        assertTrue(text.length() <= 4096);
        assertFalse(text.contains("…"));
    }

    @Test public void emptySupplementalEnvelopeDisplaysExplicitAbsenceWithoutInventedPosition() throws Exception {
        MainActivity activity = ready(GeoDetailsTest.fixture("geo-details-empty-compact-report.json"));
        ViewGroup results = activity.findViewById(R.id.results);
        results.findViewWithTag("geo_details_toggle").performClick();
        assertTrue(visibleText(results).contains("No supplemental metadata entries were returned."));
        assertNull(results.findViewWithTag("geo_details_body"));
        assertNull(results.findViewWithTag("geo_details_next"));
    }

    private MainActivity ready(String raw) {
        DiagnosticsSession session = new DiagnosticsSession(new RequestCoordinator(request -> {
            Call call = new Call(); calls.add(call); return call;
        }), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> session);
        controller = Robolectric.buildActivity(MainActivity.class).setup().visible();
        MainActivity activity = controller.get();
        ((EditText) activity.findViewById(R.id.api_url)).setText("https://api.example.test");
        activity.findViewById(R.id.run).performClick();
        calls.get(0).callback.onSuccess(raw, ReportParser.parse(raw));
        assertEquals(MainActivity.PresentationPhase.RENDERED, activity.presentationPhaseForTests());
        return activity;
    }

    private static int count(View view) {
        int result = 1;
        if (view instanceof ViewGroup group) for (int i = 0; i < group.getChildCount(); i++) result += count(group.getChildAt(i));
        return result;
    }
    private static String visibleText(View view) {
        if (view.getVisibility() != View.VISIBLE) return "";
        StringBuilder out = new StringBuilder();
        if (view instanceof TextView text) out.append(text.getText()).append('\n');
        if (view instanceof ViewGroup group) for (int i = 0; i < group.getChildCount(); i++) out.append(visibleText(group.getChildAt(i)));
        return out.toString();
    }
    private static final class Call implements RequestCoordinator.CancellableCall {
        RequestCoordinator.Callback callback;
        public void start(RequestCoordinator.Callback callback) { this.callback = callback; }
        public void cancel() {}
    }
}
