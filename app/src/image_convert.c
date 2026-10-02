#include "image_convert.h"

#include <stdlib.h>
#include <string.h>
#include <limits.h>
#ifdef _WIN32
#define COBJMACROS
#include <windows.h>
#include <wincodec.h>
#endif

#include <libavcodec/avcodec.h>
#include <libavutil/avutil.h>
#include <libavutil/frame.h>
#include <libswscale/swscale.h>

#include "util/log.h"

// JPEG quality (1-100)
#define SC_JPEG_QUALITY 95

bool
sc_image_to_bgra(const uint8_t *data, size_t size, const char *mime,
                 uint8_t **pixels, unsigned *width, unsigned *height) {
#ifdef _WIN32
    if (!size || size > UINT_MAX) { return false; }
    HRESULT initialized = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    if (FAILED(initialized) && initialized != RPC_E_CHANGED_MODE) { return false; }
    IWICImagingFactory *factory = NULL;
    IWICStream *stream = NULL;
    IWICBitmapDecoder *decoder = NULL;
    IWICBitmapFrameDecode *frame = NULL;
    IWICFormatConverter *converter = NULL;
    uint8_t *bgra = NULL;
    UINT w = 0, h = 0;
    bool ok = false;
    HRESULT hr = CoCreateInstance(&CLSID_WICImagingFactory, NULL, CLSCTX_INPROC_SERVER,
                                   &IID_IWICImagingFactory, (void **) &factory);
    if (FAILED(hr)) { goto wic_end; }
    hr = IWICImagingFactory_CreateStream(factory, &stream);
    if (FAILED(hr)) { goto wic_end; }
    hr = IWICStream_InitializeFromMemory(stream, (BYTE *) data, (DWORD) size);
    if (FAILED(hr)) { goto wic_end; }
    hr = IWICImagingFactory_CreateDecoderFromStream(factory, (IStream *) stream, NULL,
                                                    WICDecodeMetadataCacheOnLoad, &decoder);
    if (FAILED(hr)) { goto wic_end; }
    hr = IWICBitmapDecoder_GetFrame(decoder, 0, &frame);
    if (FAILED(hr)) { goto wic_end; }
    hr = IWICBitmapFrameDecode_GetSize(frame, &w, &h);
    if (FAILED(hr) || !w || !h || (uint64_t) w * h > 256u * 1024u * 1024u / 4) { goto wic_end; }
    bgra = malloc((size_t) w * h * 4);
    if (!bgra) { goto wic_end; }
    hr = IWICImagingFactory_CreateFormatConverter(factory, &converter);
    if (FAILED(hr)) { goto wic_end; }
    hr = IWICFormatConverter_Initialize(converter, (IWICBitmapSource *) frame,
                                        &GUID_WICPixelFormat32bppBGRA, WICBitmapDitherTypeNone,
                                        NULL, 0, WICBitmapPaletteTypeCustom);
    if (FAILED(hr)) { goto wic_end; }
    hr = IWICFormatConverter_CopyPixels(converter, NULL, w * 4, w * h * 4, bgra);
    if (FAILED(hr)) { goto wic_end; }
    *pixels = bgra;
    *width = w;
    *height = h;
    bgra = NULL;
    ok = true;
wic_end:
    if (!ok) { LOGW("Windows image decoder failed (%s, HRESULT=0x%08lx)", mime, (unsigned long) hr); }
    free(bgra);
    if (converter) { IWICFormatConverter_Release(converter); }
    if (frame) { IWICBitmapFrameDecode_Release(frame); }
    if (decoder) { IWICBitmapDecoder_Release(decoder); }
    if (stream) { IWICStream_Release(stream); }
    if (factory) { IWICImagingFactory_Release(factory); }
    if (SUCCEEDED(initialized)) { CoUninitialize(); }
    return ok;
#else
    enum AVCodecID id;
    if (!strcmp(mime, "image/png")) { id = AV_CODEC_ID_PNG; }
    else if (!strcmp(mime, "image/jpeg") || !strcmp(mime, "image/jpg")) { id = AV_CODEC_ID_MJPEG; }
    else if (!strcmp(mime, "image/bmp")) { id = AV_CODEC_ID_BMP; }
    else if (!strcmp(mime, "image/webp")) { id = AV_CODEC_ID_WEBP; }
    else if (!strcmp(mime, "image/gif")) { id = AV_CODEC_ID_GIF; }
    else { return false; }
    if (!size || size > INT_MAX - AV_INPUT_BUFFER_PADDING_SIZE) { return false; }
    const AVCodec *codec = avcodec_find_decoder(id);
    AVCodecContext *ctx = codec ? avcodec_alloc_context3(codec) : NULL;
    AVFrame *frame = av_frame_alloc();
    AVPacket *packet = av_packet_alloc();
    struct SwsContext *sws = NULL;
    uint8_t *bgra = NULL;
    bool ok = false;
    if (!ctx || !frame || !packet || avcodec_open2(ctx, codec, NULL) < 0
            || av_new_packet(packet, (int) size) < 0) {
        LOGW("Clipboard image decoder initialization failed (%s)", mime);
        goto end;
    }
    // The decoder may read padding beyond the payload: av_new_packet zeros it.
    memcpy(packet->data, data, size);
    ctx->max_pixels = 256u * 1024u * 1024u / 4;
    int sent = avcodec_send_packet(ctx, packet);
    int decoded = sent < 0 ? sent : avcodec_receive_frame(ctx, frame);
    if (decoded < 0
            || frame->width <= 0 || frame->height <= 0
            || (uint64_t) frame->width * frame->height > (uint64_t) ctx->max_pixels) {
        LOGW("Clipboard image decode failed (%s, code=%d, dimensions=%dx%d)", mime, decoded, frame->width, frame->height);
        goto end;
    }
    size_t bytes = (size_t) frame->width * frame->height * 4;
    bgra = malloc(bytes);
    if (!bgra) { goto end; }
    sws = sws_getContext(frame->width, frame->height, frame->format,
                         frame->width, frame->height, AV_PIX_FMT_BGRA,
                         SWS_POINT, NULL, NULL, NULL);
    if (!sws) { LOGW("Clipboard pixel converter initialization failed"); goto end; }
    uint8_t *dst[] = { bgra, NULL, NULL, NULL };
    int strides[] = { frame->width * 4, 0, 0, 0 };
    if (sws_scale(sws, (const uint8_t *const *) frame->data, frame->linesize,
                  0, frame->height, dst, strides) != frame->height) { goto end; }
    *pixels = bgra;
    *width = frame->width;
    *height = frame->height;
    bgra = NULL;
    ok = true;
end:
    free(bgra);
    sws_freeContext(sws);
    av_packet_free(&packet);
    av_frame_free(&frame);
    avcodec_free_context(&ctx);
    return ok;
#endif
}

bool
sc_image_bmp_to_jpeg(const uint8_t *bmp_data, size_t bmp_size,
                     uint8_t **out_data, size_t *out_size) {
    if (!bmp_data || bmp_size == 0 || bmp_size > INT_MAX) {
        return false;
    }

    // Decode the BMP
    const AVCodec *decoder = avcodec_find_decoder(AV_CODEC_ID_BMP);
    if (!decoder) {
        LOGE("Could not find BMP decoder");
        return false;
    }

    AVCodecContext *dec_ctx = avcodec_alloc_context3(decoder);
    if (!dec_ctx) {
        return false;
    }

    bool ok = false;
    AVFrame *dec_frame = NULL;
    AVPacket *pkt = NULL;
    AVFrame *enc_frame = NULL;
    AVCodecContext *enc_ctx = NULL;
    struct SwsContext *sws = NULL;
    AVPacket *out_pkt = NULL;

    if (avcodec_open2(dec_ctx, decoder, NULL) < 0) {
        LOGE("Could not open BMP decoder");
        goto end;
    }

    pkt = av_packet_alloc();
    if (!pkt) {
        goto end;
    }
    // The BMP decoder processes the packet synchronously in avcodec_send_packet
    // and does not keep a reference to the data afterwards (pkt->buf is NULL).
    pkt->data = (uint8_t *) bmp_data;
    pkt->size = (int) bmp_size;

    dec_frame = av_frame_alloc();
    if (!dec_frame) {
        goto end;
    }

    if (avcodec_send_packet(dec_ctx, pkt) < 0) {
        LOGE("Could not send BMP data to decoder");
        goto end;
    }
    if (avcodec_receive_frame(dec_ctx, dec_frame) < 0) {
        LOGE("Could not decode BMP data");
        goto end;
    }

    // Encode the JPEG
    const AVCodec *encoder = avcodec_find_encoder(AV_CODEC_ID_MJPEG);
    if (!encoder) {
        LOGE("Could not find MJPEG encoder");
        goto end;
    }

    enc_ctx = avcodec_alloc_context3(encoder);
    if (!enc_ctx) {
        goto end;
    }
    enc_ctx->width = dec_frame->width;
    enc_ctx->height = dec_frame->height;
    enc_ctx->time_base = (AVRational) {1, 25};
    enc_ctx->pix_fmt = AV_PIX_FMT_YUVJ420P;
    enc_ctx->flags |= AV_CODEC_FLAG_QSCALE;
    enc_ctx->global_quality = SC_JPEG_QUALITY * FF_QUALITY_SCALE;

    if (avcodec_open2(enc_ctx, encoder, NULL) < 0) {
        LOGE("Could not open MJPEG encoder");
        goto end;
    }

    enc_frame = av_frame_alloc();
    if (!enc_frame) {
        goto end;
    }
    enc_frame->format = AV_PIX_FMT_YUVJ420P;
    enc_frame->width = dec_frame->width;
    enc_frame->height = dec_frame->height;
    if (av_frame_get_buffer(enc_frame, 32) < 0) {
        LOGE("Could not allocate frame buffer for JPEG encoding");
        goto end;
    }

    sws = sws_getContext(dec_frame->width, dec_frame->height, dec_frame->format,
                         enc_frame->width, enc_frame->height,
                         AV_PIX_FMT_YUVJ420P, SWS_BILINEAR, NULL, NULL, NULL);
    if (!sws) {
        LOGE("Could not create pixel format converter");
        goto end;
    }
    sws_scale(sws, (const uint8_t *const *) dec_frame->data,
              dec_frame->linesize, 0, dec_frame->height,
              enc_frame->data, enc_frame->linesize);

    if (avcodec_send_frame(enc_ctx, enc_frame) < 0) {
        LOGE("Could not send frame to JPEG encoder");
        goto end;
    }

    out_pkt = av_packet_alloc();
    if (!out_pkt) {
        goto end;
    }
    if (avcodec_receive_packet(enc_ctx, out_pkt) < 0) {
        LOGE("Could not encode JPEG data");
        goto end;
    }

    uint8_t *jpeg_data = malloc(out_pkt->size);
    if (!jpeg_data) {
        goto end;
    }
    memcpy(jpeg_data, out_pkt->data, out_pkt->size);
    *out_data = jpeg_data;
    *out_size = out_pkt->size;
    ok = true;

end:
    if (out_pkt) {
        av_packet_free(&out_pkt);
    }
    if (sws) {
        sws_freeContext(sws);
    }
    if (enc_frame) {
        av_frame_free(&enc_frame);
    }
    if (enc_ctx) {
        avcodec_free_context(&enc_ctx);
    }
    if (dec_frame) {
        av_frame_free(&dec_frame);
    }
    if (pkt) {
        av_packet_free(&pkt);
    }
    avcodec_free_context(&dec_ctx);
    return ok;
}
