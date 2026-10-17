#ifndef SC_VIDEO_PACER_H
#define SC_VIDEO_PACER_H

#include <stdbool.h>
#include <stdint.h>

// Experimental receiver timing only. Units are microseconds, not wall time.
#define SC_VIDEO_PACER_SAMPLES 256
struct sc_video_pacer_sample {
    int64_t recv, transit, decode;
};

struct sc_video_pacer {
    struct sc_video_pacer_sample samples[SC_VIDEO_PACER_SAMPLES];
    int64_t periods[8];
    unsigned count, write, period_count, period_write, resets;
    unsigned max_frames;
    int64_t offset, delay, period, last_pts, last_recv, lower_since;
    bool initialized;
};

void
sc_video_pacer_init(struct sc_video_pacer *p, unsigned max_frames);
void
sc_video_pacer_update(struct sc_video_pacer *p, int64_t pts, int64_t recv,
                      int64_t decoded);
int64_t
sc_video_pacer_deadline(const struct sc_video_pacer *p, int64_t pts);
int64_t
sc_video_pacer_bounded_deadline(const struct sc_video_pacer *p, int64_t pts,
                                 int64_t recv);

#endif
