package com.genymobile.scrcpy.control;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.fail;

public class KeepAwakeLeaseTest {
    private static class Locks implements KeepAwakeLease.Factory {
        int acquired;
        int released;
        boolean failRelease;

        @Override
        public KeepAwakeLease.Lock acquire() {
            ++acquired;
            return () -> {
                if (failRelease) {
                    throw new Exception("release failed");
                }
                ++released;
            };
        }
    }

    @Test public void idleVideoDoesNotExpireWhileClientIsAlive() throws Exception {
        Locks locks = new Locks();
        KeepAwakeLease lease = new KeepAwakeLease(locks);
        for (long now = 0; now <= 120000; now += 5000) {
            lease.heartbeat(now);
            lease.expire(now + 4999);
        }
        assertEquals(1, locks.acquired);
        assertEquals(0, locks.released);
        lease.close();
        assertEquals(1, locks.released);
    }

    @Test public void stalledConnectionExpiresAndCanRecover() throws Exception {
        Locks locks = new Locks();
        KeepAwakeLease lease = new KeepAwakeLease(locks);
        lease.expire(100000); // No lock before the first heartbeat.
        assertEquals(0, locks.acquired);
        lease.heartbeat(100000);
        lease.expire(129999);
        assertEquals(0, locks.released);
        lease.expire(130000);
        lease.expire(140000);
        assertEquals(1, locks.released);
        lease.heartbeat(140000);
        assertEquals(2, locks.acquired);
        lease.close();
        assertEquals(2, locks.released);
    }

    @Test public void allShutdownRoutesAreIdempotentAndRejectLateHeartbeat() throws Exception {
        Locks locks = new Locks();
        KeepAwakeLease lease = new KeepAwakeLease(locks);
        lease.heartbeat(0);
        lease.close();
        lease.close();
        lease.heartbeat(5000);
        lease.expire(100000);
        assertEquals(1, locks.acquired);
        assertEquals(1, locks.released);
    }

    @Test public void shutdownBeforeStartNeverAcquires() throws Exception {
        Locks locks = new Locks();
        KeepAwakeLease lease = new KeepAwakeLease(locks);
        lease.close();
        lease.heartbeat(0);
        assertEquals(0, locks.acquired);
    }

    @Test public void sessionsOwnIndependentLocks() throws Exception {
        Locks locks = new Locks();
        KeepAwakeLease one = new KeepAwakeLease(locks);
        KeepAwakeLease two = new KeepAwakeLease(locks);
        one.heartbeat(0);
        two.heartbeat(0);
        one.close();
        two.heartbeat(20000);
        two.expire(30000);
        assertEquals(2, locks.acquired);
        assertEquals(1, locks.released);
        two.close();
        assertEquals(2, locks.released);
    }

    @Test public void unsupportedDeviceIsTriedOnceWithoutActivityFallback() throws Exception {
        final int[] attempts = {0};
        KeepAwakeLease lease = new KeepAwakeLease(() -> {
            ++attempts[0];
            throw new UnsupportedOperationException("test");
        });
        try {
            lease.heartbeat(0);
            fail("Expected acquisition failure");
        } catch (UnsupportedOperationException expected) {
            // Failure is reported once; streaming is independent of this capability.
        }
        lease.heartbeat(5000);
        lease.close();
        assertEquals(1, attempts[0]);
    }

    @Test public void failedReleaseRetainsHandleForOtherCleanupRoute() throws Exception {
        Locks locks = new Locks();
        KeepAwakeLease lease = new KeepAwakeLease(locks);
        lease.heartbeat(0);
        locks.failRelease = true;
        try {
            lease.close();
            fail("Expected release failure");
        } catch (Exception expected) {
            // Another shutdown route can retry; late heartbeats cannot reacquire.
        }
        locks.failRelease = false;
        lease.heartbeat(5000);
        lease.close();
        assertEquals(1, locks.acquired);
        assertEquals(1, locks.released);
    }
}
