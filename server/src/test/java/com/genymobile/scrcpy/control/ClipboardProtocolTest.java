package com.genymobile.scrcpy.control;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.util.Arrays;

/** Paired with the native C serializer/parser; contains only synthetic data. */
public final class ClipboardProtocolTest {
    private ClipboardProtocolTest() {
    }

    public static void main(String[] args) throws Exception {
        File folder = new File(args[0]);
        for (boolean image : new boolean[] {false, true}) {
            try (FileInputStream input = new FileInputStream(new File(folder, image ? "control-image.bin" : "control-text.bin"))) {
                ControlMessage msg = new ControlMessageReader(input).read();
                assert msg.getClipboardEpoch() == 0x1122334455667788L;
                assert msg.getClipboardVersion() == (image ? 100 : 99);
                assert msg.getSequence() == 0x800000000000002aL;
                assert msg.getPaste() == !image;
                if (image) {
                    assert msg.getType() == ControlMessage.TYPE_SET_IMAGE_CLIPBOARD;
                    assert "image/png".equals(msg.getText());
                    assert Arrays.equals(msg.getData(), new byte[] {0, 1, 2, (byte) 255});
                } else {
                    assert msg.getType() == ControlMessage.TYPE_SET_CLIPBOARD;
                    assert "ABC\n中".equals(msg.getText());
                }
            }
            try (FileOutputStream output = new FileOutputStream(new File(folder, image ? "device-image.bin" : "device-text.bin"))) {
                DeviceMessage msg = image
                        ? DeviceMessage.createImageClipboard(new byte[] {0, 1, 2, (byte) 255}, "image/png", 0x1122334455667788L)
                        : DeviceMessage.createClipboard("ABC\n中", 0x1122334455667788L);
                new DeviceMessageWriter(output).write(msg);
            }
        }
        System.out.println("PASS: C-to-Java control messages and Java-to-C clipboard snapshots");
    }
}
