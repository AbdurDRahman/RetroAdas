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

#ifdef __cplusplus
}
#endif

#endif /* CRASH_DETECT_H */
