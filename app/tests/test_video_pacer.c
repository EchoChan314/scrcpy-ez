#include "video_pacer.h"
#include <assert.h>
#include <stdio.h>

static void feed(struct sc_video_pacer *p, int64_t *pts, int n, int64_t period,
                  bool jitter) {
    for (int i = 0; i < n; ++i) {
        *pts += period;
        // Bounded variations stay below a source interval: receive order holds.
        int64_t noise = jitter ? (i % 10 == 9 ? 10000 : (i % 10 == 6 ? 7000 : 0))
                               : i % 3 * 100;
        int64_t recv = *pts + 1000000 + noise;
        sc_video_pacer_update(p, *pts, recv, recv + 300);
        assert(p->delay >= 0 && p->delay <= 32000);
        assert(p->delay <= p->max_frames * p->period);
    }
}

int main(void) {
    struct sc_video_pacer p;
    int64_t pts = 0;
    sc_video_pacer_init(&p, 2);
    feed(&p, &pts, 300, 16667, false);
    assert(p.delay < 2000);
    int64_t calm = p.delay;
    feed(&p, &pts, 240, 16667, true);
    assert(p.delay > calm + 6000);
    feed(&p, &pts, 540, 16667, false);
    assert(p.delay < 2500);
    feed(&p, &pts, 12, 8333, false);
    assert(p.period >= 8000 && p.period <= 9000);
    assert(p.delay <= 18000);
    unsigned resets = p.resets;
    sc_video_pacer_update(&p, 1000, pts + 2000000, pts + 2000300);
    assert(p.resets == resets + 1);
    assert(sc_video_pacer_deadline(&p, 1000) >= p.last_recv);
    assert(sc_video_pacer_deadline(&p, 1000) - p.last_recv <= 32000);
    sc_video_pacer_update(&p, 2000, p.last_recv + 500000, p.last_recv + 500300);
    assert(p.resets == resets + 2);
    sc_video_pacer_init(&p, 2);
    // Twelve queued old frames arrive as a burst, then delivery becomes live.
    for (int i = 0; i < 180; ++i) {
        int64_t stamp = (i + 1) * 16667;
        int64_t received = i < 12 ? 1000000 + i * 500
                                 : 1006000 + (i - 12) * 16667;
        sc_video_pacer_update(&p, stamp, received, received + 300);
        assert(sc_video_pacer_bounded_deadline(&p, stamp, received) <= received + 32000);
        if (i == 179) assert(p.offset - (received - stamp) < 2000);
    }
    p.offset += 500000;
    assert(sc_video_pacer_bounded_deadline(&p, p.last_pts, p.last_recv) <= p.last_recv + 32000);
    for (unsigned frames = 1; frames <= 2; ++frames) {
        const int64_t periods[] = {33333, 16667, 8333, 6944};
        for (unsigned rate = 0; rate < 4; ++rate) {
            sc_video_pacer_init(&p, frames);
            for (unsigned i = 0; i < 100; ++i) {
                int64_t stamp = 1000000 + i * periods[rate];
                int64_t recv = stamp + 2000000 + i % 3 * 500;
                sc_video_pacer_update(&p, stamp, recv, recv + 40000);
                int64_t cap = frames * p.period;
                if (cap > 32000) cap = 32000;
                assert(p.delay <= cap);
                assert(sc_video_pacer_bounded_deadline(&p, stamp, recv) <= recv + cap);
            }
            // PTS/idle resets retain the selected one/two-frame budget.
            sc_video_pacer_update(&p, 1, p.last_recv + 500000, p.last_recv + 500300);
            assert(p.max_frames == frames);
        }
    }
    puts("PASS timing, jitter, resets, queue bounds, one/two-frame budgets at 30/60/120/144fps");
    return 0;
}
