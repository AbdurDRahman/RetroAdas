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

/* ---- Test 3: complementary-filter tilt --------------------------------- */

#define RAD_TO_DEG_F 57.29577951308232f

void tilt_filter_init(tilt_state_t *st)
{
    st->pitch = st->roll = 0.0f;
    st->pitch0 = st->roll0 = 0.0f;
    st->n = 0;
    st->calibrated = false;
}

float tilt_filter_update(const tilt_cfg_t *cfg, tilt_state_t *st,
                         const imu_sample_t *s)
{
    float pitch_acc = atan2f(s->ay, sqrtf(s->ax * s->ax + s->az * s->az)) * RAD_TO_DEG_F;
    float roll_acc = atan2f(-s->ax, sqrtf(s->ay * s->ay + s->az * s->az)) * RAD_TO_DEG_F;

    if (st->n == 0) {
        /* seed with the accelerometer so the gyro path doesn't start from 0 */
        st->pitch = pitch_acc;
        st->roll = roll_acc;
    } else {
        float a = cfg->alpha;
        st->pitch = a * (st->pitch + s->gx * cfg->dt) + (1.0f - a) * pitch_acc;
        st->roll = a * (st->roll + s->gy * cfg->dt) + (1.0f - a) * roll_acc;
    }

    if (st->n < UINT16_MAX) {
        st->n++;
    }

    if (!st->calibrated) {
        if (st->n >= cfg->calib_samples) {
            st->pitch0 = st->pitch;
            st->roll0 = st->roll;
            st->calibrated = true;
        }
        return 0.0f;
    }

    float dp = fabsf(st->pitch - st->pitch0);
    float dr = fabsf(st->roll - st->roll0);
    return dp > dr ? dp : dr;
}
