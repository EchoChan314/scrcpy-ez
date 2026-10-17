#include "fps_sample.h"

#include <limits.h>

#define SAMPLE_PERIOD SC_TICK_FROM_MS(500)
#define IDLE_PERIOD SC_TICK_FROM_SEC(1)

void
sc_fps_sample_reset(struct sc_fps_sample *sample) {
    *sample = (struct sc_fps_sample) {0};
}

void
sc_fps_sample_add(struct sc_fps_sample *sample, sc_tick now) {
    if (!sample->active || now - sample->last_frame >= IDLE_PERIOD) {
        // The first frame establishes an interval endpoint, not an interval.
        sc_fps_sample_reset(sample);
        sample->start = now;
        sample->active = true;
    } else {
        ++sample->count;
    }
    sample->last_frame = now;
}

bool
sc_fps_sample_update(struct sc_fps_sample *sample, sc_tick now) {
    unsigned previous = sample->fps;
    if (!sample->active) {
        return false;
    }
    if (now - sample->last_frame >= IDLE_PERIOD) {
        sc_fps_sample_reset(sample);
    } else {
        sc_tick elapsed = now - sample->start;
        if (elapsed < SAMPLE_PERIOD) {
            return false;
        }
        // Round only the displayed rate; never round the source timeline.
        double rate = (double) sample->count * SC_TICK_FREQ / elapsed;
        sample->fps = rate >= UINT_MAX ? UINT_MAX : (unsigned) (rate + 0.5);
        sample->count = 0;
        sample->start = now;
    }
    return sample->fps != previous;
}
