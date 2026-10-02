package com.genymobile.scrcpy.control;

public final class DeviceMessage {

    public static final int TYPE_CLIPBOARD = 0;
    public static final int TYPE_ACK_CLIPBOARD = 1;
    public static final int TYPE_UHID_OUTPUT = 2;
    public static final int TYPE_IMAGE_CLIPBOARD = 3;
    public static final int TYPE_ABR_STATE = 4;
    // scrcpy-ez: 设备端「停止投屏」按钮被点击（无 payload）
    public static final int TYPE_STOP_MIRRORING = 5;

    public static final int TYPE_CLIPBOARD_SNAPSHOT = 6;
    public static final int TYPE_IMAGE_CLIPBOARD_SNAPSHOT = 7;

    private int type;
    private String text;
    private String mimeType;
    private long sequence;
    private long clipboardRevision;
    private int id;
    private byte[] data;
    private int bitrate;
    private int abrFps;

    private DeviceMessage() {
    }

    public static DeviceMessage createClipboard(String text) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_CLIPBOARD;
        event.text = text;
        return event;
    }

    public static DeviceMessage createClipboard(String text, long revision) {
        DeviceMessage event = createClipboard(text);
        event.type = TYPE_CLIPBOARD_SNAPSHOT; // versioned clipboard snapshot, existing types unchanged
        event.clipboardRevision = revision;
        return event;
    }

    public static DeviceMessage createImageClipboard(byte[] data, String mime, long revision) {
        DeviceMessage event = createImageClipboard(data, mime);
        event.type = TYPE_IMAGE_CLIPBOARD_SNAPSHOT;
        event.clipboardRevision = revision;
        return event;
    }

    public long getClipboardRevision() {
        return clipboardRevision;
    }

    public static DeviceMessage createAckClipboard(long sequence) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_ACK_CLIPBOARD;
        event.sequence = sequence;
        return event;
    }

    public static DeviceMessage createUhidOutput(int id, byte[] data) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_UHID_OUTPUT;
        event.id = id;
        event.data = data;
        return event;
    }

    public static DeviceMessage createImageClipboard(byte[] imageData, String mimeType) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_IMAGE_CLIPBOARD;
        event.data = imageData;
        event.mimeType = mimeType;
        return event;
    }

    public static DeviceMessage createAbrState(int bitrate, int abrFps) {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_ABR_STATE;
        event.bitrate = bitrate;
        event.abrFps = abrFps;
        return event;
    }

    // scrcpy-ez: 用户在设备端通知里点了「停止投屏」
    public static DeviceMessage createStopMirroring() {
        DeviceMessage event = new DeviceMessage();
        event.type = TYPE_STOP_MIRRORING;
        return event;
    }

    public int getType() {
        return type;
    }

    public String getText() {
        return text;
    }

    public long getSequence() {
        return sequence;
    }

    public int getId() {
        return id;
    }

    public byte[] getData() {
        return data;
    }

    public int getBitrate() {
        return bitrate;
    }

    public int getAbrFps() {
        return abrFps;
    }

    public String getMimeType() {
        return mimeType;
    }
}
