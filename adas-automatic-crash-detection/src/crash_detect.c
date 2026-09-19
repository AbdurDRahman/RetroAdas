/*
 * crash_detect.c — RetroADAS Feature 3 detection primitives.
 * See crash_detect.h for the portability rules and info.md for the math.
 */
#include "crash_detect.h"

/* ---- Test 2: G-force banding ------------------------------------------ */

gforce_band_t gforce_band_update(const gforce_cfg_t *cfg,
                                 const imu_sample_t *s,
                                 float *out_mag)
{
    float mag = imu_accel_mag(s);
    if (out_mag) {
        *out_mag = mag;
    }
    if (mag < cfg->mild_g) {
        return GFORCE_NONE;
    }
    if (mag < cfg->moderate_g) {
        return GFORCE_MILD;
    }
    if (mag < cfg->severe_g) {
        return GFORCE_MODERATE;
    }
    return GFORCE_SEVERE;
}
