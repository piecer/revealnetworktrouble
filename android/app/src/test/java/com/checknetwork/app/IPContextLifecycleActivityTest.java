package com.checknetwork.app;

import static org.junit.Assert.*;
import android.content.Intent;
import android.content.res.Configuration;
import android.os.Bundle;
import android.view.View;
import android.view.ViewGroup;
import android.widget.*;
import com.checknetwork.app.core.*;
import com.checknetwork.app.network.*;
import com.checknetwork.app.state.*;
import java.util.*;
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
public final class IPContextLifecycleActivityTest {
    ActivityController<MainActivity> controller;
    final ArrayDeque<Runnable> workers = new ArrayDeque<>(), deliveries = new ArrayDeque<>();
    final List<String> requests = new ArrayList<>(); final List<ApiConnectionConfig> configs = new ArrayList<>();
    final List<RequestCoordinator.Callback> reports = new ArrayList<>();
    IPContextSession context; DiagnosticsSession diagnostics; IPContextTransport.Failure failure;
    long nanos; String raw; IPContext suppliedContext;
    @After public void cleanup() {
        if (controller != null) controller.pause().stop().destroy();
        MainActivity.resetSessionFactoryForTests();
    }
    MainActivity ready(String fixture) throws Exception {
        context = new IPContextSession(deliveries::add, workers::add, (config,address,deadline) -> {
            requests.add(address); configs.add(config);
            return new IPContextTransport.Request() {
                public IPContext execute() throws IPContextTransport.ContextException {
                    if (failure != null) throw new IPContextTransport.ContextException(failure);
                    if (suppliedContext != null) return suppliedContext;
                    try { return IPContextOwnershipTest.context(address); } catch (Exception e) { throw new AssertionError(e); }
                }
                public void cancel() {}
            };
        }, () -> nanos, () -> IPContextOwnershipTest.NOW);
        diagnostics = new DiagnosticsSession(new RequestCoordinator(request -> new RequestCoordinator.CancellableCall() {
            public void start(RequestCoordinator.Callback callback) { reports.add(callback); }
            public void cancel() {}
        }), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> diagnostics);
        MainActivity.setContextSessionFactoryForTests(dispatcher -> context);
        controller = Robolectric.buildActivity(MainActivity.class).setup().visible();
        MainActivity activity = controller.get(); ((EditText) activity.findViewById(R.id.api_url)).setText("https://api.example.test");
        raw = fixture.equals("large-full") ? IPContextAddressesTest.largeFullReport() : GeoDetailsTest.fixture(fixture); report(activity); drain(); return activity;
    }
    void report(MainActivity activity) {
        activity.findViewById(R.id.run).performClick(); reports.get(reports.size()-1).onSuccess(raw, ReportParser.parse(raw));
        assertEquals(MainActivity.PresentationPhase.RENDERED, activity.presentationPhaseForTests());
    }
    ViewGroup results() { return controller.get().findViewById(R.id.results); }
    View tag(String name) { View view = results().findViewWithTag(name); assertNotNull(name, view); return view; }
    void open() { tag("ip_context_toggle").performClick(); drain(); }
    void drain() { IPContextSessionTest.drain(deliveries); }
    void finish() { workers.remove().run(); drain(); }
    String text() { return IPContextActivityTest.text(results()); }
    void reportStillUsable() {
        assertEquals(RequestState.Phase.READY, diagnostics.state().phase()); assertEquals(raw, diagnostics.state().rawJson().orElseThrow());
        assertEquals(MainActivity.PresentationPhase.RENDERED, controller.get().presentationPhaseForTests());
        assertTrue(controller.get().findViewById(R.id.share).isEnabled()); assertTrue(controller.get().findViewById(R.id.share_raw).isEnabled());
    }
    @Test public void explicitLookupRendersAllSectionsAndPreservesReportAndActualHumanShareIntent() throws Exception {
        MainActivity activity = ready("geo-details-rich-compact-report.json"); open();
        tag("ip_context_next").performClick(); drain(); assertTrue(requests.isEmpty());
        tag("ip_context_query").performClick(); assertEquals(List.of("1.1.1.1"), requests); assertFalse(tag("ip_context_query").isEnabled());
        reportStillUsable(); finish();
        for (String fact : new String[]{"one.example", "confirmed", "Network <>&", "ALLOCATED", "AU", "Organization", "2019-01-01T00:00:00.000Z", "2023-12-31T23:00:00.000Z", "1.1.1.1/32", "13335", "valid", "system_resolver", "rdap", "ripe_ris", "ripe_rpki", "2026-01-02T03:09:05.006Z", "Naming consistency is not identity", "Registration is not necessarily", "8-hour dump", "not a health or attack verdict"}) assertTrue(fact, text().contains(fact));
        assertEquals(0, ((TextView) tag("ip_context_body")).getAutoLinkMask());
        tag("ip_context_query").performClick(); drain(); assertEquals(1, requests.size()); assertTrue(text().contains("app memory cache"));
        reportStillUsable(); activity.findViewById(R.id.share).performClick();
        Intent send = Shadows.shadowOf(activity).getNextStartedActivity().getParcelableExtra(Intent.EXTRA_INTENT);
        assertEquals(ReportMarkdownExporter.export(diagnostics.state().report().orElseThrow()), send.getStringExtra(Intent.EXTRA_TEXT));
        assertFalse(send.getStringExtra(Intent.EXTRA_TEXT).contains("one.example"));
        assertEquals(1, reports.size());
        tag("ip_context_toggle").performClick(); assertNull(results().findViewWithTag("ip_context_body"));
    }
    @Test @Config(sdk = 35, qualifiers = "ko-rKR-w375dp-h812dp")
    public void koreanNativeActionAndSectionCaveatsAreReachable() throws Exception {
        ready("geo-details-rich-compact-report.json"); open(); tag("ip_context_next").performClick(); drain();
        assertEquals("IP 추가 정보 조회", ((TextView)tag("ip_context_query")).getText().toString());
        tag("ip_context_query").performClick(); finish();
        for (String fact : new String[]{"이름의 일치는", "등록 정보가", "8시간", "공격 여부", "1.1.1.1", "one.example"}) assertTrue(fact,text().contains(fact));
        reportStillUsable();
    }
    @Test public void partialComponentFailuresKeepIndependentFactsAndExplicitValidityLabels() throws Exception {
        MainActivity activity = ready("geo-details-rich-compact-report.json");
        org.json.JSONArray rows = IPContextParserTest.corpus().getJSONArray("cases");
        for (String id : new String[]{"reverse-timeout","registration-rate_limited","routing-unavailable","rpki-unavailable","validity-unknown"}) {
            for (int i=0;i<rows.length();i++) if(id.equals(rows.getJSONObject(i).getString("id"))) {
                suppliedContext = IPContextParser.parse(Base64.getDecoder().decode(rows.getJSONObject(i).getString("wire_base64")), "1.1.1.1"); break;
            }
            report(activity); drain(); open(); tag("ip_context_next").performClick(); drain(); tag("ip_context_query").performClick(); finish();
            assertTrue(text().contains("component failures are shown separately"));
            if (!id.startsWith("reverse")) assertTrue(id,text().contains("one.example"));
            if (!id.startsWith("registration")) assertTrue(id,text().contains("Organization"));
            if (!id.startsWith("routing")) assertTrue(id,text().contains("13335"));
            assertTrue(id,text().contains(id.substring(id.indexOf('-')+1)));
            reportStillUsable();
        }
    }
    @Test public void finalActivityDestructionRevokesQueuedWorkAndListenerWithoutRetainingActivity() throws Exception {
        MainActivity activity = ready("geo-details-rich-compact-report.json"); open(); tag("ip_context_query").performClick();
        View oldQuery = tag("ip_context_query");
        controller.pause().stop().destroy(); controller = null;
        workers.remove().run(); drain(); oldQuery.performClick();
        assertTrue(activity.isDestroyed()); assertEquals(1,requests.size()); assertNull(context.state().context());
        java.lang.reflect.Field field = IPContextSession.class.getDeclaredField("listener"); field.setAccessible(true); assertNull(field.get(context));
        assertTrue(diagnostics.isDestroyed());
    }
    @Test public void everyContextFailureLeavesGeoRawHumanSharesAndReadyStateUsable() throws Exception {
        ready("geo-details-rich-compact-report.json"); open();
        tag("ip_context_next").performClick(); drain();
        for (IPContextTransport.Failure kind : IPContextTransport.Failure.values()) {
            failure = kind; tag("ip_context_query").performClick(); finish(); reportStillUsable();
            assertTrue(((TextView) tag("ip_context_body")).getText().length() == 0);
            assertTrue(tag("ip_context_query").isEnabled()); assertEquals(1, reports.size());
        }
        tag("geo_details_toggle").performClick(); assertNotNull(results().findViewWithTag("geo_details_body"));
    }
    @Test public void recreationRetainsRequestButDetachesOldActivityAndFencesOldControls() throws Exception {
        MainActivity old = ready("geo-details-rich-compact-report.json"); open(); tag("ip_context_next").performClick(); drain();
        View oldQuery = tag("ip_context_query"), oldNext = tag("ip_context_next"); oldQuery.performClick();
        old.onRetainNonConfigurationInstance(); Configuration landscape = new Configuration(old.getResources().getConfiguration()); landscape.orientation = Configuration.ORIENTATION_LANDSCAPE;
        controller.configurationChange(landscape); assertNotSame(old, controller.get());
        assertNull(results().findViewWithTag("ip_context_query")); open();
        assertTrue(((TextView) tag("ip_context_address")).getText().toString().contains("1.1.1.1"));
        oldNext.performClick(); oldQuery.performClick(); assertEquals(1, requests.size());
        finish(); assertTrue(text().contains("one.example")); reportStillUsable(); assertEquals(1, reports.size());
        Bundle saved = new Bundle(); controller.saveInstanceState(saved);
        assertFalse(saved.toString().contains("one.example"));
    }
    @Test public void queuedSuccessErrorAndFinallyAfterReportBaseOrCredentialReplacementNeverPublish() throws Exception {
        MainActivity activity = ready("geo-details-rich-compact-report.json");
        for (int scenario = 0; scenario < 3; scenario++) {
            open(); tag("ip_context_next").performClick(); drain();
            failure = scenario == 1 ? IPContextTransport.Failure.UNSUPPORTED : null;
            tag("ip_context_query").performClick(); workers.remove().run();
            View staleQuery = tag("ip_context_query");
            if (scenario == 0) ((EditText) activity.findViewById(R.id.api_url)).setText("https://replacement.example.test");
            if (scenario == 1) { ((CheckBox) activity.findViewById(R.id.auth_enabled)).setChecked(true); ((EditText) activity.findViewById(R.id.bearer)).setText("rotated-token"); }
            report(activity); assertEquals(0, context.cacheSizeForTests());
            open(); tag("ip_context_next").performClick(); drain(); failure = null;
            tag("ip_context_query").performClick(); int count = requests.size();
            staleQuery.performClick(); drain(); assertEquals(count, requests.size()); assertTrue(context.state().loading());
            finish(); reportStillUsable(); assertTrue(text().contains("one.example"));
            if (scenario >= 1) assertEquals("Bearer rotated-token", configs.get(configs.size()-1).authorizationHeader().orElseThrow());
            tag("ip_context_toggle").performClick(); report(activity); drain();
        }
    }
    @Test public void lateViewDispatchIsTimeoutAndReadyReportIsStillShareable() throws Exception {
        ready("geo-details-rich-compact-report.json"); open(); tag("ip_context_next").performClick(); drain();
        tag("ip_context_query").performClick(); workers.remove().run();
        deliveries.remove().run(); deliveries.remove().run(); nanos = java.util.concurrent.TimeUnit.SECONDS.toNanos(10); drain();
        assertFalse(text().contains("one.example")); assertTrue(text().contains("10-second")); reportStillUsable(); assertEquals(0, context.cacheSizeForTests());
    }
    @Test public void geoAbsentPublicAddressesRemainReachableAndWidgetsStayBoundedAcrossAllPages() throws Exception {
        ready("large-full"); int initial = count(results()); open();
        List<String> addresses = IPContextAddresses.from(diagnostics.state().report().orElseThrow()).addresses(); assertEquals(500,addresses.size()); assertTrue(text().contains("Omitted: 100"));
        for (String address : addresses) {
            assertTrue(((TextView)tag("ip_context_address")).getText().toString().endsWith(address));
            assertTrue(count(results()) <= initial + 11); tag("ip_context_next").performClick(); drain();
        }
        assertFalse(tag("ip_context_next").isEnabled()); assertTrue(requests.isEmpty()); assertEquals(1,reports.size());
        for (int width : new int[]{375,667,760,1440}) {
            results().measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0,View.MeasureSpec.UNSPECIFIED));
            results().layout(0,0,width,results().getMeasuredHeight());
            for (String tag : new String[]{"ip_context_address","ip_context_query","ip_context_previous","ip_context_next"}) {
                TextView control = (TextView) tag(tag); assertTrue(control.getMeasuredWidth() <= width); assertTrue(control.getMeasuredHeight() > 0);
                assertNotNull(control.getLayout()); for (int line=0;line<control.getLayout().getLineCount();line++) assertEquals(0,control.getLayout().getEllipsisCount(line));
            }
        }
    }
    static int count(View view) { int count=1; if(view instanceof ViewGroup group) for(int i=0;i<group.getChildCount();i++) count+=count(group.getChildAt(i)); return count; }
}
