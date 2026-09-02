package com.checknetwork.app;

import static org.junit.Assert.*;
import com.checknetwork.app.core.*;
import com.checknetwork.app.network.TransportException;
import com.checknetwork.app.state.*;
import java.util.*;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class DiagnosticsSessionTest {
    private static final String RAW="{\"id\":\"r1\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[],\"summary\":{\"total\":0,\"passed\":0,\"failed\":0}}";
    @Test public void detachReattachFencesOldOwnerAndReplaysLoadingThenReady() {
        FakeFactory factory=new FakeFactory(); RequestCoordinator coordinator=new RequestCoordinator(factory);
        DiagnosticsSession session=new DiagnosticsSession(coordinator,Runnable::run,null);
        Object oldOwner=new Object(),newOwner=new Object(); List<RequestState> oldStates=new ArrayList<>(),newStates=new ArrayList<>();
        session.attach(oldOwner,oldStates::add);
        session.start(request("one.test"));
        assertEquals(RequestState.Phase.LOADING,oldStates.get(oldStates.size()-1).phase());
        session.detach(oldOwner); session.attach(newOwner,newStates::add);
        assertEquals(RequestState.Phase.LOADING,newStates.get(0).phase());
        factory.calls.get(0).succeed(RAW,ReportParser.parse(RAW));
        assertEquals(RequestState.Phase.READY,newStates.get(newStates.size()-1).phase());
        assertEquals(RequestState.Phase.LOADING,oldStates.get(oldStates.size()-1).phase());
    }
    @Test public void invalidateAndFinalDestroyCancelAndFenceCallbacks() {
        FakeFactory factory=new FakeFactory(); DiagnosticsSession session=new DiagnosticsSession(new RequestCoordinator(factory),Runnable::run,null);
        List<RequestState> states=new ArrayList<>(); Object owner=new Object(); session.attach(owner,states::add);
        session.start(request("one.test")); FakeCall first=factory.calls.get(0);
        session.invalidateInput("edited"); assertEquals(1,first.cancels); assertEquals(RequestState.Phase.IDLE,session.state().phase());
        session.start(request("two.test")); FakeCall second=factory.calls.get(1); session.destroy();
        assertEquals(1,second.cancels); int size=states.size(); second.succeed(RAW,ReportParser.parse(RAW)); assertEquals(size,states.size());
        assertTrue(session.isDestroyed());
    }
    @Test public void dispatcherRejectionAndListenerFailureAreContained() {
        FakeFactory rejectedFactory=new FakeFactory();
        DiagnosticsSession rejected=new DiagnosticsSession(new RequestCoordinator(rejectedFactory),r->{throw new IllegalStateException("rejected");},null);
        rejected.attach(new Object(),state->{});
        rejected.start(request("one.test"));
        assertEquals(RequestState.Phase.LOADING,rejected.state().phase());
        assertNotNull(rejectedFactory.calls.get(0).callback);

        FakeFactory throwingFactory=new FakeFactory();
        DiagnosticsSession throwing=new DiagnosticsSession(new RequestCoordinator(throwingFactory),Runnable::run,null);
        throwing.attach(new Object(),state->{throw new IllegalStateException("listener");});
        throwing.start(request("two.test"));
        throwingFactory.calls.get(0).succeed(RAW,ReportParser.parse(RAW));
        assertEquals(RequestState.Phase.READY,throwing.state().phase());
    }
    @Test public void queuedDeliveryIsDroppedAfterDetachOrDestroy() {
        FakeFactory factory=new FakeFactory(); QueueDispatcher dispatcher=new QueueDispatcher();
        DiagnosticsSession session=new DiagnosticsSession(new RequestCoordinator(factory),dispatcher,null);
        Object first=new Object(); List<RequestState> states=new ArrayList<>();
        session.attach(first,states::add); session.detach(first); dispatcher.runAll(); assertTrue(states.isEmpty());
        Object second=new Object(); session.attach(second,states::add); session.destroy(); dispatcher.runAll(); assertTrue(states.isEmpty());
    }
    private static ReportRequest request(String address){return ReportRequest.builder().addTarget(TargetInput.of(CheckKind.DNS,address)).build();}
    private static final class QueueDispatcher implements DiagnosticsSession.Dispatcher {final List<Runnable> queued=new ArrayList<>();public void dispatch(Runnable r){queued.add(r);}void runAll(){List<Runnable> copy=new ArrayList<>(queued);queued.clear();copy.forEach(Runnable::run);}}
    private static final class FakeFactory implements RequestCoordinator.CallFactory {final List<FakeCall> calls=new ArrayList<>();public RequestCoordinator.CancellableCall create(ReportRequest r){FakeCall c=new FakeCall();calls.add(c);return c;}}
    private static final class FakeCall implements RequestCoordinator.CancellableCall {RequestCoordinator.Callback callback;int cancels;public void start(RequestCoordinator.Callback c){callback=c;}public void cancel(){cancels++;}void succeed(String raw,Report report){callback.onSuccess(raw,report);}}
}
