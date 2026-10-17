#ifndef SC_FPS_SAMPLE_H
#define SC_FPS_SAMPLE_H

#include <stdbool.h>
#include <stdint.h>
#include "tick.h"

// Rate of new client frames, never redraws or display scans. All calls are
// made on the UI thread; the caller supplies a monotonic clock for testing.
struct sc_fps_sample {
    sc_tick start;
    sc_tick last_frame;
    uint64_t count;
    unsigned fps;
    bool active;
};

void sc_fps_sample_reset(struct sc_fps_sample *sample);
void sc_fps_sample_add(struct sc_fps_sample *sample, sc_tick now);
bool sc_fps_sample_update(struct sc_fps_sample *sample, sc_tick now);

#endif
