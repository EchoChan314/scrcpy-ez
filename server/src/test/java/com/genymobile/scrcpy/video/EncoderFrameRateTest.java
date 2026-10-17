package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.model.CodecOption;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public class EncoderFrameRateTest {
    @Test
    public void lowRateAndUnlimitedSessionsKeepLegacyBehavior() {
        for (float fps : new float[] {0, 1, 24, 30, 59.94f, 60, Float.NaN, Float.POSITIVE_INFINITY}) {
            EncoderFrameRate hint = new EncoderFrameRate(fps, null);
            assertEquals(60, hint.get());
            assertFalse(hint.retryWithLegacyRate());
        }
    }

    @Test
    public void highRateAndFractionalCeilingsHaveEnoughWorkingRate() {
        assertEquals(90, new EncoderFrameRate(90, null).get());
        assertEquals(120, new EncoderFrameRate(119.88f, null).get());
        assertEquals(121, new EncoderFrameRate(120.01f, null).get());
        assertEquals(144, new EncoderFrameRate(144, null).get());
    }

    @Test
    public void startupFailureFallsBackOnceAndSurvivesRebuilds() {
        EncoderFrameRate hint = new EncoderFrameRate(120, null);
        assertTrue(hint.retryWithLegacyRate());
        for (int rebuild = 0; rebuild < 5; ++rebuild) {
            assertEquals(60, hint.get());
            assertFalse(hint.retryWithLegacyRate());
        }
        // A new connection may try its own target again.
        assertEquals(120, new EncoderFrameRate(120, null).get());
    }

    @Test
    public void explicitHintMustNeverBeSilentlyReplaced() {
        for (String option : new String[] {"frame-rate=60", "frame-rate:float=119.88", "frame-rate=120,frame-rate=90"}) {
            EncoderFrameRate hint = new EncoderFrameRate(120, CodecOption.parse(option));
            assertFalse(hint.retryWithLegacyRate());
        }
        EncoderFrameRate otherOption = new EncoderFrameRate(120, CodecOption.parse("operating-rate:float=120"));
        assertTrue(otherOption.retryWithLegacyRate());
    }
}
