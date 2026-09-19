/*
 * crash_detect.h — RetroADAS Feature 3 detection primitives.
 *
 * Portable C99 intended for the ESP32 controller (Arduino / ESP-IDF) but with
 * no platform dependencies, so the same file is compiled by the host-side
 * benchmark in ../bench. Rules for everything in here:
 *
 *   - float only (ESP32 FPU is single precision; double is software-emulated)
 *   - no heap, no globals — all state lives in caller-owned structs
 *   - one call per IMU sample, O(1) work per call
 *
 * Formulas and thresholds come from info.md; see that file for citations.
 */
#ifndef CRASH_DETECT_H
#define CRASH_DETECT_H

#include <math.h>
#include <stdbool.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* One IMU reading. Accelerometer in g (gravity included, so ~1 at rest),
 * gyroscope in deg/s. Same units as the VZCrash dataset. */
typedef struct {
    float ax, ay, az;
    float gx, gy, gz;
} imu_sample_t;

/* ‖A‖ = √(ax²+ay²+az²), in g. Orientation-independent. */
static inline float imu_accel_mag(const imu_sample_t *s)
{
    return sqrtf(s->ax * s->ax + s->ay * s->ay + s->az * s->az);
}

/* ‖Ω‖ = √(gx²+gy²+gz²), in deg/s. */
static inline float imu_gyro_mag(const imu_sample_t *s)
{
    return sqrtf(s->gx * s->gx + s->gy * s->gy + s->gz * s->gz);
}

/* ------------------------------------------------------------------------
 * Layer 2 / Test 2 — G-force magnitude banding (Razali et al. 2021)
 *
 *   No accident  ‖A‖g <  mild_g      (5 G in the literature)
 *   Mild         ‖A‖g <  moderate_g  (20 G)
 *   Moderate     ‖A‖g <  severe_g    (40 G)
 *   Severe       otherwise
 * ---------------------------------------------------------------------- */
typedef enum {
    GFORCE_NONE = 0,
    GFORCE_MILD = 1,
    GFORCE_MODERATE = 2,
    GFORCE_SEVERE = 3,
} gforce_band_t;

typedef struct {
    float mild_g;
    float moderate_g;
    float severe_g;
} gforce_cfg_t;

#define GFORCE_CFG_LITERATURE { 5.0f, 20.0f, 40.0f }

/* Stateless: classify one sample. If out_mag is non-NULL, ‖A‖g is stored
 * there so the caller can keep a running peak without recomputing. */
gforce_band_t gforce_band_update(const gforce_cfg_t *cfg,
                                 const imu_sample_t *s,
                                 float *out_mag);

/* ------------------------------------------------------------------------
 * Layer 2 / Test 3 — complementary-filter tilt (Ibrahim et al. 2016)
 *
 *   θ_accel = atan2( ay, √(ax²+az²) )     pitch, rotation about X, rate gx
 *   φ_accel = atan2(-ax, √(ay²+az²) )     roll,  rotation about Y, rate gy
 *   θ[n]    = α·(θ[n-1] + gx·Δt) + (1-α)·θ_accel[n]
 *
 * Angles in degrees. The sensor is rarely mounted level, so the filter also
 * latches a mounting baseline after `calib_samples` samples; the rollover
 * test uses the angle *relative* to that baseline.
 * ---------------------------------------------------------------------- */
typedef struct {
    float alpha;          /* gyro trust, 0.9–0.98 */
    float dt;             /* seconds per sample: 1/52 on device, 1/100 VZCrash */
    uint16_t calib_samples; /* samples before the mounting baseline is latched */
} tilt_cfg_t;

typedef struct {
    float pitch, roll;    /* fused absolute angle, deg */
    float pitch0, roll0;  /* mounting baseline, valid once calibrated */
    uint16_t n;           /* samples seen (saturating) */
    bool calibrated;
} tilt_state_t;

void tilt_filter_init(tilt_state_t *st);

/* Fuse one sample. Returns the larger of |pitch-pitch0| and |roll-roll0|
 * (deg) once calibrated, 0 before — i.e. the value to compare against the
 * rollover threshold. Absolute angles stay readable in the state. */
float tilt_filter_update(const tilt_cfg_t *cfg, tilt_state_t *st,
                         const imu_sample_t *s);

#ifdef __cplusplus
}
#endif

#endif /* CRASH_DETECT_H */
