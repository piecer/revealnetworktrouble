package com.checknetwork.app;

import static org.junit.Assert.*;
import android.content.Intent;
import android.content.pm.ActivityInfo;
import android.content.res.Configuration;
import android.os.Bundle;
import android.text.InputType;
import android.view.View;
import android.view.accessibility.AccessibilityEvent;
import android.widget.*;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.*;
import com.checknetwork.app.state.RequestCoordinator;
import com.checknetwork.app.state.RequestState;
import com.checknetwork.app.network.TransportException;
import com.checknetwork.app.network.ApiConnectionConfig;
import java.util.*;
import org.junit.After;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.shadows.ShadowActivity;
import org.robolectric.Shadows;

@RunWith(RobolectricTestRunner.class)
@Config(sdk=35)
public final class MainActivityTest {
    @After public void reset(){MainActivity.resetSessionFactoryForTests();}

    @Test public void nativeFormHasThirteenKindsContextFieldsLabelsAndLiveRegions() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        LinearLayout targets=activity.findViewById(R.id.targets); View row=targets.getChildAt(0);
        Spinner kinds=row.findViewById(R.id.kind); assertEquals(13,kinds.getCount());
        TextView addressLabel=row.findViewById(R.id.address_label); EditText address=row.findViewById(R.id.address);
        assertEquals(address.getId(),addressLabel.getLabelFor()); assertFalse(address.getHint().toString().isBlank());
        TextView expectedLabel=row.findViewById(R.id.expected_status_label); EditText expected=row.findViewById(R.id.expected_status);
        kinds.setSelection(index(CheckKind.HTTP)); assertEquals(View.VISIBLE,expected.getVisibility()); assertEquals(expected.getId(),expectedLabel.getLabelFor());
        kinds.setSelection(index(CheckKind.TRACEROUTE)); assertEquals(View.VISIBLE,row.findViewById(R.id.attempts).getVisibility());
        TextView status=activity.findViewById(R.id.request_status), error=activity.findViewById(R.id.error);
        assertEquals(View.ACCESSIBILITY_LIVE_REGION_POLITE,status.getAccessibilityLiveRegion());
        assertEquals(View.ACCESSIBILITY_LIVE_REGION_ASSERTIVE,error.getAccessibilityLiveRegion());
        for(int id:new int[]{R.id.run,R.id.cancel,R.id.retry,R.id.share,R.id.add_target}) assertTrue(activity.findViewById(id).getMinimumHeight()>=48);
    }

    @Test @Config(sdk=26) public void staticSectionHeadingsRemainAccessibleOnMinSdk() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        assertTrue(androidx.core.view.ViewCompat.isAccessibilityHeading(activity.findViewById(R.id.hero_heading)));
        assertTrue(androidx.core.view.ViewCompat.isAccessibilityHeading(activity.findViewById(R.id.targets_heading)));
    }

    @Test public void authIsPasswordAndSecretsNeverEnterSavedState() {
        ActivityController<MainActivity> controller=Robolectric.buildActivity(MainActivity.class).setup(); MainActivity activity=controller.get();
        CheckBox toggle=activity.findViewById(R.id.auth_enabled); EditText bearer=activity.findViewById(R.id.bearer);
        toggle.setChecked(true); bearer.setText("SUPER-CREDENTIAL"); activity.findViewById(R.id.api_url).requestFocus();
        assertNotEquals(0,bearer.getInputType()&InputType.TYPE_TEXT_VARIATION_PASSWORD);
        Bundle out=new Bundle(); controller.saveInstanceState(out);
        assertFalse(out.toString().contains("SUPER-CREDENTIAL")); assertFalse(out.containsKey("raw_report")); assertFalse(out.containsKey("bearer"));
    }

    @Test public void variantDefaultMatchesResourceAndManifestIsNotPortraitLocked() throws Exception {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        assertEquals(activity.getString(R.string.default_api_base),((EditText)activity.findViewById(R.id.api_url)).getText().toString());
        ActivityInfo info=activity.getPackageManager().getActivityInfo(activity.getComponentName(),0);
        assertNotEquals(ActivityInfo.SCREEN_ORIENTATION_PORTRAIT,info.screenOrientation);
    }

    @Test public void shareUsesCurrentReadyHumanMarkdownOnly() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        activity.renderReadyForTest("{\"secret\":\"RAW-SECRET\"}",
                com.checknetwork.app.core.ReportParser.parse("{\"id\":\"id-secret\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[],\"summary\":{\"total\":0,\"passed\":0,\"failed\":0}}"));
        activity.findViewById(R.id.share).performClick();
        Intent chooser=Shadows.shadowOf(activity).getNextStartedActivity(); Intent send=(Intent)chooser.getParcelableExtra(Intent.EXTRA_INTENT);
        String text=send.getStringExtra(Intent.EXTRA_TEXT); assertTrue(text.startsWith("# Network diagnostic summary"));
        assertFalse(text.contains("RAW-SECRET")); assertFalse(text.contains("id-secret")); assertEquals("text/plain",send.getType());
    }

    @Test public void rawResultsToggleIsLastCollapsedAccessibleAndKeepsFocusAcrossToggles() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        String raw="{\"id\":\"r\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[{\"kind\":\"dns\",\"address\":\"example.test\",\"status\":\"healthy\",\"latency_ms\":1,\"started_at\":\"2026-09-02T00:00:00Z\",\"details\":{\"answer\":\"203.0.113.8\"}}],\"summary\":{\"total\":1,\"passed\":1,\"failed\":0}}";
        activity.renderReadyForTest(raw,ReportParser.parse(raw));
        LinearLayout results=activity.findViewById(R.id.results);
        Button toggle=activity.findViewById(R.id.raw_results_toggle);TextView body=activity.findViewById(R.id.raw_results_body);
        assertSame(body,results.getChildAt(results.getChildCount()-1));
        assertEquals(View.GONE,body.getVisibility());assertEquals(activity.getString(R.string.raw_results_collapsed),toggle.getStateDescription());
        assertTrue(toggle.isFocusable());toggle.requestFocus();assertTrue(toggle.performClick());
        assertEquals(View.VISIBLE,body.getVisibility());assertEquals(activity.getString(R.string.raw_results_expanded),toggle.getStateDescription());assertTrue(toggle.isFocused());
        assertTrue(toggle.performClick());assertEquals(View.GONE,body.getVisibility());assertEquals(activity.getString(R.string.raw_results_collapsed),toggle.getStateDescription());assertTrue(toggle.isFocused());
    }

    @Test public void primaryControlsWrapLargeTextAndKeepFortyEightDpTouchBoundsAt320DpLandscape() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        Configuration large=new Configuration(activity.getResources().getConfiguration());
        large.orientation=Configuration.ORIENTATION_LANDSCAPE;large.screenWidthDp=320;large.fontScale=2f;
        activity.getResources().updateConfiguration(large,activity.getResources().getDisplayMetrics());
        int width=Math.round(320*activity.getResources().getDisplayMetrics().density);
        int minTouch=Math.round(48*activity.getResources().getDisplayMetrics().density);
        for(int id:new int[]{R.id.api_url,R.id.timeout,R.id.run,R.id.add_target,R.id.share,R.id.share_raw}) {
            TextView control=activity.findViewById(id);control.setVisibility(View.VISIBLE);
            assertEquals("control must not use a fixed height",android.view.ViewGroup.LayoutParams.WRAP_CONTENT,control.getLayoutParams().height);
            assertTrue("minimum touch height",control.getMinimumHeight()>=minTouch);
            assertTrue("vertical padding",control.getPaddingTop()>0&&control.getPaddingBottom()>0);
            control.measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(0,View.MeasureSpec.UNSPECIFIED));
            control.layout(0,0,control.getMeasuredWidth(),control.getMeasuredHeight());
            assertTrue("touch bounds",control.getMeasuredHeight()>=minTouch);
            assertNotNull("text layout",control.getLayout());
            assertTrue("text must fit measured height",control.getLayout().getLineBottom(control.getLayout().getLineCount()-1)+control.getCompoundPaddingTop()+control.getCompoundPaddingBottom()<=control.getMeasuredHeight());
        }
    }

    @Test public void rotationRetainsEnabledBearerVisibilityWithoutInvalidatingActiveRequest() {
        FakeFactory calls=new FakeFactory(); DiagnosticsSession retained=new DiagnosticsSession(new RequestCoordinator(calls),Runnable::run,null);
        MainActivity.setSessionFactoryForTests(dispatcher->retained);
        ActivityController<MainActivity> controller=Robolectric.buildActivity(MainActivity.class).setup(); MainActivity old=controller.get();
        setValidApi(old);
        CheckBox oldToggle=old.findViewById(R.id.auth_enabled); EditText oldBearer=old.findViewById(R.id.bearer);
        oldToggle.setChecked(true); oldBearer.setText("ROTATED-CREDENTIAL");
        old.findViewById(R.id.run).performClick();
        assertEquals(RequestState.Phase.LOADING,retained.state().phase());
        old.onRetainNonConfigurationInstance();

        Configuration landscape=new Configuration(old.getResources().getConfiguration());
        landscape.orientation=Configuration.ORIENTATION_LANDSCAPE;
        controller.configurationChange(landscape); MainActivity current=controller.get();

        assertTrue(((CheckBox)current.findViewById(R.id.auth_enabled)).isChecked());
        assertEquals(View.VISIBLE,current.findViewById(R.id.bearer_label).getVisibility());
        assertEquals(View.VISIBLE,current.findViewById(R.id.bearer).getVisibility());
        assertEquals("ROTATED-CREDENTIAL",((EditText)current.findViewById(R.id.bearer)).getText().toString());
        assertEquals(RequestState.Phase.LOADING,retained.state().phase());
        assertEquals(1,calls.calls.size());
    }

    @Test public void immediateRetryAfterRotationPassesRetainedBearerConfig() {
        FakeFactory calls=new FakeFactory();DiagnosticsSession retained=new DiagnosticsSession(new RequestCoordinator(calls),Runnable::run,null);
        List<ApiConnectionConfig> configs=new ArrayList<>();MainActivity.setSessionFactoryForTests(dispatcher->retained);
        MainActivity.setConnectionConfigFactoryForTests((base,debug,bearer)->{ApiConnectionConfig config=ApiConnectionConfig.create(base,debug,bearer);configs.add(config);return config;});
        ActivityController<MainActivity> controller=Robolectric.buildActivity(MainActivity.class).setup();MainActivity old=controller.get();setValidApi(old);
        ((CheckBox)old.findViewById(R.id.auth_enabled)).setChecked(true);((EditText)old.findViewById(R.id.bearer)).setText("retry-secret");old.findViewById(R.id.run).performClick();
        old.onRetainNonConfigurationInstance();Configuration landscape=new Configuration(old.getResources().getConfiguration());landscape.orientation=Configuration.ORIENTATION_LANDSCAPE;controller.configurationChange(landscape);
        calls.calls.get(0).fail(TransportException.of(TransportException.Kind.NETWORK));MainActivity current=controller.get();current.findViewById(R.id.retry).performClick();

        assertEquals(2,configs.size());assertEquals("Bearer retry-secret",configs.get(1).authorizationHeader().orElseThrow());assertEquals(2,calls.calls.size());
    }

    @Test public void recreationDuringLoadingReattachesAndOldOwnerCannotConsumeCompletion() {
        FakeFactory calls=new FakeFactory(); DiagnosticsSession retained=new DiagnosticsSession(new RequestCoordinator(calls),Runnable::run,null);
        MainActivity.setSessionFactoryForTests(dispatcher->retained);
        ActivityController<MainActivity> controller=Robolectric.buildActivity(MainActivity.class).setup(); MainActivity old=controller.get();
        setValidApi(old);
        old.findViewById(R.id.run).performClick(); assertEquals(RequestState.Phase.LOADING,retained.state().phase());
        old.onRetainNonConfigurationInstance(); // Robolectric does not invoke retention for configurationChange().
        Configuration landscape=new Configuration(old.getResources().getConfiguration());
        landscape.orientation=Configuration.ORIENTATION_LANDSCAPE;
        controller.configurationChange(landscape); MainActivity current=controller.get();
        assertNotSame(old,current);
        assertEquals(RequestState.Phase.LOADING,retained.state().phase());
        assertEquals(View.VISIBLE,current.findViewById(R.id.progress).getVisibility());
        calls.calls.get(0).succeed(EMPTY_REPORT,ReportParser.parse(EMPTY_REPORT));
        assertEquals(View.VISIBLE,current.findViewById(R.id.report).getVisibility());
        assertEquals(View.GONE,old.findViewById(R.id.report).getVisibility());
    }

    @Test public void inputMutationClearsReadyAndFinalDestroyCancelsActiveSession() {
        FakeFactory calls=new FakeFactory(); DiagnosticsSession retained=new DiagnosticsSession(new RequestCoordinator(calls),Runnable::run,null);
        MainActivity.setSessionFactoryForTests(dispatcher->retained);
        ActivityController<MainActivity> controller=Robolectric.buildActivity(MainActivity.class).setup(); MainActivity activity=controller.get();
        setValidApi(activity);
        activity.findViewById(R.id.run).performClick(); calls.calls.get(0).succeed(EMPTY_REPORT,ReportParser.parse(EMPTY_REPORT));
        assertEquals(View.VISIBLE,activity.findViewById(R.id.report).getVisibility());
        ((EditText)activity.findViewById(R.id.timeout)).setText("6000");
        assertEquals(RequestState.Phase.IDLE,retained.state().phase()); assertEquals(View.GONE,activity.findViewById(R.id.report).getVisibility());
        activity.findViewById(R.id.run).performClick(); FakeCall active=calls.calls.get(1); controller.destroy();
        assertEquals(1,active.cancels); assertTrue(retained.isDestroyed());
    }

    private static final String EMPTY_REPORT="{\"id\":\"r1\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[],\"summary\":{\"total\":0,\"passed\":0,\"failed\":0}}";
    private static final class FakeFactory implements RequestCoordinator.CallFactory {final List<FakeCall> calls=new ArrayList<>();public RequestCoordinator.CancellableCall create(ReportRequest request){FakeCall c=new FakeCall();calls.add(c);return c;}}
    private static final class FakeCall implements RequestCoordinator.CancellableCall {RequestCoordinator.Callback callback;int cancels;public void start(RequestCoordinator.Callback value){callback=value;}public void cancel(){cancels++;}void succeed(String raw,Report report){callback.onSuccess(raw,report);}void fail(TransportException error){callback.onError(error);}}
    private static void setValidApi(MainActivity activity){((EditText)activity.findViewById(R.id.api_url)).setText("https://api.example.test");}
    private static int index(CheckKind kind){for(int i=0;i<TargetKindOption.all().size();i++)if(TargetKindOption.all().get(i).kind()==kind)return i;return -1;}
}
