package com.genymobile.scrcpy.video;

/** Coded bytes per media-time window, independent of the requested bitrate. */
final class EncodedRate {
    private long startPts = -1, lastPts = -1, bytes, rate;
    void reset() { startPts = lastPts = -1; bytes = rate = 0; }
    void add(long ptsUs, int size) {
        if (startPts < 0 || ptsUs <= lastPts || ptsUs - lastPts > 3000000) {
            reset(); startPts = ptsUs;
        }
        lastPts = ptsUs;
        bytes += size;
        if (ptsUs - startPts >= 500000) {
            rate = bytes * 8000000 / (ptsUs - startPts);
            startPts = ptsUs; bytes = 0;
        }
    }
    long get() { return rate; }
}
