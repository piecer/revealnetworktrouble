package com.checknetwork.app;

import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import androidx.core.content.FileProvider;
import com.checknetwork.app.core.ContractLimits;
import java.io.File;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.BasicFileAttributes;
import java.util.Objects;
import java.util.UUID;

/** Creates one bounded, uniquely named raw-share artifact per explicit confirmation. */
final class RawReportShare {
    private static final String DIRECTORY_NAME="shared-reports";
    private static final String AUTHORITY_SUFFIX=".raw-report-provider";
    private static final int MAX_TOKEN_ATTEMPTS=8;

    interface UriFactory { Uri forFile(File file); }
    interface AtomicWriter { void write(File temporary,File destination,byte[] bytes) throws IOException; }
    interface TokenSource { String next(); }
    interface Revoker { void revoke(Uri uri); }

    private final File directory;
    private final UriFactory uriFactory;
    private final AtomicWriter writer;
    private final TokenSource tokenSource;
    private final Revoker revoker;
    private File issuedFile;
    private Uri issuedUri;
    private File activeTemp;
    private String lastToken;

    /** Stage-11 core entry point; Activity adoption is intentionally a separate integration slice. */
    static RawShareRuntime processRuntime(Context context){return RawShareRuntime.forApplication(context);}

    RawReportShare(Context context){
        Context app=Objects.requireNonNull(context,"context").getApplicationContext();
        if(app==null)app=context;
        Context safeContext=app;
        directory=new File(safeContext.getCacheDir(),DIRECTORY_NAME);
        uriFactory=file->FileProvider.getUriForFile(safeContext,safeContext.getPackageName()+AUTHORITY_SUFFIX,file);
        writer=systemAtomicWriter();tokenSource=RawReportShare::randomToken;
        revoker=uri->safeContext.revokeUriPermission(uri,Intent.FLAG_GRANT_READ_URI_PERMISSION);
    }

    RawReportShare(File cacheDirectory,UriFactory uriFactory,AtomicWriter writer){
        this(cacheDirectory,uriFactory,writer,RawReportShare::randomToken,uri->{ });
    }

    RawReportShare(File cacheDirectory,UriFactory uriFactory,AtomicWriter writer,TokenSource tokenSource,Revoker revoker){
        directory=new File(Objects.requireNonNull(cacheDirectory,"cacheDirectory"),DIRECTORY_NAME);
        this.uriFactory=Objects.requireNonNull(uriFactory,"uriFactory");
        this.writer=Objects.requireNonNull(writer,"writer");
        this.tokenSource=Objects.requireNonNull(tokenSource,"tokenSource");
        this.revoker=Objects.requireNonNull(revoker,"revoker");
    }

    synchronized Uri writeConfirmed(String rawJson) throws IOException {
        Objects.requireNonNull(rawJson,"rawJson");
        retireIssued(true);
        byte[] bytes=rawJson.getBytes(StandardCharsets.UTF_8);
        if(bytes.length>ContractLimits.MAX_TRANSPORT_BYTES)throw new IllegalArgumentException("Raw report exceeds the UTF-8 transport byte limit");
        if(!directory.isDirectory()&&!directory.mkdirs()&&!directory.isDirectory())throw new IOException("Could not create raw report cache directory");
        String token=nextUniqueToken();
        File destination=new File(directory,"raw-"+token+".json");
        File temporary=new File(directory,".raw-"+token+".tmp");
        activeTemp=temporary;Uri candidateUri=null;
        try{
            writer.write(temporary,destination,bytes);
            if(!destination.isFile())throw new IOException("Atomic raw report write did not create a file");
            candidateUri=Objects.requireNonNull(uriFactory.forFile(destination),"content URI");
            issuedFile=destination;issuedUri=candidateUri;lastToken=token;activeTemp=null;
            return candidateUri;
        }catch(IOException|RuntimeException failure){
            if(candidateUri!=null)safeRevoke(candidateUri);
            deleteIfPresent(temporary);deleteIfPresent(destination);activeTemp=null;
            throw failure;
        }
    }

    synchronized void clear(){
        try{retireIssued(false);}catch(IOException impossible){throw new AssertionError(impossible);}
        File temporary=activeTemp;activeTemp=null;deleteIfPresent(temporary);
        if(directory.isDirectory())directory.delete();
    }

    synchronized File issuedFileForTest(){return issuedFile;}
    synchronized File tempFileForTest(){return activeTemp;}

    private void retireIssued(boolean failOnRevoke) throws IOException {
        Uri oldUri=issuedUri;File oldFile=issuedFile;issuedUri=null;issuedFile=null;
        RuntimeException revokeFailure=null;
        if(oldUri!=null){try{revoker.revoke(oldUri);}catch(RuntimeException failure){revokeFailure=failure;}}
        deleteIfPresent(oldFile);
        if(revokeFailure!=null&&failOnRevoke)throw new IOException("Could not revoke prior raw report URI",revokeFailure);
    }

    private String nextUniqueToken() throws IOException {
        for(int attempt=0;attempt<MAX_TOKEN_ATTEMPTS;attempt++){
            String token=Objects.requireNonNull(tokenSource.next(),"raw share token");
            if(!token.matches("[A-Za-z0-9_-]{16,128}"))throw new IOException("Raw share token is invalid");
            if(token.equals(lastToken))continue;
            File destination=new File(directory,"raw-"+token+".json");File temporary=new File(directory,".raw-"+token+".tmp");
            if(!destination.exists()&&!temporary.exists())return token;
        }
        throw new IOException("Could not allocate a unique raw report name");
    }

    static AtomicWriter systemAtomicWriter(){
        return (temporary,destination,bytes)->{
            if(!temporary.toPath().getParent().equals(destination.toPath().getParent()))throw new IOException("Raw report paths have different parents");
            BasicFileAttributes directory=Files.readAttributes(temporary.toPath().getParent(),BasicFileAttributes.class,LinkOption.NOFOLLOW_LINKS);
            if(!directory.isDirectory()||directory.isSymbolicLink())throw new IOException("Raw report parent is unsafe");
            try(FileChannel output=FileChannel.open(temporary.toPath(),StandardOpenOption.CREATE_NEW,StandardOpenOption.WRITE,LinkOption.NOFOLLOW_LINKS)){
                ByteBuffer data=ByteBuffer.wrap(bytes);while(data.hasRemaining())output.write(data);output.force(true);
            }
            if(Files.exists(destination.toPath(),LinkOption.NOFOLLOW_LINKS))throw new IOException("Raw report destination already exists");
            try{Files.move(temporary.toPath(),destination.toPath(),StandardCopyOption.ATOMIC_MOVE);}
            catch(AtomicMoveNotSupportedException unsupported){throw new IOException("Cache does not support atomic raw report replacement",unsupported);}
            BasicFileAttributes written=Files.readAttributes(destination.toPath(),BasicFileAttributes.class,LinkOption.NOFOLLOW_LINKS);
            if(!written.isRegularFile()||written.isSymbolicLink())throw new IOException("Raw report destination is unsafe");
            try(FileChannel parent=FileChannel.open(destination.toPath().getParent(),StandardOpenOption.READ)){parent.force(true);}
        };
    }

    private static String randomToken(){return UUID.randomUUID().toString().replace("-","");}
    private void safeRevoke(Uri uri){try{revoker.revoke(uri);}catch(RuntimeException ignored){}}
    private static void deleteIfPresent(File file){if(file==null)return;try{Files.deleteIfExists(file.toPath());}catch(IOException ignored){}}
}
