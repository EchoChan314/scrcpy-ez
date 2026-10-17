#include "video_regulator.h"

#include <assert.h>
#include <stdatomic.h>
#include <stdlib.h>

struct observer {
    struct sc_frame_sink sink;
    sc_mutex mutex;
    sc_cond cond;
    unsigned width;
    unsigned frames;
    unsigned barriers;
};

static atomic_int released;

static void
free_timestamp(void *opaque, uint8_t *data) {
    (void) opaque;
    free(data);
    atomic_fetch_add(&released, 1);
}

static bool
observer_open(struct sc_frame_sink *sink, const AVCodecContext *ctx,
              const struct sc_stream_session *session) {
    (void) ctx;
    struct observer *o = container_of(sink, struct observer, sink);
    o->width = session->video.width;
    o->frames = o->barriers = 0;
    return true;
}

static void
observer_close(struct sc_frame_sink *sink) {
    (void) sink;
}

static enum sc_sink_result
observer_push(struct sc_frame_sink *sink, const AVFrame *frame) {
    struct observer *o = container_of(sink, struct observer, sink);
    sc_mutex_lock(&o->mutex);
    // A frame from a different epoch must never overtake its session barrier.
    assert((unsigned) frame->width == o->width);
    ++o->frames;
    sc_cond_signal(&o->cond);
    sc_mutex_unlock(&o->mutex);
    return SC_SINK_OK;
}

static enum sc_sink_result
observer_session(struct sc_frame_sink *sink,
                 const struct sc_stream_session *session) {
    struct observer *o = container_of(sink, struct observer, sink);
    sc_mutex_lock(&o->mutex);
    assert(o->frames > 0);
    ++o->barriers;
    o->width = session->video.width;
    o->frames = 0;
    sc_cond_signal(&o->cond);
    sc_mutex_unlock(&o->mutex);
    return SC_SINK_OK;
}

static void
push_frame(struct sc_video_regulator *vr, int width, int64_t pts) {
    AVFrame *frame = av_frame_alloc();
    assert(frame);
    frame->format = AV_PIX_FMT_GRAY8;
    frame->width = width;
    frame->height = 8;
    frame->pts = pts;
    assert(!av_frame_get_buffer(frame, 0));
    sc_tick *received = malloc(sizeof(*received));
    assert(received);
    *received = sc_tick_now();
    frame->opaque_ref = av_buffer_create((uint8_t *) received,
                                         sizeof(*received), free_timestamp,
                                         NULL, 0);
    assert(frame->opaque_ref);
    assert(vr->frame_sink.ops->push(&vr->frame_sink, frame) == SC_SINK_OK);
    av_frame_free(&frame);
}

static void
test_sessions_and_shutdown(bool automatic, unsigned frames) {
    static const struct sc_frame_sink_ops ops = {
        .open = observer_open,
        .close = observer_close,
        .push = observer_push,
        .push_session = observer_session,
    };
    struct observer o = {.sink = {.ops = &ops}};
    assert(sc_mutex_init(&o.mutex));
    assert(sc_cond_init(&o.cond));
    struct sc_video_regulator vr;
    assert(sc_video_regulator_init(&vr, SC_TICK_FROM_MS(16), false, 1,
                                   automatic, frames));
    sc_frame_source_add_sink(&vr.frame_source, &o.sink);
    for (unsigned cycle = 0; cycle < 2; ++cycle) {
        int initial_released = atomic_load(&released);
        struct sc_stream_session session = {.video = {.width = 8, .height = 8}};
        assert(vr.frame_sink.ops->open(&vr.frame_sink, NULL, &session));
        assert(vr.pacer.max_frames == frames);
        for (unsigned i = 0; i < 12; ++i) {
            push_frame(&vr, 8, 1000000 + i * 16667);
        }
        session.video.width = 16;
        assert(vr.frame_sink.ops->push_session(&vr.frame_sink, &session)
                == SC_SINK_OK);
        // PTS resets while the previous epoch still has queued frames.
        for (unsigned i = 0; i < 12; ++i) {
            push_frame(&vr, 16, 1000 + i * 8333);
        }
        sc_mutex_lock(&o.mutex);
        sc_tick timeout = sc_tick_now() + SC_TICK_FROM_MS(2000);
        while (!o.barriers || !o.frames) {
            bool signaled = sc_cond_timedwait(&o.cond, &o.mutex, timeout);
            assert(signaled || (o.barriers && o.frames));
        }
        assert(o.barriers == 1);
        sc_mutex_unlock(&o.mutex);
        // Close interrupts pending waits and drains all AVFrame references.
        vr.frame_sink.ops->close(&vr.frame_sink);
        assert(atomic_load(&released) == initial_released + 24);
    }
    sc_video_regulator_destroy(&vr);
    sc_cond_destroy(&o.cond);
    sc_mutex_destroy(&o.mutex);
}

int main(void) {
    // Reproduce short waits against the QPC clock. OS millisecond timeout
    // rounding must not produce a false timeout before this deadline.
    sc_mutex mutex;
    sc_cond cond;
    assert(sc_mutex_init(&mutex));
    assert(sc_cond_init(&cond));
    sc_mutex_lock(&mutex);
    for (unsigned i = 0; i < 100; ++i) {
        sc_tick deadline = sc_tick_now() + 500;
        bool signaled = sc_cond_timedwait(&cond, &mutex, deadline);
        assert(signaled || sc_tick_now() >= deadline);
    }
    sc_mutex_unlock(&mutex);
    sc_cond_destroy(&cond);
    sc_mutex_destroy(&mutex);
    test_sessions_and_shutdown(false, 2);
    test_sessions_and_shutdown(true, 2);
    test_sessions_and_shutdown(true, 1);
    return 0;
}
