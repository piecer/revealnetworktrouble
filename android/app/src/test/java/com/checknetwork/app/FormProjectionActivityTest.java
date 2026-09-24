package com.checknetwork.app;

import static org.junit.Assert.*;

import android.os.Looper;
import android.view.View;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.Spinner;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.network.ApiConnectionConfig;
import com.checknetwork.app.network.CapabilitiesTransport;
import com.checknetwork.app.network.OperationDeadline;
import com.checknetwork.app.network.ReportTransport;
import com.checknetwork.app.state.RequestState;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;

/** Real Activity -> form -> production session -> transport POST serialization. */
@RunWith(RobolectricTestRunner.class)
@Config(sdk=35)
public final class FormProjectionActivityTest {
    private static final String CHECKS="{\"kinds\":[\"dns\",\"tcp\",\"http\",\"https\",\"traceroute\"],\"topology_modes\":[\"full\",\"compact\"],\"limits\":{\"max_targets\":20,\"max_traceroute_attempts\":10,\"timeout_ms_min\":100,\"timeout_ms_max\":30000}}";
    private static final String REPORT="{\"id\":\"r1\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[],\"summary\":{\"total\":0,\"passed\":0,\"failed\":0}}";
    private ExecutorService worker=Executors.newSingleThreadExecutor();
    private final List<Connection> posts=new ArrayList<>();
    private ActivityController<MainActivity> controller;
    private DiagnosticsSession session;

    @After public void close() throws Exception {
        if(controller!=null&&!controller.get().isDestroyed())controller.destroy();
        worker.shutdownNow();assertTrue(worker.awaitTermination(5,TimeUnit.SECONDS));
        MainActivity.resetSessionFactoryForTests();
    }

    @Test public void httpDraftDoesNotBlockDnsOrLeakIntoPost() throws Exception {
        MainActivity activity=create();
        select(activity,CheckKind.HTTP,"http://example.test");
        field(activity,R.id.expected_status).setText("201");
        select(activity,CheckKind.DNS,"example.test");
        assertEquals(View.GONE,field(activity,R.id.expected_status).getVisibility());
        JSONObject target=run(activity);
        assertEquals("dns",target.getString("kind"));
        assertFalse(target.has("expected_status"));assertFalse(target.has("attempts"));
        select(activity,CheckKind.HTTP,"http://example.test");
        assertEquals("201",field(activity,R.id.expected_status).getText().toString());
        assertEquals(201,run(activity).getInt("expected_status"));
    }

    @Test public void traceDraftDoesNotBlockHttpOrLeakIntoPost() throws Exception {
        MainActivity activity=create();select(activity,CheckKind.TRACEROUTE,"example.test");
        field(activity,R.id.attempts).setText("4");
        select(activity,CheckKind.HTTP,"http://example.test");
        field(activity,R.id.expected_status).setText("204");
        assertEquals(View.GONE,field(activity,R.id.attempts).getVisibility());
        JSONObject target=run(activity);
        assertEquals("http",target.getString("kind"));assertEquals(204,target.getInt("expected_status"));assertFalse(target.has("attempts"));
        select(activity,CheckKind.TRACEROUTE,"example.test");
        assertEquals("4",field(activity,R.id.attempts).getText().toString());
        target=run(activity);assertEquals(4,target.getInt("attempts"));assertFalse(target.has("expected_status"));
    }

    @Test public void saveRecreateKeepsInactiveDraftsWithoutSubmittingThem() throws Exception {
        MainActivity activity=create();select(activity,CheckKind.HTTP,"http://example.test");
        field(activity,R.id.expected_status).setText("202");
        select(activity,CheckKind.TRACEROUTE,"example.test");field(activity,R.id.attempts).setText("3");
        select(activity,CheckKind.DNS,"example.test");
        android.os.Bundle saved=new android.os.Bundle();controller.saveInstanceState(saved);
        controller.pause().stop().destroy();
        controller=Robolectric.buildActivity(MainActivity.class).create(saved).start().restoreInstanceState(saved).resume().visible();
        activity=controller.get();
        assertEquals("202",field(activity,R.id.expected_status).getText().toString());
        assertEquals("3",field(activity,R.id.attempts).getText().toString());
        JSONObject target=run(activity);assertEquals("dns",target.getString("kind"));
        assertFalse(target.has("expected_status"));assertFalse(target.has("attempts"));
        select(activity,CheckKind.HTTP,"http://example.test");assertEquals(202,run(activity).getInt("expected_status"));
        select(activity,CheckKind.TRACEROUTE,"example.test");assertEquals(3,run(activity).getInt("attempts"));
    }

    @Test public void httpAndHttpsDraftsProjectAwayForDnsAndTcpAcrossRotation() throws Exception {
        MainActivity activity=create();
        for(CheckKind source:List.of(CheckKind.HTTP,CheckKind.HTTPS))for(CheckKind destination:List.of(CheckKind.DNS,CheckKind.TCP)){
            select(activity,source,source==CheckKind.HTTP?"http://example.test":"https://example.test");
            field(activity,R.id.expected_status).setText("203");
            select(activity,destination,destination==CheckKind.DNS?"example.test":"example.test:443");
            activity.onRetainNonConfigurationInstance();
            android.content.res.Configuration config=new android.content.res.Configuration(activity.getResources().getConfiguration());
            config.orientation=config.orientation==android.content.res.Configuration.ORIENTATION_LANDSCAPE?android.content.res.Configuration.ORIENTATION_PORTRAIT:android.content.res.Configuration.ORIENTATION_LANDSCAPE;
            controller.configurationChange(config);activity=controller.get();
            JSONObject target=run(activity);assertEquals(destination.wireValue(),target.getString("kind"));
            assertFalse(target.has("expected_status"));assertFalse(target.has("attempts"));
            select(activity,source,source==CheckKind.HTTP?"http://example.test":"https://example.test");
            assertEquals("203",field(activity,R.id.expected_status).getText().toString());
            assertEquals(203,run(activity).getInt("expected_status"));
        }
    }

    @Test public void traceDraftProjectsAwayForDnsAndReturnsUnchanged() throws Exception {
        MainActivity activity=create();select(activity,CheckKind.TRACEROUTE,"example.test");field(activity,R.id.attempts).setText("5");
        select(activity,CheckKind.DNS,"example.test");JSONObject target=run(activity);
        assertFalse(target.has("attempts"));assertFalse(target.has("expected_status"));
        select(activity,CheckKind.TRACEROUTE,"example.test");assertEquals(5,run(activity).getInt("attempts"));
    }

    @Test public void projectionDoesNotRelaxDirectFormOrTargetValidation(){
        assertThrows(IllegalArgumentException.class,()->new FormState("https://api.example.test",5000,List.of(new FormState.Target(CheckKind.DNS,"example.test","200",""))).toRequest());
        assertThrows(IllegalArgumentException.class,()->new FormState("https://api.example.test",5000,List.of(new FormState.Target(CheckKind.HTTP,"http://example.test","","3"))).toRequest());
        assertThrows(IllegalArgumentException.class,()->com.checknetwork.app.core.TargetInput.builder(CheckKind.DNS,"example.test").expectedStatus(200).build());
        assertThrows(IllegalArgumentException.class,()->com.checknetwork.app.core.TargetInput.builder(CheckKind.HTTP,"http://example.test").attempts(3).build());
    }

    private MainActivity create(){
        ReportTransport.Clock clock=new ReportTransport.Clock(){public long nanoTime(){return 0;}public Instant now(){return Instant.EPOCH;}};
        ReportTransport.Scheduler scheduler=(task,delay)->()->{};
        MainActivity.setSessionFactoryForTests(dispatcher->{
            if(worker.isShutdown())worker=Executors.newSingleThreadExecutor();
            session=DiagnosticsSession.createProductionForTests(dispatcher,worker,new DiagnosticsSession.ProductionTransports(){
                public CapabilitiesTransport capabilities(ApiConnectionConfig config,OperationDeadline deadline){
                    return new CapabilitiesTransport(config,url->new Connection(url,CHECKS),clock,scheduler,deadline);
                }
                public ReportTransport reports(ApiConnectionConfig config,OperationDeadline deadline){
                    return new ReportTransport(config,url->{Connection connection=new Connection(url,REPORT);posts.add(connection);return connection;},clock,scheduler,deadline);
                }
            },clock);return session;
        });
        controller=Robolectric.buildActivity(MainActivity.class).setup();
        MainActivity activity=controller.get();
        ((EditText)activity.findViewById(R.id.api_url)).setText("https://api.example.test");
        return activity;
    }
    private static EditText field(MainActivity activity,int id){return row(activity).findViewById(id);}
    private static View row(MainActivity activity){return ((LinearLayout)activity.findViewById(R.id.targets)).getChildAt(0);}
    private static void select(MainActivity activity,CheckKind kind,String address){
        ((Spinner)row(activity).findViewById(R.id.kind)).setSelection(kind.ordinal());
        Shadows.shadowOf(Looper.getMainLooper()).idle();field(activity,R.id.address).setText(address);
    }
    private JSONObject run(MainActivity activity)throws Exception{
        int before=posts.size();activity.findViewById(R.id.run).performClick();
        worker.submit(()->{}).get(5,TimeUnit.SECONDS);Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals("visible form must reach POST",before+1,posts.size());
        assertEquals(RequestState.Phase.READY,session.state().phase());
        Connection post=posts.get(before);assertEquals("POST",post.getRequestMethod());
        JSONObject body=new JSONObject(post.output.toString(StandardCharsets.UTF_8));
        assertEquals(1,body.getJSONArray("targets").length());
        return body.getJSONArray("targets").getJSONObject(0);
    }
    private static final class Connection extends HttpURLConnection {
        final byte[] body;final ByteArrayOutputStream output=new ByteArrayOutputStream();
        Connection(URL url,String body){super(url);this.body=body.getBytes(StandardCharsets.UTF_8);}
        @Override public int getResponseCode(){return 200;}
        @Override public long getContentLengthLong(){return body.length;}
        @Override public java.io.InputStream getInputStream(){return new ByteArrayInputStream(body);}
        @Override public java.io.OutputStream getOutputStream(){return output;}
        @Override public void disconnect(){}
        @Override public boolean usingProxy(){return false;}
        @Override public void connect(){}
    }
}
