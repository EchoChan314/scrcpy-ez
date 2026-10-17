package com.genymobile.scrcpy.device;

/** Bound allocation before drawing vendor vector/adaptive icon resources. */
public final class IconDimensions {
    private IconDimensions() { }
    public static int[] fit(int width, int height, int limit) {
        width = Math.max(1, width);
        height = Math.max(1, height);
        limit = Math.max(1, limit);
        double scale = Math.min(1.0, (double) limit / Math.max(width, height));
        return new int[]{Math.max(1, (int) Math.round(width * scale)), Math.max(1, (int) Math.round(height * scale))};
    }
}
