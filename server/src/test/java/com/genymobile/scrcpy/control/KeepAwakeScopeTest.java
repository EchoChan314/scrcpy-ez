package com.genymobile.scrcpy.control;

import com.genymobile.scrcpy.BuildConfig;
import com.genymobile.scrcpy.Options;

import org.junit.Test;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public class KeepAwakeScopeTest {
    @Test public void mainDisplayRequiresExplicitOption() {
        assertFalse(Options.parse(BuildConfig.VERSION_NAME).getKeepActive());
        assertTrue(Options.parse(BuildConfig.VERSION_NAME, "keep_active=true").getKeepActive());
        assertFalse(Options.parse(BuildConfig.VERSION_NAME, "keep_active=false").getKeepActive());
    }

    @Test public void virtualAppSecondaryAndCameraSessionsCannotKeepMainScreenAwake() {
        assertFalse(Options.parse(BuildConfig.VERSION_NAME, "keep_active=true", "new_display=800x600").getKeepActive());
        assertFalse(Options.parse(BuildConfig.VERSION_NAME, "keep_active=true", "display_id=1").getKeepActive());
        assertFalse(Options.parse(BuildConfig.VERSION_NAME, "keep_active=true", "video_source=camera").getKeepActive());
    }
}
