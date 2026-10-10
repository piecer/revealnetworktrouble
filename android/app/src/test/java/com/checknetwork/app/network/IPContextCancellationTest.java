package com.checknetwork.app.network;
import static org.junit.Assert.*;
import java.nio.charset.StandardCharsets;
import java.util.Locale;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;
@RunWith(RobolectricTestRunner.class)
@Config(sdk=35)
public final class IPContextCancellationTest {
    @Test public void productionHttpCancellationClosesBlockedReadPromptlyWithoutRetry() throws Exception {
        java.util.concurrent.ExecutorService threads=java.util.concurrent.Executors.newFixedThreadPool(3);
        java.util.concurrent.CountDownLatch headers=new java.util.concurrent.CountDownLatch(1), release=new java.util.concurrent.CountDownLatch(1);
        try (java.net.ServerSocket server=new java.net.ServerSocket(0,1,java.net.InetAddress.getLoopbackAddress())) {
            server.setSoTimeout(2000);
            java.util.concurrent.Future<?> peer=threads.submit(()->{
                try(java.net.Socket socket=server.accept()) {
                    socket.setSoTimeout(2000);
                    java.io.BufferedReader in=new java.io.BufferedReader(new java.io.InputStreamReader(socket.getInputStream(),StandardCharsets.UTF_8));
                    int length=0;String line;
                    while(!(line=in.readLine()).isEmpty()) if(line.toLowerCase(Locale.ROOT).startsWith("content-length:"))length=Integer.parseInt(line.substring(15).trim());
                    for(int n=0;n<length;n++)assertTrue(in.read()>=0);
                    socket.getOutputStream().write("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\nConnection: close\r\n\r\n{".getBytes(StandardCharsets.US_ASCII));socket.getOutputStream().flush();headers.countDown();
                    assertTrue(release.await(3,java.util.concurrent.TimeUnit.SECONDS));
                }catch(Exception e){throw new RuntimeException(e);}
            });
            IPContextTransport.Call call=new IPContextTransport(ApiConnectionConfig.create("http://127.0.0.1:"+server.getLocalPort(),true,null)).newCall("1.1.1.1");
            java.util.concurrent.Future<IPContextTransport.Failure> outcome=threads.submit(()->{try{call.execute();throw new AssertionError();}catch(IPContextTransport.ContextException e){return e.failure();}});
            assertTrue(headers.await(2,java.util.concurrent.TimeUnit.SECONDS));Thread.sleep(100);assertFalse(outcome.isDone());
            java.util.concurrent.atomic.AtomicReference<Thread> cancellingThread=new java.util.concurrent.atomic.AtomicReference<>();
            java.util.concurrent.Future<?> cancellation=threads.submit(()->{cancellingThread.set(Thread.currentThread());call.cancel();});
            try {
                try { cancellation.get(750,java.util.concurrent.TimeUnit.MILLISECONDS); }
                catch (java.util.concurrent.TimeoutException blocked) {
                    Thread t=cancellingThread.get();System.out.println("CANCEL_BLOCKED state="+t.getState());
                    for(StackTraceElement frame:t.getStackTrace())System.out.println("CANCEL_STACK "+frame);
                    throw blocked;
                }
                assertEquals(IPContextTransport.Failure.CANCELLED,outcome.get(750,java.util.concurrent.TimeUnit.MILLISECONDS));
            } finally {release.countDown();peer.get(2,java.util.concurrent.TimeUnit.SECONDS);}
        } finally {release.countDown();threads.shutdownNow();assertTrue(threads.awaitTermination(3,java.util.concurrent.TimeUnit.SECONDS));}
    }
}
