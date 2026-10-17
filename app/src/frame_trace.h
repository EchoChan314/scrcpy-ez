#ifndef SC_FRAME_TRACE_H
#define SC_FRAME_TRACE_H

#include <stdbool.h>
#include <stdint.h>
#include <libavutil/buffer.h>
#include <libavutil/avutil.h>
#include "util/tick.h"

struct scrcpy_options;

// recv_date remains first for the existing video regulator clock mapping.
struct sc_frame_trace_meta {
    sc_tick recv_date;
    uint64_t packet_id;
    uint32_t epoch;
};

enum sc_frame_trace_stage {
    SC_TRACE_PACKET_RECV,
    SC_TRACE_DECODE_IN,
    SC_TRACE_DECODE_OUT,
    SC_TRACE_REGULATOR_ENQUEUE,
    SC_TRACE_REGULATOR_RELEASE,
    SC_TRACE_FRAME_PUSH,
    SC_TRACE_FRAME_REPLACE,
    SC_TRACE_FRAME_CONSUME,
    SC_TRACE_TEXTURE_BEGIN,
    SC_TRACE_TEXTURE_END,
    SC_TRACE_PRESENT_BEGIN,
    SC_TRACE_PRESENT_END,
    SC_TRACE_PAUSE,
    SC_TRACE_ABR,
    SC_TRACE_STREAM_RESET,
    SC_TRACE_DISPLAY_STATE,
    SC_TRACE_DECODE_ENQUEUE,
    SC_TRACE_DECODE_DEQUEUE,
    SC_TRACE_DECODE_DROP,
    SC_TRACE_DECODE_CONFIG,
    SC_TRACE_PACING_STATE,
    SC_TRACE_PACING_DROP,
    SC_TRACE_PACING_CLOCK,
    SC_TRACE_STAGE_COUNT,
};

// Immutable while the pipeline threads are running. Disabled by default.
extern bool sc_frame_trace_enabled;

// SCEZ_FRAME_TRACE_DIR explicitly enables a session in an existing directory.
// Failure is diagnostic-only and must never prevent playback.
void sc_frame_trace_init(const struct scrcpy_options *options);
void sc_frame_trace_destroy(void);

// No allocations, formatting, disk I/O or blocking lock on the producer.
// aux: replaced/new PTS, deadline, present_id, or state value per stage.
// flags: config/key packet bits; texture success; present new-frame reason.
void sc_frame_trace_emit(enum sc_frame_trace_stage stage, const AVBufferRef *ref,
                         int64_t pts, uint32_t depth, int64_t aux,
                         uint32_t flags);

#define SC_TRACE(STAGE, REF, PTS, DEPTH, AUX, FLAGS) do { \
    if (sc_frame_trace_enabled) { \
        sc_frame_trace_emit(STAGE, REF, PTS, DEPTH, AUX, FLAGS); \
    } \
} while (0)

#endif
