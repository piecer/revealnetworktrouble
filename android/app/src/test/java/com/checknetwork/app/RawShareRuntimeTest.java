package com.checknetwork.app;

import static org.junit.Assert.*;

import android.net.Uri;
import com.checknetwork.app.core.ContractLimits;
import java.io.File;
import java.io.IOException;
import java.lang.reflect.Field;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Assume;
import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

/** Deterministic contract tests for the process-owned raw-share core. */
@RunWith(RobolectricTestRunner.class)
@Config(sdk=35)
public final class RawShareRuntimeTest {
    @Rule public final TemporaryFolder temporary = new TemporaryFolder();

    @Test public void startupReconciliationIsBoundedResumableAndRecognizesOnlyExactNames() throws Exception {
        TestRig rig=rig("bounded");
        for(int i=0;i<70;i++)rig.fs.put(String.format("raw-%016d.json",i),1,100,false);
        rig.fs.put("raw-short.json",1,100,false);
        rig.fs.put("raw-0000000000000000.json.bak",1,100,false);
        rig.fs.put(".raw-0000000000000000.tmp.extra",1,100,false);
        rig.fs.put("unrelated.keep",1,100,false);

        assertEquals(RawShareRuntime.Phase.CLEANING,rig.runtime.snapshot().phase());
        rig.worker.runNext();

        assertEquals(RawShareRuntime.Phase.CLEANING,rig.runtime.snapshot().phase());
        assertTrue(rig.fs.deleted.size()<=RawShareRuntime.RECONCILE_DELETE_LIMIT);
        assertEquals(1,rig.fs.scanAfters.size());
        assertFalse(rig.worker.tasks.isEmpty());
        String firstCursor=rig.fs.scanCursors.get(0);
        rig.worker.runNext();
        assertEquals(firstCursor,rig.fs.scanAfters.get(1));
        assertTrue(rig.fs.deleted.size()<=RawShareRuntime.RECONCILE_DELETE_LIMIT*2);
        rig.worker.runAll();

        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());
        assertEquals(70,rig.fs.deleted.size());
        assertTrue(rig.fs.entries.containsKey("raw-short.json"));
        assertTrue(rig.fs.entries.containsKey("raw-0000000000000000.json.bak"));
        assertTrue(rig.fs.entries.containsKey(".raw-0000000000000000.tmp.extra"));
        assertTrue(rig.fs.entries.containsKey("unrelated.keep"));
        assertTrue(rig.fs.scanLimits.size()>=3);
        assertTrue(rig.fs.scanLimits.stream().allMatch(limit->limit==32));
        assertEquals(rig.fs.openedSessions,rig.fs.closedSessions);
    }

    @Test public void queuedNotificationResolvesExactCurrentOwnerAndListenerGeneration() throws Exception {
        QueueDispatcher callbacks=new QueueDispatcher();TestRig rig=rigWithDispatcher("listener-generation",callbacks);rig.worker.runAll();
        Object owner=new Object();AtomicInteger oldCalls=new AtomicInteger(),newCalls=new AtomicInteger();
        rig.runtime.attach(owner,state->oldCalls.incrementAndGet());
        rig.runtime.detach(owner);
        rig.runtime.attach(owner,state->newCalls.incrementAndGet());

        callbacks.runNext();callbacks.runNext();

        assertEquals(0,oldCalls.get());
        assertEquals(1,newCalls.get());
    }

    @Test public void terminalCleanupFailureDropsSecretAndOwnerBeforeRetryAndIdle() throws Exception {
        TestRig rig=rig("terminal-secrets",file->{throw new SecurityException("URI denied");});rig.worker.runAll();Object owner=new Object();
        rig.fs.deleteFailures.put("raw-0000000000000001.json",1);
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,"top-secret-raw-json"));rig.worker.runAll();

        assertEquals(RawShareRuntime.Phase.CLEANUP_FAILED,rig.runtime.snapshot().phase());
        assertNull(field(rig.runtime,"operationOwner"));assertNull(field(rig.runtime,"pendingRaw"));
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.retryCleanup());rig.worker.runAll();
        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());
        assertNull(field(rig.runtime,"operationOwner"));assertNull(field(rig.runtime,"pendingRaw"));
    }

    @Test public void reconciliationFailureRestartsBoundedPassAndPreservesUnrelatedEntries() throws Exception {
        TestRig rig=rig("resume");
        for(int i=0;i<73;i++)rig.fs.put(String.format("raw-%016d.json",i),1,100,false);
        rig.fs.put("unrelated.keep",1,100,false);rig.fs.deleteFailures.put("raw-0000000000000005.json",1);
        rig.worker.runNext();
        assertEquals(RawShareRuntime.Phase.CLEANUP_FAILED,rig.runtime.snapshot().phase());
        assertTrue(rig.fs.scanAfters.size()<=1);assertTrue(rig.fs.deleted.size()<=32);assertEquals(rig.fs.openedSessions,rig.fs.closedSessions);

        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.retryCleanup());
        int before=rig.fs.deleted.size();rig.worker.runNext();
        assertTrue(rig.fs.deleted.size()-before<=32);assertNull(rig.fs.scanAfters.get(rig.fs.scanAfters.size()-1));
        while(rig.runtime.snapshot().phase()==RawShareRuntime.Phase.CLEANING){
            int deleted=rig.fs.deleted.size(),scans=rig.fs.scanAfters.size();rig.worker.runNext();
            assertTrue(rig.fs.deleted.size()-deleted<=32);assertEquals(scans+1,rig.fs.scanAfters.size());
        }
        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());assertEquals(0,rig.fs.recognizedCount());
        assertTrue(rig.fs.entries.containsKey("unrelated.keep"));assertEquals(rig.fs.openedSessions,rig.fs.closedSessions);
    }

    @Test public void disposalDuringPagedReconciliationClosesSessionAndDropsOperationReferences() throws Exception {
        TestRig rig=rig("dispose-reconciliation");for(int i=0;i<70;i++)rig.fs.put(String.format("raw-%016d.json",i),1,100,false);
        rig.worker.runNext();assertEquals(1,rig.fs.openedSessions);assertEquals(0,rig.fs.closedSessions);
        CompletableFuture<Void> done=rig.runtime.dispose();assertNull(field(rig.runtime,"operationOwner"));assertNull(field(rig.runtime,"pendingRaw"));
        rig.worker.runAll();done.get(5,TimeUnit.SECONDS);
        assertEquals(RawShareRuntime.Phase.DISPOSED,rig.runtime.snapshot().phase());assertEquals(rig.fs.openedSessions,rig.fs.closedSessions);
    }

    @Test public void singletonRemainsOldRuntimeUntilShutdownFutureActuallyTerminates() throws Exception {
        TestRig rig=rig("singleton-termination");rig.worker.runAll();rig.worker.autoTerminate=false;
        Field singleton=RawShareRuntime.class.getDeclaredField("singleton");singleton.setAccessible(true);singleton.set(null,rig.runtime);
        try {
            CompletableFuture<Void> disposal=rig.runtime.dispose();rig.worker.runNext();
            assertEquals(RawShareRuntime.Phase.DISPOSED,rig.runtime.snapshot().phase());assertFalse(disposal.isDone());
            assertSame(rig.runtime,RawReportShare.processRuntime(RuntimeEnvironment.getApplication()));
            rig.worker.terminated.complete(null);disposal.get(5,TimeUnit.SECONDS);
            RawShareRuntime replacement=RawReportShare.processRuntime(RuntimeEnvironment.getApplication());assertNotSame(rig.runtime,replacement);
            replacement.dispose().get(5,TimeUnit.SECONDS);
        } finally {singleton.set(null,null);}
    }

    @Test public void applicationEntryPointReturnsOneProcessRuntimeAndDisposeTerminatesIt() throws Exception {
        RawShareRuntime first=RawReportShare.processRuntime(RuntimeEnvironment.getApplication());
        RawShareRuntime second=RawReportShare.processRuntime(RuntimeEnvironment.getApplication());
        assertSame(first,second);
        long deadline=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
        while(first.snapshot().phase()==RawShareRuntime.Phase.CLEANING&&System.nanoTime()<deadline)Thread.yield();
        CompletableFuture<Void> completion=first.dispose();completion.get(5,TimeUnit.SECONDS);
        assertEquals(RawShareRuntime.Phase.DISPOSED,first.snapshot().phase());
    }

    @Test public void blockedWriterRejectsEveryOtherManagerAndKeepsSlotUntilActualReturn() throws Exception {
        TestRig rig=rig("blocked");rig.worker.runAll();Object first=new Object(),second=new Object();
        CountDownLatch entered=new CountDownLatch(1),release=new CountDownLatch(1);
        rig.fs.writeBlock=()->{entered.countDown();await(release);};
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(first,"one"));
        Thread workerThread=new Thread(rig.worker::runNext,"fake-fs-worker");workerThread.start();
        assertTrue(entered.await(5,TimeUnit.SECONDS));

        assertEquals(RawShareRuntime.Admission.BUSY,rig.runtime.prepare(first,"duplicate"));
        assertEquals(RawShareRuntime.Admission.BUSY,rig.runtime.prepare(second,"other manager"));
        rig.runtime.detach(first);
        assertEquals(RawShareRuntime.Admission.BUSY,rig.runtime.prepare(second,"still busy"));
        assertEquals(0,rig.worker.tasks.size());

        release.countDown();workerThread.join(5000);assertFalse(workerThread.isAlive());
        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(second,"after return"));
    }

    @Test public void utf8SizingWriteScanFlushPublicationAndCleanupRunOffCallingThread() throws Exception {
        long caller=Thread.currentThread().getId();TestRig rig=rig("threads");rig.fs.forbiddenThread=caller;
        rig.worker.runAllOnThread();Object owner=new Object();
        String exact="é".repeat(ContractLimits.MAX_TRANSPORT_BYTES/2);
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,exact));
        assertTrue(rig.mainChecks.get()>0);
        rig.worker.runAllOnThread();
        assertEquals(ContractLimits.MAX_TRANSPORT_BYTES,rig.fs.lastBytes.length);
        assertNotNull(rig.runtime.grant(owner));
        rig.runtime.retire(owner);rig.worker.runAllOnThread();
        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());
        assertTrue(rig.fs.ioThreads.stream().allMatch(id->id!=caller));
    }

    @Test public void exactUtf8LimitAcceptedAndLimitPlusOneFailsWithoutArtifact() throws Exception {
        TestRig rig=rig("limits");rig.worker.runAll();Object owner=new Object();
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,"x".repeat(ContractLimits.MAX_TRANSPORT_BYTES)));
        rig.worker.runAll();assertEquals(RawShareRuntime.Phase.MATERIALIZED,rig.runtime.snapshot().phase());
        rig.runtime.retire(owner);rig.worker.runAll();
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,"é".repeat(ContractLimits.MAX_TRANSPORT_BYTES/2)+"x"));
        rig.worker.runAll();
        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());
        assertTrue(rig.runtime.snapshot().failure().contains("UTF-8"));
        assertEquals(0,rig.fs.recognizedCount());
    }

    @Test public void replacementRevokesOnceBeforeDeleteAndWriteAndUrisNeverRepeat() throws Exception {
        TestRig rig=rig("replace");rig.worker.runAll();Object owner=new Object();
        materializeAndGrant(rig,owner,"first");Uri first=rig.runtime.snapshot().uri();
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,"second"));rig.worker.runAll();Uri second=rig.runtime.grant(owner);

        assertNotEquals(first,second);assertEquals(1,rig.revoked.size());assertEquals(first,rig.revoked.get(0));
        int revoke=rig.events.indexOf("revoke:"+first),delete=firstIndex(rig.events,"delete:raw-"),secondWrite=rig.events.lastIndexOf("write");
        assertTrue(revoke>=0&&revoke<delete&&delete<secondWrite);
        assertEquals(1,rig.fs.recognizedCount());
    }

    @Test public void leaseExpiresAtExactObservedBoundaryAndRevokesOnlyOnce() throws Exception {
        TestRig rig=rig("ttl");rig.clock.now=1234;rig.worker.runAll();Object owner=new Object();materializeAndGrant(rig,owner,"leased");Uri uri=rig.runtime.snapshot().uri();
        long expiry=1234+RawShareRuntime.LEASE_MILLIS;assertEquals(expiry,rig.runtime.snapshot().leaseExpiresAt());
        rig.clock.now=expiry-1;rig.runtime.observe();assertEquals(RawShareRuntime.Phase.GRANTED,rig.runtime.snapshot().phase());
        rig.clock.now=expiry;rig.runtime.observe();assertEquals(RawShareRuntime.Phase.RETIRED,rig.runtime.snapshot().phase());
        rig.runtime.observe();rig.worker.runAll();rig.runtime.observe();
        assertEquals(List.of(uri),rig.revoked);assertEquals(0,rig.fs.recognizedCount());assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());
    }

    @Test public void startupTreatsYoungPriorProcessFilesAsImmediateOrphansAndHonorsLiveBounds() throws Exception {
        TestRig rig=rig("orphans");
        rig.fs.put("raw-aaaaaaaaaaaaaaaa.json",8L*1024*1024,rig.clock.now+60_000,false);
        rig.fs.put(".raw-bbbbbbbbbbbbbbbb.tmp",8L*1024*1024,rig.clock.now+60_000,false);
        rig.fs.put("raw-cccccccccccccccc.json",1,rig.clock.now+60_000,false);
        rig.worker.runAll();
        assertEquals(0,rig.fs.recognizedCount());assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());
        Object owner=new Object();materializeAndGrant(rig,owner,"x".repeat(ContractLimits.MAX_TRANSPORT_BYTES));
        assertEquals(1,rig.fs.recognizedCount());assertTrue(rig.fs.recognizedBytes()<=RawShareRuntime.MAX_LIVE_BYTES);
    }

    @Test public void deleteFailureKeepsAdmissionClosedAndRetryConverges() throws Exception {
        TestRig rig=rig("delete-failure");String orphan="raw-aaaaaaaaaaaaaaaa.json";rig.fs.put(orphan,1,0,false);rig.fs.deleteFailures.put(orphan,1);
        rig.worker.runAll();
        assertEquals(RawShareRuntime.Phase.CLEANUP_FAILED,rig.runtime.snapshot().phase());
        assertEquals(RawShareRuntime.Admission.CLOSED,rig.runtime.prepare(new Object(),"no"));
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.retryCleanup());rig.worker.runAll();
        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());assertFalse(rig.fs.entries.containsKey(orphan));
    }

    @Test public void revokeFailureStillDeletesOnceAndRetryNeverRevokesOrDeletesAgain() throws Exception {
        TestRig rig=rig("revoke-failure");rig.worker.runAll();Object owner=new Object();materializeAndGrant(rig,owner,"old");
        String oldName=rig.fs.entries.keySet().stream().filter(name->name.endsWith(".json")).findFirst().orElseThrow();
        rig.revoker.failure=new SecurityException("revoke denied");
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,"new"));rig.worker.runAll();
        assertEquals(RawShareRuntime.Phase.CLEANUP_FAILED,rig.runtime.snapshot().phase());assertEquals(1,rig.revoker.attempts);assertFalse(rig.fs.entries.containsKey(oldName));assertEquals(1,rig.fs.deleteAttempts.get(oldName).intValue());
        rig.revoker.failure=null;assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.retryCleanup());rig.worker.runAll();
        assertEquals(1,rig.revoker.attempts);assertEquals(1,rig.fs.deleteAttempts.get(oldName).intValue());assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());
    }

    @Test public void uriFailureDeletesTempAndFinalExactlyOnceBeforeSlotRelease() throws Exception {
        TestRig rig=rig("uri-failure",file->{throw new SecurityException("URI denied");});rig.worker.runAll();Object owner=new Object();
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,"secret"));rig.worker.runAll();
        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());assertEquals(0,rig.fs.recognizedCount());
        assertEquals(1,rig.fs.deleteAttempts.get(".raw-0000000000000001.tmp").intValue());
        assertEquals(1,rig.fs.deleteAttempts.get("raw-0000000000000001.json").intValue());
    }

    @Test public void detachedOwnerNeverReceivesMaterializedCandidateAndOutsideSwapFailureIsCleaned() throws Exception {
        TestRig rig=rig("stale");rig.worker.runAll();Object owner=new Object();
        rig.fs.writeFailure=new IOException("directory swapped");
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,"secret"));rig.runtime.detach(owner);rig.worker.runAll();
        assertEquals(RawShareRuntime.Phase.IDLE,rig.runtime.snapshot().phase());assertNull(rig.runtime.snapshot().uri());assertEquals(0,rig.fs.recognizedCount());
        assertNull(field(rig.runtime,"operationOwner"));assertNull(field(rig.runtime,"pendingRaw"));
    }

    @Test public void disposalClosesListenersAndAdmissionButWaitsForNoncooperativeWorkerAndTermination() throws Exception {
        TestRig rig=rig("dispose");rig.worker.runAll();Object owner=new Object();AtomicInteger callbacks=new AtomicInteger();rig.runtime.attach(owner,state->callbacks.incrementAndGet());
        CountDownLatch entered=new CountDownLatch(1),release=new CountDownLatch(1);rig.fs.writeBlock=()->{entered.countDown();await(release);};
        assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,"secret"));
        Thread workerThread=new Thread(rig.worker::runNext);workerThread.start();assertTrue(entered.await(5,TimeUnit.SECONDS));int before=callbacks.get();
        CompletableFuture<Void> done=rig.runtime.dispose();
        assertFalse(done.isDone());assertFalse(rig.worker.shutdown);assertEquals(RawShareRuntime.Admission.CLOSED,rig.runtime.prepare(new Object(),"closed"));
        release.countDown();workerThread.join(5000);
        assertTrue(done.isDone());assertTrue(rig.worker.shutdown);assertEquals(RawShareRuntime.Phase.DISPOSED,rig.runtime.snapshot().phase());assertEquals(before,callbacks.get());
    }

    @Test public void systemFilesystemRejectsSymlinkDirectoryAndCreateNewCollisionsWithoutTouchingCanary() throws Exception {
        File cache=temporary.newFolder("system-security"),outside=temporary.newFolder("outside");Path directory=new File(cache,"shared-reports").toPath();
        try{Files.createSymbolicLink(directory,outside.toPath());}catch(UnsupportedOperationException|IOException unsupported){Assume.assumeNoException(unsupported);}
        RawShareRuntime.SystemFileSystem symlinked=new RawShareRuntime.SystemFileSystem(cache);
        assertThrows(IOException.class,symlinked::prepareDirectory);assertEquals(0,outside.list().length);
        Files.delete(directory);RawShareRuntime.SystemFileSystem fs=new RawShareRuntime.SystemFileSystem(cache);fs.prepareDirectory();
        String token="aaaaaaaaaaaaaaaa",temp=".raw-"+token+".tmp",destination="raw-"+token+".json";
        Path tempPath=directory.resolve(temp);Files.write(tempPath,"canary".getBytes(StandardCharsets.UTF_8));
        assertThrows(IOException.class,()->fs.writeAtomic(temp,destination,"new".getBytes(StandardCharsets.UTF_8)));
        assertEquals("canary",new String(Files.readAllBytes(tempPath),StandardCharsets.UTF_8));
        Files.delete(tempPath);Path destinationPath=directory.resolve(destination);Files.write(destinationPath,"destination-canary".getBytes(StandardCharsets.UTF_8));
        assertThrows(IOException.class,()->fs.writeAtomic(temp,destination,"new".getBytes(StandardCharsets.UTF_8)));
        assertEquals("destination-canary",new String(Files.readAllBytes(destinationPath),StandardCharsets.UTF_8));
    }

    @Test public void systemFilesystemRejectsFinalSymlinkAndHardLinkBeforeUri() throws Exception {
        File cache=temporary.newFolder("final-links"),outside=temporary.newFile("outside-canary");RawShareRuntime.SystemFileSystem fs=new RawShareRuntime.SystemFileSystem(cache);fs.prepareDirectory();
        Path directory=new File(cache,"shared-reports").toPath(),link=directory.resolve("raw-aaaaaaaaaaaaaaaa.json");
        try{Files.createSymbolicLink(link,outside.toPath());}catch(UnsupportedOperationException|IOException unsupported){Assume.assumeNoException(unsupported);}
        assertThrows(IOException.class,()->fs.validateForUri(link.getFileName().toString()));assertTrue(outside.isFile());
        Files.delete(link);Files.write(link,new byte[]{1});Path hard=temporary.getRoot().toPath().resolve("hard-canary");
        try{Files.createLink(hard,link);}catch(UnsupportedOperationException|IOException unsupported){Assume.assumeNoException(unsupported);}
        assertThrows(IOException.class,()->fs.validateForUri(link.getFileName().toString()));assertArrayEquals(new byte[]{1},Files.readAllBytes(hard));
    }

    private TestRig rig(String name) throws Exception{return rig(name,file->Uri.parse("content://provider/shared_reports/"+file.getName()));}
    private TestRig rig(String name,RawShareRuntime.UriFactory uriFactory) throws Exception {
        return rig(name,uriFactory,Runnable::run);
    }
    private TestRig rigWithDispatcher(String name,RawShareRuntime.Dispatcher dispatcher) throws Exception {
        return rig(name,file->Uri.parse("content://provider/shared_reports/"+file.getName()),dispatcher);
    }
    private TestRig rig(String name,RawShareRuntime.UriFactory uriFactory,RawShareRuntime.Dispatcher dispatcher) throws Exception {
        FakeFs fs=new FakeFs(temporary.newFolder(name));ManualWorker worker=new ManualWorker();FakeClock clock=new FakeClock();AtomicInteger token=new AtomicInteger();
        List<Uri> revoked=new ArrayList<>();List<String> events=fs.events;AtomicInteger mainChecks=new AtomicInteger();FakeRevoker revoker=new FakeRevoker(revoked,events);
        RawShareRuntime runtime=new RawShareRuntime(fs,worker,clock,()->String.format("%016d",token.incrementAndGet()),uriFactory,
                revoker,dispatcher,mainChecks::incrementAndGet);
        return new TestRig(runtime,fs,worker,clock,revoked,events,mainChecks,revoker);
    }
    private static void materializeAndGrant(TestRig rig,Object owner,String raw){assertEquals(RawShareRuntime.Admission.ACCEPTED,rig.runtime.prepare(owner,raw));rig.worker.runAll();assertEquals(String.valueOf(rig.runtime.snapshot().failure()),RawShareRuntime.Phase.MATERIALIZED,rig.runtime.snapshot().phase());assertNotNull(rig.runtime.grant(owner));}
    private static int firstIndex(List<String> values,String prefix){for(int i=0;i<values.size();i++)if(values.get(i).startsWith(prefix))return i;return -1;}
    private static void await(CountDownLatch latch){try{if(!latch.await(5,TimeUnit.SECONDS))throw new AssertionError("timed out");}catch(InterruptedException interrupted){Thread.currentThread().interrupt();throw new AssertionError(interrupted);}}
    private static Object field(Object target,String name) throws Exception {Field field=target.getClass().getDeclaredField(name);field.setAccessible(true);return field.get(target);}
    private record TestRig(RawShareRuntime runtime,FakeFs fs,ManualWorker worker,FakeClock clock,List<Uri> revoked,List<String> events,AtomicInteger mainChecks,FakeRevoker revoker){}

    private static final class FakeRevoker implements RawShareRuntime.Revoker {
        final List<Uri> revoked;final List<String> events;Exception failure;int attempts;
        FakeRevoker(List<Uri> revoked,List<String> events){this.revoked=revoked;this.events=events;}
        public void revoke(Uri uri) throws Exception{attempts++;events.add("revoke:"+uri);revoked.add(uri);if(failure!=null)throw failure;}
    }

    private static final class FakeClock implements RawShareRuntime.Clock {long now;public long nowMillis(){return now;}}
    private static final class ManualWorker implements RawShareRuntime.Worker {
        final ArrayDeque<Runnable> tasks=new ArrayDeque<>();final CompletableFuture<Void> terminated=new CompletableFuture<>();boolean shutdown,autoTerminate=true;
        public synchronized void execute(Runnable task){if(shutdown)throw new IllegalStateException("shutdown");tasks.add(task);}
        public synchronized CompletableFuture<Void> shutdown(){shutdown=true;if(autoTerminate&&tasks.isEmpty())terminated.complete(null);return terminated;}
        void runNext(){Runnable task;synchronized(this){task=tasks.removeFirst();}task.run();synchronized(this){if(autoTerminate&&shutdown&&tasks.isEmpty())terminated.complete(null);}}
        void runAll(){while(true){synchronized(this){if(tasks.isEmpty())return;}runNext();}}
        void runAllOnThread() throws Exception{Thread thread=new Thread(this::runAll,"deterministic-fs-worker");thread.start();thread.join(10000);assertFalse(thread.isAlive());}
    }
    private static class FakeFs implements RawShareRuntime.FileSystem {
        final File directory;final Map<String,RawShareRuntime.Entry> entries=new LinkedHashMap<>();final List<String> deleted=new ArrayList<>(),events=new ArrayList<>();
        final List<Integer> scanLimits=new ArrayList<>();final List<String> scanAfters=new ArrayList<>(),scanCursors=new ArrayList<>();final Map<String,Integer> deleteFailures=new HashMap<>(),deleteAttempts=new HashMap<>();
        volatile Runnable writeBlock;volatile IOException writeFailure;volatile long forbiddenThread=-1;byte[] lastBytes;int openedSessions,closedSessions;
        FakeFs(File directory){this.directory=directory;}
        void put(String name,long size,long mtime,boolean link){entries.put(name,new RawShareRuntime.Entry(name,size,mtime,!link,link));}
        int recognizedCount(){return (int)entries.keySet().stream().filter(RawShareRuntime::recognizedName).count();}
        long recognizedBytes(){return entries.values().stream().filter(entry->RawShareRuntime.recognizedName(entry.name())).mapToLong(RawShareRuntime.Entry::size).sum();}
        void io(){long id=Thread.currentThread().getId();ioThreads.add(id);if(id==forbiddenThread)throw new AssertionError("filesystem work ran on caller thread");}
        final List<Long> ioThreads=new ArrayList<>();
        public File directory(){return directory;}
        public void prepareDirectory(){io();}
        public RawShareRuntime.ScanSession openScan() throws IOException {openedSessions++;RawShareRuntime.ScanSession delegate=RawShareRuntime.FileSystem.super.openScan();return new RawShareRuntime.ScanSession(){boolean closed;public RawShareRuntime.ScanPage next(int limit)throws IOException{return delegate.next(limit);}public void close()throws IOException{if(!closed){closed=true;closedSessions++;delegate.close();}}};}
        public RawShareRuntime.ScanPage scan(String after,int limit){io();scanLimits.add(limit);scanAfters.add(after);List<RawShareRuntime.Entry> sorted=entries.values().stream().sorted(Comparator.comparing(RawShareRuntime.Entry::name)).toList();List<RawShareRuntime.Entry> page=new ArrayList<>();boolean more=false;for(RawShareRuntime.Entry entry:sorted){if(after!=null&&entry.name().compareTo(after)<=0)continue;if(page.size()==limit){more=true;break;}page.add(entry);}String cursor=page.isEmpty()?after:page.get(page.size()-1).name();scanCursors.add(cursor);return new RawShareRuntime.ScanPage(page,cursor,more);}
        public boolean exists(String name){io();return entries.containsKey(name);}
        public void writeAtomic(String temporary,String destination,byte[] bytes) throws IOException {io();events.add("write");lastBytes=bytes;put(temporary,bytes.length,0,false);if(writeBlock!=null)writeBlock.run();if(writeFailure!=null)throw writeFailure;if(entries.containsKey(destination))throw new IOException("collision");entries.remove(temporary);put(destination,bytes.length,0,false);}
        public File validateForUri(String name) throws IOException {io();RawShareRuntime.Entry entry=entries.get(name);if(entry==null||!entry.regular()||entry.symbolicLink())throw new IOException("unsafe final");return new File(directory,name);}
        public void delete(String name) throws IOException {io();deleteAttempts.merge(name,1,Integer::sum);Integer remaining=deleteFailures.get(name);if(remaining!=null&&remaining>0){deleteFailures.put(name,remaining-1);throw new IOException("delete failed");}events.add("delete:"+name);if(entries.remove(name)!=null)deleted.add(name);}
    }
    private static final class QueueDispatcher implements RawShareRuntime.Dispatcher {
        final ArrayDeque<Runnable> callbacks=new ArrayDeque<>();public void dispatch(Runnable callback){callbacks.add(callback);}void runNext(){callbacks.removeFirst().run();}
    }
}
