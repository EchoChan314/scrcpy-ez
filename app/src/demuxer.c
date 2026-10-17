#include "demuxer.h"
#include "frame_trace.h"

#include <assert.h>
#include <inttypes.h>
#include <libavcodec/avcodec.h>
#include <libavutil/channel_layout.h>
#ifdef SC_ABR_TEST_HOOKS
#include <SDL3/SDL_timer.h>
#include <stdlib.h>
#endif

#include "packet_merger.h"
#include "util/binary.h"
#include "util/log.h"

#define SC_PACKET_HEADER_SIZE 20

#define SC_PACKET_FLAG_CONFIG    (UINT64_C(1) << 62)
#define SC_PACKET_FLAG_KEY_FRAME (UINT64_C(1) << 61)

#define SC_PACKET_PTS_MASK (SC_PACKET_FLAG_KEY_FRAME - 1)

static enum AVCodecID
sc_demuxer_to_avcodec_id(uint32_t codec_id) {
#define SC_CODEC_ID_H264 UINT32_C(0x68323634) // "h264" in ASCII
#define SC_CODEC_ID_H265 UINT32_C(0x68323635) // "h265" in ASCII
#define SC_CODEC_ID_AV1 UINT32_C(0x00617631) // "av1" in ASCII
#define SC_CODEC_ID_VP8 UINT32_C(0x00767038) // "vp8" in ASCII
#define SC_CODEC_ID_VP9 UINT32_C(0x00767039) // "vp9" in ASCII
#define SC_CODEC_ID_OPUS UINT32_C(0x6f707573) // "opus" in ASCII
#define SC_CODEC_ID_AAC UINT32_C(0x00616163) // "aac" in ASCII
#define SC_CODEC_ID_FLAC UINT32_C(0x666c6163) // "flac" in ASCII
#define SC_CODEC_ID_RAW UINT32_C(0x00726177) // "raw" in ASCII
    switch (codec_id) {
        case SC_CODEC_ID_H264:
            return AV_CODEC_ID_H264;
        case SC_CODEC_ID_H265:
            return AV_CODEC_ID_HEVC;
        case SC_CODEC_ID_AV1:
            return AV_CODEC_ID_AV1;
        case SC_CODEC_ID_VP8:
            return AV_CODEC_ID_VP8;
        case SC_CODEC_ID_VP9:
            return AV_CODEC_ID_VP9;
        case SC_CODEC_ID_OPUS:
            return AV_CODEC_ID_OPUS;
        case SC_CODEC_ID_AAC:
            return AV_CODEC_ID_AAC;
        case SC_CODEC_ID_FLAC:
            return AV_CODEC_ID_FLAC;
        case SC_CODEC_ID_RAW:
            return AV_CODEC_ID_PCM_S16LE;
        default:
            LOGE("Unknown codec id 0x%08" PRIx32, codec_id);
            return AV_CODEC_ID_NONE;
    }
}

static bool
sc_demuxer_recv_codec_id(struct sc_demuxer *demuxer, uint32_t *codec_id) {
    uint8_t data[4];
    ssize_t r = net_recv_all(demuxer->socket, data, 4);
    if (r < 4) {
        return false;
    }

    *codec_id = sc_read32be(data);
    return true;
}

static inline bool
sc_demuxer_recv_header(struct sc_demuxer *demuxer,
                       uint8_t buf[static SC_PACKET_HEADER_SIZE]) {
    // The video and audio streams contain a sequence of raw packets (as
    // provided by MediaCodec), each prefixed with a "meta" header.
    //
    // The "meta" header length is 12 bytes.
    //
    //
    // If the MSB is 1, then it is a session packet (for a video stream only),
    // which only contains a 12-byte header:
    //
    //  byte 0   byte 1   byte 2   byte 3
    // 10000000 00000000 00000000 0000000R
    // ^<------------------------------->^
    // |               padding           |
    //  `- session packet flag            `- client resized flag
    //
    //  byte 4   byte 5   byte 6   byte 7   byte 8   byte 9   byte 10  byte 11
    // ........ ........ ........ ........ ........ ........ ........ ........
    // <---------------------------------> <--------------------------------->
    //             video width                         video height
    //
    //
    // If the MSB is 0, then it is a media packet, comprised of a 12-byte header
    // followed by <packet_size> bytes containing the packet/frame:
    //
    // [. . . . . . . .|. . . .]. . . . . . . . . . . . . . . ...
    //  <-------------> <-----> <-----------------------------...
    //        PTS        packet        raw packet
    //                    size
    //  <--------------------->
    //       frame header
    //
    // The most significant bits of the PTS are used for packet flags:
    //
    //  byte 0   byte 1   byte 2   byte 3   byte 4   byte 5   byte 6   byte 7
    // 0CK..... ........ ........ ........ ........ ........ ........ ........
    // ^^^<------------------------------------------------------------------>
    // |||                                PTS
    // || `- key frame
    // | `-- config packet
    //  `--- media packet flag
    //
    //  byte 8   byte 9   byte 10  byte 11
    // ........ ........ ........ ........ ........ ........ . . .
    // <---------------------------------> <---------------- . . .
    //            packet size                       raw packet
    //
    ssize_t r = net_recv_all(demuxer->socket, buf, SC_PACKET_HEADER_SIZE);
    assert(r <= SC_PACKET_HEADER_SIZE);
    return r == SC_PACKET_HEADER_SIZE;
}

static bool
sc_demuxer_is_session(const uint8_t *header) {
    return header[0] & 0x80;
}

static void
sc_demuxer_parse_session(const uint8_t *header,
                         struct sc_stream_session *session) {
    assert(sc_demuxer_is_session(header));
    session->video.width = sc_read32be(&header[4]);
    session->video.height = sc_read32be(&header[8]);
    session->video.client_resized = header[3] & 1;
}

static bool
sc_demuxer_recv_packet(struct sc_demuxer *demuxer, const uint8_t *header,
                       AVPacket *packet) {
    assert(!sc_demuxer_is_session(header));
    uint64_t pts_flags = sc_read64be(header);
    uint32_t len = sc_read32be(&header[8]);
    if (!len) {
        LOGE("Invalid packet length: 0");
        return false;
    }

    if (av_new_packet(packet, len)) {
        LOG_OOM();
        return false;
    }

#ifdef SC_ABR_TEST_HOOKS
    const char *limit = getenv("SCEZ_TEST_LINK_KBPS");
    unsigned long kbps = limit ? strtoul(limit, NULL, 10) : 0;
    const char *duration = getenv("SCEZ_TEST_FAULT_SECONDS");
    unsigned long duration_s = duration ? strtoul(duration, NULL, 10) : 0;
    static sc_tick fault_start;
    if (!fault_start) fault_start = sc_tick_now();
    if (kbps >= 100 && kbps <= 100000 && (!duration_s || sc_tick_now() - fault_start < SC_TICK_FROM_SEC(duration_s))) {
        SDL_Delay((uint64_t) len * 8 / kbps);
    }
#endif
    ssize_t r = net_recv_all(demuxer->socket, packet->data, len);
    if (r < 0 || ((uint32_t) r) < len) {
        av_packet_unref(packet);
        return false;
    }

    if (demuxer->abr_monitor && !(pts_flags & SC_PACKET_FLAG_CONFIG)) {
        sc_abr_monitor_arrival(demuxer->abr_monitor, sc_read64be(&header[12]), sc_tick_now(), len);
    }
    bool trace_video = sc_frame_trace_enabled && !strcmp(demuxer->name, "video");
    if (demuxer->set_recv_date || trace_video) {
        sc_tick recv_date = sc_tick_now();

        // Store the recv date as an opaque ref
        size_t meta_size = trace_video
                        ? sizeof(struct sc_frame_trace_meta) : sizeof(sc_tick);
        packet->opaque_ref = av_buffer_allocz(meta_size);
        if (!packet->opaque_ref) {
            if (demuxer->set_recv_date) {
                av_packet_unref(packet);
                LOG_OOM();
                return false;
            }
            // Lost diagnostic association must not stop unbuffered playback.
        } else {
            *(sc_tick *) packet->opaque_ref->data = recv_date;
        }
    }

    if (pts_flags & SC_PACKET_FLAG_CONFIG) {
        packet->pts = AV_NOPTS_VALUE;
    } else {
        packet->pts = pts_flags & SC_PACKET_PTS_MASK;
    }

    if (pts_flags & SC_PACKET_FLAG_KEY_FRAME) {
        packet->flags |= AV_PKT_FLAG_KEY;
    }

    packet->dts = packet->pts;
    if (trace_video) {
        if (packet->pts != AV_NOPTS_VALUE) {
            if (demuxer->trace_last_pts != AV_NOPTS_VALUE
                    && packet->pts <= demuxer->trace_last_pts) {
                ++demuxer->trace_epoch;
            }
            demuxer->trace_last_pts = packet->pts;
        }
        ++demuxer->trace_packet_id;
        if (packet->opaque_ref) {
            struct sc_frame_trace_meta *meta =
                (struct sc_frame_trace_meta *) packet->opaque_ref->data;
            meta->packet_id = demuxer->trace_packet_id;
            meta->epoch = demuxer->trace_epoch;
        }
        SC_TRACE(SC_TRACE_PACKET_RECV, packet->opaque_ref, packet->pts, 0,
                 packet->size, (packet->pts == AV_NOPTS_VALUE ? 1 : 0)
                               | ((packet->flags & AV_PKT_FLAG_KEY) ? 2 : 0));
    }
    return true;
}

static int
run_demuxer(void *data) {
    struct sc_demuxer *demuxer = data;

    // Flag to report end-of-stream (i.e. device disconnected)
    enum sc_demuxer_status status = SC_DEMUXER_STATUS_ERROR;

    uint32_t raw_codec_id;
    bool ok = sc_demuxer_recv_codec_id(demuxer, &raw_codec_id);
    if (!ok) {
        LOGE("Demuxer '%s': stream disabled due to connection error",
             demuxer->name);
        goto end;
    }

    if (raw_codec_id == 0) {
        LOGW("Demuxer '%s': stream explicitly disabled by the device",
             demuxer->name);
        sc_packet_source_sinks_disable(&demuxer->packet_source);
        status = SC_DEMUXER_STATUS_DISABLED;
        goto end;
    }

    if (raw_codec_id == 1) {
        LOGE("Demuxer '%s': stream configuration error on the device",
             demuxer->name);
        goto end;
    }

    enum AVCodecID codec_id = sc_demuxer_to_avcodec_id(raw_codec_id);
    if (codec_id == AV_CODEC_ID_NONE) {
        LOGE("Demuxer '%s': stream disabled due to unsupported codec",
             demuxer->name);
        sc_packet_source_sinks_disable(&demuxer->packet_source);
        goto end;
    }

    enum AVMediaType codec_type = avcodec_get_type(codec_id);

    AVCodecParameters *params = avcodec_parameters_alloc();
    if (!params) {
        LOG_OOM();
        goto end;
    }

    params->codec_type = codec_type;
    params->codec_id = codec_id;

    uint8_t header[SC_PACKET_HEADER_SIZE];
    struct sc_stream_session session_data;

    struct sc_stream_session *session = NULL;
    if (codec_type == AVMEDIA_TYPE_VIDEO) {
        bool ok = sc_demuxer_recv_header(demuxer, header);
        if (!ok) {
            goto finally_free_params;
        }

        if (!sc_demuxer_is_session(header)) {
            LOGE("Unexpected packet (not a session header)");
            goto finally_free_params;
        }

        session = &session_data;
        sc_demuxer_parse_session(header, session);
        if (demuxer->abr_monitor) sc_abr_monitor_epoch(demuxer->abr_monitor, true);

        if (!session_data.video.width || !session_data.video.height) {
            LOGE("Invalid session video size: %" PRIu32 "x%" PRIu32,
                 session_data.video.width, session_data.video.height);
            goto finally_free_params;
        }

        params->width = session_data.video.width;
        params->height = session_data.video.height;
        params->format = AV_PIX_FMT_YUV420P;

    } else {
        // Hardcoded audio properties
        params->ch_layout = (AVChannelLayout) AV_CHANNEL_LAYOUT_STEREO;
        params->sample_rate = 48000;

        if (raw_codec_id == SC_CODEC_ID_FLAC) {
            // The sample_fmt is not set by the FLAC decoder
            params->format = AV_SAMPLE_FMT_S16;
        }
    }

    if (!sc_packet_source_sinks_open(&demuxer->packet_source, params,
                                     session)) {
        goto finally_free_params;
    }

    // Config packets must be merged with the next non-config packet only for
    // H.26x
    bool must_merge_config_packet = raw_codec_id == SC_CODEC_ID_H264
                                 || raw_codec_id == SC_CODEC_ID_H265;

    struct sc_packet_merger merger;

    if (must_merge_config_packet) {
        sc_packet_merger_init(&merger);
    }

    AVPacket *packet = av_packet_alloc();
    if (!packet) {
        LOG_OOM();
        goto finally_close_sinks;
    }

    for (;;) {
        bool ok = sc_demuxer_recv_header(demuxer, header);
        if (!ok) {
            // end of stream
            status = SC_DEMUXER_STATUS_EOS;
            break;
        }

        if (sc_demuxer_is_session(header)) {
            sc_demuxer_parse_session(header, &session_data);
                if (demuxer->abr_monitor) sc_abr_monitor_epoch(demuxer->abr_monitor, true);
            if (sc_frame_trace_enabled && !strcmp(demuxer->name, "video")) {
                ++demuxer->trace_epoch;
                demuxer->trace_last_pts = AV_NOPTS_VALUE;
            }
            enum sc_sink_result result =
                sc_packet_source_sinks_push_session(&demuxer->packet_source,
                                                    &session_data);
            if (result != SC_SINK_OK) {
                break;
            }
        } else {
            bool ok = sc_demuxer_recv_packet(demuxer, header, packet);
            if (!ok) {
                break;
            }

            if (must_merge_config_packet) {
                // Prepend any config packet to the next media packet
                ok = sc_packet_merger_merge(&merger, packet);
                if (!ok) {
                    av_packet_unref(packet);
                    break;
                }
            }

            enum sc_sink_result result =
                sc_packet_source_sinks_push(&demuxer->packet_source, packet);
            av_packet_unref(packet);
            if (result != SC_SINK_OK) {
                break;
            }
        }
    }

    LOGD("Demuxer '%s': end of frames", demuxer->name);

    if (must_merge_config_packet) {
        sc_packet_merger_destroy(&merger);
    }

    av_packet_free(&packet);
finally_close_sinks:
    sc_packet_source_sinks_close(&demuxer->packet_source);
finally_free_params:
    avcodec_parameters_free(&params);
end:
    demuxer->cbs->on_ended(demuxer, status, demuxer->cbs_userdata);

    return 0;
}

void
sc_demuxer_init(struct sc_demuxer *demuxer, const char *name, sc_socket socket,
                bool set_recv_date,
                const struct sc_demuxer_callbacks *cbs, void *cbs_userdata) {
    assert(socket != SC_SOCKET_NONE);

    demuxer->name = name;
    demuxer->abr_monitor = NULL; // statically allocated
    demuxer->socket = socket;
    sc_packet_source_init(&demuxer->packet_source);

    demuxer->set_recv_date = set_recv_date;
    demuxer->trace_packet_id = 0;
    demuxer->trace_epoch = 1;
    demuxer->trace_last_pts = AV_NOPTS_VALUE;

    assert(cbs && cbs->on_ended);

    demuxer->cbs = cbs;
    demuxer->cbs_userdata = cbs_userdata;
}

bool
sc_demuxer_start(struct sc_demuxer *demuxer) {
    LOGD("Demuxer '%s': starting thread", demuxer->name);

    bool ok = sc_thread_create(&demuxer->thread, run_demuxer, "scrcpy-demuxer",
                               demuxer);
    if (!ok) {
        LOGE("Demuxer '%s': could not start thread", demuxer->name);
        return false;
    }
    return true;
}

void
sc_demuxer_join(struct sc_demuxer *demuxer) {
    sc_thread_join(&demuxer->thread, NULL);
}
