#include "video_regulator.h"
#include "frame_trace.h"

#include <assert.h>
#include <stdlib.h>
#include <libavcodec/avcodec.h>

#include "util/log.h"

/** Downcast frame_sink to sc_video_regulator */
#define DOWNCAST(SINK) container_of(SINK, struct sc_video_regulator, frame_sink)

static sc_tick
sc_pacer_frame_deadline(const struct sc_video_regulator *vr,
                        const struct sc_delayed_packet *packet) {
    // A later session/PTS epoch must not retime already queued old frames.
    if (packet->pacing_generation != vr->pacing_generation) {
        return packet->pacing_deadline;
    }
    const AVFrame *frame = packet->frame;
    sc_tick recv = *(const sc_tick *) frame->opaque_ref->data;
    return sc_video_pacer_bounded_deadline(&vr->pacer,
                                          SC_TICK_FROM_US(frame->pts), recv);
}

static bool
sc_delayed_packet_init_frame(struct sc_delayed_packet *dpacket,
                             const AVFrame *frame) {
    dpacket->type = SC_DELAYED_PACKET_TYPE_FRAME;
    dpacket->frame = av_frame_alloc();
    if (!dpacket->frame) {
        LOG_OOM();
        return false;
    }

    if (av_frame_ref(dpacket->frame, frame)) {
        LOG_OOM();
        av_frame_free(&dpacket->frame);
        return false;
    }

    return true;
}

static void
sc_delayed_packet_init_session(struct sc_delayed_packet *dpacket,
                               const struct sc_stream_session *session) {
    dpacket->type = SC_DELAYED_PACKET_TYPE_SESSION;
    dpacket->session = *session;
}

static void
sc_delayed_packet_destroy(struct sc_delayed_packet *dpacket) {
    if (dpacket->type == SC_DELAYED_PACKET_TYPE_FRAME) {
        av_frame_unref(dpacket->frame);
        av_frame_free(&dpacket->frame);
    }
}

static int
run_buffering(void *data) {
    struct sc_video_regulator *vr = data;

    assert(vr->delay > 0);

    for (;;) {
        sc_mutex_lock(&vr->mutex);

        while (!vr->stopped && sc_vecdeque_is_empty(&vr->queue)) {
            sc_cond_wait(&vr->queue_cond, &vr->mutex);
        }

        if (vr->stopped) {
            sc_mutex_unlock(&vr->mutex);
            goto stopped;
        }

        struct sc_delayed_packet dpacket = sc_vecdeque_pop(&vr->queue);
        sc_cond_signal(&vr->backpressure_cond);

        enum sc_sink_result result;
        if (dpacket.type == SC_DELAYED_PACKET_TYPE_FRAME) {
            sc_tick max_deadline = sc_tick_now() + (vr->automatic ? SC_TICK_FROM_MS(32) : vr->delay);
            // PTS (written by the server) are expressed in microseconds
            sc_tick pts = SC_TICK_FROM_US(dpacket.frame->pts);

            bool timed_out = false;
            sc_tick deadline = max_deadline;
            while (!vr->stopped && !timed_out) {
                deadline = vr->automatic
                    ? sc_pacer_frame_deadline(vr, &dpacket)
                    : sc_clock_to_system_time(&vr->clock, pts) + vr->delay;
                if (deadline > max_deadline) {
                    deadline = max_deadline;
                }

                timed_out =
                    !sc_cond_timedwait(&vr->wait_cond, &vr->mutex, deadline);
            }

            // Shed obsolete decoded frames only when a newer frame is ready
            // and due. Never skip a session barrier or compressed references.
            bool obsolete = false;
            if (vr->automatic && !sc_vecdeque_is_empty(&vr->queue)) {
                struct sc_delayed_packet *next = &vr->queue.data[vr->queue.origin];
                obsolete = next->type == SC_DELAYED_PACKET_TYPE_FRAME
                    && next->pacing_generation == dpacket.pacing_generation
                    && next->frame->pts > dpacket.frame->pts
                    && sc_pacer_frame_deadline(vr, next) <= sc_tick_now();
            }
            if (obsolete && !vr->stopped) {
                SC_TRACE(SC_TRACE_PACING_DROP, dpacket.frame->opaque_ref,
                         dpacket.frame->pts, sc_vecdeque_size(&vr->queue),
                         sc_tick_now() - deadline, 0);
                sc_mutex_unlock(&vr->mutex);
                sc_delayed_packet_destroy(&dpacket);
                continue;
            }
            bool stopped = vr->stopped;
            sc_mutex_unlock(&vr->mutex);

            if (stopped) {
                sc_delayed_packet_destroy(&dpacket);
                goto stopped;
            }
            SC_TRACE(SC_TRACE_REGULATOR_RELEASE, dpacket.frame->opaque_ref,
                     dpacket.frame->pts, 0, deadline, 0);

#ifdef SC_BUFFERING_DEBUG
            LOGD("Buffering: %" PRItick ";%" PRItick ";%" PRItick,
                 pts, dframe.push_date, sc_tick_now());
#endif

            result = sc_frame_source_sinks_push(&vr->frame_source,
                                                dpacket.frame);
        } else {
            assert(dpacket.type == SC_DELAYED_PACKET_TYPE_SESSION);
            sc_mutex_unlock(&vr->mutex);
            result = sc_frame_source_sinks_push_session(&vr->frame_source,
                                                        &dpacket.session);
        }

        sc_delayed_packet_destroy(&dpacket);
        if (result != SC_SINK_OK) {
            if (result == SC_SINK_KO) {
                LOGE("Delayed packet could not be pushed, stopping");
            }
            sc_mutex_lock(&vr->mutex);
            // Prevent to push any new packet
            vr->stopped = true;
            sc_cond_signal(&vr->backpressure_cond);
            sc_mutex_unlock(&vr->mutex);
            goto stopped;
        }
    }

stopped:
    assert(vr->stopped);

    // Flush queue
    while (!sc_vecdeque_is_empty(&vr->queue)) {
        struct sc_delayed_packet *dpacket = sc_vecdeque_popref(&vr->queue);
        sc_delayed_packet_destroy(dpacket);
    }

    LOGD("Buffering thread ended");

    return 0;
}

static bool
sc_video_regulator_frame_sink_open(struct sc_frame_sink *sink,
                                   const AVCodecContext *ctx,
                                   const struct sc_stream_session *session) {
    struct sc_video_regulator *vr = DOWNCAST(sink);
    (void) ctx;
    (void) session;

    sc_clock_init(&vr->clock);
    sc_video_pacer_init(&vr->pacer, vr->automatic_frames);
    vr->pacer_last_trace = 0;
    vr->pacing_generation = 0;
    vr->stopped = false;
    sc_vecdeque_init(&vr->queue);

    if (!sc_frame_source_sinks_open(&vr->frame_source, ctx, session)) {
        return false;
    }

    bool ok = sc_thread_create(&vr->thread, run_buffering, "scrcpy-vruf", vr);
    if (!ok) {
        LOGE("Could not start buffering thread");
        sc_frame_source_sinks_close(&vr->frame_source);
        return false;
    }

    return true;
}

static void
sc_video_regulator_frame_sink_close(struct sc_frame_sink *sink) {
    struct sc_video_regulator *vr = DOWNCAST(sink);

    sc_video_regulator_stop(vr);
    sc_thread_join(&vr->thread, NULL);

    sc_frame_source_sinks_close(&vr->frame_source);

    sc_vecdeque_destroy(&vr->queue);
}

static enum sc_sink_result
sc_video_regulator_frame_sink_push(struct sc_frame_sink *sink,
                                   const AVFrame *frame) {
    struct sc_video_regulator *vr = DOWNCAST(sink);

    sc_mutex_lock(&vr->mutex);

    if (vr->stopped) {
        sc_mutex_unlock(&vr->mutex);
        return SC_SINK_STOPPED;
    }

    assert(frame->opaque_ref);
    assert(frame->opaque_ref->size >= sizeof(sc_tick));
    // Retrieve the recv date set on the AVPacket by the demuxer
    sc_tick recv_date = *(const sc_tick *) frame->opaque_ref->data;

    sc_tick pts = SC_TICK_FROM_US(frame->pts);
    sc_clock_update(&vr->clock, recv_date, pts);
    if (vr->automatic) {
        unsigned previous_resets = vr->pacer.resets;
        sc_video_pacer_update(&vr->pacer, pts, recv_date, sc_tick_now());
        if (vr->pacer.resets != previous_resets) {
            ++vr->pacing_generation;
        }
        if (recv_date - vr->pacer_last_trace >= SC_TICK_FROM_MS(250)) {
            SC_TRACE(SC_TRACE_PACING_STATE, frame->opaque_ref, frame->pts,
                     sc_vecdeque_size(&vr->queue), vr->pacer.delay,
                     (uint32_t) vr->pacer.period);
            SC_TRACE(SC_TRACE_PACING_CLOCK, frame->opaque_ref, frame->pts, 0,
                     pts + vr->pacer.offset - recv_date, 0);
            vr->pacer_last_trace = recv_date;
        }
    }
    sc_cond_signal(&vr->wait_cond);

    if (vr->first_frame_asap && vr->clock.range == 1) {
        SC_TRACE(SC_TRACE_REGULATOR_RELEASE, frame->opaque_ref, frame->pts,
                 0, sc_tick_now(), 1);
        sc_mutex_unlock(&vr->mutex);
        return sc_frame_source_sinks_push(&vr->frame_source, frame);
    }

    struct sc_delayed_packet *dpacket =
        sc_vecdeque_push_uninitialized(&vr->queue);
    if (!dpacket) {
        sc_mutex_unlock(&vr->mutex);
        LOG_OOM();
        return SC_SINK_KO;
    }

    bool ok = sc_delayed_packet_init_frame(dpacket, frame);
    if (!ok) {
        sc_mutex_unlock(&vr->mutex);
        LOG_OOM();
        return SC_SINK_KO;
    }

    if (vr->automatic) {
        dpacket->pacing_generation = vr->pacing_generation;
        dpacket->pacing_deadline = sc_video_pacer_bounded_deadline(&vr->pacer,
                                                                 pts, recv_date);
    }

#ifdef SC_BUFFERING_DEBUG
    dpacket->push_date = sc_tick_now();
#endif

    SC_TRACE(SC_TRACE_REGULATOR_ENQUEUE, frame->opaque_ref, frame->pts,
             sc_vecdeque_size(&vr->queue),
             sc_clock_to_system_time(&vr->clock, pts) + vr->delay, 0);
    sc_cond_signal(&vr->queue_cond);

    sc_mutex_unlock(&vr->mutex);

    return SC_SINK_OK;
}

static enum sc_sink_result
sc_video_regulator_frame_sink_push_session(struct sc_frame_sink *sink,
                                      const struct sc_stream_session *session) {
    struct sc_video_regulator *vr = DOWNCAST(sink);

    sc_mutex_lock(&vr->mutex);

    if (vr->stopped) {
        sc_mutex_unlock(&vr->mutex);
        return SC_SINK_STOPPED;
    }

    struct sc_delayed_packet *dpacket =
        sc_vecdeque_push_uninitialized(&vr->queue);
    if (!dpacket) {
        sc_mutex_unlock(&vr->mutex);
        LOG_OOM();
        return SC_SINK_KO;
    }

    sc_delayed_packet_init_session(dpacket, session);
    if (vr->automatic) {
        // Keep the barrier in the queue, and freeze the old epoch deadlines.
        ++vr->pacing_generation;
        sc_video_pacer_init(&vr->pacer, vr->automatic_frames);
        vr->pacer_last_trace = 0;
    }

#ifdef SC_BUFFERING_DEBUG
    dpacket->push_date = sc_tick_now();
#endif

    sc_cond_signal(&vr->queue_cond);

    sc_mutex_unlock(&vr->mutex);

    return SC_SINK_OK;
}

static void
sc_video_regulator_frame_sink_apply_backpressure(struct sc_frame_sink *sink) {
    struct sc_video_regulator *vr = DOWNCAST(sink);

    if (!vr->backpressure_threshold) {
        // Backpressure disabled
        return;
    }

    sc_mutex_lock(&vr->mutex);
    // This not only counts queued frames, but also queued session packets.
    // But session packets are rare and consumed quickly, so keep it simple.
    while (!vr->stopped
            && sc_vecdeque_size(&vr->queue) > vr->backpressure_threshold) {
        sc_cond_wait(&vr->backpressure_cond, &vr->mutex);
    }
    sc_mutex_unlock(&vr->mutex);
}

bool
sc_video_regulator_init(struct sc_video_regulator *vr, sc_tick delay,
                        bool first_frame_asap,
                        uint32_t backpressure_threshold, bool automatic,
                        unsigned automatic_frames) {
    assert(delay > 0);

    bool ok = sc_mutex_init(&vr->mutex);
    if (!ok) {
        return false;
    }

    ok = sc_cond_init(&vr->queue_cond);
    if (!ok) {
        goto error_destroy_mutex;
    }

    ok = sc_cond_init(&vr->wait_cond);
    if (!ok) {
        goto error_destroy_queue_cond;
    }

    ok = sc_cond_init(&vr->backpressure_cond);
    if (!ok) {
        goto error_destroy_wait_cond;
    }

    vr->delay = delay;
    vr->automatic = automatic;
    vr->automatic_frames = automatic_frames == 1 ? 1 : 2;
    sc_video_pacer_init(&vr->pacer, vr->automatic_frames);
    vr->pacer_last_trace = 0;
    vr->pacing_generation = 0;
    if (vr->automatic) {
        LOGI("Adaptive video buffering enabled (experimental, max %u frame%s / 32ms)",
             vr->automatic_frames, vr->automatic_frames == 1 ? "" : "s");
    }
    vr->first_frame_asap = first_frame_asap;
    vr->backpressure_threshold = backpressure_threshold;
    vr->stopped = false;

    sc_frame_source_init(&vr->frame_source);

    static const struct sc_frame_sink_ops ops = {
        .open = sc_video_regulator_frame_sink_open,
        .close = sc_video_regulator_frame_sink_close,
        .push = sc_video_regulator_frame_sink_push,
        .push_session = sc_video_regulator_frame_sink_push_session,
        .apply_backpressure = sc_video_regulator_frame_sink_apply_backpressure,
    };

    vr->frame_sink.ops = &ops;

    return true;

error_destroy_wait_cond:
    sc_cond_destroy(&vr->wait_cond);
error_destroy_queue_cond:
    sc_cond_destroy(&vr->queue_cond);
error_destroy_mutex:
    sc_mutex_destroy(&vr->mutex);

    return false;
}

void
sc_video_regulator_stop(struct sc_video_regulator *vr) {
    sc_mutex_lock(&vr->mutex);
    vr->stopped = true;
    sc_cond_signal(&vr->queue_cond);
    sc_cond_signal(&vr->wait_cond);
    sc_cond_signal(&vr->backpressure_cond);
    sc_mutex_unlock(&vr->mutex);
}

void
sc_video_regulator_destroy(struct sc_video_regulator *vr) {
    sc_cond_destroy(&vr->backpressure_cond);
    sc_cond_destroy(&vr->wait_cond);
    sc_cond_destroy(&vr->queue_cond);
    sc_mutex_destroy(&vr->mutex);
}
