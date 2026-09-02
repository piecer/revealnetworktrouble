package com.checknetwork.app;

import static org.junit.Assert.*;

import android.app.AlertDialog;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Looper;
import android.content.res.Configuration;
import android.widget.Button;
import android.widget.EditText;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportParser;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.state.RequestCoordinator;
import com.checknetwork.app.state.RequestState;
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.After;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.shadows.ShadowAlertDialog;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class RawShareActivityTest {
    private static final String RAW = "{\"id\":\"private-id\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[],\"summary\":{\"total\":0,\"passed\":0,\"failed\":0}}";
    private static final Report REPORT = ReportParser.parse(RAW);

    private final List<ActivityController<MainActivity>> controllers = new ArrayList<>();

    @After public void reset() {
        for (ActivityController<MainActivity> controller : controllers) {
            if (controller.get() != null && !controller.get().isDestroyed()) controller.destroy();
        }
        MainActivity.resetSessionFactoryForTests();
    }

    @Test public void rawShareIsUnavailableBeforeCurrentRequestIsReady() {
        Fixture fixture = fixture();
        Button rawShare = fixture.activity.findViewById(R.id.share_raw);

        assertFalse(rawShare.isEnabled());
        rawShare.performClick();

        assertNull(ShadowAlertDialog.getLatestAlertDialog());
        assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
        assertEquals(0,sharedFiles(fixture.activity).length);
    }

    @Test public void warningCancelWritesAndStartsNothing() {
        Fixture fixture = readyFixture(RAW);
        fixture.activity.findViewById(R.id.share_raw).performClick();
        AlertDialog warning = ShadowAlertDialog.getLatestAlertDialog();
        assertNotNull(warning);
        assertTrue(((android.widget.TextView) warning.findViewById(android.R.id.message))
                .getText().toString().contains("sensitive"));

        warning.getButton(AlertDialog.BUTTON_NEGATIVE).performClick();

        assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
        assertEquals(0,sharedFiles(fixture.activity).length);
    }

    @Test public void warningConfirmSharesExactFileByContentUriOnly() throws Exception {
        Fixture fixture = readyFixture(RAW);
        fixture.activity.findViewById(R.id.share_raw).performClick();
        ShadowAlertDialog.getLatestAlertDialog().getButton(AlertDialog.BUTTON_POSITIVE).performClick();
        Shadows.shadowOf(Looper.getMainLooper()).idle();

        Intent chooser = Shadows.shadowOf(fixture.activity).getNextStartedActivity();
        assertNotNull("raw share error: " + ((android.widget.TextView) fixture.activity.findViewById(R.id.error)).getText(), chooser);
        Intent send = chooser.getParcelableExtra(Intent.EXTRA_INTENT);
        assertEquals(Intent.ACTION_SEND, send.getAction());
        assertEquals("application/json", send.getType());
        assertTrue((send.getFlags() & Intent.FLAG_GRANT_READ_URI_PERMISSION) != 0);
        assertFalse(send.hasExtra(Intent.EXTRA_TEXT));
        Uri stream = send.getParcelableExtra(Intent.EXTRA_STREAM);
        assertNotNull(stream);
        assertEquals("content", stream.getScheme());
        assertEquals(fixture.activity.getPackageName() + ".raw-report-provider", stream.getAuthority());
        assertArrayEquals(RAW.getBytes(StandardCharsets.UTF_8), Files.readAllBytes(onlySharedFile(fixture.activity).toPath()));
    }

    @Test public void staleConfirmationAfterInputMutationCannotWriteOrShare() {
        Fixture fixture = readyFixture(RAW);
        fixture.activity.findViewById(R.id.share_raw).performClick();
        AlertDialog warning = ShadowAlertDialog.getLatestAlertDialog();

        ((EditText) fixture.activity.findViewById(R.id.timeout)).setText("6000");
        assertEquals(RequestState.Phase.IDLE, fixture.session.state().phase());
        warning.getButton(AlertDialog.BUTTON_POSITIVE).performClick();
        Shadows.shadowOf(Looper.getMainLooper()).idle();

        assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
        assertEquals(0,sharedFiles(fixture.activity).length);
        assertFalse(fixture.activity.findViewById(R.id.share_raw).isEnabled());
    }

    @Test public void oversizedReadyRawIsNotShareEligible() {
        Fixture fixture = readyFixture("x".repeat(com.checknetwork.app.core.ContractLimits.MAX_TRANSPORT_BYTES + 1));

        assertFalse(fixture.activity.findViewById(R.id.share_raw).isEnabled());
        fixture.activity.findViewById(R.id.share_raw).performClick();
        assertNull(ShadowAlertDialog.getLatestAlertDialog());
        assertEquals(0,sharedFiles(fixture.activity).length);
    }

    @Test public void newRequestDeletesConfirmedCacheFile() {
        Fixture fixture = readyFixture(RAW);
        confirmRawShare(fixture.activity);
        assertTrue(onlySharedFile(fixture.activity).isFile());

        fixture.activity.findViewById(R.id.run).performClick();

        assertEquals(0,sharedFiles(fixture.activity).length);
    }

    @Test public void finalDestroyDeletesConfirmedCacheFile() {
        Fixture fixture = readyFixture(RAW);
        confirmRawShare(fixture.activity);
        assertTrue(onlySharedFile(fixture.activity).isFile());

        fixture.controller.destroy();

        assertEquals(0,sharedFiles(fixture.activity).length);
    }

    @Test public void savedInstanceStateNeverContainsReadyRawJson() {
        Fixture fixture = readyFixture(RAW);
        Bundle state = new Bundle();

        fixture.controller.saveInstanceState(state);

        assertFalse(state.toString().contains("private-id"));
        for (String key : state.keySet()) assertFalse(key.toLowerCase().contains("raw"));
    }

    @Test public void rotationRetainsIssuedUriAndRevokesItBeforeNextConfirmation() {
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture.activity);
        File firstFile=onlySharedFile(fixture.activity);
        Uri firstUri=Uri.parse("content://"+fixture.activity.getPackageName()+".raw-report-provider/shared_reports/"+firstFile.getName());
        fixture.activity.onRetainNonConfigurationInstance();
        Configuration landscape=new Configuration(fixture.activity.getResources().getConfiguration());landscape.orientation=Configuration.ORIENTATION_LANDSCAPE;
        fixture.controller.configurationChange(landscape);MainActivity current=fixture.controller.get();
        assertEquals(1,fixture.shares.size());

        confirmRawShare(current);

        assertTrue(fixture.revoked.contains(firstUri));assertFalse(firstFile.exists());assertEquals(1,sharedFiles(current).length);
    }

    private static void confirmRawShare(MainActivity activity) {
        Button button = activity.findViewById(R.id.share_raw);
        assertTrue("raw share should be eligible", button.isEnabled());
        button.performClick();
        AlertDialog warning = ShadowAlertDialog.getLatestAlertDialog();
        assertNotNull(warning);
        assertTrue(warning.isShowing());
        warning.getButton(AlertDialog.BUTTON_POSITIVE).performClick();
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        Intent chooser = Shadows.shadowOf(activity).getNextStartedActivity();
        assertNotNull("raw share error: " + ((android.widget.TextView) activity.findViewById(R.id.error)).getText(), chooser);
    }

    private Fixture readyFixture(String raw) {
        Fixture fixture = fixture();
        fixture.activity.findViewById(R.id.run).performClick();
        fixture.calls.calls.get(0).succeed(raw, REPORT);
        return fixture;
    }

    private Fixture fixture() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession session = new DiagnosticsSession(new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> session);
        List<RawReportShare> shares=new ArrayList<>();List<Uri> revoked=new ArrayList<>();AtomicInteger token=new AtomicInteger();
        MainActivity.setRawShareFactoryForTests(activity -> {
            RawReportShare share=new RawReportShare(activity.getCacheDir(),
                    file -> Uri.parse("content://" + activity.getPackageName()+ ".raw-report-provider/shared_reports/" + file.getName()),
                    RawReportShare.systemAtomicWriter(),()->String.format("%032x",token.incrementAndGet()),revoked::add);
            shares.add(share);return share;
        });
        ActivityController<MainActivity> controller = Robolectric.buildActivity(MainActivity.class).setup();
        controllers.add(controller);
        ((EditText) controller.get().findViewById(R.id.api_url)).setText("https://api.example.test");
        return new Fixture(controller, controller.get(), session, calls,shares,revoked);
    }

    private static File[] sharedFiles(MainActivity activity) {
        File[] files=new File(activity.getCacheDir(),"shared-reports").listFiles((directory,name)->name.endsWith(".json"));return files==null?new File[0]:files;
    }
    private static File onlySharedFile(MainActivity activity){File[] files=sharedFiles(activity);assertEquals(1,files.length);return files[0];}

    private record Fixture(ActivityController<MainActivity> controller, MainActivity activity,
                           DiagnosticsSession session, FakeFactory calls,List<RawReportShare> shares,List<Uri> revoked) {}
    private static final class FakeFactory implements RequestCoordinator.CallFactory {
        final List<FakeCall> calls = new ArrayList<>();
        public RequestCoordinator.CancellableCall create(ReportRequest request) {
            FakeCall call = new FakeCall(); calls.add(call); return call;
        }
    }
    private static final class FakeCall implements RequestCoordinator.CancellableCall {
        RequestCoordinator.Callback callback;
        public void start(RequestCoordinator.Callback callback) { this.callback = callback; }
        public void cancel() {}
        void succeed(String raw, Report report) { callback.onSuccess(raw, report); }
    }
}
