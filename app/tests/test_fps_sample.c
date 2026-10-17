#include <assert.h>
#include "util/fps_sample.h"

int main(void) {
    struct sc_fps_sample s;
    sc_fps_sample_reset(&s);
    assert(!sc_fps_sample_update(&s, SC_TICK_FROM_SEC(20)));
    sc_fps_sample_add(&s, 0);
    for (int i = 1; i <= 72; ++i) {
        sc_fps_sample_add(&s, i * SC_TICK_FROM_MS(600) / 72);
    }
    // Delayed 600ms window: the old fixed multiplier would report 144.
    assert(sc_fps_sample_update(&s, SC_TICK_FROM_MS(600)));
    assert(s.fps == 120);
    assert(!sc_fps_sample_update(&s, SC_TICK_FROM_MS(700)));
    // Idle refresh happens without any new frame.
    assert(sc_fps_sample_update(&s, SC_TICK_FROM_MS(1600)));
    assert(s.fps == 0 && !s.active);
    sc_fps_sample_add(&s, SC_TICK_FROM_SEC(30));
    assert(s.fps == 0);
    for (int i = 1; i <= 30; ++i) {
        sc_fps_sample_add(&s, SC_TICK_FROM_SEC(30)
                              + i * SC_TICK_FROM_MS(500) / 30);
    }
    assert(sc_fps_sample_update(&s, SC_TICK_FROM_MS(30500)));
    assert(s.fps == 60);
    // Explicit pause/resume discards the old measurement window.
    sc_fps_sample_reset(&s);
    sc_fps_sample_add(&s, SC_TICK_FROM_SEC(40));
    for (int i = 1; i <= 60; ++i) {
        sc_fps_sample_add(&s, SC_TICK_FROM_SEC(40)
                              + i * SC_TICK_FROM_MS(500) / 60);
    }
    assert(sc_fps_sample_update(&s, SC_TICK_FROM_MS(40500)));
    assert(s.fps == 120);
    // Short stall is included in elapsed time, rather than hidden by *2.
    sc_fps_sample_add(&s, SC_TICK_FROM_MS(40600));
    assert(sc_fps_sample_update(&s, SC_TICK_FROM_MS(41100)));
    assert(s.fps == 2);
    return 0;
}
