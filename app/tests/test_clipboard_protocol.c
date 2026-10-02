#include "common.h"

#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "control_msg.h"
#include "device_msg.h"

static void
write_message(const char *root, const char *name, struct sc_control_msg *msg) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/%s", root, name);
    size_t size;
    uint8_t *wire = sc_control_msg_serialize(msg, &size);
    assert(wire);
    FILE *f = fopen(path, "wb");
    assert(f && fwrite(wire, 1, size, f) == size);
    fclose(f);
    free(wire);
}

static void
read_message(const char *root, const char *name, bool image) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/%s", root, name);
    FILE *f = fopen(path, "rb");
    uint8_t wire[1024];
    assert(f);
    size_t size = fread(wire, 1, sizeof(wire), f);
    fclose(f);
    struct sc_device_msg msg;
    for (size_t length = 0; length < size; ++length) {
        assert(sc_device_msg_deserialize(wire, length, &msg) == 0);
    }
    assert(sc_device_msg_deserialize(wire, size, &msg) == (ssize_t) size);
    assert(msg.clipboard_revision == UINT64_C(0x1122334455667788));
    if (image) {
        assert(msg.type == DEVICE_MSG_TYPE_IMAGE_CLIPBOARD);
        assert(!strcmp(msg.image_clipboard.mimetype, "image/png"));
        const uint8_t data[] = {0, 1, 2, 255};
        assert(msg.image_clipboard.size == sizeof(data));
        assert(!memcmp(msg.image_clipboard.data, data, sizeof(data)));
    } else {
        assert(msg.type == DEVICE_MSG_TYPE_CLIPBOARD);
        assert(!strcmp(msg.clipboard.text, "ABC\n\xE4\xB8\xAD"));
    }
    sc_device_msg_destroy(&msg);
}

int
main(int argc, char **argv) {
    assert(argc == 3);
    if (!strcmp(argv[1], "write")) {
        struct sc_control_msg text = {
            .type = SC_CONTROL_MSG_TYPE_SET_CLIPBOARD_VERSIONED,
            .clipboard_epoch = UINT64_C(0x1122334455667788),
            .clipboard_version = 99,
            .set_clipboard = {.sequence = UINT64_C(0x800000000000002a),
                             .text = "ABC\n\xE4\xB8\xAD", .paste = true},
        };
        write_message(argv[2], "control-text.bin", &text);
        uint8_t data[] = {0, 1, 2, 255};
        struct sc_control_msg image = {
            .type = SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD_VERSIONED,
            .clipboard_epoch = text.clipboard_epoch,
            .clipboard_version = 100,
            .set_image_clipboard = {.sequence = text.set_clipboard.sequence,
                .mimetype = "image/png", .data = data, .size = sizeof(data),
                .paste = false},
        };
        write_message(argv[2], "control-image.bin", &image);
    } else {
        read_message(argv[2], "device-text.bin", false);
        read_message(argv[2], "device-image.bin", true);
    }
    puts("PASS: C clipboard protocol and all fragmented prefixes");
    return 0;
}
