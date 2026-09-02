package com.checknetwork.app.state;

import static org.junit.Assert.*;

import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportParser;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.core.TargetInput;
import com.checknetwork.app.network.TransportException;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;

@RunWith(RobolectricTestRunner.class)
public final class RequestCoordinatorTest {
    private static final String RAW = "{\"id\":\"r1\",\"status\":\"healthy\",\"started_at\":\"2026-09-02T00:00:00Z\",\"duration_ms\":1,\"results\":[{\"kind\":\"dns\",\"address\":\"example.test\",\"status\":\"healthy\",\"latency_ms\":1,\"started_at\":\"2026-09-02T00:00:00Z\",\"details\":{}}],\"summary\":{\"total\":1,\"passed\":1,\"failed\":0}}";
    private static final Report REPORT = ReportParser.parse(RAW);

    private static ReportRequest request(String address) {
        return ReportRequest.builder().addTarget(TargetInput.of(CheckKind.DNS, address)).build();
    }

    @Test public void startsIdleThenPublishesOnlyStrictReadyResult() {
        FakeFactory factory = new FakeFactory();
        RequestCoordinator coordinator = new RequestCoordinator(factory);
        assertEquals(RequestState.Phase.IDLE, coordinator.state().phase());
        assertFalse(coordinator.state().busy());

        ReportRequest request = request("one.test");
        long owner = coordinator.start(request);
        assertEquals(1L, owner);
        assertEquals(RequestState.Phase.LOADING, coordinator.state().phase());
        assertEquals(request.signature(), coordinator.state().signature());
        assertTrue(coordinator.state().busy());
        assertFalse(coordinator.state().report().isPresent());
        assertFalse(coordinator.state().shareEligible());

        factory.calls.get(0).succeed(RAW, REPORT);
        assertEquals(RequestState.Phase.READY, coordinator.state().phase());
        assertEquals("r1", coordinator.state().report().orElseThrow().id());
        assertEquals(RAW, coordinator.state().rawJson().orElseThrow());
        assertTrue(coordinator.state().shareEligible());
        assertFalse(coordinator.state().busy());
    }

    @Test public void replacementCancelsOnceAndEveryStaleCallbackIsHarmless() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        long firstOwner = coordinator.start(request("one.test")); FakeCall first = factory.calls.get(0);
        long secondOwner = coordinator.start(request("two.test")); FakeCall second = factory.calls.get(1);
        assertTrue(secondOwner > firstOwner);
        assertEquals(1, first.cancels);
        RequestState successor = coordinator.state();

        first.succeed(RAW, REPORT);
        first.fail(TransportException.of(TransportException.Kind.NETWORK));
        first.finish();
        assertSame(successor, coordinator.state());
        assertEquals(RequestState.Phase.LOADING, coordinator.state().phase());
        assertTrue(coordinator.state().busy());

        second.succeed(RAW, REPORT);
        assertEquals(RequestState.Phase.READY, coordinator.state().phase());
    }

    @Test public void newStartAndInputInvalidationImmediatelyClearReadyAndShare() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        coordinator.start(request("one.test")); factory.calls.get(0).succeed(RAW, REPORT);
        assertTrue(coordinator.state().shareEligible());

        coordinator.start(request("two.test"));
        assertEquals(RequestState.Phase.LOADING, coordinator.state().phase());
        assertFalse(coordinator.state().report().isPresent());
        assertFalse(coordinator.state().rawJson().isPresent());
        assertFalse(coordinator.state().shareEligible());
        FakeCall active = factory.calls.get(1);

        coordinator.invalidateInput("canonical-edited-input");
        assertEquals(1, active.cancels);
        assertEquals(RequestState.Phase.IDLE, coordinator.state().phase());
        assertEquals("canonical-edited-input", coordinator.state().signature());
        assertFalse(coordinator.state().busy());
    }

    @Test public void explicitCancelAndDestroyAreTerminalForTheInvalidatedOwner() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        long first = coordinator.start(request("one.test")); FakeCall call = factory.calls.get(0);
        coordinator.cancel(); coordinator.cancel();
        assertEquals(1, call.cancels);
        assertEquals(RequestState.Phase.CANCELLED, coordinator.state().phase());
        call.succeed(RAW, REPORT); call.finish();
        assertEquals(RequestState.Phase.CANCELLED, coordinator.state().phase());

        long second = coordinator.start(request("two.test"));
        assertTrue(second > first);
        FakeCall secondCall = factory.calls.get(1);
        coordinator.destroy();
        assertEquals(1, secondCall.cancels);
        assertEquals(RequestState.Phase.CANCELLED, coordinator.state().phase());
        assertThrows(IllegalStateException.class, () -> coordinator.start(request("three.test")));
        secondCall.succeed(RAW, REPORT); secondCall.finish();
        assertEquals(RequestState.Phase.CANCELLED, coordinator.state().phase());
    }

    @Test public void currentErrorIsStableAndClearsBusyWithoutServerText() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        coordinator.start(request("one.test"));
        factory.calls.get(0).fail(TransportException.of(TransportException.Kind.TIMEOUT));
        assertEquals(RequestState.Phase.ERROR, coordinator.state().phase());
        assertEquals(TransportException.Kind.TIMEOUT, coordinator.state().error().orElseThrow().kind());
        assertFalse(coordinator.state().busy());
        assertFalse(coordinator.state().report().isPresent());
        assertFalse(coordinator.state().shareEligible());
    }

    @Test public void observableSnapshotsArePublishedForEveryOwnedTransitionOnly() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        List<RequestState> observed = new ArrayList<>();
        coordinator.setStateListener(observed::add);
        assertEquals(RequestState.Phase.IDLE, observed.get(0).phase());
        coordinator.start(request("one.test")); FakeCall stale = factory.calls.get(0);
        coordinator.start(request("two.test")); FakeCall current = factory.calls.get(1);
        int afterReplacement = observed.size();
        stale.succeed(RAW, REPORT); stale.finish();
        assertEquals(afterReplacement, observed.size());
        current.succeed(RAW, REPORT);
        assertEquals(RequestState.Phase.READY, observed.get(observed.size() - 1).phase());
        coordinator.invalidateInput("edited");
        assertEquals(RequestState.Phase.IDLE, observed.get(observed.size() - 1).phase());
    }

    @Test public void latchedOldSuccessAndFinallyCannotClearNewBusyLease() throws Exception {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        coordinator.start(request("one.test")); FakeCall old = factory.calls.get(0);
        CountDownLatch released = new CountDownLatch(1); CountDownLatch delivered = new CountDownLatch(1);
        ExecutorService executor = Executors.newSingleThreadExecutor();
        try {
            executor.submit(() -> { try { released.await(); old.succeed(RAW, REPORT); old.finish(); }
                catch (InterruptedException interrupted) { Thread.currentThread().interrupt(); } finally { delivered.countDown(); } });
            long successor = coordinator.start(request("two.test"));
            released.countDown(); assertTrue(delivered.await(2, TimeUnit.SECONDS));
            assertEquals(successor, coordinator.state().ownerId());
            assertEquals(RequestState.Phase.LOADING, coordinator.state().phase());
            assertTrue(coordinator.state().busy());
        } finally { executor.shutdownNow(); }
    }

    @Test public void throwingPreviousCancelCannotPreventSuccessorStartOrLoadingPublication() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        List<RequestState.Phase> phases = new ArrayList<>(); coordinator.setStateListener(s -> phases.add(s.phase()));
        coordinator.start(request("one.test"));
        factory.calls.get(0).cancelFailure = new IllegalStateException("cancel exploded");

        coordinator.start(request("two.test"));

        assertNotNull(factory.calls.get(1).callback);
        assertEquals(RequestState.Phase.LOADING, coordinator.state().phase());
        assertEquals(RequestState.Phase.LOADING, phases.get(phases.size() - 1));
    }

    @Test public void listenerFailureCannotInterruptStartCancelInvalidateDestroyOrCompletion() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        coordinator.setStateListener(state -> { throw new IllegalStateException("listener exploded"); });
        coordinator.start(request("one.test")); FakeCall first = factory.calls.get(0);
        assertNotNull(first.callback);
        first.finish();
        assertEquals(RequestState.Phase.ERROR, coordinator.state().phase());

        coordinator.start(request("two.test")); coordinator.cancel();
        assertEquals(RequestState.Phase.CANCELLED, coordinator.state().phase());
        coordinator.start(request("three.test")); coordinator.invalidateInput("edited");
        assertEquals(RequestState.Phase.IDLE, coordinator.state().phase());
        coordinator.start(request("four.test")); coordinator.destroy();
        assertEquals(RequestState.Phase.CANCELLED, coordinator.state().phase());
    }

    @Test public void finalizationWithoutResultPublishesErrorExactlyOnce() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        List<RequestState> observed = new ArrayList<>(); coordinator.setStateListener(observed::add);
        coordinator.start(request("one.test")); int before = observed.size();
        factory.calls.get(0).finish(); factory.calls.get(0).finish();
        assertEquals(before + 1, observed.size());
        assertEquals(RequestState.Phase.ERROR, observed.get(observed.size() - 1).phase());
        assertFalse(coordinator.state().busy());
    }

    @Test public void staleCallbacksValidateNothingIncludingNullPayloads() {
        FakeFactory factory = new FakeFactory(); RequestCoordinator coordinator = new RequestCoordinator(factory);
        coordinator.start(request("one.test")); FakeCall stale = factory.calls.get(0);
        coordinator.start(request("two.test"));
        stale.succeed(null, null); stale.fail(null);
        assertEquals(RequestState.Phase.LOADING, coordinator.state().phase());
        assertNotNull(factory.calls.get(1).callback);
    }

    private static final class FakeFactory implements RequestCoordinator.CallFactory {
        final List<FakeCall> calls = new ArrayList<>();
        @Override public RequestCoordinator.CancellableCall create(ReportRequest request) {
            FakeCall call = new FakeCall(); calls.add(call); return call;
        }
    }

    private static final class FakeCall implements RequestCoordinator.CancellableCall {
        RequestCoordinator.Callback callback; int cancels; RuntimeException cancelFailure;
        @Override public void start(RequestCoordinator.Callback callback) { this.callback = callback; }
        @Override public void cancel() { cancels++; if (cancelFailure != null) throw cancelFailure; }
        void succeed(String raw, Report report) { callback.onSuccess(raw, report); }
        void fail(TransportException error) { callback.onError(error); }
        void finish() { callback.onFinally(); }
    }
}
