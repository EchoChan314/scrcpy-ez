package com.genymobile.scrcpy.util;

import java.io.File;
import java.io.IOException;
import java.io.RandomAccessFile;
import java.nio.channels.FileLock;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.Arrays;

/** Shared metadata for all shell servers on one device. No clipboard payload is
 * stored here. The lock is held only while reading/applying a completed copy,
 * never while transmitting it over a control socket. */
public final class ClipboardJournal {
    private static final Object LOCAL_LOCK = new Object();
    private static final int MAGIC = 0x216cb002;
    private final File file;

    // Mutable transaction state is accessed only inside a withLock operation.
    @SuppressWarnings("checkstyle:VisibilityModifier")
    public static final class Record {
        public long revision;
        public long pcEpoch;
        public long pcVersion;
        public byte[] current = new byte[32];
        public byte[] remote = new byte[32];
        public byte[] remoteContent = new byte[32];

        public long observe(byte[] key) {
            if (!Arrays.equals(current, key)) {
                revision = Math.max(revision + 1, System.nanoTime());
                current = key;
            }
            return revision;
        }

        public boolean isRemote(byte[] key) {
            return Arrays.equals(remote, key);
        }

        public boolean accept(long epoch, long version) {
            if (epoch == 0) {
                return true;
            } // legacy/manual protocol
            if (pcEpoch == epoch && version < pcVersion) {
                return false;
            }
            pcEpoch = epoch;
            pcVersion = version;
            return true;
        }
    }

    public interface Operation<T> {
        T run(Record record) throws IOException;
    }

    public ClipboardJournal(File folder) {
        file = new File(folder, ".scrcpy-ez-clipboard-state");
    }

    public <T> T withLock(Operation<T> op) throws IOException {
        // FileChannel.lock() rejects overlapping locks within one JVM instead
        // of waiting, so serialize local callers before the cross-process lock.
        synchronized (LOCAL_LOCK) {
            try (RandomAccessFile raf = new RandomAccessFile(file, "rw");
                    FileLock ignored = raf.getChannel().lock()) {
                Record record = new Record();
                if (raf.length() == 124 && raf.readInt() == MAGIC) {
                    record.revision = raf.readLong();
                    record.pcEpoch = raf.readLong();
                    record.pcVersion = raf.readLong();
                    raf.readFully(record.current);
                    raf.readFully(record.remote);
                    raf.readFully(record.remoteContent);
                }
                T result = op.run(record);
                raf.seek(0);
                raf.writeInt(MAGIC);
                raf.writeLong(record.revision);
                raf.writeLong(record.pcEpoch);
                raf.writeLong(record.pcVersion);
                raf.write(record.current);
                raf.write(record.remote);
                raf.write(record.remoteContent);
                raf.setLength(124);
                return result;
            }
        }
    }

    public static byte[] digest(String mime, byte[] data) {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            digest.update(mime.getBytes(java.nio.charset.StandardCharsets.UTF_8));
            digest.update((byte) 0);
            return digest.digest(data);
        } catch (NoSuchAlgorithmException e) {
            throw new AssertionError(e); // SHA-256 is required by Java/Android
        }
    }
}
