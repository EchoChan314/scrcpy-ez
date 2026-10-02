package com.genymobile.scrcpy.control;

import com.genymobile.scrcpy.util.Ln;

import java.io.IOException;
import java.util.Iterator;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;

public final class DeviceMessageSender {

    private final ControlChannel controlChannel;

    private Thread thread;
    private final BlockingQueue<DeviceMessage> queue = new LinkedBlockingQueue<>();

    public DeviceMessageSender(ControlChannel controlChannel) {
        this.controlChannel = controlChannel;
    }

    public synchronized void send(DeviceMessage msg) {
        int type = msg.getType();
        if (isClipboard(type)) {
            // Keep only the newest waiting snapshot, without dropping ACKs.
            // Iterator.remove() also works before Android API 24, where the
            // Collection.removeIf() default method is unavailable.
            Iterator<DeviceMessage> iterator = queue.iterator();
            while (iterator.hasNext()) {
                if (isClipboard(iterator.next().getType())) {
                    iterator.remove();
                }
            }
        } else if (type == DeviceMessage.TYPE_UHID_OUTPUT && queue.size() >= 64) {
            Ln.w("Device message dropped: " + type);
            return;
        }
        queue.offer(msg);
    }

    private static boolean isClipboard(int type) {
        return type == DeviceMessage.TYPE_CLIPBOARD || type == DeviceMessage.TYPE_IMAGE_CLIPBOARD
                || type == DeviceMessage.TYPE_CLIPBOARD_SNAPSHOT || type == DeviceMessage.TYPE_IMAGE_CLIPBOARD_SNAPSHOT;
    }

    private void loop() throws IOException, InterruptedException {
        while (!Thread.currentThread().isInterrupted()) {
            DeviceMessage msg = queue.take();
            controlChannel.send(msg);
        }
    }

    public void start() {
        thread = new Thread(() -> {
            try {
                loop();
            } catch (IOException | InterruptedException e) {
                // this is expected on close
            } finally {
                Ln.d("Device message sender stopped");
            }
        }, "control-send");
        thread.start();
    }

    public void stop() {
        if (thread != null) {
            thread.interrupt();
        }
    }

    public void join() throws InterruptedException {
        if (thread != null) {
            thread.join();
        }
    }
}
