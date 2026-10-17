#include "decoder.h"
#include "frame_trace.h"

#include <errno.h>
#include <stdio.h>
#include <libavutil/pixdesc.h>
#ifdef SC_ABR_TEST_HOOKS
#include <SDL3/SDL_timer.h>
#include <stdlib.h>
#endif
#include <libavcodec/packet.h>
#include <libavutil/avutil.h>
#include <libavutil/error.h>

#include "util/log.h"

/** Downcast packet_sink to decoder */
#define DOWNCAST(SINK) container_of(SINK, struct sc_decoder, packet_sink)

static AVCodecContext *
sc_decoder_create_context(const char *decoder_name, const AVCodec *codec,
                          const AVCodecParameters *params,
                          struct sc_hwdec *hwdec, bool copy_opaque) {
    AVCodecContext *ctx = avcodec_alloc_context3(codec);
    if (!ctx) {
        LOG_OOM();
        return NULL;
    }

    int r = avcodec_parameters_to_context(ctx, params);
    if (r < 0) {
        LOGE("Decoder '%s': could not set codec parameters", decoder_name);
        avcodec_free_context(&ctx);
        return NULL;
    }

    ctx->flags |= AV_CODEC_FLAG_LOW_DELAY;

    if (copy_opaque) {
        // Propagate AVPacket.opaque_ref (the recv_date) to the decoded AVFrame
        ctx->flags |= AV_CODEC_FLAG_COPY_OPAQUE;
    }

    if (hwdec && !sc_hwdec_configure(hwdec, ctx)) {
        if (hwdec->hw_forced) {
            LOGE("Decoder '%s': hardware decoding unavailable", decoder_name);
            avcodec_free_context(&ctx);
            return NULL;
        }

        // Use software decoding (the codec context is left untouched)
        LOGW("Decoder '%s': hardware decoding unavailable, using software "
             "decoding", decoder_name);
    }

    r = avcodec_open2(ctx, codec, NULL);
    if (r < 0) {
        LOGE("Decoder '%s': could not open codec", decoder_name);
        avcodec_free_context(&ctx);
        return NULL;
    }

    if (ctx->codec_type == AVMEDIA_TYPE_VIDEO) {
        SC_TRACE(SC_TRACE_DECODE_CONFIG, NULL, AV_NOPTS_VALUE, 0,
                 ctx->thread_count, ctx->active_thread_type);
    }

    return ctx;
}

static bool
sc_decoder_fallback_to_software(struct sc_decoder *decoder) {
    const AVCodec *codec = avcodec_find_decoder(decoder->params->codec_id);
    if (!codec) {
        LOGE("Decoder '%s': no software decoder", decoder->name);
        return false;
    }

    // Pass NULL as hwdec
    AVCodecContext *ctx =
        sc_decoder_create_context(decoder->name, codec, decoder->params, NULL,
                                  decoder->copy_opaque);
    if (!ctx) {
        return false;
    }

    avcodec_free_context(&decoder->ctx);
    decoder->ctx = ctx;
    return true;
}

static bool
sc_decoder_reopen(struct sc_decoder *decoder) {
    assert(decoder->ctx->hw_device_ctx);

    AVCodecContext *ctx =
        sc_decoder_create_context(decoder->name, decoder->ctx->codec,
                                  decoder->params, decoder->hwdec,
                                  decoder->copy_opaque);
    if (!ctx) {
        return false;
    }

    avcodec_free_context(&decoder->ctx);
    decoder->ctx = ctx;
    return true;
}

static bool
sc_decoder_open(struct sc_decoder *decoder, const AVCodecParameters *params,
                const struct sc_stream_session *session) {
    // A video stream must have a session
    assert(session || params->codec_type != AVMEDIA_TYPE_VIDEO);

    const AVCodec *codec = NULL;

    if (decoder->hwdec && decoder->hwdec->hw_type != AV_HWDEVICE_TYPE_NONE) {
        // The default decoder may not support hardware decoding (for example,
        // libdav1d is preferred for AV1, but only the native av1 decoder
        // supports the hardware devices)
        codec = sc_hwdec_find_decoder(decoder->hwdec, params->codec_id);
    }

    if (!codec) {
        codec = avcodec_find_decoder(params->codec_id);
        if (!codec) {
            LOGE("Decoder '%s': no decoder for %s", decoder->name,
                 avcodec_get_name(params->codec_id));
            return false;
        }
    }

    LOGD("Decoder '%s': %s", decoder->name, codec->name);

    decoder->params = avcodec_parameters_alloc();
    if (!decoder->params) {
        LOG_OOM();
        return false;
    }

    int r = avcodec_parameters_copy(decoder->params, params);
    if (r < 0) {
        LOG_OOM();
        goto error_free_params;
    }

    decoder->ctx = sc_decoder_create_context(decoder->name, codec, params,
                                             decoder->hwdec,
                                             decoder->copy_opaque);
    if (!decoder->ctx) {
        goto error_free_params;
    }

    decoder->frame = av_frame_alloc();
    if (!decoder->frame) {
        LOG_OOM();
        goto error_free_context;
    }

    if (!sc_frame_source_sinks_open(&decoder->frame_source, decoder->ctx,
                                    session)) {
        goto error_free_frame;
    }

    if (session) {
        decoder->session = *session;
    }

    memset(&decoder->frame_size, 0, sizeof(decoder->frame_size));
    decoder->frame_decoded = false;

    return true;

error_free_frame:
    av_frame_free(&decoder->frame);
error_free_context:
    avcodec_free_context(&decoder->ctx);
error_free_params:
    avcodec_parameters_free(&decoder->params);

    return false;
}

static void
sc_decoder_close(struct sc_decoder *decoder) {
    sc_mutex_lock(&decoder->mutex);
    decoder->stopped = true;
    sc_cond_signal(&decoder->cond);

    // Wait until the decoder thread finished using the ctx, frame and the frame
    // sinks
    while (!decoder->ended) {
        sc_cond_wait(&decoder->cond, &decoder->mutex);
    }

    sc_mutex_unlock(&decoder->mutex);

    sc_frame_source_sinks_close(&decoder->frame_source);
    av_frame_free(&decoder->frame);
    avcodec_free_context(&decoder->ctx);
    avcodec_parameters_free(&decoder->params);
}

static enum sc_sink_result
sc_decoder_process_packet(struct sc_decoder *decoder, const AVPacket *packet) {
    // Downstream pacing wait is intentional, not decoder overload.
    // active_recv is installed when this packet is dequeued.
    if (decoder->abr_monitor) {
        sc_mutex_lock(&decoder->abr_monitor->mutex);
        decoder->abr_monitor->blocked = true;
        sc_mutex_unlock(&decoder->abr_monitor->mutex);
    }
    // Do not decode more packets before the frame sinks are ready
    sc_frame_source_sinks_apply_backpressure(&decoder->frame_source);
    if (decoder->abr_monitor) {
        sc_mutex_lock(&decoder->abr_monitor->mutex);
        decoder->abr_monitor->blocked = false;
        sc_mutex_unlock(&decoder->abr_monitor->mutex);
    }

    sc_tick work_start = sc_tick_now();
    sc_tick downstream_wait = 0;
    if (decoder->ctx->codec_type == AVMEDIA_TYPE_VIDEO) {
#ifdef SC_ABR_TEST_HOOKS
        const char *delay = getenv("SCEZ_TEST_DECODE_DELAY_MS");
        unsigned long delay_ms = delay ? strtoul(delay, NULL, 10) : 0;
        const char *duration = getenv("SCEZ_TEST_FAULT_SECONDS");
        unsigned long duration_s = duration ? strtoul(duration, NULL, 10) : 0;
        static sc_tick fault_start;
        if (!fault_start) fault_start = sc_tick_now();
        if (delay_ms && delay_ms <= 500 && (!duration_s || sc_tick_now() - fault_start < SC_TICK_FROM_SEC(duration_s))) {
            SDL_Delay(delay_ms);
        }
#endif
        SC_TRACE(SC_TRACE_DECODE_IN, packet->opaque_ref, packet->pts, 0, 0, 0);
    }
    int ret = avcodec_send_packet(decoder->ctx, packet);

    if (ret < 0 && ret != AVERROR(EAGAIN)) {
        bool fallback_to_software = decoder->ctx->hw_device_ctx
                                 && !decoder->hwdec->hw_forced
                                 && !decoder->frame_decoded;
        if (fallback_to_software) {
            LOGW("Decoder '%s': hardware decoding failed (%s), falling back "
                 "to software decoding", decoder->name, av_err2str(ret));
            if (!sc_decoder_fallback_to_software(decoder)) {
                return SC_SINK_KO;
            }

            // Send the packet to the new decoder
            ret = avcodec_send_packet(decoder->ctx, packet);
        }
    }

    if (ret < 0 && ret != AVERROR(EAGAIN)) {
        LOGE("Decoder '%s': could not send video packet: %s",
             decoder->name, av_err2str(ret));
        return SC_SINK_KO;
    }

    for (;;) {
        ret = avcodec_receive_frame(decoder->ctx, decoder->frame);
        if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
            break;
        }

        if (ret) {
            LOGE("Decoder '%s', could not receive video frame: %s",
                 decoder->name, av_err2str(ret));
            return SC_SINK_KO;
        }

        if (decoder->abr_monitor) {
            const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get(decoder->frame->format);
            sc_abr_monitor_output(decoder->abr_monitor,
                                  desc && (desc->flags & AV_PIX_FMT_FLAG_HWACCEL));
        }
        // a frame was received
        decoder->frame_decoded = true;

        if (decoder->ctx->codec_type == AVMEDIA_TYPE_VIDEO) {
            SC_TRACE(SC_TRACE_DECODE_OUT, decoder->frame->opaque_ref,
                     decoder->frame->pts, 0, decoder->frame->format, 0);
            assert(decoder->frame->width >= 0);
            assert(decoder->frame->height >= 0);
            struct sc_size frame_size = {
                .width = decoder->frame->width,
                .height = decoder->frame->height,
            };
            if (decoder->frame_size.width != frame_size.width
                    || decoder->frame_size.height != frame_size.height) {
                // The frame size has changed, check if it matches the session
                uint32_t sw = decoder->session.video.width;
                uint32_t sh = decoder->session.video.height;
                if (frame_size.width != sw || frame_size.height != sh) {
                    LOGW("Unexpected video size: %" PRIu32 "x%" PRIu32
                         " (expected %" PRIu32 "x%" PRIu32 ")",
                         frame_size.width, frame_size.height, sw, sh);

                    LOGW("The encoder did not respect the requested size, "
                         "please retry with a lower resolution "
                         "(-m/--max-size)");
                }
            }

            decoder->frame_size = frame_size;
        }

        sc_tick sink_start = sc_tick_now();
        if (decoder->abr_monitor) {
            sc_mutex_lock(&decoder->abr_monitor->mutex);
            decoder->abr_monitor->blocked = true;
            sc_mutex_unlock(&decoder->abr_monitor->mutex);
        }
        enum sc_sink_result result =
            sc_frame_source_sinks_push(&decoder->frame_source, decoder->frame);
        downstream_wait += sc_tick_now() - sink_start;
        if (decoder->abr_monitor) {
            sc_mutex_lock(&decoder->abr_monitor->mutex);
            decoder->abr_monitor->blocked = false;
            sc_mutex_unlock(&decoder->abr_monitor->mutex);
        }
        av_frame_unref(decoder->frame);
        if (result != SC_SINK_OK) {
            // Not a decoder error
            return SC_SINK_STOPPED;
        }
    }

    if (decoder->abr_monitor) sc_abr_monitor_work(decoder->abr_monitor, sc_tick_now() - work_start - downstream_wait);
    return SC_SINK_OK;
}

static void
sc_decoder_queue_clear(struct sc_decoder_queue *queue) {
    while (!sc_vecdeque_is_empty(queue)) {
        struct sc_decoder_packet *dp = sc_vecdeque_popref(queue);
        if (dp->type == SC_DECODER_PACKET_TYPE_AV_PACKET) {
            if (dp->packet->opaque_ref
                    && dp->packet->opaque_ref->size == sizeof(struct sc_frame_trace_meta)) {
                SC_TRACE(SC_TRACE_DECODE_DROP, dp->packet->opaque_ref,
                         dp->packet->pts, sc_vecdeque_size(queue), 0, 1);
            }
            av_packet_free(&dp->packet);
        }
    }
}

static bool
sc_decoder_decode(struct sc_decoder *decoder) {
    for (;;) {
        sc_mutex_lock(&decoder->mutex);
        while (!decoder->stopped && sc_vecdeque_is_empty(&decoder->queue)) {
            sc_cond_wait(&decoder->cond, &decoder->mutex);
        }
        if (decoder->stopped) {
            sc_mutex_unlock(&decoder->mutex);
            return true;
        }

        struct sc_decoder_packet dp = sc_vecdeque_pop(&decoder->queue);
        if (dp.type == SC_DECODER_PACKET_TYPE_AV_PACKET
                && decoder->ctx->codec_type == AVMEDIA_TYPE_VIDEO) {
            SC_TRACE(SC_TRACE_DECODE_DEQUEUE, dp.packet->opaque_ref,
                     dp.packet->pts, sc_vecdeque_size(&decoder->queue), 0, 0);
        }
        if (decoder->abr_monitor) {
            sc_tick oldest = 0;
            if (!sc_vecdeque_is_empty(&decoder->queue)) {
                oldest = decoder->queue.data[decoder->queue.origin].queued_at;
            }
            sc_abr_monitor_queue(decoder->abr_monitor, oldest, sc_vecdeque_size(&decoder->queue));
            sc_abr_monitor_decode(decoder->abr_monitor, dp.epoch,
                                  dp.type == SC_DECODER_PACKET_TYPE_AV_PACKET ? dp.queued_at : 0, false);
        }
        sc_mutex_unlock(&decoder->mutex);

        enum sc_sink_result result;
        if (dp.type == SC_DECODER_PACKET_TYPE_AV_PACKET) {
            result = sc_decoder_process_packet(decoder, dp.packet);
            av_packet_free(&dp.packet);
            if (decoder->abr_monitor) sc_abr_monitor_decode(decoder->abr_monitor, dp.epoch, 0, false);
        } else {
            assert(dp.type == SC_DECODER_PACKET_TYPE_SESSION);
            decoder->session = dp.session;
            SC_TRACE(SC_TRACE_STREAM_RESET, NULL, AV_NOPTS_VALUE, 0, 0, 0);

            // A new session starts a new stream possibly with a different size.
            // The FFmpeg av1 decoder does not reinitialize its hardware decoder
            // in that case, so recreate the codec context.
            bool av1_hw = decoder->ctx->codec_id == AV_CODEC_ID_AV1
                       && decoder->ctx->hw_device_ctx;
            if (av1_hw && !sc_decoder_reopen(decoder)) {
                return false;
            }

            enum sc_sink_result push_result =
                sc_frame_source_sinks_push_session(&decoder->frame_source,
                                                   &dp.session);
            // Not a decoder error
            result = push_result == SC_SINK_OK ? SC_SINK_OK : SC_SINK_STOPPED;
        }

        if (result == SC_SINK_STOPPED) {
            // No error
            return true;
        }

        if (result == SC_SINK_KO) {
            return false;
        }
    }
    return true;
}

static int
run_decoder(void *data) {
    struct sc_decoder *decoder = data;

    bool success = sc_decoder_decode(decoder);

    sc_mutex_lock(&decoder->mutex);
    // Prevent the producer from pushing any new packet
    decoder->stopped = true;
    // Discard pending packets
    sc_decoder_queue_clear(&decoder->queue);

    decoder->ended = true;
    sc_cond_signal(&decoder->cond);

    sc_mutex_unlock(&decoder->mutex);

    if (!success) {
        LOGE("Decoding (%s) failed", decoder->name);
    }

    LOGD("Decoder (%s) thread ended", decoder->name);

    decoder->cbs->on_ended(decoder, success, decoder->cbs_userdata);

    return 0;
}

static AVPacket *
sc_decoder_packet_ref(const AVPacket *packet) {
    AVPacket *p = av_packet_alloc();
    if (!p) {
        LOG_OOM();
        return NULL;
    }

    if (av_packet_ref(p, packet)) {
        LOG_OOM();
        av_packet_free(&p);
        return NULL;
    }

    return p;
}

static enum sc_sink_result
sc_decoder_push(struct sc_decoder *decoder, const AVPacket *packet) {
    bool is_config = packet->pts == AV_NOPTS_VALUE;
    if (is_config) {
        // nothing to do
        return SC_SINK_OK;
    }

    sc_mutex_lock(&decoder->mutex);

    if (decoder->stopped) {
        // reject any new packet
        sc_mutex_unlock(&decoder->mutex);
        return SC_SINK_STOPPED;
    }

    AVPacket *p = sc_decoder_packet_ref(packet);
    if (!p) {
        sc_mutex_unlock(&decoder->mutex);
        return SC_SINK_KO;
    }

    struct sc_decoder_packet *dp =
        sc_vecdeque_push_uninitialized(&decoder->queue);
    if (!dp) {
        LOG_OOM();
        sc_mutex_unlock(&decoder->mutex);
        av_packet_free(&p);
        return SC_SINK_KO;
    }

    dp->type = SC_DECODER_PACKET_TYPE_AV_PACKET;
    dp->packet = p;
    dp->queued_at = sc_tick_now();
    dp->epoch = decoder->abr_monitor ? sc_abr_monitor_epoch(decoder->abr_monitor, false) : 0;
    if (decoder->abr_monitor) {
        sc_abr_monitor_queue(decoder->abr_monitor, decoder->queue.data[decoder->queue.origin].queued_at, sc_vecdeque_size(&decoder->queue));
    }
    if (decoder->hwdec) {
        SC_TRACE(SC_TRACE_DECODE_ENQUEUE, p->opaque_ref, p->pts,
                 sc_vecdeque_size(&decoder->queue), 0, 0);
    }

    sc_cond_signal(&decoder->cond);

    sc_mutex_unlock(&decoder->mutex);
    return SC_SINK_OK;
}

static enum sc_sink_result
sc_decoder_push_session(struct sc_decoder *decoder,
                        const struct sc_stream_session *session) {
    sc_mutex_lock(&decoder->mutex);

    if (decoder->stopped) {
        // reject any new packet
        sc_mutex_unlock(&decoder->mutex);
        return SC_SINK_STOPPED;
    }

    struct sc_decoder_packet *dp =
        sc_vecdeque_push_uninitialized(&decoder->queue);
    if (!dp) {
        LOG_OOM();
        sc_mutex_unlock(&decoder->mutex);
        return SC_SINK_KO;
    }

    dp->type = SC_DECODER_PACKET_TYPE_SESSION;
    dp->session = *session;
    dp->queued_at = 0;
    dp->epoch = decoder->abr_monitor ? sc_abr_monitor_epoch(decoder->abr_monitor, false) : 0;

    sc_cond_signal(&decoder->cond);

    sc_mutex_unlock(&decoder->mutex);
    return SC_SINK_OK;
}

static bool
sc_decoder_packet_sink_open(struct sc_packet_sink *sink,
                            const AVCodecParameters *params,
                            const struct sc_stream_session *session) {
    struct sc_decoder *decoder = DOWNCAST(sink);
    return sc_decoder_open(decoder, params, session);
}

static void
sc_decoder_packet_sink_close(struct sc_packet_sink *sink) {
    struct sc_decoder *decoder = DOWNCAST(sink);
    sc_decoder_close(decoder);
}

static enum sc_sink_result
sc_decoder_packet_sink_push(struct sc_packet_sink *sink,
                            const AVPacket *packet) {
    struct sc_decoder *decoder = DOWNCAST(sink);
    return sc_decoder_push(decoder, packet);
}

static enum sc_sink_result
sc_decoder_packet_sink_push_session(struct sc_packet_sink *sink,
                                    const struct sc_stream_session *session) {
    struct sc_decoder *decoder = DOWNCAST(sink);
    return sc_decoder_push_session(decoder, session);
}

bool
sc_decoder_init(struct sc_decoder *decoder, const char *name,
                struct sc_hwdec *hwdec, bool copy_opaque,
                const struct sc_decoder_callbacks *cbs, void *cbs_userdata) {
    bool ok = sc_mutex_init(&decoder->mutex);
    if (!ok) {
        return false;
    }

    ok = sc_cond_init(&decoder->cond);
    if (!ok) {
        sc_mutex_destroy(&decoder->mutex);
        return false;
    }

    decoder->name = name;
    decoder->abr_monitor = NULL; // statically allocated
    sc_frame_source_init(&decoder->frame_source);

    decoder->hwdec = hwdec;
    decoder->copy_opaque = copy_opaque;

    sc_vecdeque_init(&decoder->queue);
    decoder->stopped = false;
    decoder->ended = false;

    assert(cbs && cbs->on_ended);
    decoder->cbs = cbs;
    decoder->cbs_userdata = cbs_userdata;

    static const struct sc_packet_sink_ops ops = {
        .open = sc_decoder_packet_sink_open,
        .close = sc_decoder_packet_sink_close,
        .push = sc_decoder_packet_sink_push,
        .push_session = sc_decoder_packet_sink_push_session,
    };

    decoder->packet_sink.ops = &ops;

    return true;
}

bool
sc_decoder_start(struct sc_decoder *decoder) {
    char thread_name[16];
    // "scrcpy-vid-dec" or "scrcpy-aud-dec"
    snprintf(thread_name, sizeof(thread_name), "scrcpy-%.3s-dec",
             decoder->name);
    bool ok = sc_thread_create(&decoder->thread, run_decoder, thread_name,
                               decoder);
    if (!ok) {
        LOGE("Could not start decoder (%s) thread", decoder->name);
        return false;
    }

    return true;
}

void
sc_decoder_stop(struct sc_decoder *decoder) {
    sc_mutex_lock(&decoder->mutex);
    decoder->stopped = true;
    sc_cond_signal(&decoder->cond);
    sc_mutex_unlock(&decoder->mutex);
}

void
sc_decoder_join(struct sc_decoder *decoder) {
    sc_thread_join(&decoder->thread, NULL);
}

void
sc_decoder_destroy(struct sc_decoder *decoder) {
    sc_vecdeque_destroy(&decoder->queue);
    sc_cond_destroy(&decoder->cond);
    sc_mutex_destroy(&decoder->mutex);
}
