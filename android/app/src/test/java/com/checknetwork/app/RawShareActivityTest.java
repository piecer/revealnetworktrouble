package com.checknetwork.app;

import static org.junit.Assert.*;

import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
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
import java.util.ArrayDeque;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
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
        fixture.flushRaw();

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

    @Test public void utf8EligibilityIsDeferredToRuntimeWorker() {
        Fixture fixture = readyFixture("x".repeat(com.checknetwork.app.core.ContractLimits.MAX_TRANSPORT_BYTES + 1));

        assertTrue("rendering must not UTF-8 encode or size raw JSON on main",
                fixture.activity.findViewById(R.id.share_raw).isEnabled());
        fixture.activity.findViewById(R.id.share_raw).performClick();
        AlertDialog warning = ShadowAlertDialog.getLatestAlertDialog();
        assertNotNull(warning);
        warning.getButton(AlertDialog.BUTTON_POSITIVE).performClick();
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        fixture.flushRaw();

        assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
        assertEquals(fixture.activity.getString(R.string.raw_share_error),
                ((android.widget.TextView) fixture.activity.findViewById(R.id.error)).getText().toString());
        assertEquals(0,sharedFiles(fixture.activity).length);
    }

    @Test public void newRequestDeletesConfirmedCacheFile() {
        Fixture fixture = readyFixture(RAW);
        confirmRawShare(fixture,fixture.activity);
        assertTrue(onlySharedFile(fixture.activity).isFile());

        fixture.activity.findViewById(R.id.run).performClick();
        fixture.flushRaw();

        assertEquals(0,sharedFiles(fixture.activity).length);
    }

    @Test public void finalDestroyDeletesConfirmedCacheFile() {
        Fixture fixture = readyFixture(RAW);
        confirmRawShare(fixture,fixture.activity);
        assertTrue(onlySharedFile(fixture.activity).isFile());

        fixture.controller.destroy();
        fixture.flushRaw();

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
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);
        File firstFile=onlySharedFile(fixture.activity);
        Uri firstUri=Uri.parse("content://"+fixture.activity.getPackageName()+".raw-report-provider/shared_reports/"+firstFile.getName());
        fixture.activity.onRetainNonConfigurationInstance();
        Configuration landscape=new Configuration(fixture.activity.getResources().getConfiguration());landscape.orientation=Configuration.ORIENTATION_LANDSCAPE;
        fixture.controller.configurationChange(landscape);MainActivity current=fixture.controller.get();
        assertEquals(RawShareRuntime.Phase.GRANTED,fixture.runtime.snapshot().phase());
        assertNull("rotation must not relaunch the chooser",Shadows.shadowOf(current).getNextStartedActivity());

        confirmRawShare(fixture,current);

        assertTrue(fixture.revoked.contains(firstUri));assertFalse(firstFile.exists());assertEquals(1,sharedFiles(current).length);
    }

    @Test public void confirmationIsAsynchronousBusyAndHasNoSecondPreparationQueue() {
        Fixture fixture=readyFixture(RAW);beginConfirmation(fixture.activity);
        assertEquals(RawShareRuntime.Phase.CLEANING,fixture.runtime.snapshot().phase());
        assertEquals(1,fixture.worker.pending());
        assertFalse(fixture.activity.findViewById(R.id.share_raw).isEnabled());
        assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
        assertEquals(fixture.activity.getString(R.string.raw_share_state_cleaning),((android.widget.TextView)fixture.activity.findViewById(R.id.request_status)).getText().toString());
        fixture.worker.runAll();
        assertEquals(RawShareRuntime.Phase.MATERIALIZED,fixture.runtime.snapshot().phase());
        assertNull("worker completion cannot launch off-main",Shadows.shadowOf(fixture.activity).getNextStartedActivity());
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(RawShareRuntime.Phase.GRANTED,fixture.runtime.snapshot().phase());
        assertNotNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
    }

    @Test public void rotationDuringPendingPreparationNeverAutoLaunchesAndDeletesCandidate() {
        Fixture fixture=readyFixture(RAW);beginConfirmation(fixture.activity);MainActivity old=fixture.activity,current=rotate(fixture);
        fixture.flushRaw();
        assertNull(Shadows.shadowOf(old).getNextStartedActivity());assertNull(Shadows.shadowOf(current).getNextStartedActivity());
        assertEquals(0,sharedFiles(current).length);assertEquals(RawShareRuntime.Phase.IDLE,fixture.runtime.snapshot().phase());
        assertTrue("fresh explicit confirmation remains available",current.findViewById(R.id.share_raw).isEnabled());
    }

    @Test public void rotationAfterMaterializationBeforeMainCallbackRetiresWithoutLaunch() {
        Fixture fixture=readyFixture(RAW);beginConfirmation(fixture.activity);fixture.worker.runAll();
        assertEquals(RawShareRuntime.Phase.MATERIALIZED,fixture.runtime.snapshot().phase());MainActivity old=fixture.activity,current=rotate(fixture);
        Shadows.shadowOf(Looper.getMainLooper()).idle();fixture.flushRaw();
        assertNull(Shadows.shadowOf(old).getNextStartedActivity());assertNull(Shadows.shadowOf(current).getNextStartedActivity());assertEquals(0,sharedFiles(current).length);
    }

    @Test public void rotationWhileWriterIsPreparingNeverLaunchesAfterNoncooperativeReturn() throws Exception {
        Fixture fixture=readyFixture(RAW);CountDownLatch entered=new CountDownLatch(1),release=new CountDownLatch(1);
        fixture.fs.beforeWrite=()->{entered.countDown();try{if(!release.await(5,TimeUnit.SECONDS))throw new AssertionError("release timed out");}catch(InterruptedException interrupted){Thread.currentThread().interrupt();throw new AssertionError(interrupted);}};
        beginConfirmation(fixture.activity);Thread writer=fixture.worker.runAllAsync();assertTrue(entered.await(5,TimeUnit.SECONDS));assertEquals(RawShareRuntime.Phase.PREPARING,fixture.runtime.snapshot().phase());
        MainActivity old=fixture.activity,current=rotate(fixture);release.countDown();writer.join(5000);assertFalse(writer.isAlive());Shadows.shadowOf(Looper.getMainLooper()).idle();fixture.flushRaw();
        assertNull(Shadows.shadowOf(old).getNextStartedActivity());assertNull(Shadows.shadowOf(current).getNextStartedActivity());assertEquals(0,sharedFiles(current).length);assertEquals(RawShareRuntime.Phase.IDLE,fixture.runtime.snapshot().phase());
    }

    @Test public void newReportBeforeMaterializedCallbackMakesCallbackStaleAndNonlaunching() {
        Fixture fixture=readyFixture(RAW);beginConfirmation(fixture.activity);fixture.worker.runAll();assertEquals(RawShareRuntime.Phase.MATERIALIZED,fixture.runtime.snapshot().phase());
        fixture.activity.findViewById(R.id.run).performClick();Shadows.shadowOf(Looper.getMainLooper()).idle();fixture.flushRaw();
        assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());assertEquals(0,sharedFiles(fixture.activity).length);assertEquals(RequestState.Phase.LOADING,fixture.session.state().phase());
    }

    @Test public void chooserLaunchFailuresRetireRevokeDeleteOnceAndUseFixedError() {
        for(RuntimeException launchFailure:List.of(new ActivityNotFoundException("PRIVATE"),new SecurityException("PRIVATE"),new IllegalStateException("PRIVATE"))){
            Fixture fixture=readyFixture(RAW);MainActivity.setChooserLauncherForTests((activity,chooser)->{throw launchFailure;});beginConfirmation(fixture.activity);fixture.flushRaw();fixture.flushRaw();
            assertEquals(1,fixture.revoked.size());assertEquals(0,sharedFiles(fixture.activity).length);
            String message=((android.widget.TextView)fixture.activity.findViewById(R.id.error)).getText().toString();assertEquals(fixture.activity.getString(R.string.raw_share_error),message);assertFalse(message.contains("PRIVATE"));
            fixture.controller.destroy();fixture.flushRaw();MainActivity.resetSessionFactoryForTests();
        }
    }

    @Test public void resumeBeforeLeaseExpiryPreservesGrantedUriAndFile() {
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);Uri granted=fixture.runtime.snapshot().uri();long expiry=fixture.runtime.snapshot().leaseExpiresAt();File grantedFile=onlySharedFile(fixture.activity);
        fixture.controller.pause().stop();fixture.clock.now=expiry-1;

        fixture.controller.restart().start().resume();fixture.flushRaw();

        assertEquals(RawShareRuntime.Phase.GRANTED,fixture.runtime.snapshot().phase());assertEquals(granted,fixture.runtime.snapshot().uri());assertTrue(grantedFile.isFile());assertTrue(fixture.revoked.isEmpty());assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
    }

    @Test public void sameActivityResumeAtExactLeaseExpiryRevokesAndDeletesExactlyOnce() {
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);Uri granted=fixture.runtime.snapshot().uri();long expiry=fixture.runtime.snapshot().leaseExpiresAt();
        fixture.controller.pause().stop();fixture.clock.now=expiry;

        fixture.controller.restart().start().resume();fixture.flushRaw();

        assertEquals(List.of(granted),fixture.revoked);assertEquals(0,sharedFiles(fixture.activity).length);assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
    }

    @Test public void pausedActivityResumeAtExactLeaseExpiryObservesWithoutStart() {
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);Uri granted=fixture.runtime.snapshot().uri();long expiry=fixture.runtime.snapshot().leaseExpiresAt();
        fixture.controller.pause();fixture.clock.now=expiry;

        fixture.controller.resume();fixture.flushRaw();

        assertEquals(List.of(granted),fixture.revoked);assertEquals(0,sharedFiles(fixture.activity).length);assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
    }

    @Test public void resumeAfterLeaseExpiryRevokesAndDeletesExactlyOnce() {
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);Uri granted=fixture.runtime.snapshot().uri();long expiry=fixture.runtime.snapshot().leaseExpiresAt();
        fixture.controller.pause().stop();fixture.clock.now=expiry+1;

        fixture.controller.restart().start().resume();fixture.flushRaw();

        assertEquals(List.of(granted),fixture.revoked);assertEquals(0,sharedFiles(fixture.activity).length);assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
    }

    @Test public void repeatedResumesAtExactExpiryScheduleAndCleanUpOnlyOnceCount100() {
        for(int iteration=0;iteration<100;iteration++) {
            Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);Uri granted=fixture.runtime.snapshot().uri();long expiry=fixture.runtime.snapshot().leaseExpiresAt();
            fixture.controller.pause().stop();fixture.clock.now=expiry;

            fixture.controller.restart().start().resume();assertEquals("iteration "+iteration,1,fixture.worker.pending());
            fixture.controller.pause().resume();fixture.controller.pause().stop().restart().start().resume();assertEquals("iteration "+iteration,1,fixture.worker.pending());
            fixture.flushRaw();fixture.controller.pause().resume();fixture.flushRaw();

            assertEquals("iteration "+iteration,List.of(granted),fixture.revoked);assertEquals("iteration "+iteration,0,sharedFiles(fixture.activity).length);assertNull("iteration "+iteration,Shadows.shadowOf(fixture.activity).getNextStartedActivity());
            fixture.controller.destroy();fixture.flushRaw();MainActivity.resetSessionFactoryForTests();
        }
    }

    @Test public void runtimeObservationFailureIsContainedWithFixedError() {
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);long expiry=fixture.runtime.snapshot().leaseExpiresAt();
        fixture.controller.pause().stop();fixture.clock.now=expiry;fixture.worker.rejectExecute=true;

        fixture.controller.restart().start().resume();

        String message=((android.widget.TextView)fixture.activity.findViewById(R.id.error)).getText().toString();
        assertEquals(fixture.activity.getString(R.string.raw_share_error),message);assertFalse(message.contains("PRIVATE"));assertNull(Shadows.shadowOf(fixture.activity).getNextStartedActivity());
    }

    @Test public void ttlObservationAfterGrantedRotationRevokesAndDeletesExactlyOnce() {
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);Uri granted=fixture.runtime.snapshot().uri();long expiry=fixture.runtime.snapshot().leaseExpiresAt();MainActivity current=rotate(fixture);assertEquals(granted,fixture.runtime.snapshot().uri());
        fixture.clock.now=expiry;fixture.runtime.observe();fixture.flushRaw();fixture.runtime.observe();fixture.flushRaw();
        assertEquals(List.of(granted),fixture.revoked);assertEquals(0,sharedFiles(current).length);assertNull(Shadows.shadowOf(current).getNextStartedActivity());
    }

    @Test public void rotationWhileChooserFailureIsRetiredCannotRelaunchOrDuplicateCleanup() {
        Fixture fixture=readyFixture(RAW);MainActivity.setChooserLauncherForTests((activity,chooser)->{throw new SecurityException("PRIVATE");});beginConfirmation(fixture.activity);
        fixture.worker.runAll();Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(RawShareRuntime.Phase.RETIRED,fixture.runtime.snapshot().phase());MainActivity old=fixture.activity,current=rotate(fixture);fixture.flushRaw();
        assertNull(Shadows.shadowOf(old).getNextStartedActivity());assertNull(Shadows.shadowOf(current).getNextStartedActivity());assertEquals(1,fixture.revoked.size());assertEquals(0,sharedFiles(current).length);
    }

    @Test public void explicitTestShutdownDisposedRuntimeSurvivesRotationWithoutActivityDisposalOrLaunch() throws Exception {
        Fixture fixture=readyFixture(RAW);fixture.runtime.dispose();fixture.flushRaw();assertEquals(RawShareRuntime.Phase.DISPOSED,fixture.runtime.snapshot().phase());
        MainActivity current=rotate(fixture);
        assertEquals(RawShareRuntime.Phase.DISPOSED,fixture.runtime.snapshot().phase());assertTrue(fixture.worker.shutdown);assertNull(Shadows.shadowOf(current).getNextStartedActivity());
    }

    @Test public void finalDestroyRetiresSessionAndLeaseWithoutDisposingProcessRuntime() {
        Fixture fixture=readyFixture(RAW);confirmRawShare(fixture,fixture.activity);fixture.controller.destroy();fixture.flushRaw();
        assertTrue(fixture.session.isDestroyed());assertFalse(fixture.worker.shutdown);assertNotEquals(RawShareRuntime.Phase.DISPOSED,fixture.runtime.snapshot().phase());assertEquals(0,sharedFiles(fixture.activity).length);
    }

    private static void beginConfirmation(MainActivity activity){activity.findViewById(R.id.share_raw).performClick();AlertDialog warning=ShadowAlertDialog.getLatestAlertDialog();assertNotNull(warning);warning.getButton(AlertDialog.BUTTON_POSITIVE).performClick();Shadows.shadowOf(Looper.getMainLooper()).idle();}
    private static MainActivity rotate(Fixture fixture){MainActivity old=fixture.controller.get();old.onRetainNonConfigurationInstance();Configuration changed=new Configuration(old.getResources().getConfiguration());changed.orientation=changed.orientation==Configuration.ORIENTATION_LANDSCAPE?Configuration.ORIENTATION_PORTRAIT:Configuration.ORIENTATION_LANDSCAPE;fixture.controller.configurationChange(changed);return fixture.controller.get();}

    private static void confirmRawShare(Fixture fixture,MainActivity activity) {
        Button button = activity.findViewById(R.id.share_raw);
        assertTrue("raw share should be eligible", button.isEnabled());
        button.performClick();
        AlertDialog warning = ShadowAlertDialog.getLatestAlertDialog();
        assertNotNull(warning);
        assertTrue(warning.isShowing());
        warning.getButton(AlertDialog.BUTTON_POSITIVE).performClick();
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        fixture.flushRaw();
        Intent chooser = Shadows.shadowOf(activity).getNextStartedActivity();
        assertNotNull("raw share error: " + ((android.widget.TextView) activity.findViewById(R.id.error)).getText()+" phase="+fixture.runtime.snapshot().phase()+" status="+((android.widget.TextView)activity.findViewById(R.id.request_status)).getText(), chooser);
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
        List<Uri> revoked=new ArrayList<>();AtomicInteger token=new AtomicInteger();ManualWorker worker=new ManualWorker();FakeClock clock=new FakeClock();RawShareRuntime[] runtime={null};HookFs[] fs={null};
        MainActivity.setRawRuntimeFactoryForTests(activity -> {
            if(runtime[0]==null){fs[0]=new HookFs(new RawShareRuntime.SystemFileSystem(activity.getCacheDir()));runtime[0]=new RawShareRuntime(fs[0],worker,clock,
                    ()->String.format("%032x",token.incrementAndGet()),
                    file -> Uri.parse("content://" + activity.getPackageName()+ ".raw-report-provider/shared_reports/" + file.getName()),
                    revoked::add,callback->new Handler(Looper.getMainLooper()).post(callback),()->{});}
            return runtime[0];
        });
        ActivityController<MainActivity> controller = Robolectric.buildActivity(MainActivity.class).setup();
        controllers.add(controller);
        worker.runAll();Shadows.shadowOf(Looper.getMainLooper()).idle();
        ((EditText) controller.get().findViewById(R.id.api_url)).setText("https://api.example.test");
        return new Fixture(controller, controller.get(), session, calls,runtime[0],worker,clock,fs[0],revoked);
    }

    private static File[] sharedFiles(MainActivity activity) {
        File[] files=new File(activity.getCacheDir(),"shared-reports").listFiles((directory,name)->name.endsWith(".json"));return files==null?new File[0]:files;
    }
    private static File onlySharedFile(MainActivity activity){File[] files=sharedFiles(activity);assertEquals(1,files.length);return files[0];}

    private record Fixture(ActivityController<MainActivity> controller, MainActivity activity,
                           DiagnosticsSession session, FakeFactory calls,RawShareRuntime runtime,ManualWorker worker,FakeClock clock,HookFs fs,List<Uri> revoked) {
        void flushRaw(){worker.runAll();Shadows.shadowOf(Looper.getMainLooper()).idle();}
    }
    private static final class ManualWorker implements RawShareRuntime.Worker {
        final ArrayDeque<Runnable> tasks=new ArrayDeque<>();boolean shutdown;boolean rejectExecute;
        public synchronized void execute(Runnable work){if(rejectExecute)throw new IllegalStateException("PRIVATE observation failure");if(shutdown)throw new IllegalStateException("shutdown");tasks.add(work);}
        public synchronized CompletableFuture<Void> shutdown(){shutdown=true;return CompletableFuture.completedFuture(null);}
        synchronized int pending(){return tasks.size();}
        Thread runAllAsync(){Thread thread=new Thread(this::runTasks,"activity-raw-worker");thread.start();return thread;}
        void runAll(){
            RuntimeException[] failure={null};
            Thread thread=new Thread(()->{try{runTasks();}catch(RuntimeException problem){failure[0]=problem;}},"activity-raw-worker");
            thread.start();try{thread.join(10000);}catch(InterruptedException interrupted){Thread.currentThread().interrupt();throw new AssertionError(interrupted);}assertFalse("raw worker stalled",thread.isAlive());if(failure[0]!=null)throw failure[0];
        }
        private void runTasks(){while(true){Runnable task;synchronized(this){if(tasks.isEmpty())return;task=tasks.removeFirst();}task.run();}}
    }
    private static final class FakeClock implements RawShareRuntime.Clock {long now=1000;public long nowMillis(){return now;}}
    private static final class HookFs implements RawShareRuntime.FileSystem {
        final RawShareRuntime.FileSystem delegate;volatile Runnable beforeWrite;
        HookFs(RawShareRuntime.FileSystem delegate){this.delegate=delegate;}
        public File directory(){return delegate.directory();}public void prepareDirectory()throws java.io.IOException{delegate.prepareDirectory();}
        public RawShareRuntime.ScanPage scan(String after,int limit)throws java.io.IOException{return delegate.scan(after,limit);}public RawShareRuntime.ScanSession openScan()throws java.io.IOException{return delegate.openScan();}
        public boolean exists(String name)throws java.io.IOException{return delegate.exists(name);}public void writeAtomic(String temporary,String destination,byte[] bytes)throws java.io.IOException{Runnable hook=beforeWrite;if(hook!=null)hook.run();delegate.writeAtomic(temporary,destination,bytes);}
        public File validateForUri(String name)throws java.io.IOException{return delegate.validateForUri(name);}public void delete(String name)throws java.io.IOException{delegate.delete(name);}
    }
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
