package com.genymobile.scrcpy.device;

import org.junit.Test;
import static org.junit.Assert.*;

public class IconDimensionsTest {
    @Test public void preservesSmallIcons() { assertArrayEquals(new int[]{48, 48}, IconDimensions.fit(48, 48, 144)); }
    @Test public void boundsHugeVendorDimensions() {
        assertArrayEquals(new int[]{144, 72}, IconDimensions.fit(Integer.MAX_VALUE, Integer.MAX_VALUE / 2, 144));
        assertArrayEquals(new int[]{72, 144}, IconDimensions.fit(5000, 10000, 144));
    }
    @Test public void handlesInvalidDimensionsAndKeepsNonzeroSize() {
        assertArrayEquals(new int[]{1, 1}, IconDimensions.fit(-1, 0, 144));
        assertArrayEquals(new int[]{144, 1}, IconDimensions.fit(Integer.MAX_VALUE, 1, 144));
    }
}
