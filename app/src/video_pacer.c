#include "video_pacer.h"

#include <stdlib.h>
#include <string.h>

static int64_t
minimum(int64_t a, int64_t b) {
    return a < b ? a : b;
}
static int64_t
maximum(int64_t a, int64_t b) {
    return a > b ? a : b;
}
static int
compare(const void *a, const void *b) {
    int64_t x = *(const int64_t *) a, y = *(const int64_t *) b;
    return (x > y) - (x < y);
}

void
sc_video_pacer_init(struct sc_video_pacer *p, unsigned max_frames) {
    memset(p, 0, sizeof(*p));
    p->max_frames = max_frames == 1 ? 1 : 2;
    p->period = 16667;
}

void
sc_video_pacer_update(struct sc_video_pacer *p, int64_t pts, int64_t recv,
                      int64_t decoded) {
    int64_t dt = p->initialized ? recv - p->last_recv : 0;
    if (p->initialized && (pts <= p->last_pts || dt < 0 || dt > 250000)) {
        unsigned resets = p->resets + 1;
        sc_video_pacer_init(p, p->max_frames);
        p->resets = resets;
        dt = 0;
    }
    if (!p->initialized) {
        p->initialized = true;
        p->offset = recv - pts;
    } else {
        int64_t period = pts - p->last_pts;
        if (period >= 4000 && period <= 100000) {
            p->periods[p->period_write++ % 8] = period;
            if (p->period_count < 8) ++p->period_count;
            int64_t sorted[8];
            memcpy(sorted, p->periods, p->period_count * sizeof(*sorted));
            qsort(sorted, p->period_count, sizeof(*sorted), compare);
            p->period = sorted[p->period_count / 2];
        }
    }
    p->last_pts = pts;
    p->last_recv = recv;
    p->samples[p->write++ % SC_VIDEO_PACER_SAMPLES] = (struct sc_video_pacer_sample) {
        .recv = recv, .transit = recv - pts,
        .decode = maximum(0, decoded - recv),
    };
    if (p->count < SC_VIDEO_PACER_SAMPLES) ++p->count;

    int64_t transit[SC_VIDEO_PACER_SAMPLES], decode[SC_VIDEO_PACER_SAMPLES];
    unsigned n = 0;
    for (unsigned i = 0; i < p->count; ++i) {
        if (recv - p->samples[i].recv <= 2000000) {
            transit[n] = p->samples[i].transit;
            decode[n++] = p->samples[i].decode;
        }
    }
    qsort(transit, n, sizeof(*transit), compare);
    qsort(decode, n, sizeof(*decode), compare);
    int64_t base = transit[(n - 1) / 10];
    // Startup may deliver a burst of old frames. Converge downward promptly;
    // only upward movement is restricted to 0.5 ms/s so sustained backlog
    // does not immediately redefine the clock.
    if (p->count < 8) p->offset = base;
    else {
        int64_t up = maximum(1, dt / 2000);
        p->offset += minimum(up, maximum(-2000, base - p->offset));
    }
    int64_t cap = minimum(32000, p->max_frames * p->period);
    int64_t goal = transit[(n - 1) * 95 / 100] - p->offset
                 + decode[(n - 1) * 90 / 100] + 500;
    goal = minimum(cap, maximum(1000, goal));
    if (goal > p->delay + 250) {
        p->delay = minimum(goal, p->delay + 2000);
        p->lower_since = 0;
    } else if (goal < p->delay - 1000) {
        if (!p->lower_since) p->lower_since = recv;
        if (recv - p->lower_since >= 2000000) {
            p->delay = maximum(goal, p->delay - maximum(1, dt / 200));
        }
    } else {
        p->lower_since = 0;
    }
    p->delay = minimum(p->delay, cap);
}

int64_t
sc_video_pacer_deadline(const struct sc_video_pacer *p, int64_t pts) {
    return pts + p->offset + p->delay;
}

int64_t
sc_video_pacer_bounded_deadline(const struct sc_video_pacer *p, int64_t pts,
                                 int64_t recv) {
    // Bound age from reception, including time already spent in queues.
    return minimum(sc_video_pacer_deadline(p, pts),
                   recv + minimum(32000, p->max_frames * p->period));
}
