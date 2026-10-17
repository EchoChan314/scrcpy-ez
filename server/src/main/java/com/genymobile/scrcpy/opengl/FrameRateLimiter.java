package com.genymobile.scrcpy.opengl;

/** Preserve a PTS phase instead of restarting a whole interval at each callback. */
final class FrameRateLimiter {
    private int previousTarget;
    private long lastPts = -1, nextDue;

    boolean shouldRender(long ptsNs, int target) {
        if (target <= 0) {
            previousTarget = 0;
            lastPts = ptsNs;
            return true;
        }
        long period = 1000000000L / target;
        if (previousTarget != target || lastPts < 0 || ptsNs <= lastPts || ptsNs - lastPts > 500000000L) {
            previousTarget = target;
            lastPts = ptsNs;
            nextDue = ptsNs + period;
            return true;
        }
        lastPts = ptsNs;
        long tolerance = Math.min(200000, period / 100);
        if (ptsNs + tolerance < nextDue) return false;
        do { nextDue += period; } while (nextDue <= ptsNs);
        return true;
    }
}
