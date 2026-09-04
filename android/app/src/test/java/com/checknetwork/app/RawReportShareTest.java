package com.checknetwork.app;

import static org.junit.Assert.*;

import android.net.Uri;
import com.checknetwork.app.core.ContractLimits;
import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public final class RawReportShareTest {
    @Rule public final TemporaryFolder temporary = new TemporaryFolder();

    @Test public void everyConfirmationGetsUniqueTokenUriAndRevokesBeforeOwnedReplacement() throws Exception {
        File cache=temporary.newFolder("cache");List<String> events=new ArrayList<>();
        ArrayDeque<String> tokens=new ArrayDeque<>(List.of("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"));
        RawReportShare.AtomicWriter system=RawReportShare.systemAtomicWriter();
        RawReportShare share=new RawReportShare(cache,
                file->Uri.parse("content://provider/shared_reports/"+file.getName()),
                (temp,destination,bytes)->{events.add("write:"+destination.getName());system.write(temp,destination,bytes);},
                tokens::removeFirst,uri->events.add("revoke:"+uri));

        Uri first=share.writeConfirmed("{\"id\":\"report-id-must-not-leak\",\"target\":\"target.example\"}");
        File old=share.issuedFileForTest();
        Uri second=share.writeConfirmed("{\"id\":\"second-report-id\"}");

        assertNotEquals(first,second);assertFalse(old.exists());assertTrue(share.issuedFileForTest().isFile());
        assertTrue(events.indexOf("revoke:"+first)<events.indexOf("write:"+share.issuedFileForTest().getName()));
        String filename=share.issuedFileForTest().getName();
        assertFalse(filename.contains("report-id-must-not-leak"));assertFalse(filename.contains("second-report-id"));assertFalse(filename.contains("target.example"));
        assertArrayEquals("{\"id\":\"second-report-id\"}".getBytes(StandardCharsets.UTF_8),Files.readAllBytes(share.issuedFileForTest().toPath()));

        share.clear();assertTrue(events.contains("revoke:"+second));assertNull(share.issuedFileForTest());
    }

    @Test public void exactByteCapAcceptedAndOverCapRevokesAndDeletesPriorArtifact() throws Exception {
        File cache=temporary.newFolder("cap-cache");List<Uri> revoked=new ArrayList<>();
        RawReportShare share=share(cache,new ArrayDeque<>(List.of("cccccccccccccccccccccccccccccccc","dddddddddddddddddddddddddddddddd")),revoked);
        String exact="x".repeat(ContractLimits.MAX_TRANSPORT_BYTES);Uri prior=share.writeConfirmed(exact);File priorFile=share.issuedFileForTest();
        assertEquals(ContractLimits.MAX_TRANSPORT_BYTES,priorFile.length());

        assertThrows(IllegalArgumentException.class,()->share.writeConfirmed(exact+"x"));
        assertEquals(List.of(prior),revoked);assertFalse(priorFile.exists());assertNull(share.issuedFileForTest());assertNull(share.tempFileForTest());
    }

    @Test public void writeAndUriFailuresCleanOnlyOwnedFilesAndRevokeAnyIssuedUri() throws Exception {
        File cache=temporary.newFolder("failure-cache");File directory=new File(cache,"shared-reports");assertTrue(directory.mkdirs());
        File unrelated=new File(directory,"unrelated.keep");Files.write(unrelated.toPath(),"keep".getBytes(StandardCharsets.UTF_8));
        List<Uri> revoked=new ArrayList<>();ArrayDeque<String> tokens=new ArrayDeque<>(List.of("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"));
        RawReportShare failing=new RawReportShare(cache,Uri::fromFile,(temp,destination,bytes)->{Files.write(temp.toPath(),bytes);throw new IOException("write failed");},tokens::removeFirst,revoked::add);

        assertThrows(IOException.class,()->failing.writeConfirmed("new"));
        assertTrue(unrelated.isFile());assertNull(failing.issuedFileForTest());assertNull(failing.tempFileForTest());assertTrue(revoked.isEmpty());
    }

    @Test public void uriFailureDeletesCompletedCandidateWithoutTouchingUnrelatedFiles() throws Exception {
        File cache=temporary.newFolder("uri-failure-cache");File directory=new File(cache,"shared-reports");assertTrue(directory.mkdirs());
        File unrelated=new File(directory,"unrelated.keep");Files.write(unrelated.toPath(),new byte[]{1});
        RawReportShare share=new RawReportShare(cache,file->{throw new IllegalStateException("URI failed");},RawReportShare.systemAtomicWriter(),()->"11111111111111111111111111111111",uri->{fail("no URI was issued");});
        assertThrows(IllegalStateException.class,()->share.writeConfirmed("new"));
        assertTrue(unrelated.isFile());assertEquals(1,directory.listFiles().length);assertNull(share.issuedFileForTest());
    }

    @Test public void revokeFailureStillDeletesPriorFileAndFailsClosedBeforeReplacement() throws Exception {
        File cache=temporary.newFolder("revoke-failure-cache");ArrayDeque<String> tokens=new ArrayDeque<>(List.of("22222222222222222222222222222222","33333333333333333333333333333333"));
        List<Uri> attempts=new ArrayList<>();RawReportShare share=new RawReportShare(cache,Uri::fromFile,RawReportShare.systemAtomicWriter(),tokens::removeFirst,uri->{attempts.add(uri);throw new SecurityException("revoke failed");});
        Uri first=share.writeConfirmed("old");File old=share.issuedFileForTest();
        assertThrows(IOException.class,()->share.writeConfirmed("new"));
        assertEquals(List.of(first),attempts);assertFalse(old.exists());assertNull(share.issuedFileForTest());assertEquals(0,sharedJsonFiles(cache).length);
    }

    @Test public void multibyteLimitCountsUtf8Bytes() throws Exception {
        File cache=temporary.newFolder("utf8-cache");RawReportShare share=share(cache,new ArrayDeque<>(List.of("ffffffffffffffffffffffffffffffff")),new ArrayList<>());
        String over="é".repeat(ContractLimits.MAX_TRANSPORT_BYTES/2+1);
        assertThrows(IllegalArgumentException.class,()->share.writeConfirmed(over));assertNull(share.issuedFileForTest());
    }

    @Test public void applicationProviderCreatesDifferentContentUris() throws Exception {
        RawReportShare share=new RawReportShare(RuntimeEnvironment.getApplication());
        Uri first=share.writeConfirmed("{\"attempt\":1}");Uri second=share.writeConfirmed("{\"attempt\":2}");
        assertEquals("content",first.getScheme());assertEquals("content",second.getScheme());assertNotEquals(first,second);share.clear();
    }

    @Test public void atomicWriterUsesCreateNewAndNeverReplacesTempOrDestinationCollision() throws Exception {
        File directory=temporary.newFolder("create-new"),temp=new File(directory,".raw-aaaaaaaaaaaaaaaa.tmp"),destination=new File(directory,"raw-aaaaaaaaaaaaaaaa.json");
        Files.write(temp.toPath(),"temp-canary".getBytes(StandardCharsets.UTF_8));
        assertThrows(IOException.class,()->RawReportShare.systemAtomicWriter().write(temp,destination,"new".getBytes(StandardCharsets.UTF_8)));
        assertArrayEquals("temp-canary".getBytes(StandardCharsets.UTF_8),Files.readAllBytes(temp.toPath()));
        Files.delete(temp.toPath());Files.write(destination.toPath(),"destination-canary".getBytes(StandardCharsets.UTF_8));
        assertThrows(IOException.class,()->RawReportShare.systemAtomicWriter().write(temp,destination,"new".getBytes(StandardCharsets.UTF_8)));
        assertArrayEquals("destination-canary".getBytes(StandardCharsets.UTF_8),Files.readAllBytes(destination.toPath()));
    }

    private static RawReportShare share(File cache,ArrayDeque<String> tokens,List<Uri> revoked){
        return new RawReportShare(cache,file->Uri.parse("content://provider/shared_reports/"+file.getName()),RawReportShare.systemAtomicWriter(),tokens::removeFirst,revoked::add);
    }
    private static File[] sharedJsonFiles(File cache){File[] files=new File(cache,"shared-reports").listFiles((directory,name)->name.endsWith(".json"));return files==null?new File[0]:files;}
}
