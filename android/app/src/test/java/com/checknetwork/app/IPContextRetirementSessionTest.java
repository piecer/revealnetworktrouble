package com.checknetwork.app;

import static org.junit.Assert.*;
import com.checknetwork.app.core.IPContext;
import com.checknetwork.app.network.*;
import java.util.ArrayDeque;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk=35)
public final class IPContextRetirementSessionTest {
    @Test public void successfulReturnWithoutRetirementCannotCacheOrReleaseAdmission() throws Exception {
        for(boolean throwingReceipt:new boolean[]{false,true}) {
            ArrayDeque<Runnable> work=new ArrayDeque<>(); AtomicInteger calls=new AtomicInteger(); boolean[] retired={false};
            IPContext context=IPContextOwnershipTest.context("1.1.1.1");
            IPContextSession session=new IPContextSession(Runnable::run,work::add,(config,address,deadline)->{
                calls.incrementAndGet(); return new IPContextTransport.Request(){
                    public IPContext execute(){return context;}
                    public void cancel(){throw new IllegalStateException("injected cancel failure");}
                    public boolean isQuiescent(){if(throwingReceipt&&!retired[0])throw new IllegalStateException("receipt unavailable");return retired[0];}
                };
            },System::nanoTime,()->IPContextOwnershipTest.NOW);
            try {
                session.bind(IPContextOwnershipTest.report(),ApiConnectionConfig.create("https://api.example.test",false,null));
                session.select("1.1.1.1");session.lookup();work.remove().run();
                assertNull("no success before cleanup acknowledgement",session.state().context());
                assertEquals(0,session.cacheSizeForTests());session.cancel();session.cancel();session.lookup();
                assertEquals(1,calls.get());assertTrue(work.isEmpty());
                retired[0]=true;session.lookup();assertEquals(2,calls.get());work.remove().run();
                assertNotNull(session.state().context());
            } finally {session.destroy();}
        }
    }
    @Test public void realTransportCleanupRetainsSessionChargeAcrossCancelAndDestroy() throws Exception {
        for(boolean destroy:new boolean[]{false,true}) {
            java.util.concurrent.CountDownLatch closing=new java.util.concurrent.CountDownLatch(1),release=new java.util.concurrent.CountDownLatch(1);
            java.util.concurrent.ExecutorService worker=java.util.concurrent.Executors.newSingleThreadExecutor();
            java.util.concurrent.ExecutorService observer=java.util.concurrent.Executors.newSingleThreadExecutor();
            AtomicInteger creates=new AtomicInteger(),disconnects=new AtomicInteger();
            java.util.List<IPContextTransport.Call> requests=new java.util.concurrent.CopyOnWriteArrayList<>();
            byte[] bytes=java.util.Base64.getDecoder().decode(com.checknetwork.app.core.IPContextParserTest.corpus().getJSONArray("cases").getJSONObject(0).getString("wire_base64"));
            IPContextSession session=new IPContextSession(Runnable::run,worker,(config,address,deadline)->{
                int invocation=creates.incrementAndGet();
                IPContextTransport.Call call=new IPContextTransport(config,url->new java.net.HttpURLConnection(url){
                    public void connect(){} public boolean usingProxy(){return false;}
                    public void disconnect(){disconnects.incrementAndGet();}
                    public int getResponseCode(){return 200;}
                    public long getContentLengthLong(){return bytes.length;}
                    public java.io.OutputStream getOutputStream(){return new java.io.ByteArrayOutputStream();}
                    public java.io.InputStream getInputStream(){return new java.io.ByteArrayInputStream(bytes){
                        public void close() throws java.io.IOException {
                            if(invocation==1){closing.countDown();try{assertTrue(release.await(5,java.util.concurrent.TimeUnit.SECONDS));}catch(InterruptedException e){throw new AssertionError(e);}}
                            super.close();
                        }
                    };}
                },System::nanoTime).newCall(address,deadline);requests.add(call);return call;
            },System::nanoTime,()->IPContextOwnershipTest.NOW);
            try {
                session.bind(IPContextOwnershipTest.report(),ApiConnectionConfig.create("https://api.example.test",false,null));session.select("1.1.1.1");session.lookup();
                assertTrue(closing.await(2,java.util.concurrent.TimeUnit.SECONDS));assertNull(session.state().context());
                observer.submit(session::cancel).get(750,java.util.concurrent.TimeUnit.MILLISECONDS);
                observer.submit(session::cancel).get(750,java.util.concurrent.TimeUnit.MILLISECONDS);
                worker.submit(()->{}).get(750,java.util.concurrent.TimeUnit.MILLISECONDS);
                assertFalse(requests.get(0).isQuiescent());assertEquals(0,session.cacheSizeForTests());session.lookup();assertEquals(1,creates.get());
                if(destroy){observer.submit(session::destroy).get(750,java.util.concurrent.TimeUnit.MILLISECONDS);assertTrue(worker.awaitTermination(750,java.util.concurrent.TimeUnit.MILLISECONDS));assertFalse(requests.get(0).isQuiescent());}
                release.countDown();com.checknetwork.app.network.IPContextTransportRetirementTest.retired(requests.get(0));
                if(!destroy){session.lookup();worker.submit(()->{}).get(2,java.util.concurrent.TimeUnit.SECONDS);assertEquals(2,creates.get());assertNotNull(session.state().context());}
                else {session.lookup();assertEquals(1,creates.get());assertNull(session.state().context());}
            } finally {
                release.countDown();session.destroy();for(IPContextTransport.Call request:requests)com.checknetwork.app.network.IPContextTransportRetirementTest.retired(request);
                worker.shutdown();observer.shutdown();assertTrue(worker.awaitTermination(3,java.util.concurrent.TimeUnit.SECONDS));assertTrue(observer.awaitTermination(3,java.util.concurrent.TimeUnit.SECONDS));
            }
            assertEquals(creates.get(),disconnects.get());
        }
    }
    @Test public void rejectedExecutionCancelsOnceAndKeepsUnretiredAcquisitionCharged() throws Exception {
        AtomicInteger creates=new AtomicInteger(),cancels=new AtomicInteger(),executes=new AtomicInteger();boolean[] retired={false};
        IPContextSession session=new IPContextSession(Runnable::run,task->{throw new java.util.concurrent.RejectedExecutionException();},(config,address,deadline)->{
            creates.incrementAndGet();return new IPContextTransport.Request(){
                public IPContext execute(){executes.incrementAndGet();throw new AssertionError();}
                public void cancel(){cancels.incrementAndGet();}
                public boolean isQuiescent(){return retired[0];}
            };
        },System::nanoTime,()->IPContextOwnershipTest.NOW);
        try {
            session.bind(IPContextOwnershipTest.report(),ApiConnectionConfig.create("https://api.example.test",false,null));session.select("1.1.1.1");session.lookup();
            assertEquals(IPContextTransport.Failure.UNAVAILABLE,session.state().failure());assertEquals(1,cancels.get());
            session.lookup();assertEquals(1,creates.get());assertEquals(0,executes.get());
            retired[0]=true;session.lookup();assertEquals(2,creates.get());assertEquals(2,cancels.get());
        } finally {session.destroy();}
    }
}
