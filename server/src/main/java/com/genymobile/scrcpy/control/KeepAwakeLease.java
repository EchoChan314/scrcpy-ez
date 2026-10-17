package com.genymobile.scrcpy.control;

/** A session-owned screen lock, with a deadline independent of video and input activity. */
public final class KeepAwakeLease implements AutoCloseable {
    public static final long TIMEOUT_MS = 30000;

    public interface Lock {
        void release() throws Exception;
    }

    public interface Factory {
        Lock acquire() throws Exception;
    }

    private final Factory factory;
    private Lock lock;
    private long deadlineMs;
    private boolean closed;
    private boolean failed;

    public KeepAwakeLease(Factory factory) {
        this.factory = factory;
    }

    public synchronized void heartbeat(long nowMs) throws Exception {
        if (closed || failed) {
            return;
        }
        if (lock == null) {
            try {
                lock = factory.acquire();
            } catch (Exception e) {
                failed = true; // Do not retry an unsupported API on every heartbeat.
                throw e;
            }
        }
        deadlineMs = nowMs + TIMEOUT_MS;
    }

    public synchronized void expire(long nowMs) throws Exception {
        if (lock != null && nowMs >= deadlineMs) {
            release();
        }
    }

    private void release() throws Exception {
        lock.release();
        lock = null; // Keep the handle available for another cleanup attempt on failure.
    }

    @Override
    public synchronized void close() throws Exception {
        closed = true; // A racing/late heartbeat must never reacquire after shutdown.
        if (lock != null) {
            release();
        }
    }
}
