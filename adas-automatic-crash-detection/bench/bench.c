/*
 * bench.c — host-side harness for src/crash_detect.c.
 *
 * Reads one shard exported by export_events.py, runs every detector over
 * every event, and writes:
 *
 *   1. a binary results file (per-event peaks, read by run_bench.py) so the
 *      threshold sweeps happen in Python without re-running C, and
 *   2. one JSON line on stdout with ns/sample for each detector.
 *
 * Usage: bench <shard.bin> <out.bin>
 *
 * Results layout (little-endian):
 *   uint32 magic 0x56435232 ("VCR2"), uint32 n_events, uint32 n_alpha,
 *   float alpha[n_alpha]
 *   per event:
 *     int64 event_id, int32 label, int32 n_imu, int32 n_speed
 *     float peak_g                       Test 2: max ‖A‖g
 *     float peak_gyro                    max ‖Ω‖ (deg/s), for reference
 *     float peak_tilt_rel[n_alpha]       Test 3: max relative tilt, deg
 *     float peak_tilt_abs[n_alpha]       Test 3: max absolute tilt, deg
 *     float peak_delta                   Test 4: max |Δ‖A‖| over the event
 *     float sec_peak_delta[n_speed]      Test 4: max |Δ‖A‖| per GPS second
 */
#define _POSIX_C_SOURCE 199309L
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "../src/crash_detect.h"

#define IN_MAGIC 0x56435231u
#define OUT_MAGIC 0x56435232u

/* 0.90–0.98 is the info.md range; 0.99/0.995 are the 100 Hz equivalents of
 * that range (same time constant τ = α·Δt/(1-α) as at 52 Hz). */
static const float ALPHAS[] = { 0.90f, 0.92f, 0.94f, 0.96f, 0.98f, 0.99f, 0.995f };
#define N_ALPHA ((uint32_t)(sizeof(ALPHAS) / sizeof(ALPHAS[0])))

#define IMU_HZ 100          /* VZCrash data rate */
#define CALIB_SAMPLES 100   /* 1 s mounting-baseline window */

typedef struct {
    int64_t event_id;
    int32_t label, n_imu, n_speed, reserved;
} event_hdr_t;

typedef struct {
    event_hdr_t hdr;
    const float *acc;   /* [n_imu][3] */
    const float *gyr;   /* [n_imu][3] */
    const float *spd;   /* [n_speed] */
} event_t;

static double now_ns(void)
{
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return ts.tv_sec * 1e9 + ts.tv_nsec;
}

static inline imu_sample_t sample_at(const event_t *e, int32_t i)
{
    imu_sample_t s = { e->acc[i * 3], e->acc[i * 3 + 1], e->acc[i * 3 + 2],
                       e->gyr[i * 3], e->gyr[i * 3 + 1], e->gyr[i * 3 + 2] };
    return s;
}

/* ---- load shard into memory and index the events ----------------------- */

static uint8_t *read_file(const char *path, size_t *len)
{
    FILE *f = fopen(path, "rb");
    if (!f) { perror(path); exit(1); }
    fseek(f, 0, SEEK_END);
    *len = (size_t)ftell(f);
    fseek(f, 0, SEEK_SET);
    uint8_t *buf = malloc(*len);
    if (!buf || fread(buf, 1, *len, f) != *len) {
        fprintf(stderr, "read failed: %s\n", path);
        exit(1);
    }
    fclose(f);
    return buf;
}

static event_t *index_events(const uint8_t *buf, size_t len, uint32_t *n_out)
{
    uint32_t magic, n;
    memcpy(&magic, buf, 4);
    memcpy(&n, buf + 4, 4);
    if (magic != IN_MAGIC) { fprintf(stderr, "bad magic\n"); exit(1); }

    event_t *ev = calloc(n, sizeof(event_t));
    size_t off = 8;
    for (uint32_t k = 0; k < n; k++) {
        memcpy(&ev[k].hdr, buf + off, sizeof(event_hdr_t));
        off += sizeof(event_hdr_t);
        ev[k].acc = (const float *)(buf + off);
        off += (size_t)ev[k].hdr.n_imu * 3 * 4;
        ev[k].gyr = (const float *)(buf + off);
        off += (size_t)ev[k].hdr.n_imu * 3 * 4;
        ev[k].spd = (const float *)(buf + off);
        off += (size_t)ev[k].hdr.n_speed * 4;
        if (off > len) { fprintf(stderr, "truncated shard\n"); exit(1); }
    }
    *n_out = n;
    return ev;
}

/* ---- per-event feature extraction ------------------------------------ */

static void run_event(const event_t *e, FILE *out)
{
    const int32_t n = e->hdr.n_imu;
    const int32_t ns = e->hdr.n_speed;

    gforce_cfg_t gcfg = GFORCE_CFG_LITERATURE;
    tilt_cfg_t tcfg[N_ALPHA];
    tilt_state_t tst[N_ALPHA];
    for (uint32_t a = 0; a < N_ALPHA; a++) {
        tcfg[a].alpha = ALPHAS[a];
        tcfg[a].dt = 1.0f / IMU_HZ;
        tcfg[a].calib_samples = CALIB_SAMPLES;
        tilt_filter_init(&tst[a]);
    }
    delta_cfg_t dcfg = { 0.0f, 0.0f };
    delta_state_t dst;
    delta_speed_init(&dst);

    float peak_g = 0, peak_gyro = 0, peak_delta = 0;
    float peak_rel[N_ALPHA] = { 0 }, peak_abs[N_ALPHA] = { 0 };
    float *sec_peak = calloc((size_t)ns, sizeof(float));

    for (int32_t i = 0; i < n; i++) {
        imu_sample_t s = sample_at(e, i);
        int32_t sec = i / IMU_HZ;
        if (sec >= ns) sec = ns - 1;
        if (i % IMU_HZ == 0 && i / IMU_HZ < ns) {
            delta_speed_feed_speed(&dcfg, &dst, e->spd[i / IMU_HZ]);
        }

        float mag;
        gforce_band_update(&gcfg, &s, &mag);
        if (mag > peak_g) peak_g = mag;
        float gm = imu_gyro_mag(&s);
        if (gm > peak_gyro) peak_gyro = gm;

        for (uint32_t a = 0; a < N_ALPHA; a++) {
            float rel = tilt_filter_update(&tcfg[a], &tst[a], &s);
            if (rel > peak_rel[a]) peak_rel[a] = rel;
            float ab = fabsf(tst[a].pitch);
            float r2 = fabsf(tst[a].roll);
            if (r2 > ab) ab = r2;
            if (ab > peak_abs[a]) peak_abs[a] = ab;
        }

        float d;
        delta_speed_update(&dcfg, &dst, &s, &d, NULL);
        if (d > peak_delta) peak_delta = d;
        if (d > sec_peak[sec]) sec_peak[sec] = d;
    }

    fwrite(&e->hdr.event_id, 8, 1, out);
    fwrite(&e->hdr.label, 4, 1, out);
    fwrite(&n, 4, 1, out);
    fwrite(&ns, 4, 1, out);
    fwrite(&peak_g, 4, 1, out);
    fwrite(&peak_gyro, 4, 1, out);
    fwrite(peak_rel, 4, N_ALPHA, out);
    fwrite(peak_abs, 4, N_ALPHA, out);
    fwrite(&peak_delta, 4, 1, out);
    fwrite(sec_peak, 4, (size_t)ns, out);
    free(sec_peak);
}

/* ---- timing passes ----------------------------------------------------
 * Each pass runs one detector alone over every sample of every event and
 * folds the result into a checksum so the compiler can't drop the work.
 * The "baseline" pass only loads the sample, so subtracting it isolates
 * the detector's own cost from memory traffic. */

static volatile float g_sink;

static double time_pass(const event_t *ev, uint32_t n_ev, int which,
                        uint64_t *n_samples)
{
    gforce_cfg_t gcfg = GFORCE_CFG_LITERATURE;
    tilt_cfg_t tcfg = { 0.96f, 1.0f / IMU_HZ, CALIB_SAMPLES };
    delta_cfg_t dcfg = { 0.5f, 0.0f };
    float sink = 0;
    uint64_t cnt = 0;

    double t0 = now_ns();
    for (uint32_t k = 0; k < n_ev; k++) {
        const event_t *e = &ev[k];
        tilt_state_t tst;
        delta_state_t dst;
        tilt_filter_init(&tst);
        delta_speed_init(&dst);
        for (int32_t i = 0; i < e->hdr.n_imu; i++) {
            imu_sample_t s = sample_at(e, i);
            switch (which) {
            case 0:
                sink += s.ax + s.gz;
                break;
            case 1: {
                float m;
                sink += (float)gforce_band_update(&gcfg, &s, &m) + m;
                break;
            }
            case 2:
                sink += tilt_filter_update(&tcfg, &tst, &s);
                break;
            case 3: {
                if (i % IMU_HZ == 0 && i / IMU_HZ < e->hdr.n_speed) {
                    delta_speed_feed_speed(&dcfg, &dst, e->spd[i / IMU_HZ]);
                }
                float d;
                sink += (float)delta_speed_update(&dcfg, &dst, &s, &d, NULL) + d;
                break;
            }
            }
        }
        cnt += (uint64_t)e->hdr.n_imu;
    }
    double t1 = now_ns();
    g_sink = sink;
    *n_samples = cnt;
    return (t1 - t0) / (double)cnt;
}

int main(int argc, char **argv)
{
    if (argc != 3) {
        fprintf(stderr, "usage: %s <shard.bin> <out.bin>\n", argv[0]);
        return 2;
    }
    size_t len;
    uint8_t *buf = read_file(argv[1], &len);
    uint32_t n_ev;
    event_t *ev = index_events(buf, len, &n_ev);

    FILE *out = fopen(argv[2], "wb");
    if (!out) { perror(argv[2]); return 1; }
    uint32_t magic = OUT_MAGIC, na = N_ALPHA;
    fwrite(&magic, 4, 1, out);
    fwrite(&n_ev, 4, 1, out);
    fwrite(&na, 4, 1, out);
    fwrite(ALPHAS, 4, N_ALPHA, out);
    for (uint32_t k = 0; k < n_ev; k++) {
        run_event(&ev[k], out);
    }
    fclose(out);

    /* timing: best of 3 passes each, to shrug off scheduler noise */
    const char *names[] = { "baseline", "gforce_band", "tilt_filter", "delta_speed" };
    double best[4];
    uint64_t n_samples = 0;
    for (int w = 0; w < 4; w++) {
        best[w] = 1e30;
        for (int rep = 0; rep < 3; rep++) {
            double t = time_pass(ev, n_ev, w, &n_samples);
            if (t < best[w]) best[w] = t;
        }
    }
    printf("{\"n_events\": %u, \"n_samples\": %llu, \"ns_per_sample\": {",
           n_ev, (unsigned long long)n_samples);
    for (int w = 0; w < 4; w++) {
        printf("%s\"%s\": %.2f", w ? ", " : "", names[w], best[w]);
    }
    printf("}}\n");

    free(ev);
    free(buf);
    return 0;
}
