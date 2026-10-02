package com.genymobile.scrcpy.util;

import java.io.File;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;

/** Standalone JVM regression test; Android framework and device clipboard are
 * deliberately not used. Run with assertions enabled. */
public final class ClipboardJournalTest {
    private ClipboardJournalTest() {
    }

    public static void main(String[] args) throws Exception {
        if (args.length > 0 && "worker".equals(args[0])) {
            ClipboardJournal journal = new ClipboardJournal(new File(args[1]));
            for (int i = 0; i < 50; ++i) {
                journal.withLock(record -> {
                    ++record.pcVersion; return null;
                });
            }
            return;
        }
        File folder = Files.createTempDirectory(new File(args[0]).toPath(), "journal-test-").toFile();
        ClipboardJournal first = new ClipboardJournal(folder);
        ClipboardJournal second = new ClipboardJournal(folder);
        byte[] a = ClipboardJournal.digest("image/png", new byte[] {1, 2, 3});
        byte[] b = ClipboardJournal.digest("image/png", new byte[] {1, 2, 4});
        long revision = first.withLock(record -> record.observe(a));
        assert second.withLock(record -> record.observe(a)) == revision;
        assert second.withLock(record -> record.observe(b)) > revision;
        first.withLock(record -> {
            record.remote = b;
            record.remoteContent = a;
            assert record.accept(7, 2);
            return null;
        });
        second.withLock(record -> {
            assert record.isRemote(b);
            assert !record.isRemote(a); // a new local/history copy is not suppressed
            assert !record.accept(7, 1); // delayed older route must not overwrite
            assert record.accept(7, 2); // safe idempotent retry after lost ACK
            assert record.accept(7, 3);
            assert record.accept(8, 1); // a new PC session epoch is independent
            record.pcVersion = 0;
            return null;
        });
        String javaExecutable = new File(System.getProperty("java.home"), "bin/java.exe").getPath();
        List<Process> workers = new ArrayList<>();
        for (int i = 0; i < 4; ++i) {
            workers.add(new ProcessBuilder(javaExecutable, "-ea", "-cp", System.getProperty("java.class.path"),
                    ClipboardJournalTest.class.getName(), "worker", folder.getPath()).inheritIO().start());
        }
        for (Process worker : workers) {
            assert worker.waitFor() == 0;
        }
        assert first.withLock(record -> record.pcVersion) == 200;
        assert !java.util.Arrays.equals(ClipboardJournal.digest("text/plain", "图片".getBytes(StandardCharsets.UTF_8)), a);
        System.out.println("PASS: four-process journal locking, shared provenance, duplicate revisions and stale-copy rejection");
    }
}
