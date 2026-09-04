package com.checknetwork.app;

import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Looper;
import androidx.core.content.FileProvider;
import com.checknetwork.app.core.ContractLimits;
import java.io.File;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.DirectoryStream;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.OpenOption;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.BasicFileAttributes;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.Iterator;
import java.util.List;
import java.util.Objects;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.regex.Pattern;

/** Process-owned, single-slot asynchronous state machine for sensitive raw report files. */
final class RawShareRuntime {
    static final int RECONCILE_ENTRY_LIMIT=32;
    static final int RECONCILE_DELETE_LIMIT=32;
    static final long LEASE_MILLIS=15L*60L*1000L;
    static final long MAX_LIVE_BYTES=16L*1024L*1024L;
    static final int MAX_LIVE_FILES=2;
    private static final int MAX_TOKEN_ATTEMPTS=8;
    private static final Pattern FINAL_NAME=Pattern.compile("raw-[A-Za-z0-9_-]{16,128}\\.json");
    private static final Pattern TEMP_NAME=Pattern.compile("\\.raw-[A-Za-z0-9_-]{16,128}\\.tmp");
    private static final Object SINGLETON_LOCK=new Object();
    private static RawShareRuntime singleton;

    enum Phase { CLEANING, IDLE, PREPARING, MATERIALIZED, GRANTED, RETIRED, CLEANUP_FAILED, DISPOSED }
    enum Admission { ACCEPTED, BUSY, CLOSED }
    record Entry(String name,long size,long modifiedMillis,boolean regular,boolean symbolicLink) {}
    record ScanPage(List<Entry> entries,String cursor,boolean hasMore) {
        ScanPage { entries=List.copyOf(entries); }
    }
    record Snapshot(Phase phase,Uri uri,String failure,long leaseExpiresAt) {}

    interface Clock { long nowMillis(); }
    interface TokenSource { String next(); }
    interface UriFactory { Uri forFile(File file); }
    interface Revoker { void revoke(Uri uri) throws Exception; }
    interface Dispatcher { void dispatch(Runnable callback); }
    interface MainThread { void assertMainThread(); }
    interface Listener { void changed(Snapshot snapshot); }
    interface ScanSession extends AutoCloseable {
        ScanPage next(int limit) throws IOException;
        @Override void close() throws IOException;
    }
    interface Worker {
        void execute(Runnable work);
        CompletableFuture<Void> shutdown();
    }
    interface FileSystem {
        File directory();
        void prepareDirectory() throws IOException;
        ScanPage scan(String afterName,int limit) throws IOException;
        default ScanSession openScan() throws IOException {
            return new ScanSession() {
                private String cursor;
                private boolean closed;
                public ScanPage next(int limit) throws IOException {
                    if(closed)throw new IOException("Reconciliation scan is closed");
                    ScanPage page=scan(cursor,limit);cursor=page.cursor();return page;
                }
                public void close(){closed=true;}
            };
        }
        boolean exists(String name) throws IOException;
        void writeAtomic(String temporaryName,String destinationName,byte[] bytes) throws IOException;
        File validateForUri(String name) throws IOException;
        void delete(String name) throws IOException;
    }

    private final Object lock=new Object();
    private final FileSystem fs;
    private final Worker worker;
    private final Clock clock;
    private final TokenSource tokens;
    private final UriFactory uriFactory;
    private final Revoker revoker;
    private final Dispatcher dispatcher;
    private final MainThread mainThread;
    private final CompletableFuture<Void> disposed=new CompletableFuture<>();
    private Phase phase=Phase.CLEANING;
    private Listener listener;
    private Object listenerOwner;
    private long listenerGeneration;
    private Object operationOwner;
    private String pendingRaw;
    private long generation;
    private boolean slotOwned=true;
    private boolean disposalRequested;
    private String finalName;
    private String temporaryName;
    private Uri uri;
    private boolean revoked;
    private long leaseExpiresAt;
    private String failure;
    private Exception disposalFailure;
    private Reconciliation reconciliation;
    private final Set<String> usedTokens=new HashSet<>();

    RawShareRuntime(FileSystem fs,Worker worker,Clock clock,TokenSource tokens,UriFactory uriFactory,
                    Revoker revoker,Dispatcher dispatcher,MainThread mainThread) {
        this(fs,worker,clock,tokens,uriFactory,revoker,dispatcher,mainThread,true);
    }

    private RawShareRuntime(FileSystem fs,Worker worker,Clock clock,TokenSource tokens,UriFactory uriFactory,
                    Revoker revoker,Dispatcher dispatcher,MainThread mainThread,boolean start) {
        this.fs=Objects.requireNonNull(fs,"fs");this.worker=Objects.requireNonNull(worker,"worker");
        this.clock=Objects.requireNonNull(clock,"clock");this.tokens=Objects.requireNonNull(tokens,"tokens");
        this.uriFactory=Objects.requireNonNull(uriFactory,"uriFactory");this.revoker=Objects.requireNonNull(revoker,"revoker");
        this.dispatcher=Objects.requireNonNull(dispatcher,"dispatcher");this.mainThread=Objects.requireNonNull(mainThread,"mainThread");
        if(start){reconciliation=new Reconciliation(this::finishIdle);worker.execute(this::reconcileStepOnWorker);}
    }

    static RawShareRuntime forApplication(Context context) {
        Context application=Objects.requireNonNull(context,"context").getApplicationContext();
        if(application==null)application=context;
        RawShareRuntime result;boolean start=false;
        synchronized(SINGLETON_LOCK) {
            if(singleton==null) {
                Context app=application;
                singleton=new RawShareRuntime(new SystemFileSystem(app.getCacheDir()),new SystemWorker(),System::currentTimeMillis,
                        ()->UUID.randomUUID().toString().replace("-",""),
                        file->FileProvider.getUriForFile(app,app.getPackageName()+".raw-report-provider",file),
                        value->app.revokeUriPermission(value,Intent.FLAG_GRANT_READ_URI_PERMISSION),
                        callback->new android.os.Handler(Looper.getMainLooper()).post(callback),
                        ()->{if(Looper.myLooper()!=Looper.getMainLooper())throw new IllegalStateException("raw-share API must run on main thread");},false);
                start=true;
            }
            result=singleton;
        }
        if(start)result.worker.execute(result::startupCleanup);
        return result;
    }

    static boolean recognizedName(String name){return name!=null&&(FINAL_NAME.matcher(name).matches()||TEMP_NAME.matcher(name).matches());}

    Snapshot snapshot(){synchronized(lock){return snapshotLocked();}}

    void attach(Object owner,Listener value) {
        mainThread.assertMainThread();Objects.requireNonNull(owner,"owner");Objects.requireNonNull(value,"listener");
        Notification notification;
        synchronized(lock){if(disposalRequested)return;listenerOwner=owner;listener=value;listenerGeneration++;notification=notificationLocked();}
        dispatch(notification);
    }

    void detach(Object owner) {
        mainThread.assertMainThread();
        boolean retire=false;
        synchronized(lock){
            if(listenerOwner==owner){listenerOwner=null;listener=null;listenerGeneration++;}
            if(operationOwner==owner&&(phase==Phase.CLEANING||phase==Phase.PREPARING)){dropOperationLocked();generation++;}
            else if(operationOwner==owner&&phase==Phase.MATERIALIZED)retire=markRetireLocked("detached");
        }
        if(retire)scheduleRetire();
    }

    Admission prepare(Object owner,String rawJson) {
        mainThread.assertMainThread();Objects.requireNonNull(owner,"owner");Objects.requireNonNull(rawJson,"rawJson");
        long operation;
        boolean expire;
        synchronized(lock) {
            expire=markExpiryLocked();
            if(!expire) {
                if(disposalRequested||phase==Phase.CLEANUP_FAILED||phase==Phase.DISPOSED)return Admission.CLOSED;
                if(slotOwned||phase==Phase.CLEANING||phase==Phase.PREPARING||phase==Phase.RETIRED)return Admission.BUSY;
                slotOwned=true;phase=Phase.CLEANING;failure=null;pendingRaw=rawJson;operationOwner=owner;operation=++generation;
            } else operation=-1;
        }
        if(expire){scheduleRetire();return Admission.BUSY;}
        publish();
        worker.execute(()->prepareOnWorker(operation));
        return Admission.ACCEPTED;
    }

    Uri grant(Object owner) {
        mainThread.assertMainThread();
        boolean expire;
        synchronized(lock) {
            expire=markExpiryLocked();
            if(!expire) {
                if(disposalRequested||phase!=Phase.MATERIALIZED||operationOwner!=owner||uri==null)return null;
                phase=Phase.GRANTED;leaseExpiresAt=safeAdd(clock.nowMillis(),LEASE_MILLIS);
                return uri;
            }
        }
        scheduleRetire();return null;
    }

    void observe() {mainThread.assertMainThread();boolean retire;synchronized(lock){retire=markExpiryLocked();}if(retire)scheduleRetire();}

    void retire(Object owner) {
        mainThread.assertMainThread();
        boolean retire;synchronized(lock){retire=operationOwner==owner&&markRetireLocked("retired");}if(retire)scheduleRetire();
    }

    Admission retryCleanup() {
        mainThread.assertMainThread();
        synchronized(lock){if(disposalRequested)return Admission.CLOSED;if(phase!=Phase.CLEANUP_FAILED||slotOwned)return Admission.BUSY;slotOwned=true;phase=Phase.CLEANING;failure=null;}
        publish();worker.execute(this::retryCleanupOnWorker);return Admission.ACCEPTED;
    }

    CompletableFuture<Void> dispose() {
        mainThread.assertMainThread();boolean begin=false;
        synchronized(lock){
            if(disposalRequested)return disposed;
            disposalRequested=true;listener=null;listenerOwner=null;listenerGeneration++;dropOperationLocked();generation++;
            if(!slotOwned){slotOwned=true;phase=Phase.RETIRED;begin=true;}
        }
        if(begin)worker.execute(this::disposeOnWorker);
        return disposed;
    }


    private void startupCleanup() {
        beginReconciliation(this::finishIdle,false);
    }

    private void prepareOnWorker(long operation) {
        try {
            int retired=retireOwnedOnWorker();
            beginReconciliation(()->materializeOnWorker(operation),retired>0);
        } catch(Exception cleanupProblem) {
            finishCleanupFailure(cleanupProblem);
        }
    }

    private void materializeOnWorker(long operation) {
        String raw;
        try {
            synchronized(lock){if(operation!=generation)throw new StaleOperationException();phase=Phase.PREPARING;raw=pendingRaw;}
            publish();
            byte[] bytes=raw.getBytes(StandardCharsets.UTF_8);
            if(bytes.length>ContractLimits.MAX_TRANSPORT_BYTES)throw new IllegalArgumentException("Raw report exceeds the UTF-8 transport byte limit");
            fs.prepareDirectory();
            String token=allocateToken();
            String candidate="raw-"+token+".json",temporary=".raw-"+token+".tmp";
            synchronized(lock){if(operation!=generation)throw new StaleOperationException();finalName=candidate;temporaryName=temporary;}
            fs.writeAtomic(temporary,candidate,bytes);
            File safe=fs.validateForUri(candidate);
            Uri candidateUri=Objects.requireNonNull(uriFactory.forFile(safe),"content URI");
            boolean stale;
            synchronized(lock){
                stale=operation!=generation||operationOwner==null||disposalRequested;
                if(!stale){uri=candidateUri;temporaryName=null;phase=Phase.MATERIALIZED;pendingRaw=null;slotOwned=false;}
            }
            if(stale){cleanupCandidate(candidateUri,temporary,candidate);finishAfterTerminalCleanup();return;}
            publish();
        } catch(StaleOperationException stale) {
            terminalFailure(operation,stale);
        } catch(Exception problem) {
            terminalFailure(operation,problem);
        }
    }

    private void terminalFailure(long operation,Exception problem) {
        String temporary,destination;Uri candidate;
        synchronized(lock){temporary=temporaryName;destination=finalName;candidate=uri;}
        try {cleanupCandidate(candidate,temporary,destination);}
        catch(Exception cleanup){finishCleanupFailure(cleanup);return;}
        synchronized(lock){
            temporaryName=null;finalName=null;uri=null;pendingRaw=null;operationOwner=null;leaseExpiresAt=0;
            if(disposalRequested){phase=Phase.DISPOSED;}else{phase=Phase.IDLE;failure=safeFailure(problem);}
            slotOwned=false;
        }
        publish();if(disposalRequested)shutdownWorker();
    }

    private void finishAfterTerminalCleanup() {
        synchronized(lock){temporaryName=null;finalName=null;uri=null;pendingRaw=null;operationOwner=null;leaseExpiresAt=0;slotOwned=false;phase=disposalRequested?Phase.DISPOSED:Phase.IDLE;}
        publish();if(disposalRequested)shutdownWorker();
    }

    private void retryCleanupOnWorker() {
        try {int retired=retireOwnedOnWorker();beginReconciliation(this::finishIdle,retired>0);}
        catch(Exception problem){finishCleanupFailure(problem);}
    }

    private void disposeOnWorker() {
        try {int retired=retireOwnedOnWorker();beginReconciliation(this::finishDisposal,retired>0);}
        catch(Exception problem){finishCleanupFailure(problem);}
    }

    private void finishDisposal() {
        synchronized(lock){dropOperationLocked();slotOwned=false;phase=Phase.DISPOSED;}
        shutdownWorker();
    }

    private void finishIdle() {
        boolean shutdown;
        synchronized(lock){dropOperationLocked();slotOwned=false;phase=disposalRequested?Phase.DISPOSED:Phase.IDLE;failure=null;shutdown=disposalRequested;}
        publish();if(shutdown)shutdownWorker();
    }

    private void finishCleanupFailure(Exception problem) {
        boolean shutdown;
        synchronized(lock){dropOperationLocked();slotOwned=false;phase=disposalRequested?Phase.DISPOSED:Phase.CLEANUP_FAILED;failure=safeFailure(problem);shutdown=disposalRequested;if(shutdown)disposalFailure=problem;}
        publish();if(shutdown)shutdownWorker();
    }

    private void beginReconciliation(CheckedRunnable success,boolean defer) {
        if(reconciliation!=null){finishCleanupFailure(new IOException("Reconciliation already active"));return;}
        reconciliation=new Reconciliation(success);
        if(defer)worker.execute(this::reconcileStepOnWorker);else reconcileStepOnWorker();
    }

    private void reconcileStepOnWorker() {
        Reconciliation state=reconciliation;
        if(state==null)return;
        try {
            if(state.session==null){fs.prepareDirectory();state.session=fs.openScan();state.cursor=null;}
            ScanPage page=state.session.next(RECONCILE_ENTRY_LIMIT);
            if(page.entries().size()>RECONCILE_ENTRY_LIMIT)throw new IOException("Filesystem exceeded reconciliation entry limit");
            int deletes=0;
            for(Entry entry:page.entries()) {
                if(!recognizedName(entry.name()))continue;
                if(deletes==RECONCILE_DELETE_LIMIT)throw new IOException("Reconciliation delete budget exhausted");
                fs.delete(entry.name());deletes++;state.passDeletes++;
            }
            if(page.hasMore()) {
                if(page.cursor()==null||Objects.equals(state.cursor,page.cursor()))throw new IOException("Filesystem reconciliation cursor did not advance");
                state.cursor=page.cursor();worker.execute(this::reconcileStepOnWorker);return;
            }
            closeScan(state);state.cursor=null;
            if(state.passDeletes>0){state.passDeletes=0;worker.execute(this::reconcileStepOnWorker);return;}
            reconciliation=null;state.success.run();
        } catch(Exception problem) {
            Exception failure=closeScanMerging(state,problem);reconciliation=null;finishCleanupFailure(failure);
        }
    }

    private String allocateToken() throws IOException {
        for(int attempt=0;attempt<MAX_TOKEN_ATTEMPTS;attempt++) {
            String token=Objects.requireNonNull(tokens.next(),"raw share token");
            if(!token.matches("[A-Za-z0-9_-]{16,128}"))throw new IOException("Raw share token is invalid");
            String destination="raw-"+token+".json",temporary=".raw-"+token+".tmp";
            boolean unused;synchronized(lock){unused=!usedTokens.contains(token);}
            if(unused&&!fs.exists(destination)&&!fs.exists(temporary)){synchronized(lock){usedTokens.add(token);}return token;}
        }
        throw new IOException("Could not allocate a unique raw report name");
    }

    private int retireOwnedOnWorker() throws Exception {
        Uri oldUri;String oldFinal,oldTemporary;boolean needsRevoke;
        synchronized(lock){oldUri=uri;oldFinal=finalName;oldTemporary=temporaryName;needsRevoke=oldUri!=null&&!revoked;if(needsRevoke)revoked=true;}
        Exception failure=null;
        if(needsRevoke)try{revoker.revoke(oldUri);}catch(Exception problem){failure=problem;}
        if(oldUri!=null)synchronized(lock){if(uri==oldUri){uri=null;leaseExpiresAt=0;}}
        int deletes=0;
        if(oldTemporary!=null)try{fs.delete(oldTemporary);deletes++;synchronized(lock){if(Objects.equals(temporaryName,oldTemporary))temporaryName=null;}}catch(Exception problem){failure=mergeFailure(failure,problem);}
        if(oldFinal!=null)try{fs.delete(oldFinal);deletes++;synchronized(lock){if(Objects.equals(finalName,oldFinal))finalName=null;}}catch(Exception problem){failure=mergeFailure(failure,problem);}
        if(failure!=null)throw failure;
        synchronized(lock){revoked=false;}
        return deletes;
    }

    private void cleanupCandidate(Uri candidate,String temporary,String destination) throws Exception {
        Exception failure=null;
        if(candidate!=null)try{revoker.revoke(candidate);}catch(Exception problem){failure=problem;}
        if(temporary!=null)try{fs.delete(temporary);synchronized(lock){if(Objects.equals(temporaryName,temporary))temporaryName=null;}}catch(Exception problem){failure=mergeFailure(failure,problem);}
        if(destination!=null)try{fs.delete(destination);synchronized(lock){if(Objects.equals(finalName,destination))finalName=null;}}catch(Exception problem){failure=mergeFailure(failure,problem);}
        if(failure!=null)throw failure;
    }

    private boolean markRetireLocked(String reason) {
        if(disposalRequested||slotOwned)return false;
        slotOwned=true;phase=Phase.RETIRED;failure=reason;generation++;pendingRaw=null;operationOwner=null;
        return true;
    }

    private boolean markExpiryLocked() {
        return phase==Phase.GRANTED&&clock.nowMillis()>=leaseExpiresAt&&markRetireLocked("lease expired");
    }

    private void scheduleRetire(){publish();worker.execute(this::retryCleanupOnWorker);}

    private void shutdownWorker(){
        worker.shutdown().whenComplete((ignored,problem)->{
            Exception cleanup;synchronized(lock){cleanup=disposalFailure;}
            synchronized(SINGLETON_LOCK){
                if(singleton==this)singleton=null;
                if(problem!=null)disposed.completeExceptionally(problem);
                else if(cleanup!=null)disposed.completeExceptionally(cleanup);
                else disposed.complete(null);
            }
        });
    }
    private Snapshot snapshotLocked(){return new Snapshot(phase,uri,failure,leaseExpiresAt);}
    private Notification notificationLocked(){return listener==null?null:new Notification(listenerOwner,listenerGeneration,snapshotLocked());}
    private void publish(){Notification notification;synchronized(lock){notification=notificationLocked();}dispatch(notification);}
    private void dispatch(Notification notification){if(notification!=null)dispatcher.dispatch(()->deliver(notification));}
    private void deliver(Notification notification){
        Listener current;
        synchronized(lock){
            if(disposalRequested||listenerOwner!=notification.owner||listenerGeneration!=notification.listenerGeneration)return;
            current=listener;
        }
        if(current!=null)current.changed(notification.snapshot);
    }
    private void dropOperationLocked(){operationOwner=null;pendingRaw=null;}
    private void closeScan(Reconciliation state) throws IOException {ScanSession session=state.session;state.session=null;if(session!=null)session.close();}
    private Exception closeScanMerging(Reconciliation state,Exception failure){try{closeScan(state);}catch(Exception close){failure=mergeFailure(failure,close);}return failure;}
    private static long safeAdd(long left,long right){return left>Long.MAX_VALUE-right?Long.MAX_VALUE:left+right;}
    private static String safeFailure(Exception value){String text=value.getMessage();return text==null?value.getClass().getSimpleName():text;}
    private static Exception mergeFailure(Exception first,Exception next){if(first==null)return next;first.addSuppressed(next);return first;}
    private interface CheckedRunnable {void run() throws Exception;}
    private record Notification(Object owner,long listenerGeneration,Snapshot snapshot){}
    private static final class Reconciliation {
        final CheckedRunnable success;ScanSession session;String cursor;int passDeletes;
        Reconciliation(CheckedRunnable success){this.success=success;}
    }
    private static final class StaleOperationException extends Exception {}

    private static final class SystemWorker implements Worker {
        private final ExecutorService executor=Executors.newSingleThreadExecutor(r->{Thread thread=new Thread(r,"raw-share-filesystem");thread.setDaemon(true);return thread;});
        public void execute(Runnable work){executor.execute(work);}
        public CompletableFuture<Void> shutdown(){executor.shutdown();return CompletableFuture.runAsync(()->{try{while(!executor.awaitTermination(1,java.util.concurrent.TimeUnit.DAYS)){} }catch(InterruptedException interrupted){Thread.currentThread().interrupt();throw new RuntimeException(interrupted);}});}
    }

    static final class SystemFileSystem implements FileSystem {
        private final Path cacheRoot;
        private final Path directory;
        SystemFileSystem(File cacheRoot){this.cacheRoot=Objects.requireNonNull(cacheRoot,"cacheRoot").toPath();this.directory=this.cacheRoot.resolve("shared-reports");}
        public File directory(){return directory.toFile();}
        public void prepareDirectory() throws IOException {
            Path rootReal=cacheRoot.toRealPath();
            if(Files.exists(directory,LinkOption.NOFOLLOW_LINKS)) {
                BasicFileAttributes attrs=Files.readAttributes(directory,BasicFileAttributes.class,LinkOption.NOFOLLOW_LINKS);
                if(!attrs.isDirectory()||attrs.isSymbolicLink())throw new IOException("Raw-share directory is not a real directory");
            } else Files.createDirectory(directory);
            Path real=directory.toRealPath(LinkOption.NOFOLLOW_LINKS);
            if(!real.getParent().equals(rootReal))throw new IOException("Raw-share directory escaped cache root");
        }
        public ScanPage scan(String after,int limit) throws IOException {
            throw new IOException("System reconciliation requires a persistent scan session");
        }
        @Override public ScanSession openScan() throws IOException {
            prepareDirectory();
            DirectoryStream<Path> stream=Files.newDirectoryStream(directory);Iterator<Path> paths=stream.iterator();
            return new ScanSession() {
                private boolean closed;
                public ScanPage next(int limit) throws IOException {
                    if(closed)throw new IOException("Reconciliation scan is closed");
                    if(limit<1)throw new IOException("Reconciliation scan limit must be positive");
                    List<Entry> page=new ArrayList<>(Math.min(limit,RECONCILE_ENTRY_LIMIT));String cursor=null;int inspected=0;
                    try {
                        while(inspected<limit&&paths.hasNext()) {
                            Path path=paths.next();inspected++;String name=path.getFileName().toString();
                            BasicFileAttributes attrs=Files.readAttributes(path,BasicFileAttributes.class,LinkOption.NOFOLLOW_LINKS);
                            page.add(new Entry(name,attrs.size(),attrs.lastModifiedTime().toMillis(),attrs.isRegularFile(),attrs.isSymbolicLink()));cursor=name;
                        }
                        return new ScanPage(page,cursor,paths.hasNext());
                    } catch(RuntimeException problem) {throw new IOException("Could not continue raw-share directory scan",problem);}
                }
                public void close() throws IOException {if(!closed){closed=true;stream.close();}}
            };
        }
        public boolean exists(String name) throws IOException {prepareDirectory();return Files.exists(child(name),LinkOption.NOFOLLOW_LINKS);}
        public void writeAtomic(String temporaryName,String destinationName,byte[] bytes) throws IOException {
            prepareDirectory();DirectoryIdentity identity=identity();Path temporary=child(temporaryName),destination=child(destinationName);
            OpenOption[] options={StandardOpenOption.CREATE_NEW,StandardOpenOption.WRITE,LinkOption.NOFOLLOW_LINKS};
            try(FileChannel output=FileChannel.open(temporary,options)) {ByteBuffer data=ByteBuffer.wrap(bytes);while(data.hasRemaining())output.write(data);output.force(true);}
            requireSameDirectory(identity);
            if(Files.exists(destination,LinkOption.NOFOLLOW_LINKS))throw new IOException("Raw-share destination collision");
            try{Files.move(temporary,destination,StandardCopyOption.ATOMIC_MOVE);}
            catch(AtomicMoveNotSupportedException unsupported){throw new IOException("Cache does not support atomic raw report move",unsupported);}
            requireSameDirectory(identity);validateForUri(destinationName);fsyncDirectory();
        }
        public File validateForUri(String name) throws IOException {
            prepareDirectory();Path value=child(name);BasicFileAttributes attrs=Files.readAttributes(value,BasicFileAttributes.class,LinkOption.NOFOLLOW_LINKS);
            if(!attrs.isRegularFile()||attrs.isSymbolicLink())throw new IOException("Raw-share artifact is not a regular no-link file");
            try{Object links=Files.getAttribute(value,"unix:nlink",LinkOption.NOFOLLOW_LINKS);if(links instanceof Number&&((Number)links).longValue()!=1)throw new IOException("Raw-share artifact has multiple links");}catch(UnsupportedOperationException ignored){}
            Path real=value.toRealPath();Path parent=directory.toRealPath(LinkOption.NOFOLLOW_LINKS);if(!real.getParent().equals(parent))throw new IOException("Raw-share artifact escaped directory");return value.toFile();
        }
        public void delete(String name) throws IOException {
            prepareDirectory();Path value=child(name);if(!Files.exists(value,LinkOption.NOFOLLOW_LINKS))return;
            BasicFileAttributes attrs=Files.readAttributes(value,BasicFileAttributes.class,LinkOption.NOFOLLOW_LINKS);
            if(attrs.isDirectory()&&!attrs.isSymbolicLink())throw new IOException("Refusing to delete recognized directory");
            Files.delete(value);fsyncDirectory();
        }
        private Path child(String name) throws IOException {if(!recognizedName(name))throw new IOException("Unrecognized raw-share name");Path value=directory.resolve(name).normalize();if(!value.getParent().equals(directory))throw new IOException("Raw-share path escaped directory");return value;}
        private DirectoryIdentity identity() throws IOException {BasicFileAttributes attrs=Files.readAttributes(directory,BasicFileAttributes.class,LinkOption.NOFOLLOW_LINKS);return new DirectoryIdentity(directory.toRealPath(LinkOption.NOFOLLOW_LINKS),attrs.fileKey());}
        private void requireSameDirectory(DirectoryIdentity expected) throws IOException {DirectoryIdentity current=identity();if(!expected.real.equals(current.real)||expected.fileKey!=null&&!expected.fileKey.equals(current.fileKey))throw new IOException("Raw-share directory changed during publication");}
        private void fsyncDirectory() throws IOException {try(FileChannel channel=FileChannel.open(directory,StandardOpenOption.READ)){channel.force(true);}}
        private record DirectoryIdentity(Path real,Object fileKey){}
    }
}
