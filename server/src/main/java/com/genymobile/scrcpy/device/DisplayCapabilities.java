package com.genymobile.scrcpy.device;

import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.Workarounds;

import android.content.Context;
import android.hardware.display.DisplayManager;
import android.os.Build;
import android.os.Looper;
import android.view.Display;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.FileDescriptor;
import java.io.FileOutputStream;
import java.io.PrintStream;

/** Read-only metadata entry point; never creates a display or changes its mode. */
public final class DisplayCapabilities {
    private DisplayCapabilities() { }

    public static void main(String... args) {
        PrintStream output = new PrintStream(new FileOutputStream(FileDescriptor.out));
        int status = 0;
        try {
            Looper.prepareMainLooper();
            Workarounds.apply();
            DisplayManager manager = (DisplayManager) FakeContext.get().getSystemService(Context.DISPLAY_SERVICE);
            // The default display is the device screen. Do not include scrcpy virtual displays.
            Display display = manager.getDisplay(Display.DEFAULT_DISPLAY);
            if (display == null || !display.isValid() || Build.VERSION.SDK_INT < 23) {
                throw new IllegalStateException("Physical display modes unavailable");
            }
            JSONArray modes = new JSONArray();
            for (Display.Mode mode : display.getSupportedModes()) {
                modes.put(new JSONObject().put("width", mode.getPhysicalWidth()).put("height", mode.getPhysicalHeight())
                        .put("refreshHz", mode.getRefreshRate()));
            }
            JSONObject result = new JSONObject().put("version", 1).put("serial", AppCatalog.serial())
                    .put("sdk", Build.VERSION.SDK_INT).put("displayId", display.getDisplayId()).put("modes", modes);
            output.println("SCEZ_DISPLAY_CAPS:" + result);
        } catch (Throwable error) {
            error.printStackTrace(output);
            status = 1;
        } finally {
            output.flush();
            System.exit(status);
        }
    }
}
