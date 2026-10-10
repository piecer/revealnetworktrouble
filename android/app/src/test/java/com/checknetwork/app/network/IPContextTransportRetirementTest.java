package com.checknetwork.app.network;

import static org.junit.Assert.*;
import com.checknetwork.app.core.IPContext;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.Locale;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk=35)
public final class IPContextTransportRetirementTest {
    private static ApiConnectionConfig config() { return ApiConnectionConfig.create("https://api.example.test",false,null); }
    private static IPContextTransport.Failure outcome(IPContextTransport.Call call) {
        try { call.execute(); throw new AssertionError("expected failure"); }
        catch(IPContextTransport.ContextException e) { return e.failure(); }
    }
    private static void hold(CountDownLatch latch) {
        boolean interrupted=false;
        for(;;) try { assertTrue(latch.await(5,TimeUnit.SECONDS)); break; }
        catch(InterruptedException e) { interrupted=true; }
        if(interrupted)Thread.currentThread().interrupt();
    }
    public static void retired(IPContextTransport.Request call) throws Exception {
        long end=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
        while(!call.isQuiescent()&&System.nanoTime()<end)Thread.sleep(2);
        assertTrue("actual owned execution and cleanup retired",call.isQuiescent());
    }
    private static class Connection extends HttpURLConnection {
        final AtomicInteger sends=new AtomicInteger(),disconnects=new AtomicInteger();
        final byte[] bytes;
        Runnable closing=()->{},reading=()->{};boolean broken;
        Connection() throws Exception {super(new URL("https://api.example.test"));bytes=IPContextTransportBoundsTest.wire("ipv4");}
        public void connect(){} public boolean usingProxy(){return false;}
        public void disconnect(){disconnects.incrementAndGet();if(broken)throw new IllegalStateException("disconnect failure");}
        public OutputStream getOutputStream(){sends.incrementAndGet();return new ByteArrayOutputStream();}
        public int getResponseCode(){return 200;}
        public long getContentLengthLong(){return bytes.length;}
        public InputStream getInputStream(){return new ByteArrayInputStream(bytes){
            public synchronized int read(byte[] b,int off,int n){reading.run();if(broken)throw new IllegalStateException("read failure");return super.read(b,off,n);}
            public void close()throws IOException{closing.run();if(broken)throw new IOException("close failure");super.close();}
        };}
    }
    @Test public void heldCleanupPreventsSuccessAndCancellationDoesNotWaitForIt() throws Exception {
        Connection c=new Connection();CountDownLatch closing=new CountDownLatch(1),release=new CountDownLatch(1);
        c.closing=()->{closing.countDown();hold(release);};
        IPContextTransport.Call call=new IPContextTransport(config(),url->c,System::nanoTime).newCall("1.1.1.1");
        ExecutorService waiter=Executors.newSingleThreadExecutor();
        try {
            Future<IPContextTransport.Failure> result=waiter.submit(()->outcome(call));
            assertTrue(closing.await(2,TimeUnit.SECONDS));assertFalse(result.isDone());assertFalse(call.isQuiescent());
            call.cancel();call.cancel();assertEquals(IPContextTransport.Failure.CANCELLED,result.get(750,TimeUnit.MILLISECONDS));
            assertFalse(call.isQuiescent());assertEquals(0,c.disconnects.get());
            Connection healthy=new Connection();assertNotNull(new IPContextTransport(config(),url->healthy,System::nanoTime).newCall("1.1.1.1").execute());
        } finally {release.countDown();retired(call);waiter.shutdown();assertTrue(waiter.awaitTermination(3,TimeUnit.SECONDS));}
        assertEquals(1,c.disconnects.get());assertEquals(1,c.sends.get());
    }
    @Test public void cancellationAlsoWakesResultWhileCompletedWorkerStillRetires() throws Exception {
        Connection c=new Connection();CountDownLatch retiring=new CountDownLatch(1),release=new CountDownLatch(1);
        ThreadFactory factory=task->new Thread(()->{task.run();retiring.countDown();hold(release);});
        IPContextTransport.Call call=new IPContextTransport(config(),url->c,System::nanoTime,factory).newCall("1.1.1.1");
        ExecutorService waiter=Executors.newSingleThreadExecutor();
        try {
            Future<IPContextTransport.Failure> result=waiter.submit(()->outcome(call));
            assertTrue(retiring.await(2,TimeUnit.SECONDS));assertFalse(result.isDone());assertFalse(call.isQuiescent());
            call.cancel();assertEquals(IPContextTransport.Failure.CANCELLED,result.get(750,TimeUnit.MILLISECONDS));assertFalse(call.isQuiescent());
        } finally {release.countDown();retired(call);waiter.shutdown();assertTrue(waiter.awaitTermination(3,TimeUnit.SECONDS));}
        assertEquals(1,c.disconnects.get());
    }
    @Test public void lateConnectionOpenIsStillOwnedAndClosedWithoutSending() throws Exception {
        Connection c=new Connection();CountDownLatch opening=new CountDownLatch(1),release=new CountDownLatch(1);
        IPContextTransport.Call call=new IPContextTransport(config(),url->{opening.countDown();hold(release);return c;},System::nanoTime).newCall("1.1.1.1");
        ExecutorService waiter=Executors.newSingleThreadExecutor();
        try {
            Future<IPContextTransport.Failure> result=waiter.submit(()->outcome(call));assertTrue(opening.await(2,TimeUnit.SECONDS));
            call.cancel();assertEquals(IPContextTransport.Failure.CANCELLED,result.get(750,TimeUnit.MILLISECONDS));assertFalse(call.isQuiescent());
        } finally {release.countDown();retired(call);waiter.shutdown();assertTrue(waiter.awaitTermination(3,TimeUnit.SECONDS));}
        assertEquals(0,c.sends.get());assertEquals(1,c.disconnects.get());
    }
    @Test public void throwingCleanupAndRejectedWorkerCreationDoNotEscapeOrAcquireAgain() throws Exception {
        Connection c=new Connection();c.broken=true;
        IPContextTransport.Call call=new IPContextTransport(config(),url->c,System::nanoTime).newCall("1.1.1.1");
        assertEquals(IPContextTransport.Failure.NETWORK,outcome(call));retired(call);assertEquals(1,c.disconnects.get());
        AtomicInteger opens=new AtomicInteger();
        for(ThreadFactory factory:new ThreadFactory[]{task->{throw new RejectedExecutionException();},task->null,task->new Thread(task){public synchronized void start(){throw new IllegalThreadStateException();}}}) {
            IPContextTransport.Call denied=new IPContextTransport(config(),url->{opens.incrementAndGet();return c;},System::nanoTime,factory).newCall("1.1.1.1");
            assertEquals(IPContextTransport.Failure.UNAVAILABLE,outcome(denied));assertTrue(denied.isQuiescent());denied.cancel();denied.cancel();
        }
        assertEquals(0,opens.get());
    }
    @Test public void cancellationBeforeExecutionAcquiresNothing() throws Exception {
        AtomicInteger opens=new AtomicInteger();Connection c=new Connection();
        IPContextTransport.Call call=new IPContextTransport(config(),url->{opens.incrementAndGet();return c;},System::nanoTime).newCall("1.1.1.1");
        call.cancel();call.cancel();assertEquals(IPContextTransport.Failure.CANCELLED,outcome(call));assertTrue(call.isQuiescent());assertEquals(0,opens.get());
    }
    @Test public void deadlinesReturnIndependentlyWhileOtherCleanupIsHeld() throws Exception {
        Connection c=new Connection();CountDownLatch closing=new CountDownLatch(1),release=new CountDownLatch(1);
        c.closing=()->{closing.countDown();hold(release);};
        OperationDeadline.Clock clock=System::nanoTime;
        IPContextTransport.Call call=new IPContextTransport(config(),url->c,clock).newCall("1.1.1.1",new OperationDeadline(clock,150));
        ExecutorService waiter=Executors.newSingleThreadExecutor();
        try {
            Future<IPContextTransport.Failure> result=waiter.submit(()->outcome(call));assertTrue(closing.await(2,TimeUnit.SECONDS));
            assertEquals(IPContextTransport.Failure.TIMEOUT,result.get(750,TimeUnit.MILLISECONDS));assertFalse(call.isQuiescent());
            Connection second=new Connection();second.reading=()->hold(release);
            IPContextTransport.Call other=new IPContextTransport(config(),url->second,clock).newCall("1.1.1.1",new OperationDeadline(clock,100));
            try {assertEquals(IPContextTransport.Failure.TIMEOUT,outcome(other));assertFalse(other.isQuiescent());}
            finally {release.countDown();retired(other);}
        } finally {release.countDown();retired(call);waiter.shutdown();assertTrue(waiter.awaitTermination(3,TimeUnit.SECONDS));}
    }
    @Test public void healthySlowTrickledAndChunkedBodiesKeepEveryByteAndOnePost() throws Exception {
        assertEquals(10000,IPContextTransport.DEADLINE_MILLIS);
        byte[] bytes=IPContextTransportBoundsTest.wire("ipv4");
        for(int style=0;style<3;style++) {
            final int mode=style;ExecutorService peer=Executors.newSingleThreadExecutor();
            try(ServerSocket server=new ServerSocket(0,1,InetAddress.getByName("127.0.0.1"))) {
                server.setSoTimeout(5000);
                Future<?> served=peer.submit(()->{
                    try(Socket socket=server.accept()) {
                        socket.setSoTimeout(5000);BufferedReader in=new BufferedReader(new InputStreamReader(socket.getInputStream(),StandardCharsets.US_ASCII));
                        assertEquals("POST /api/v1/ip-context HTTP/1.1",in.readLine());int length=0;String line;
                        while(!(line=in.readLine()).isEmpty())if(line.toLowerCase(Locale.ROOT).startsWith("content-length:"))length=Integer.parseInt(line.substring(15).trim());
                        StringBuilder request=new StringBuilder();for(int n=0;n<length;n++)request.append((char)in.read());assertEquals("{\"address\":\"1.1.1.1\"}",request.toString());
                        OutputStream out=socket.getOutputStream();if(mode==0)Thread.sleep(800);
                        out.write(("HTTP/1.1 200 OK\r\n"+(mode==2?"Transfer-Encoding: chunked":"Content-Length: "+bytes.length)+"\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));out.flush();
                        for(int offset=0;offset<bytes.length;) {
                            Thread.sleep(mode==0?0:350);int size=Math.min(bytes.length-offset,Math.max(1,bytes.length/4));
                            if(mode==2){out.write(Integer.toHexString(size).getBytes(StandardCharsets.US_ASCII));out.flush();Thread.sleep(50);out.write("\r\n".getBytes(StandardCharsets.US_ASCII));}
                            out.write(bytes,offset,size);if(mode==2)out.write("\r\n".getBytes(StandardCharsets.US_ASCII));out.flush();offset+=size;
                        }
                        if(mode==2){out.write("0\r\n\r\n".getBytes(StandardCharsets.US_ASCII));out.flush();}
                    }catch(Exception e){throw new RuntimeException(e);}
                });
                IPContextTransport.Call call=new IPContextTransport(ApiConnectionConfig.create("http://127.0.0.1:"+server.getLocalPort(),true,null)).newCall("1.1.1.1");
                IPContext result=call.execute();assertEquals(com.checknetwork.app.core.IPContextParser.parse(bytes,"1.1.1.1").projection(),result.projection());assertTrue(call.isQuiescent());served.get(5,TimeUnit.SECONDS);
                server.setSoTimeout(100);assertThrows(SocketTimeoutException.class,server::accept);
            }finally{peer.shutdownNow();assertTrue(peer.awaitTermination(5,TimeUnit.SECONDS));}
        }
    }
}
