package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.model.CodecOption;

import java.util.List;

/** Configure-time throughput hint, independent of the variable-rate input limit. */
final class EncoderFrameRate {
    private static final int LEGACY_FRAME_RATE = 60;

    private final int requested;
    private final boolean explicit;
    private boolean fallback;

    EncoderFrameRate(float maxFps, List<CodecOption> options) {
        // Preserve the proven low-rate/unlimited path. Fractional ceilings must
        // not ask the encoder to work more slowly than the permitted input.
        requested = !Float.isNaN(maxFps) && !Float.isInfinite(maxFps) && maxFps > LEGACY_FRAME_RATE
                ? (int) Math.ceil(maxFps) : LEGACY_FRAME_RATE;
        boolean found = false;
        if (options != null) {
            for (CodecOption option : options) {
                if ("frame-rate".equals(option.getKey())) {
                    found = true;
                    break;
                }
            }
        }
        explicit = found;
    }

    int get() {
        return fallback ? LEGACY_FRAME_RATE : requested;
    }

    /** Retry the same geometry once; explicit codec options remain authoritative. */
    boolean retryWithLegacyRate() {
        if (explicit || fallback || requested == LEGACY_FRAME_RATE) {
            return false;
        }
        fallback = true;
        return true;
    }
}
