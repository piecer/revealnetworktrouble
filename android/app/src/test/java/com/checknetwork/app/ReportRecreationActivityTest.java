package com.checknetwork.app;

import static org.junit.Assert.*;
import android.app.AlertDialog;
import android.content.Intent;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.EditText;
import android.widget.Spinner;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.network.ApiConnectionConfig;
import com.checknetwork.app.state.RequestState;
import java.net.ServerSocket;
import java.net.Socket;
import java.io.*;
import java.util.concurrent.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.TimeUnit;
import org.junit.*;
import org.junit.runner.RunWith;
import org.robolectric.*;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.*;
import org.robolectric.shadows.ShadowAlertDialog;

/** Native form notifications, real HTTP and production diagnostics; no injected READY. */
@RunWith(RobolectricTestRunner.class)
@Config(sdk=35)
@LooperMode(LooperMode.Mode.PAUSED)
public final class ReportRecreationActivityTest {
    private ActivityController<MainActivity> controller;
    private ServerSocket server;
    private ExecutorService peer = Executors.newSingleThreadExecutor();
    private Future<?> serving;
    private DiagnosticsSession session;
    private String raw=RAW;
    private byte[] contextPayload;
    private final CountDownLatch contextHeaders=new CountDownLatch(1),contextRelease=new CountDownLatch(1);
    private final AtomicInteger contexts=new AtomicInteger();
    private IPContextSession context;
    private ExecutorService contextWorker;
    private final java.util.List<com.checknetwork.app.network.IPContextTransport.Call> calls=new java.util.concurrent.CopyOnWriteArrayList<>();
    private final AtomicInteger posts = new AtomicInteger();
    private static final String RAW = "{\"id\":\"retained\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[],\"summary\":{\"total\":0,\"passed\":0,\"failed\":0}}";
    private MainActivity activity() { return controller.get(); }
    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private View row(int i) { return ((ViewGroup) activity().findViewById(R.id.targets)).getChildAt(i); }
    private void create() throws Exception {
        server = new ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1"));
        serving = peer.submit(() -> {
            try {
                while (!server.isClosed()) try (Socket socket = server.accept()) {
                    socket.setSoTimeout(5000);
                    BufferedReader input = new BufferedReader(new InputStreamReader(socket.getInputStream(),StandardCharsets.US_ASCII));
                    String request = input.readLine(), line; int length=0;
                    while (!(line=input.readLine()).isEmpty()) if(line.toLowerCase(java.util.Locale.ROOT).startsWith("content-length:")) length=Integer.parseInt(line.substring(15).trim());
                    for(int n=0;n<length;n++) assertTrue(input.read()>=0);
                    String kinds = java.util.Arrays.stream(CheckKind.values()).map(k -> "\""+k.wireValue()+"\"").collect(java.util.stream.Collectors.joining(","));
                    String body = "{\"kinds\":["+kinds+"],\"topology_modes\":[\"full\",\"compact\"],\"limits\":{\"max_targets\":20,\"max_traceroute_attempts\":10,\"timeout_ms_min\":100,\"timeout_ms_max\":30000}}";
                    if(request.startsWith("POST /api/v1/ip-context")) {
                        contexts.incrementAndGet();
                        if(contextPayload!=null){socket.getOutputStream().write(("HTTP/1.1 200 OK\r\nContent-Length: "+contextPayload.length+"\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));socket.getOutputStream().write(contextPayload);socket.getOutputStream().flush();continue;}
                        socket.getOutputStream().write("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\nConnection: close\r\n\r\n{".getBytes(StandardCharsets.US_ASCII));socket.getOutputStream().flush();contextHeaders.countDown();
                        try { assertTrue(contextRelease.await(5,TimeUnit.SECONDS)); } catch(InterruptedException e) {throw new AssertionError(e);}
                        continue;
                    }
                    if(request.startsWith("POST /api/v1/reports")) { posts.incrementAndGet(); body=raw; }
                    else assertTrue(request.startsWith("GET /api/v1/checks"));
                    byte[] bytes=body.getBytes(StandardCharsets.UTF_8);
                    socket.getOutputStream().write(("HTTP/1.1 200 OK\r\nContent-Length: "+bytes.length+"\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));
                    socket.getOutputStream().write(bytes); socket.getOutputStream().flush();
                }
            } catch(java.net.SocketException closed) { if(!server.isClosed())throw new RuntimeException(closed); }
            catch(IOException failure) {throw new RuntimeException(failure);}
        });
        MainActivity.setConnectionConfigFactoryForTests((base,debug,bearer) -> ApiConnectionConfig.create(base,true,bearer));
        MainActivity.setSessionFactoryForTests(dispatcher -> { session=DiagnosticsSession.create(dispatcher); return session; });
        controller=Robolectric.buildActivity(MainActivity.class).setup().visible();
        ((EditText) activity().findViewById(R.id.api_url)).setText("http://127.0.0.1:"+server.getLocalPort());
    }
    private void runReport() throws Exception {
        idle(); activity().findViewById(R.id.run).performClick(); long end=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
        while (System.nanoTime()<end && activity().presentationPhaseForTests()!=MainActivity.PresentationPhase.RENDERED) { idle(); Thread.sleep(2); }
        usable();
    }
    private void usable() {
        assertEquals(RequestState.Phase.READY,session.state().phase());
        assertEquals(MainActivity.PresentationPhase.RENDERED,activity().presentationPhaseForTests());
        assertEquals(raw,session.state().rawJson().orElseThrow());
        assertTrue(activity().findViewById(R.id.share).isEnabled()); assertTrue(activity().findViewById(R.id.share_raw).isEnabled());
    }
    private String human() {
        activity().findViewById(R.id.share).performClick(); Intent chooser=Shadows.shadowOf(activity()).getNextStartedActivity();
        Intent send=chooser.getParcelableExtra(Intent.EXTRA_INTENT); return send.getStringExtra(Intent.EXTRA_TEXT);
    }
    @Test public void nativeRestorationNotificationsPreserveRenderedReportAndSharing() throws Exception {
        create(); activity().findViewById(R.id.add_target).performClick(); activity().findViewById(R.id.add_target).performClick();
        for(int i=0;i<3;i++) {
            ((Spinner)row(i).findViewById(R.id.kind)).setSelection(CheckKind.TRACEROUTE.ordinal());
            ((EditText)row(i).findViewById(R.id.address)).setText("8.8.8."+(i+1));
            ((EditText)row(i).findViewById(R.id.attempts)).setText("2");
        }
        runReport(); String human=human();
        for(int round=0;round<3;round++) {
            controller.recreate(); idle(); usable(); assertEquals(human,human()); assertEquals(1,posts.get());
            for(int i=0;i<3;i++) assertEquals(CheckKind.TRACEROUTE.ordinal(),((Spinner)row(i).findViewById(R.id.kind)).getSelectedItemPosition());
            activity().findViewById(R.id.share_raw).performClick(); AlertDialog warning=ShadowAlertDialog.getLatestAlertDialog();
            assertTrue(warning.isShowing()); warning.getButton(AlertDialog.BUTTON_NEGATIVE).performClick(); idle(); usable();
        }
        ((Spinner)row(2).findViewById(R.id.kind)).setSelection(CheckKind.DNS.ordinal()); idle();
        assertEquals(RequestState.Phase.IDLE,session.state().phase()); assertFalse(activity().findViewById(R.id.share).isEnabled());
    }
    @Test public void everyRestoredRowAndKindKeepsReportButGenuineEditsInvalidate() throws Exception {
        create();
        for(int i=1;i<20;i++)activity().findViewById(R.id.add_target).performClick();
        for(int i=0;i<20;i++) {
            CheckKind kind=CheckKind.values()[i%CheckKind.values().length];
            ((Spinner)row(i).findViewById(R.id.kind)).setSelection(kind.ordinal());
            ((EditText)row(i).findViewById(R.id.address)).setText("example"+i+".test");
            ((EditText)row(i).findViewById(R.id.expected_status)).setText("204");
            ((EditText)row(i).findViewById(R.id.attempts)).setText("3");
        }
        runReport();
        for(int round=0;round<2;round++) {
            Spinner old=row(19).findViewById(R.id.kind);
            android.widget.AdapterView.OnItemSelectedListener stale=old.getOnItemSelectedListener();
            controller.recreate();idle();usable();
            old.setSelection(CheckKind.TRACEROUTE.ordinal());stale.onItemSelected(old,null,CheckKind.TRACEROUTE.ordinal(),0);idle();usable();
            for(int i=0;i<20;i++) {
                Spinner kind=row(i).findViewById(R.id.kind);int selected=i%CheckKind.values().length;
                assertEquals(selected,kind.getSelectedItemPosition());assertEquals("example"+i+".test",((EditText)row(i).findViewById(R.id.address)).getText().toString());
                assertEquals("204",((EditText)row(i).findViewById(R.id.expected_status)).getText().toString());assertEquals("3",((EditText)row(i).findViewById(R.id.attempts)).getText().toString());
                kind.getOnItemSelectedListener().onItemSelected(kind,null,selected,0);usable();
            }
            assertEquals(1,posts.get());
        }
        ((EditText)row(19).findViewById(R.id.address)).setText("changed.test");idle();assertEquals(RequestState.Phase.IDLE,session.state().phase());
        runReport();((Spinner)row(19).findViewById(R.id.kind)).setSelection(CheckKind.DNS.ordinal());idle();assertEquals(RequestState.Phase.IDLE,session.state().phase());
        runReport();((android.widget.CheckBox)activity().findViewById(R.id.auth_enabled)).setChecked(true);assertEquals(RequestState.Phase.IDLE,session.state().phase());
        ((android.widget.CheckBox)activity().findViewById(R.id.auth_enabled)).setChecked(false);runReport();
        ((EditText)activity().findViewById(R.id.api_url)).setText("http://127.0.0.1:1");assertEquals(RequestState.Phase.IDLE,session.state().phase());assertFalse(activity().findViewById(R.id.share).isEnabled());
    }
    @Test public void repeatedNativeRecreationRetainsActualContextAndFencesOldControls() throws Exception {
        raw=com.checknetwork.app.core.GeoDetailsTest.fixture("geo-details-rich-compact-report.json");
        contextPayload=java.util.Base64.getDecoder().decode(com.checknetwork.app.core.IPContextParserTest.corpus().getJSONArray("cases").getJSONObject(0).getString("wire_base64"));
        contextWorker=Executors.newSingleThreadExecutor();
        MainActivity.setContextSessionFactoryForTests(dispatcher->{context=new IPContextSession(dispatcher,contextWorker,(config,address,deadline)->{
            com.checknetwork.app.network.IPContextTransport.Call call=new com.checknetwork.app.network.IPContextTransport(config).newCall(address,deadline);calls.add(call);return call;
        },System::nanoTime,()->IPContextOwnershipTest.NOW);return context;});
        create();runReport();String human=human();
        activity().findViewById(R.id.results).findViewWithTag("ip_context_toggle").performClick();idle();
        activity().findViewById(R.id.results).findViewWithTag("ip_context_next").performClick();idle();assertEquals("1.1.1.1",context.state().address());
        activity().findViewById(R.id.results).findViewWithTag("ip_context_query").performClick();
        contextWorker.submit(()->{}).get(5,TimeUnit.SECONDS);idle();assertNotNull(context.state().context());
        com.checknetwork.app.core.IPContext retained=context.state().context();
        for(int round=0;round<3;round++) {
            View oldQuery=activity().findViewById(R.id.results).findViewWithTag("ip_context_query"),oldNext=activity().findViewById(R.id.results).findViewWithTag("ip_context_next");
            controller.recreate();idle();usable();assertSame(retained,context.state().context());
            oldQuery.performClick();oldNext.performClick();idle();assertEquals("1.1.1.1",context.state().address());
            activity().findViewById(R.id.results).findViewWithTag("ip_context_toggle").performClick();idle();
            assertTrue(((android.widget.TextView)activity().findViewById(R.id.results).findViewWithTag("ip_context_body")).getText().toString().contains("one.example"));
            assertEquals(human,human());assertEquals(1,contexts.get());assertEquals(1,posts.get());assertEquals(1,calls.size());
        }
    }
    @Test public void nativeCollapseAndFinalDestroyStayPromptWithRealStalledBody() throws Exception {
        raw=com.checknetwork.app.core.GeoDetailsTest.fixture("geo-details-rich-compact-report.json");
        contextWorker=Executors.newSingleThreadExecutor();
        MainActivity.setContextSessionFactoryForTests(dispatcher->{context=new IPContextSession(dispatcher,contextWorker,(config,address,deadline)->{
            com.checknetwork.app.network.IPContextTransport.Call call=new com.checknetwork.app.network.IPContextTransport(config).newCall(address,deadline);calls.add(call);return call;
        },System::nanoTime,java.time.Instant::now);return context;});
        create();runReport();String human=human();
        View toggle=activity().findViewById(R.id.results).findViewWithTag("ip_context_toggle");toggle.performClick();idle();
        View query=activity().findViewById(R.id.results).findViewWithTag("ip_context_query");query.performClick();
        assertTrue(contextHeaders.await(2,TimeUnit.SECONDS));Thread.sleep(100);assertFalse(calls.get(0).isQuiescent());
        long before=System.nanoTime();toggle.performClick();assertTrue("collapse waits for no peer",System.nanoTime()-before<TimeUnit.MILLISECONDS.toNanos(750));idle();usable();
        contextWorker.submit(()->{}).get(750,TimeUnit.MILLISECONDS);assertFalse(calls.get(0).isQuiescent());assertEquals(human,human());
        toggle.performClick();idle();activity().findViewById(R.id.results).findViewWithTag("ip_context_query").performClick();idle();
        assertEquals(1,calls.size());assertEquals(1,contexts.get());assertEquals(0,context.cacheSizeForTests());usable();
        before=System.nanoTime();controller.pause().stop().destroy();controller=null;
        assertTrue("destroy waits for no peer",System.nanoTime()-before<TimeUnit.MILLISECONDS.toNanos(750));assertFalse(calls.get(0).isQuiescent());
        query.performClick();assertEquals(1,calls.size());
    }
    @After public void close() throws Exception {
        contextRelease.countDown();
        if(controller!=null) { controller.pause().stop().destroy(); idle(); }
        if(session!=null)session.destroy(); if(server!=null)server.close();
        peer.shutdown(); assertTrue(peer.awaitTermination(5,TimeUnit.SECONDS)); if(serving!=null)serving.get();
        if(context!=null)context.destroy();
        if(contextWorker!=null){contextWorker.shutdown();assertTrue(contextWorker.awaitTermination(5,TimeUnit.SECONDS));}
        for(com.checknetwork.app.network.IPContextTransport.Call call:calls)com.checknetwork.app.network.IPContextTransportRetirementTest.retired(call);
        MainActivity.resetSessionFactoryForTests();
    }
}
