#include "frame_trace.h"

#include <inttypes.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <SDL3/SDL.h>
#ifdef _WIN32
#include <windows.h>
#include <io.h>
#include <fcntl.h>
#else
#include <unistd.h>
#endif

#include "options.h"
#include "util/env.h"
#include "util/file.h"
#include "util/log.h"
#include "util/str.h"
#include "util/thread.h"

#ifndef SC_FRAME_TRACE_CAPACITY
#define SC_FRAME_TRACE_CAPACITY 32768
#endif

struct sc_trace_event {
    int64_t mono_us;
    int64_t pts_us;
    int64_t aux;
    uint64_t seq;
    uint64_t packet_id;
    uint32_t epoch;
    uint32_t stage;
    uint32_t depth;
    uint32_t flags;
};

_Static_assert(2 * SC_FRAME_TRACE_CAPACITY * sizeof(struct sc_trace_event)
               <= 8 * 1024 * 1024, "trace buffers exceed budget");

static const char *const stage_names[] = {
    "packet_recv", "decode_in", "decode_out", "regulator_enqueue",
    "regulator_release", "frame_push", "frame_replace", "frame_consume",
    "texture_begin", "texture_end", "present_begin", "present_end",
    "pause", "abr", "stream_reset", "display_state",
    "decode_enqueue", "decode_dequeue", "decode_drop", "decode_config",
    "pacing_state", "pacing_drop", "pacing_clock_residual",
};

bool sc_frame_trace_enabled;
static struct {
    SDL_Mutex *mutex;
    sc_thread thread;
    FILE *file;
    struct sc_trace_event *queue;
    struct sc_trace_event *batch;
    size_t count;
    uint64_t seq;
    atomic_bool stopped;
    atomic_bool io_failed;
    atomic_uint_fast64_t dropped;
    atomic_uint_fast64_t counts[SC_TRACE_STAGE_COUNT];
    char session[64];
    unsigned long pid;
} trace;

static void
write_string(FILE *file, const char *value) {
    fputc('"', file);
    if (value) {
        for (const unsigned char *p = (const unsigned char *) value; *p; ++p) {
            if (*p == '"' || *p == '\\') {
                fputc('\\', file);
            }
            if (*p < 0x20) {
                fprintf(file, "\\u%04x", *p);
            } else {
                fputc(*p, file);
            }
        }
    }
    fputc('"', file);
}

static int
run_trace(void *userdata) {
    (void) userdata;
    for (;;) {
        SDL_LockMutex(trace.mutex);
        size_t count = trace.count;
        memcpy(trace.batch, trace.queue, count * sizeof(*trace.batch));
        trace.count = 0;
        bool stopped = atomic_load(&trace.stopped);
        SDL_UnlockMutex(trace.mutex);
        for (size_t i = 0; i < count; ++i) {
            struct sc_trace_event *e = &trace.batch[i];
            fprintf(trace.file, "{\"schema\":1,\"session\":\"%s\",\"pid\":%lu,"
                    "\"stage\":\"%s\",\"mono_us\":%" PRId64 ","
                    "\"pts_us\":%" PRId64 ",\"seq\":%" PRIu64 ","
                    "\"packet_id\":%" PRIu64 ",\"epoch\":%" PRIu32 ","
                    "\"queue_depth\":%" PRIu32 ",\"aux\":%" PRId64 ","
                    "\"flags\":%" PRIu32 "}\n",
                    trace.session, trace.pid, stage_names[e->stage], e->mono_us,
                    e->pts_us, e->seq, e->packet_id, e->epoch, e->depth,
                    e->aux, e->flags);
        }
        if (fflush(trace.file) || ferror(trace.file)) {
            atomic_store(&trace.io_failed, true);
            LOGW("Frame trace write failed; diagnostic events are discarded");
            return 0;
        }
        if (stopped) {
            break;
        }
        SDL_Delay(100);
    }
    fprintf(trace.file, "{\"schema\":1,\"stage\":\"summary\","
            "\"session\":\"%s\",\"trace_dropped\":%" PRIuFAST64 ",\"counts\":{",
            trace.session, atomic_load(&trace.dropped));
    for (unsigned i = 0; i < SC_TRACE_STAGE_COUNT; ++i) {
        fprintf(trace.file, "%s\"%s\":%" PRIuFAST64, i ? "," : "",
                stage_names[i], atomic_load(&trace.counts[i]));
    }
    fputs("}}\n", trace.file);
    return 0;
}

void
sc_frame_trace_init(const struct scrcpy_options *options) {
    char *dir = sc_get_env("SCEZ_FRAME_TRACE_DIR");
    if (!dir || !*dir) {
        free(dir);
        return;
    }
    memset(&trace, 0, sizeof(trace));
    atomic_init(&trace.stopped, false);
    atomic_init(&trace.io_failed, false);
    atomic_init(&trace.dropped, 0);
    for (unsigned i = 0; i < SC_TRACE_STAGE_COUNT; ++i) {
        atomic_init(&trace.counts[i], 0);
    }
#ifdef _WIN32
    trace.pid = GetCurrentProcessId();
#else
    trace.pid = getpid();
#endif
    sc_tick now = sc_tick_now();
    snprintf(trace.session, sizeof(trace.session), "%lu-%" PRId64,
             trace.pid, now);
    char filename[100];
    snprintf(filename, sizeof(filename), "frame-trace-%s.jsonl", trace.session);
    char *path = sc_file_build_path(dir, filename);
    free(dir);
    if (!path) {
        return;
    }
#ifdef _WIN32
    wchar_t *wide = sc_str_to_wchars(path);
    HANDLE handle = wide ? CreateFileW(wide, GENERIC_WRITE, FILE_SHARE_READ,
                                      NULL, CREATE_NEW, FILE_ATTRIBUTE_NORMAL,
                                      NULL) : INVALID_HANDLE_VALUE;
    if (handle != INVALID_HANDLE_VALUE) {
        int fd = _open_osfhandle((intptr_t) handle, _O_WRONLY | _O_BINARY);
        if (fd >= 0) {
            trace.file = _fdopen(fd, "wb");
            if (!trace.file) {
                _close(fd);
            }
        } else {
            CloseHandle(handle);
        }
    }
    free(wide);
#else
    trace.file = fopen(path, "wx");
#endif
    if (!trace.file) {
        LOGW("Could not create frame trace; continuing without diagnostics");
        free(path);
        return;
    }
    trace.queue = calloc(SC_FRAME_TRACE_CAPACITY, sizeof(*trace.queue));
    trace.batch = calloc(SC_FRAME_TRACE_CAPACITY, sizeof(*trace.batch));
    trace.mutex = SDL_CreateMutex();
    if (!trace.queue || !trace.batch || !trace.mutex) {
        goto failed;
    }
    fprintf(trace.file, "{\"schema\":1,\"stage\":\"metadata\",\"session\":\"%s\","
            "\"pid\":%lu,\"mono_us\":%" PRId64 ",\"wall_unix_s\":%lld,"
            "\"protocol\":\"%s\",\"source_revision\":\"%s\","
            "\"measurement_scope\":\"PC pipeline; Present API is not scanout\","
            "\"phone_counter_kind\":\"unknown\",\"phone_mode\":\"unknown\","
            "\"connection\":\"unknown\",\"vrr\":\"unknown\","
            "\"buffer_us\":%" PRItick ",\"lock_fps\":%s,\"lock_bitrate\":%s,"
            "\"virtual_display\":%s,\"bitrate\":%" PRIu32 ",\"max_fps\":",
            trace.session, trace.pid, now, (long long) time(NULL),
            SCRCPY_VERSION, SC_SOURCE_REVISION, options->video_buffer,
            options->abr_lock_fps ? "true" : "false",
            options->abr_lock_bitrate ? "true" : "false",
            options->new_display ? "true" : "false", options->video_bit_rate);
    write_string(trace.file, options->max_fps ? options->max_fps : "unlimited");
    fputs("}\n", trace.file);
    if (fflush(trace.file) || !sc_thread_create(&trace.thread, run_trace,
                                               "scrcpy-trace", NULL)) {
        goto failed;
    }
    sc_frame_trace_enabled = true;
    LOGI("Frame trace: %s", path);
    free(path);
    return;
failed:
    LOGW("Could not start frame trace; continuing without diagnostics");
    if (trace.mutex) {
        SDL_DestroyMutex(trace.mutex);
    }
    free(trace.batch);
    free(trace.queue);
    fclose(trace.file);
    free(path);
}

void
sc_frame_trace_emit(enum sc_frame_trace_stage stage, const AVBufferRef *ref,
                    int64_t pts, uint32_t depth, int64_t aux, uint32_t flags) {
    if (!sc_frame_trace_enabled) {
        return;
    }
    atomic_fetch_add_explicit(&trace.counts[stage], 1, memory_order_relaxed);
    if (atomic_load_explicit(&trace.io_failed, memory_order_relaxed)) {
        atomic_fetch_add_explicit(&trace.dropped, 1, memory_order_relaxed);
        return;
    }
    sc_tick now = sc_tick_now();
    if (!SDL_TryLockMutex(trace.mutex)) {
        atomic_fetch_add_explicit(&trace.dropped, 1, memory_order_relaxed);
        return;
    }
    if (trace.count == SC_FRAME_TRACE_CAPACITY) {
        atomic_fetch_add_explicit(&trace.dropped, 1, memory_order_relaxed);
    } else {
        struct sc_frame_trace_meta meta = {0};
        if (ref && ref->size == sizeof(meta)) {
            memcpy(&meta, ref->data, sizeof(meta));
        }
        trace.queue[trace.count++] = (struct sc_trace_event) {
            .mono_us = now, .pts_us = pts, .aux = aux, .seq = ++trace.seq,
            .packet_id = meta.packet_id, .epoch = meta.epoch,
            .stage = stage, .depth = depth, .flags = flags,
        };
    }
    SDL_UnlockMutex(trace.mutex);
}

void
sc_frame_trace_destroy(void) {
    // All pipeline producers must already be joined.
    if (!sc_frame_trace_enabled) {
        return;
    }
    atomic_store(&trace.stopped, true);
    sc_thread_join(&trace.thread, NULL);
    if (atomic_load(&trace.io_failed)) {
        LOGW("Frame trace incomplete (disk error)");
    }
    fclose(trace.file);
    SDL_DestroyMutex(trace.mutex);
    free(trace.queue);
    free(trace.batch);
    sc_frame_trace_enabled = false;
}
