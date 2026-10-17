#include "common.h"
#include <assert.h>
#include <SDL3/SDL.h>
#include <stdlib.h>
#include "frame_trace.h"
#include "options.h"
#include "util/thread.h"
#include "util/env.h"
#include "util/str.h"

static void
set_trace_dir(const char *dir) {
#ifdef _WIN32
    wchar_t *wide = sc_str_to_wchars(dir ? dir : "");
    assert(wide);
    assert(!_wputenv_s(L"SCEZ_FRAME_TRACE_DIR", wide));
    free(wide);
#else
    if (dir) setenv("SCEZ_FRAME_TRACE_DIR", dir, 1);
    else unsetenv("SCEZ_FRAME_TRACE_DIR");
#endif
}

static int
produce(void *userdata) {
    (void) userdata;
    for (unsigned i = 0; i < 10000; ++i) {
        SC_TRACE(SC_TRACE_FRAME_CONSUME, NULL, i * 8333, 0, 0, 0);
    }
    return 0;
}

int main(void) {
    struct scrcpy_options options = {0};
    assert(SDL_Init(0));
    char *dir = sc_get_env("SCEZ_TRACE_TEST_DIR");
    assert(dir && *dir); // runner supplies an isolated, possibly Unicode path
    set_trace_dir(NULL);
    sc_frame_trace_init(&options);
    assert(!sc_frame_trace_enabled);
    SC_TRACE(SC_TRACE_FRAME_CONSUME, NULL, 0, 0, 0, 0);
    sc_frame_trace_destroy();
    set_trace_dir(dir);
    sc_frame_trace_init(&options);
    assert(sc_frame_trace_enabled);
    sc_thread threads[3];
    for (unsigned i = 0; i < 3; ++i) {
        assert(sc_thread_create(&threads[i], produce, "trace-test", NULL));
    }
    for (unsigned i = 0; i < 3; ++i) {
        sc_thread_join(&threads[i], NULL);
    }
    sc_frame_trace_destroy();
    assert(!sc_frame_trace_enabled);
    set_trace_dir("/no-such-ez-test-directory/trace");
    sc_frame_trace_init(&options);
    assert(!sc_frame_trace_enabled);
    sc_frame_trace_destroy();
    free(dir);
    SDL_Quit();
    return 0;
}
