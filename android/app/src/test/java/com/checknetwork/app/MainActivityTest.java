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
import com.checknetwork.app.network.CapabilitiesTransport;
import com.checknetwork.app.network.ReportTransport;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.time.Instant;
import java.util.*;
import org.json.JSONArray;
import org.json.JSONObject;
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

    @Test public void editingTargetAddressImmediatelyRefreshesRemoveAccessibilityName() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        LinearLayout targets=activity.findViewById(R.id.targets);View row=targets.getChildAt(0);
        EditText address=row.findViewById(R.id.address);Button remove=row.findViewById(R.id.remove);
        assertTrue(remove.getContentDescription().toString().contains("example.com"));

        address.requestFocus();address.setText("edited.example");

        String description=remove.getContentDescription().toString();
        assertTrue(description.contains("edited.example"));
        assertFalse(description.contains("example.com"));
        assertTrue(address.isFocused());
        assertEquals(activity.getString(R.string.remove_symbol),remove.getText().toString());
        assertTrue(remove.getMinimumHeight()>=Math.round(48*activity.getResources().getDisplayMetrics().density));
    }

    @Test public void ambiguousUrlAuthorityControlsAndFormatsUseNeutralAccessibilityName() {
        FakeFactory calls=new FakeFactory();DiagnosticsSession retained=new DiagnosticsSession(
                new RequestCoordinator(calls),Runnable::run,null);
        MainActivity.setSessionFactoryForTests(dispatcher->retained);
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        View row=((LinearLayout)activity.findViewById(R.id.targets)).getChildAt(0);
        EditText address=row.findViewById(R.id.address);
        String[] ambiguous={
                "https:\n//user:CONTROL-CREDENTIAL@host.example/path?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL",
                "https:\u0000//user:CONTROL-CREDENTIAL@host.example/path?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL",
                "https:\u0085//user:CONTROL-CREDENTIAL@host.example/path?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL",
                "https\u202e://user:CONTROL-CREDENTIAL@host.example/path?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL",
                "https:\u200b//user:CONTROL-CREDENTIAL@host.example/path?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL",
                "https:/\u2060/user:CONTROL-CREDENTIAL@host.example/path?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL",
                "https://user:CONTROL-CREDENTIAL\u200b@host.example/path?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL",
                "https://user:CONTROL-CREDENTIAL@\u202ehost.example/path?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL"
        };
        String neutral=activity.getString(R.string.remove_target_without_address,1,
                activity.getString(R.string.kind_dns));
        for(int iteration=0;iteration<100;iteration++)for(String raw:ambiguous){
            address.setText(raw);
            String description=row.findViewById(R.id.remove).getContentDescription().toString();
            assertEquals("ambiguous authority must fail closed",neutral,description);
            assertFalse(description.contains("CONTROL-CREDENTIAL"));
            assertFalse(description.contains("QUERY-CREDENTIAL"));
            assertFalse(description.contains("FRAGMENT-CREDENTIAL"));
        }

        String exactRaw=ambiguous[0];address.setText(exactRaw);setValidApi(activity);
        activity.findViewById(R.id.run).performClick();
        assertEquals("presentation must not normalize request input",exactRaw,
                calls.requests.get(0).targets().get(0).address());
    }

    @Test public void unambiguousAddressPresentationRedactsBeforeWhitespaceNormalization() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        ((CheckBox)activity.findViewById(R.id.auth_enabled)).setChecked(true);
        ((EditText)activity.findViewById(R.id.bearer)).setText("BEARER-NEVER-IN-LABEL");
        View row=((LinearLayout)activity.findViewById(R.id.targets)).getChildAt(0);
        EditText address=row.findViewById(R.id.address);

        address.setText("  https://user:CONTROL-CREDENTIAL@例え.テスト/a\tb"
                +"?token=QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL  ");
        String description=row.findViewById(R.id.remove).getContentDescription().toString();
        assertEquals(activity.getString(R.string.remove_target_with_address,1,
                activity.getString(R.string.kind_dns),"https://例え.テスト/a b"),description);
        for(String secret:List.of("CONTROL-CREDENTIAL","QUERY-CREDENTIAL",
                "FRAGMENT-CREDENTIAL","BEARER-NEVER-IN-LABEL"))assertFalse(description.contains(secret));

        address.setText("\nhttps://host.example/a\tb#FRAGMENT-CREDENTIAL");
        assertEquals(activity.getString(R.string.remove_target_with_address,1,
                activity.getString(R.string.kind_dns),"https://host.example/a b"),
                row.findViewById(R.id.remove).getContentDescription().toString());

        address.setText("https://host.example/path?QUERY-CREDENTIAL");
        assertEquals(activity.getString(R.string.remove_target_with_address,1,
                activity.getString(R.string.kind_dns),"https://host.example/path"),
                row.findViewById(R.id.remove).getContentDescription().toString());
        address.setText("https://host.example/path#FRAGMENT-CREDENTIAL");
        assertEquals(activity.getString(R.string.remove_target_with_address,1,
                activity.getString(R.string.kind_dns),"https://host.example/path"),
                row.findViewById(R.id.remove).getContentDescription().toString());

        String encoded="https://h.test/%40e/%3Fq/%23f";
        address.setText(encoded);
        assertEquals(activity.getString(R.string.remove_target_with_address,1,
                activity.getString(R.string.kind_dns),encoded),
                row.findViewById(R.id.remove).getContentDescription().toString());

        address.setText("  例え.テスト\t😀  ");
        assertEquals(activity.getString(R.string.remove_target_with_address,1,
                activity.getString(R.string.kind_dns),"例え.テスト 😀"),
                row.findViewById(R.id.remove).getContentDescription().toString());
    }

    @Test public void removeAccessibilityAddressIsBoundedTo48CodePointsWithoutSurrogateSplit() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        View row=((LinearLayout)activity.findViewById(R.id.targets)).getChildAt(0);
        String raw="https://host.example/"+"😀".repeat(100)+"?QUERY-CREDENTIAL#FRAGMENT-CREDENTIAL";
        ((EditText)row.findViewById(R.id.address)).setText(raw);

        String visible="https://host.example/"+"😀".repeat(100);
        String bounded=visible.substring(0,visible.offsetByCodePoints(0,47))+"…";
        assertEquals(48,bounded.codePointCount(0,bounded.length()));
        String description=row.findViewById(R.id.remove).getContentDescription().toString();
        assertEquals(activity.getString(R.string.remove_target_with_address,1,
                activity.getString(R.string.kind_dns),bounded),description);
        assertFalse(description.contains("QUERY-CREDENTIAL"));
        assertFalse(description.contains("FRAGMENT-CREDENTIAL"));
        assertWellFormedUtf16(description);
    }

    @Test public void emptyDuplicateRotatedAndReindexedRowsHaveStableCurrentNames() {
        ActivityController<MainActivity> controller=Robolectric.buildActivity(MainActivity.class).setup();
        MainActivity activity=controller.get();LinearLayout targets=activity.findViewById(R.id.targets);
        ((EditText)targets.getChildAt(0).findViewById(R.id.address)).setText("duplicate.example");
        activity.findViewById(R.id.add_target).performClick();
        activity.findViewById(R.id.add_target).performClick();
        ((EditText)targets.getChildAt(1).findViewById(R.id.address)).setText("   ");
        ((EditText)targets.getChildAt(2).findViewById(R.id.address)).setText("duplicate.example");

        assertEquals(activity.getString(R.string.remove_target_without_address,2,
                        activity.getString(R.string.kind_dns)),
                targets.getChildAt(1).findViewById(R.id.remove).getContentDescription().toString());
        assertNotEquals(targets.getChildAt(0).findViewById(R.id.remove).getContentDescription(),
                targets.getChildAt(2).findViewById(R.id.remove).getContentDescription());

        activity.onRetainNonConfigurationInstance();
        Configuration landscape=new Configuration(activity.getResources().getConfiguration());
        landscape.orientation=Configuration.ORIENTATION_LANDSCAPE;
        controller.configurationChange(landscape);activity=controller.get();targets=activity.findViewById(R.id.targets);
        assertEquals(activity.getString(R.string.remove_target_without_address,2,
                        activity.getString(R.string.kind_dns)),
                targets.getChildAt(1).findViewById(R.id.remove).getContentDescription().toString());
        assertTrue(targets.getChildAt(0).findViewById(R.id.remove).getContentDescription().toString().contains("duplicate.example"));
        assertTrue(targets.getChildAt(2).findViewById(R.id.remove).getContentDescription().toString().contains("duplicate.example"));

        targets.getChildAt(0).findViewById(R.id.remove).performClick();
        assertEquals(2,targets.getChildCount());
        assertEquals(activity.getString(R.string.remove_target_without_address,1,
                        activity.getString(R.string.kind_dns)),
                targets.getChildAt(0).findViewById(R.id.remove).getContentDescription().toString());
        assertEquals(activity.getString(R.string.remove_target_with_address,2,
                        activity.getString(R.string.kind_dns),"duplicate.example"),
                targets.getChildAt(1).findViewById(R.id.remove).getContentDescription().toString());
    }

    @Test public void rapidEditsUseLatestNameAndRemovedRowWatchersBecomeStale() {
        FakeFactory calls=new FakeFactory();DiagnosticsSession retained=new DiagnosticsSession(
                new RequestCoordinator(calls),Runnable::run,null);
        MainActivity.setSessionFactoryForTests(dispatcher->retained);
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        setValidApi(activity);LinearLayout targets=activity.findViewById(R.id.targets);
        activity.findViewById(R.id.add_target).performClick();
        View removed=targets.getChildAt(0),remaining=targets.getChildAt(1);
        EditText removedAddress=removed.findViewById(R.id.address);
        EditText removedExpected=removed.findViewById(R.id.expected_status);
        EditText removedAttempts=removed.findViewById(R.id.attempts);
        EditText remainingAddress=remaining.findViewById(R.id.address);
        remainingAddress.requestFocus();
        for(int edit=0;edit<100;edit++)remainingAddress.setText("rapid-"+edit+".example");
        assertEquals("Remove target 2, DNS lookup, rapid-99.example",
                remaining.findViewById(R.id.remove).getContentDescription().toString());
        assertTrue(remainingAddress.isFocused());

        removed.findViewById(R.id.remove).performClick();
        assertEquals("Remove target 1, DNS lookup, rapid-99.example",
                remaining.findViewById(R.id.remove).getContentDescription().toString());
        activity.findViewById(R.id.run).performClick();
        assertEquals("presentation must not change request data","rapid-99.example",
                calls.requests.get(0).targets().get(0).address());
        calls.calls.get(0).succeed(EMPTY_REPORT,ReportParser.parse(EMPTY_REPORT));
        assertEquals(RequestState.Phase.READY,retained.state().phase());
        String currentName=remaining.findViewById(R.id.remove).getContentDescription().toString();

        for(int edit=0;edit<100;edit++)removedAddress.setText("stale-"+edit+".example");
        removedExpected.setText("599");removedAttempts.setText("10");

        assertEquals(RequestState.Phase.READY,retained.state().phase());
        assertEquals(currentName,remaining.findViewById(R.id.remove).getContentDescription().toString());
        assertEquals(View.VISIBLE,activity.findViewById(R.id.report).getVisibility());
        assertEquals(1,calls.calls.size());
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

    @Test public void rawShareLifecycleUsesFixedSafePresentationStrings() {
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        assertEquals("Cleaning previous raw share",activity.getString(R.string.raw_share_state_cleaning));
        assertEquals("Preparing raw report",activity.getString(R.string.raw_share_state_preparing));
        assertEquals("Raw report prepared",activity.getString(R.string.raw_share_state_materialized));
        assertEquals("Raw report shared",activity.getString(R.string.raw_share_state_granted));
        assertEquals("Raw report share retired",activity.getString(R.string.raw_share_state_retired));
        assertEquals("Raw share runtime disposed",activity.getString(R.string.raw_share_state_disposed));
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
        String raw="{\"id\":\"r\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[{\"kind\":\"dns\",\"address\":\"example.test\",\"status\":\"healthy\",\"latency_ms\":1,\"started_at\":\"2026-09-02T00:00:00Z\",\"details\":{\"addresses\":[\"192.0.2.1\"],\"answer_count\":1}}],\"summary\":{\"total\":1,\"passed\":1,\"failed\":0}}";
        activity.renderReadyForTest(raw,ReportParser.parse(raw));
        LinearLayout results=activity.findViewById(R.id.results);
        LinearLayout candidate=(LinearLayout)results.getChildAt(0);
        Button toggle=activity.findViewById(R.id.raw_results_toggle);TextView body=activity.findViewById(R.id.raw_results_body);
        assertSame(body,candidate.getChildAt(candidate.getChildCount()-1));
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

    @Test public void everyCapabilityMismatchReasonUsesFixedLocalizedPrivateAccessibleError() {
        Map<CheckCapabilities.CapabilityMismatchException.Reason,Integer> resources = Map.of(
                CheckCapabilities.CapabilityMismatchException.Reason.CHECK_KIND, R.string.error_capability_check_kind,
                CheckCapabilities.CapabilityMismatchException.Reason.TARGET_COUNT, R.string.error_capability_target_count,
                CheckCapabilities.CapabilityMismatchException.Reason.TIMEOUT, R.string.error_capability_timeout,
                CheckCapabilities.CapabilityMismatchException.Reason.TRACEROUTE_ATTEMPTS, R.string.error_capability_traceroute_attempts,
                CheckCapabilities.CapabilityMismatchException.Reason.TOPOLOGY_MODE, R.string.error_capability_topology_mode);
        assertEquals(CheckCapabilities.CapabilityMismatchException.Reason.values().length, resources.size());

        for (CheckCapabilities.CapabilityMismatchException.Reason reason
                : CheckCapabilities.CapabilityMismatchException.Reason.values()) {
            FakeFactory calls = new FakeFactory();
            DiagnosticsSession retained = new DiagnosticsSession(new RequestCoordinator(calls), Runnable::run, null);
            MainActivity.setSessionFactoryForTests(dispatcher -> retained);
            ActivityController<MainActivity> controller = Robolectric.buildActivity(MainActivity.class).setup();
            MainActivity activity = controller.get();
            setValidApi(activity);
            ((EditText) activity.findViewById(R.id.address)).setText("PRIVATE-TARGET.example");
            activity.findViewById(R.id.run).performClick();
            calls.calls.get(0).fail(TransportException.unsupportedCapability(reason));

            TextView error = activity.findViewById(R.id.error);
            String message = error.getText().toString();
            assertEquals(activity.getString(resources.get(reason)), message);
            assertEquals(View.VISIBLE, error.getVisibility());
            assertTrue(error.isFocused());
            assertEquals(activity.getString(R.string.accessibility_error_state), error.getStateDescription());
            for (String secret : List.of("PRIVATE-TARGET", "server prose", "Bearer", "token"))
                assertFalse(message.contains(secret));
            controller.destroy();
            MainActivity.resetSessionFactoryForTests();
        }
    }

    @Test public void exactProducerApiErrorsHaveExhaustiveFixedLocalActivityPresentation() throws Exception {
        JSONArray fixtures=apiErrorContract().getJSONArray("errors");
        assertEquals(16,fixtures.length());
        FakeFactory calls=new FakeFactory();DiagnosticsSession retained=new DiagnosticsSession(
                new RequestCoordinator(calls),Runnable::run,null);
        MainActivity.setSessionFactoryForTests(dispatcher->retained);
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();
        setValidApi(activity);
        ((CheckBox)activity.findViewById(R.id.auth_enabled)).setChecked(true);
        EditText bearer=activity.findViewById(R.id.bearer);bearer.setText("LOCAL-CREDENTIAL-CANARY");
        Set<String> exercised=new LinkedHashSet<>();

        for(int index=0;index<fixtures.length();index++){
            JSONObject fixture=fixtures.getJSONObject(index);
            String key=fixture.getString("key");
            String body=fixture.getString("body");
            activity.findViewById(R.id.run).performClick();
            calls.calls.get(index).fail(apiFailure(index%2==0,fixture.getInt("status"),body,"30"));

            int localMessage=activity.getResources().getIdentifier("api_error_"+key,"string",activity.getPackageName());
            assertNotEquals("missing local presentation for "+key,0,localMessage);
            String expected=activity.getString(localMessage);
            if(fixture.getBoolean("retry_after"))
                expected+="\n"+activity.getString(R.string.retry_after,NOW.plusSeconds(30).toString());
            TextView error=activity.findViewById(R.id.error);
            assertEquals(key,expected,error.getText().toString());
            assertFalse(key,error.getText().toString().contains(fixture.getString("code")));
            assertEquals(key,fixture.getBoolean("retryable")?View.VISIBLE:View.GONE,
                    activity.findViewById(R.id.retry).getVisibility());
            assertEquals(key,View.GONE,activity.findViewById(R.id.progress).getVisibility());
            assertEquals(key,activity.getString(R.string.state_error),
                    ((TextView)activity.findViewById(R.id.request_status)).getText().toString());
            if(key.equals("unauthorized"))assertTrue("exact auth row focuses credential",bearer.isFocused());
            else assertTrue(key+" focuses fixed error",error.isFocused());
            exercised.add(key);
        }
        assertEquals(fixtures.length(),exercised.size());
    }

    @Test public void malformedUnknownAndContradictoryCriticalStatusesHaveOneInvalidServerResponseUi() throws Exception {
        JSONArray fixtures=apiErrorContract().getJSONArray("errors");
        Map<Integer,String> contradictory=Map.of(
                401,"invalid_request",422,"rate_limited",429,"internal_error",
                500,"server_busy",503,"unauthorized");
        FakeFactory calls=new FakeFactory();DiagnosticsSession retained=new DiagnosticsSession(
                new RequestCoordinator(calls),Runnable::run,null);
        MainActivity.setSessionFactoryForTests(dispatcher->retained);
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();setValidApi(activity);
        ((CheckBox)activity.findViewById(R.id.auth_enabled)).setChecked(true);
        EditText bearer=activity.findViewById(R.id.bearer);bearer.setText("CREDENTIAL-CANARY");
        int call=0;
        for(int status:new int[]{401,422,429,500,503})for(String body:new String[]{
                "<html>HOSTILE-REMOTE-PROSE</html>",
                new JSONObject().put("error",new JSONObject().put("code","UNKNOWN-HOSTILE-CODE")
                        .put("message","HOSTILE-REMOTE-PROSE")).toString(),
                new JSONObject().put("error",new JSONObject().put("code",contradictory.get(status))
                        .put("message","HOSTILE-REMOTE-PROSE")).toString()}){
            activity.findViewById(R.id.run).performClick();
            calls.calls.get(call++).fail(apiFailure(call%2==0,status,body,"30"));
            TextView error=activity.findViewById(R.id.error);
            int invalidMessage=activity.getResources().getIdentifier("api_error_invalid_server_response","string",activity.getPackageName());
            assertNotEquals("missing dedicated invalid-server-response presentation",0,invalidMessage);
            assertEquals(activity.getString(invalidMessage),error.getText().toString());
            assertTrue(error.isFocused());assertFalse(bearer.isFocused());
            assertEquals(View.GONE,activity.findViewById(R.id.retry).getVisibility());
            assertEquals(View.GONE,activity.findViewById(R.id.progress).getVisibility());
            assertFalse(error.getText().toString().contains("HOSTILE"));
            assertFalse(error.getText().toString().contains("busy"));
            assertFalse(error.getText().toString().contains("Retry-After"));
        }
        assertEquals(15,call);
        assertEquals(16,fixtures.length());
    }

    @Test public void strictWireFailuresFromBothTransportsReachOneFinalNonretryableUi() throws Exception {
        String canonical="{\"error\":{\"code\":\"server_busy\",\"message\":\"HOSTILE-PROSE\"}}";
        String deep="{\"error\":"+"[".repeat(12_000)+"]".repeat(12_000)+"}";
        List<String> malformed=List.of(
                canonical+canonical,
                canonical+" HOSTILE-TRAILING",
                "{\"error\":{\"code\":\"server_busy\",\"code\":\"server_busy\",\"message\":\"HOSTILE-PROSE\"}}",
                "{\"error\":{\"code\":\"server_busy\",\"message\":\"HOSTILE-PROSE\"},\"error\":{\"code\":\"server_busy\",\"message\":\"HOSTILE-PROSE\"}}",
                "{\"error\":{\"code\":\"server_busy\",\"message\":\"HOSTILE-PROSE\",\"extra\":[]}}",
                "{\"error\":{\"code\":\"server_busy\"}}",
                "{\"error\":{\"code\":\"server_busy\",\"message\":{}}}",
                deep);
        FakeFactory calls=new FakeFactory();DiagnosticsSession retained=new DiagnosticsSession(
                new RequestCoordinator(calls),Runnable::run,null);
        MainActivity.setSessionFactoryForTests(dispatcher->retained);
        MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();setValidApi(activity);
        int invalidMessage=activity.getResources().getIdentifier(
                "api_error_invalid_server_response","string",activity.getPackageName());
        assertNotEquals(0,invalidMessage);

        for(int index=0;index<malformed.size();index++){
            activity.findViewById(R.id.run).performClick();
            TransportException failure=apiFailure(index%2==0,503,malformed.get(index),"30");
            assertEquals(TransportException.Kind.API,failure.kind());
            calls.calls.get(index).fail(failure);

            assertEquals(RequestState.Phase.ERROR,retained.state().phase());
            assertEquals("invalid_server_response",retained.state().error().orElseThrow()
                    .apiError().orElseThrow().code());
            TextView error=activity.findViewById(R.id.error);
            assertEquals(activity.getString(invalidMessage),error.getText().toString());
            assertTrue(error.isFocused());
            assertEquals(View.GONE,activity.findViewById(R.id.retry).getVisibility());
            assertEquals(View.GONE,activity.findViewById(R.id.progress).getVisibility());
            assertFalse(error.getText().toString().contains("HOSTILE"));
            assertFalse(error.getText().toString().contains("Retry-After"));
        }
    }

    @Test public void staleApiErrorsCannotPublishFocusBusyOrRetryAcrossOwnersCount100() throws Exception {
        for(int iteration=0;iteration<100;iteration++){
            FakeFactory calls=new FakeFactory();DiagnosticsSession retained=new DiagnosticsSession(
                    new RequestCoordinator(calls),Runnable::run,null);
            MainActivity.setSessionFactoryForTests(dispatcher->retained);
            MainActivity activity=Robolectric.buildActivity(MainActivity.class).setup().get();setValidApi(activity);
            ((CheckBox)activity.findViewById(R.id.auth_enabled)).setChecked(true);
            EditText bearer=activity.findViewById(R.id.bearer);bearer.setText("STALE-CREDENTIAL");
            activity.findViewById(R.id.run).performClick();FakeCall stale=calls.calls.get(0);
            activity.findViewById(R.id.run).performClick();
            stale.fail(apiFailure(false,401,"{\"error\":{\"code\":\"unauthorized\",\"message\":\"HOSTILE\"}}",null));
            assertEquals(RequestState.Phase.LOADING,retained.state().phase());
            assertEquals(View.VISIBLE,activity.findViewById(R.id.progress).getVisibility());
            assertEquals(View.GONE,activity.findViewById(R.id.retry).getVisibility());
            assertFalse(bearer.isFocused());
            assertEquals(View.GONE,activity.findViewById(R.id.error).getVisibility());
            MainActivity.resetSessionFactoryForTests();
        }
    }

    @Test public void detachedBuildFailuresNeverPublishReadyOrPartialChildren() {
        for (MainActivity.RenderPoint fault : List.of(
                MainActivity.RenderPoint.BEFORE_FIRST_VIEW,
                MainActivity.RenderPoint.MID_BUILD)) {
            FakeFactory calls = new FakeFactory();
            DiagnosticsSession retained = new DiagnosticsSession(
                    new RequestCoordinator(calls), Runnable::run, null);
            MainActivity.setSessionFactoryForTests(dispatcher -> retained);
            MainActivity.PresentationRenderer delegate =
                    MainActivity.defaultPresentationRendererForTests();
            MainActivity.setPresentationRendererForTests((activity, presentation, checkpoint) ->
                    delegate.render(activity, presentation, point -> {
                        assertEquals(View.GONE, activity.findViewById(R.id.report).getVisibility());
                        assertFalse(activity.findViewById(R.id.share).isEnabled());
                        assertFalse(activity.findViewById(R.id.share_raw).isEnabled());
                        assertEquals(activity.getString(R.string.state_loading),
                                ((TextView) activity.findViewById(R.id.request_status)).getText().toString());
                        if (point == fault) throw new IllegalStateException("injected " + point);
                    }));
            MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
            setValidApi(activity);
            activity.findViewById(R.id.run).performClick();

            calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));

            assertEquals("network truth remains READY", RequestState.Phase.READY,
                    retained.state().phase());
            assertEquals(MainActivity.PresentationPhase.RENDER_ERROR,
                    activity.presentationPhaseForTests());
            assertEquals(View.GONE, activity.findViewById(R.id.report).getVisibility());
            assertEquals(0, ((LinearLayout) activity.findViewById(R.id.results)).getChildCount());
            assertFalse(activity.findViewById(R.id.share).isEnabled());
            assertFalse(activity.findViewById(R.id.share_raw).isEnabled());
            assertEquals(activity.getString(R.string.state_render_error),
                    ((TextView) activity.findViewById(R.id.request_status)).getText().toString());
            MainActivity.resetSessionFactoryForTests();
        }
    }

    @Test public void commitAndEveryPostSwapPublicationFailureRestoreFixedEmptyErrorUi() {
        List<MainActivity.CommitPoint> checkpointFaults = List.of(
                MainActivity.CommitPoint.PRE_SWAP,
                MainActivity.CommitPoint.REPORT_VISIBILITY,
                MainActivity.CommitPoint.READY_STATUS,
                MainActivity.CommitPoint.FOCUS,
                MainActivity.CommitPoint.LIVE_ANNOUNCEMENT,
                MainActivity.CommitPoint.SHARE_ELIGIBILITY);
        for (MainActivity.CommitPoint fault : checkpointFaults) {
            FakeFactory calls = new FakeFactory();
            DiagnosticsSession retained = new DiagnosticsSession(
                    new RequestCoordinator(calls), Runnable::run, null);
            MainActivity.setSessionFactoryForTests(dispatcher -> retained);
            MainActivity.PresentationCommitter delegate =
                    MainActivity.defaultPresentationCommitterForTests();
            MainActivity.setPresentationCommitterForTests(new MainActivity.PresentationCommitter() {
                public void checkpoint(MainActivity.CommitPoint point) {
                    if (point == fault) throw new IllegalStateException("injected " + point);
                }
                public void swap(LinearLayout target, View candidate) {
                    delegate.swap(target, candidate);
                }
                public void rollback(LinearLayout target) { delegate.rollback(target); }
            });
            MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
            setValidApi(activity); activity.findViewById(R.id.run).performClick();
            calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));
            assertFixedPresentationError(activity, retained);
            MainActivity.resetSessionFactoryForTests();
        }

        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        MainActivity.PresentationCommitter delegate =
                MainActivity.defaultPresentationCommitterForTests();
        MainActivity.setPresentationCommitterForTests(new MainActivity.PresentationCommitter() {
            public void checkpoint(MainActivity.CommitPoint ignored) { }
            public void swap(LinearLayout target, View candidate) {
                MainActivity activity = (MainActivity) target.getContext();
                assertEquals(View.GONE, activity.findViewById(R.id.report).getVisibility());
                assertFalse(activity.findViewById(R.id.share).isEnabled());
                target.addView(new TextView(target.getContext()));
                target.addView(candidate);
                throw new IllegalStateException("injected partial swap");
            }
            public void rollback(LinearLayout target) { delegate.rollback(target); }
        });
        MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
        setValidApi(activity); activity.findViewById(R.id.run).performClick();
        calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));
        assertFixedPresentationError(activity, retained);
    }

    @Test public void rollbackFailureIsContainedByHardEmptyFallback() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        MainActivity.PresentationCommitter delegate =
                MainActivity.defaultPresentationCommitterForTests();
        MainActivity.setPresentationCommitterForTests(new MainActivity.PresentationCommitter() {
            public void checkpoint(MainActivity.CommitPoint point) {
                if (point == MainActivity.CommitPoint.READY_STATUS)
                    throw new IllegalStateException("post-swap failure");
            }
            public void swap(LinearLayout target, View candidate) { delegate.swap(target, candidate); }
            public void rollback(LinearLayout target) {
                throw new IllegalStateException("rollback failure");
            }
        });
        MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
        setValidApi(activity); activity.findViewById(R.id.run).performClick();
        calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));
        assertFixedPresentationError(activity, retained);
    }

    @Test public void ownerChangeDuringDetachedBuildDiscardsCandidateWithoutOverwritingCancelledUi() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        MainActivity.PresentationRenderer delegate = MainActivity.defaultPresentationRendererForTests();
        MainActivity.setPresentationRendererForTests((activity, presentation, ignored) ->
                delegate.render(activity, presentation, point -> {
                    if (point == MainActivity.RenderPoint.MID_BUILD) retained.cancel();
                }));
        MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
        setValidApi(activity); activity.findViewById(R.id.run).performClick();
        calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));

        assertEquals(RequestState.Phase.CANCELLED, retained.state().phase());
        assertEquals(activity.getString(R.string.state_cancelled),
                ((TextView) activity.findViewById(R.id.request_status)).getText().toString());
        assertEquals(0, ((LinearLayout) activity.findViewById(R.id.results)).getChildCount());
        assertFalse(activity.findViewById(R.id.share).isEnabled());
    }

    @Test public void ownerChangeImmediatelyBeforeSwapDiscardsDetachedCandidate() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        MainActivity.PresentationCommitter delegate = MainActivity.defaultPresentationCommitterForTests();
        MainActivity.setPresentationCommitterForTests(new MainActivity.PresentationCommitter() {
            public void checkpoint(MainActivity.CommitPoint point) {
                if (point == MainActivity.CommitPoint.PRE_SWAP) retained.cancel();
            }
            public void swap(LinearLayout target, View candidate) { delegate.swap(target, candidate); }
            public void rollback(LinearLayout target) { delegate.rollback(target); }
        });
        MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
        setValidApi(activity); activity.findViewById(R.id.run).performClick();
        calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));

        assertEquals(RequestState.Phase.CANCELLED, retained.state().phase());
        assertEquals(0, ((LinearLayout) activity.findViewById(R.id.results)).getChildCount());
        assertEquals(View.GONE, activity.findViewById(R.id.report).getVisibility());
        assertFalse(activity.findViewById(R.id.share).isEnabled());
    }

    @Test public void stalePostSwapFailureCannotRollbackAReentrantNewOwnerCommit() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        MainActivity.PresentationCommitter delegate = MainActivity.defaultPresentationCommitterForTests();
        MainActivity[] activityRef = {null};
        boolean[] first = {true};
        View[] firstCandidate = {null};
        View[] newerCandidate = {null};
        long[] newerOwner = {-1};
        Report secondReport = ReportParser.parse(SECOND_REPORT);
        MainActivity.setPresentationCommitterForTests(new MainActivity.PresentationCommitter() {
            public void checkpoint(MainActivity.CommitPoint point) {
                if (!first[0] || point != MainActivity.CommitPoint.REPORT_VISIBILITY) return;
                first[0] = false;
                activityRef[0].findViewById(R.id.run).performClick();
                newerOwner[0] = retained.state().ownerId();
                calls.calls.get(1).succeed(SECOND_REPORT, secondReport);
                throw new IllegalStateException("stale first-owner publication failure");
            }
            public void swap(LinearLayout target, View candidate) {
                if (firstCandidate[0] == null) firstCandidate[0] = candidate;
                else newerCandidate[0] = candidate;
                delegate.swap(target, candidate);
            }
            public void rollback(LinearLayout target) { delegate.rollback(target); }
        });
        MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
        activityRef[0] = activity;
        setValidApi(activity); activity.findViewById(R.id.run).performClick();
        calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));

        assertEquals(RequestState.Phase.READY, retained.state().phase());
        assertEquals(MainActivity.PresentationPhase.RENDERED, activity.presentationPhaseForTests());
        assertEquals(View.VISIBLE, activity.findViewById(R.id.report).getVisibility());
        assertTrue(activity.findViewById(R.id.share).isEnabled());
        assertTrue(activity.findViewById(R.id.share_raw).isEnabled());
        assertTrue(activity.findViewById(R.id.report).isFocused());
        assertEquals(activity.getString(R.string.state_ready),
                ((TextView) activity.findViewById(R.id.request_status)).getText().toString());
        LinearLayout results = activity.findViewById(R.id.results);
        assertSame("stale publication failure cannot replace the newer hierarchy",
                newerCandidate[0], results.getChildAt(0));
        assertSame(results, newerCandidate[0].getParent());
        assertNull("stale candidate must be detached", firstCandidate[0].getParent());
        assertTrue(((android.view.ViewGroup) newerCandidate[0]).getChildCount() > 0);
        assertEquals(newerOwner[0], retained.state().ownerId());
        assertSame(secondReport, retained.state().report().orElseThrow());
        assertReportIdsAbsent(results);
    }

    @Test public void staleFaultingSwapCannotEraseReentrantNewOwnerHierarchy() {
        for (int iteration = 0; iteration < 100; iteration++) {
            try {
                assertStaleFaultingSwapCannotEraseReentrantNewOwnerHierarchy();
            } finally {
                MainActivity.resetSessionFactoryForTests();
            }
        }
    }

    private void assertStaleFaultingSwapCannotEraseReentrantNewOwnerHierarchy() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        MainActivity.PresentationCommitter delegate =
                MainActivity.defaultPresentationCommitterForTests();
        MainActivity[] activityRef = {null};
        boolean[] outer = {true};
        View[] staleCandidate = {null};
        View[] newerCandidate = {null};
        long[] newerOwner = {-1};
        Report secondReport = ReportParser.parse(SECOND_REPORT);
        MainActivity.setPresentationCommitterForTests(new MainActivity.PresentationCommitter() {
            public void checkpoint(MainActivity.CommitPoint point) { }
            public void swap(LinearLayout target, View candidate) {
                if (!outer[0]) {
                    newerCandidate[0] = candidate;
                    delegate.swap(target, candidate);
                    return;
                }
                outer[0] = false;
                staleCandidate[0] = candidate;
                activityRef[0].findViewById(R.id.run).performClick();
                newerOwner[0] = retained.state().ownerId();
                calls.calls.get(1).succeed(SECOND_REPORT, secondReport);
                delegate.swap(target, candidate);
                throw new IllegalStateException("stale swap fault after partial mutation");
            }
            public void rollback(LinearLayout target) { delegate.rollback(target); }
        });
        MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
        activityRef[0] = activity;
        setValidApi(activity);
        activity.findViewById(R.id.run).performClick();
        calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));

        assertEquals(RequestState.Phase.READY, retained.state().phase());
        assertEquals(MainActivity.PresentationPhase.RENDERED,
                activity.presentationPhaseForTests());
        LinearLayout results = activity.findViewById(R.id.results);
        assertEquals("new owner must retain exactly one complete hierarchy", 1,
                results.getChildCount());
        assertSame("stale faulting swap must restore the exact newer hierarchy",
                newerCandidate[0], results.getChildAt(0));
        assertSame(results, newerCandidate[0].getParent());
        assertNull("stale hierarchy must not remain installed", staleCandidate[0].getParent());
        assertTrue(((android.view.ViewGroup) newerCandidate[0]).getChildCount() > 0);
        assertEquals(newerOwner[0], retained.state().ownerId());
        assertSame(secondReport, retained.state().report().orElseThrow());
        assertReportIdsAbsent(results);
    }

    @Test public void fixedRenderErrorOwnsFocusAfterPostFocusFailure() {
        for (int iteration = 0; iteration < 100; iteration++) {
            try {
                assertFixedRenderErrorOwnsFocusAfterPostFocusFailure();
            } finally {
                MainActivity.resetSessionFactoryForTests();
            }
        }
    }

    private void assertFixedRenderErrorOwnsFocusAfterPostFocusFailure() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        MainActivity.PresentationCommitter delegate =
                MainActivity.defaultPresentationCommitterForTests();
        MainActivity.setPresentationCommitterForTests(new MainActivity.PresentationCommitter() {
            public void checkpoint(MainActivity.CommitPoint point) {
                if (point == MainActivity.CommitPoint.LIVE_ANNOUNCEMENT)
                    throw new IllegalStateException("fault after report focus");
            }
            public void swap(LinearLayout target, View candidate) {
                delegate.swap(target, candidate);
            }
            public void rollback(LinearLayout target) { delegate.rollback(target); }
        });
        MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
        setValidApi(activity);
        activity.findViewById(R.id.run).performClick();
        calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));

        assertEquals(MainActivity.PresentationPhase.RENDER_ERROR,
                activity.presentationPhaseForTests());
        assertEquals(View.VISIBLE, activity.findViewById(R.id.error).getVisibility());
        assertTrue("fixed visible error must receive focus after hidden report rollback",
                activity.findViewById(R.id.error).isFocused());
    }

    @Test public void retryUsesFreshOwnerAndRotationRebuildsRetainedReadyThroughRenderer() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        int[] renders = {0};
        MainActivity.PresentationRenderer delegate = MainActivity.defaultPresentationRendererForTests();
        MainActivity.setPresentationRendererForTests((activity, presentation, checkpoint) -> {
            renders[0]++;
            if (renders[0] == 1) throw new IllegalStateException("first presentation fails");
            return delegate.render(activity, presentation, checkpoint);
        });
        ActivityController<MainActivity> controller = Robolectric.buildActivity(MainActivity.class).setup();
        MainActivity old = controller.get(); setValidApi(old); old.findViewById(R.id.run).performClick();
        long firstOwner = retained.state().ownerId();
        calls.calls.get(0).succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));
        assertEquals(1, renders[0]);
        assertEquals(MainActivity.PresentationPhase.RENDER_ERROR, old.presentationPhaseForTests());
        assertEquals(View.VISIBLE, old.findViewById(R.id.retry).getVisibility());

        old.findViewById(R.id.retry).performClick();
        assertTrue(retained.state().ownerId() > firstOwner);
        long secondOwner = retained.state().ownerId();
        assertEquals(RequestState.Phase.LOADING, retained.state().phase());
        Report secondReport = ReportParser.parse(SECOND_REPORT);
        calls.calls.get(1).succeed(SECOND_REPORT, secondReport);
        assertEquals(2, renders[0]);
        LinearLayout oldResults = old.findViewById(R.id.results);
        assertEquals(1, oldResults.getChildCount());
        View oldHierarchy = oldResults.getChildAt(0);
        int completeChildCount = ((android.view.ViewGroup) oldHierarchy).getChildCount();
        assertTrue(completeChildCount > 0);
        assertEquals(secondOwner, retained.state().ownerId());
        assertSame(secondReport, retained.state().report().orElseThrow());
        assertReportIdsAbsent(oldResults);

        old.onRetainNonConfigurationInstance();
        Configuration landscape = new Configuration(old.getResources().getConfiguration());
        landscape.orientation = Configuration.ORIENTATION_LANDSCAPE;
        controller.configurationChange(landscape);
        MainActivity current = controller.get();
        assertEquals(3, renders[0]);
        assertEquals(MainActivity.PresentationPhase.RENDERED, current.presentationPhaseForTests());
        assertEquals(View.VISIBLE, current.findViewById(R.id.report).getVisibility());
        LinearLayout currentResults = current.findViewById(R.id.results);
        assertEquals(1, currentResults.getChildCount());
        View rebuiltHierarchy = currentResults.getChildAt(0);
        assertNotSame("rotation must rebuild a distinct hierarchy", oldHierarchy, rebuiltHierarchy);
        assertEquals("rotation must rebuild the complete hierarchy", completeChildCount,
                ((android.view.ViewGroup) rebuiltHierarchy).getChildCount());
        assertSame(currentResults, rebuiltHierarchy.getParent());
        assertEquals("rotation retains the retry owner", secondOwner, retained.state().ownerId());
        assertSame("rotation retains the exact parsed SECOND report", secondReport,
                retained.state().report().orElseThrow());
        assertReportIdsAbsent(currentResults);
    }

    @Test public void staleCallbackAfterRetryCannotReplaceNewerRenderedHierarchy() {
        FakeFactory calls = new FakeFactory();
        DiagnosticsSession retained = new DiagnosticsSession(
                new RequestCoordinator(calls), Runnable::run, null);
        MainActivity.setSessionFactoryForTests(dispatcher -> retained);
        int[] renderGeneration = {0};
        MainActivity.PresentationRenderer delegate = MainActivity.defaultPresentationRendererForTests();
        MainActivity.setPresentationRendererForTests((owner, presentation, checkpoint) -> {
            renderGeneration[0]++;
            return delegate.render(owner, presentation, checkpoint);
        });
        MainActivity activity = Robolectric.buildActivity(MainActivity.class).setup().get();
        setValidApi(activity); activity.findViewById(R.id.run).performClick();
        FakeCall stale = calls.calls.get(0);
        activity.findViewById(R.id.run).performClick();
        long currentOwner = retained.state().ownerId();
        FakeCall current = calls.calls.get(1);
        Report secondReport = ReportParser.parse(SECOND_REPORT);
        current.succeed(SECOND_REPORT, secondReport);
        LinearLayout results = activity.findViewById(R.id.results);
        assertEquals(1, results.getChildCount());
        View currentHierarchy = results.getChildAt(0);
        stale.succeed(EMPTY_REPORT, ReportParser.parse(EMPTY_REPORT));

        assertEquals(MainActivity.PresentationPhase.RENDERED, activity.presentationPhaseForTests());
        assertEquals("stale callback must not start another presentation generation", 1,
                renderGeneration[0]);
        assertEquals(1, results.getChildCount());
        assertSame("stale callback must not replace the committed hierarchy",
                currentHierarchy, results.getChildAt(0));
        assertSame(results, currentHierarchy.getParent());
        assertEquals(currentOwner, retained.state().ownerId());
        assertSame(secondReport, retained.state().report().orElseThrow());
        assertReportIdsAbsent(results);
        assertTrue(activity.findViewById(R.id.share).isEnabled());
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
    private static final String SECOND_REPORT="{\"id\":\"r2\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:01Z\",\"duration_ms\":2,\"results\":[],\"summary\":{\"total\":0,\"passed\":0,\"failed\":0}}";
    private static final Instant NOW=Instant.parse("2026-09-01T12:00:00Z");
    private static JSONObject apiErrorContract() throws Exception {
        Path fixture=Paths.get(System.getProperty("user.dir"),"..","..","testdata","api-error-contract.json").normalize();
        assertTrue("missing producer fixture at "+fixture,Files.isRegularFile(fixture));
        return new JSONObject(new String(Files.readAllBytes(fixture),StandardCharsets.UTF_8));
    }
    private static TransportException apiFailure(boolean report,int status,String body,String retryAfter) throws Exception {
        ErrorConnection connection=new ErrorConnection(status,body);
        if(retryAfter!=null)connection.headers.put("Retry-After",List.of(retryAfter));
        ApiConnectionConfig config=ApiConnectionConfig.create("https://api.example.test",false,null);
        ReportTransport.Clock clock=new ReportTransport.Clock(){
            public long nanoTime(){return 0L;}public Instant now(){return NOW;}
        };
        ReportTransport.Scheduler scheduler=(task,delay)->()->{};
        try{
            if(report){
                ReportRequest request=ReportRequest.builder().timeoutMs(1_000)
                        .addTarget(TargetInput.of(CheckKind.DNS,"example.test")).build();
                new ReportTransport(config,url->connection,clock,scheduler).newCall(request).execute();
            }else new CapabilitiesTransport(config,url->connection,clock,scheduler).newCall().execute();
            fail("expected API failure");return null;
        }catch(TransportException failure){
            assertEquals(TransportException.Kind.API,failure.kind());return failure;
        }
    }
    private static final class ErrorConnection extends HttpURLConnection {
        final byte[] body;final Map<String,List<String>> headers=new LinkedHashMap<>();
        final ByteArrayOutputStream output=new ByteArrayOutputStream();
        ErrorConnection(int status,String body) throws Exception {super(new URL("https://fake.invalid"));responseCode=status;this.body=body.getBytes(StandardCharsets.UTF_8);}
        @Override public int getResponseCode(){return responseCode;}
        @Override public long getContentLengthLong(){return -1L;}
        @Override public java.io.InputStream getErrorStream(){return new ByteArrayInputStream(body);}
        @Override public java.io.OutputStream getOutputStream(){return output;}
        @Override public Map<String,List<String>> getHeaderFields(){return headers;}
        @Override public void disconnect(){}
        @Override public boolean usingProxy(){return false;}
        @Override public void connect(){}
    }
    private static void assertFixedPresentationError(MainActivity activity, DiagnosticsSession retained) {
        assertEquals("presentation failure cannot rewrite parsed truth", RequestState.Phase.READY,
                retained.state().phase());
        assertEquals(MainActivity.PresentationPhase.RENDER_ERROR,
                activity.presentationPhaseForTests());
        assertEquals(0, ((LinearLayout) activity.findViewById(R.id.results)).getChildCount());
        assertEquals(View.GONE, activity.findViewById(R.id.report).getVisibility());
        assertFalse(activity.findViewById(R.id.share).isEnabled());
        assertFalse(activity.findViewById(R.id.share_raw).isEnabled());
        assertEquals(activity.getString(R.string.state_render_error),
                ((TextView) activity.findViewById(R.id.request_status)).getText().toString());
        TextView error = activity.findViewById(R.id.error);
        assertEquals(View.VISIBLE, error.getVisibility());
        assertEquals(activity.getString(R.string.error_render_report),
                error.getText().toString());
        assertEquals(activity.getString(R.string.accessibility_error_state),
                error.getStateDescription());
        assertTrue("fixed render error must own focus", error.isFocused());
    }
    private static String allText(View view) {
        StringBuilder out = new StringBuilder();
        appendText(view, out);
        return out.toString();
    }
    private static void assertReportIdsAbsent(View humanPresentation) {
        String text = allText(humanPresentation);
        assertFalse("r1 must stay out of human presentation", text.contains("r1"));
        assertFalse("r2 must stay out of human presentation", text.contains("r2"));
    }
    private static void appendText(View view, StringBuilder out) {
        if (view instanceof TextView text) out.append(text.getText()).append('\n');
        if (view instanceof android.view.ViewGroup group)
            for (int i = 0; i < group.getChildCount(); i++) appendText(group.getChildAt(i), out);
    }
    private static void assertWellFormedUtf16(String value) {
        for(int i=0;i<value.length();i++) {
            char current=value.charAt(i);
            if(Character.isHighSurrogate(current)) {
                assertTrue("high surrogate must have a low pair",
                        i+1<value.length()&&Character.isLowSurrogate(value.charAt(++i)));
            } else assertFalse("isolated low surrogate",Character.isLowSurrogate(current));
        }
    }
    private static final class FakeFactory implements RequestCoordinator.CallFactory {final List<FakeCall> calls=new ArrayList<>();final List<ReportRequest> requests=new ArrayList<>();public RequestCoordinator.CancellableCall create(ReportRequest request){requests.add(request);FakeCall c=new FakeCall();calls.add(c);return c;}}
    private static final class FakeCall implements RequestCoordinator.CancellableCall {RequestCoordinator.Callback callback;int cancels;public void start(RequestCoordinator.Callback value){callback=value;}public void cancel(){cancels++;}void succeed(String raw,Report report){callback.onSuccess(raw,report);}void fail(TransportException error){callback.onError(error);}}
    private static void setValidApi(MainActivity activity){((EditText)activity.findViewById(R.id.api_url)).setText("https://api.example.test");}
    private static int index(CheckKind kind){for(int i=0;i<TargetKindOption.all().size();i++)if(TargetKindOption.all().get(i).kind()==kind)return i;return -1;}
}
