package com.genymobile.scrcpy.opengl;
import org.junit.Test;
import static org.junit.Assert.*;

public class FrameRateLimiterTest {
    @Test public void nonIntegerRatiosKeepTheIntendedAverage() {
        for (int[] ratio : new int[][] {{120,90}, {144,60}, {120,45}, {60,30}}) {
            FrameRateLimiter limiter = new FrameRateLimiter();
            int kept = 0;
            for (int i = 0; i < ratio[0] * 10; ++i) {
                long pts = i * 1000000000L / ratio[0] + (i % 3 - 1) * 1000;
                if (limiter.shouldRender(pts, ratio[1])) ++kept;
            }
            assertEquals(ratio[1] * 10, kept, 2);
        }
    }
    @Test public void fullRateAndDiscontinuitiesDoNotDropOrCatchUp() {
        FrameRateLimiter limiter = new FrameRateLimiter();
        for (int i=0; i<120; ++i) assertTrue(limiter.shouldRender(i*8333333L, 0));
        assertTrue(limiter.shouldRender(1000000000L, 60));
        assertFalse(limiter.shouldRender(1001000000L, 60));
        assertTrue(limiter.shouldRender(10, 60));
        assertTrue(limiter.shouldRender(9000000000L, 60));
        assertTrue(limiter.shouldRender(9001000000L, 90));
    }
}
