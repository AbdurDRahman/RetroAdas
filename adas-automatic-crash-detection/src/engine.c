/*
 * engine.c — RetroADAS Feature 3 detection engine. See engine.h / algorithm.md.
 */
#include "engine.h"
#include "crash_model.h"

#include <math.h>
#include <string.h>

#define G_MPS2 9.80665f

/* ---------------------------------------------------------------------- */

void engine_cfg_default(engine_cfg_t *c)
{
    memset(c, 0, sizeof(*c));
    c->dt = 0.01f;
    c->max_g = 20.0f;
    c->max_dps = 2500.0f;
    c->stuck_samples = 100;
    c->grav_tau_s = 1.0f;
    c->grav_freeze_g = 0.5f;
    c->pretrig_g = 1.8f;
    c->capture_samples = 50;
    c->override_sus50_g = 2.5f;
    c->override_peak_g = 4.0f;
    c->override_peak_sus_g = 1.2f;
    c->score_threshold = CRASH_MODEL_THRESHOLD;
    c->roll_alpha = 0.99f;
    c->roll_deg = 60.0f;
    c->roll_hold = 100;
    c->roll_calib = 200;
    /* road branch: swept on PVS + RoadSens (eval/sweep_road.py, evaluation.md) */
    c->road_trig_g = 0.30f;
    c->road_quiet_std_g = 0.15f;
    c->pot_drop_g = 0.35f;
    c->pot_impact_g = 0.40f;
    c->pot_order_ms = 250;
    c->pot_width_ms = 400;
    c->road_capture = 60;
    c->pot_ratelimit_ms = 2000;
    c->alarm_ms = 15000;
    c->lockout_ms = 60000;
}

void engine_init(engine_t *e, const engine_cfg_t *cfg)
{
    memset(e, 0, sizeof(*e));
    e->cfg = *cfg;
    e->grav_alpha = cfg->dt / (cfg->grav_tau_s + cfg->dt);
    e->tcfg90.alpha = 0.90f;
    e->tcfg90.dt = cfg->dt;
    e->tcfg90.calib_samples = cfg->roll_calib;
    e->tcfg99.alpha = cfg->roll_alpha;
    e->tcfg99.dt = cfg->dt;
    e->tcfg99.calib_samples = cfg->roll_calib;
    tilt_filter_init(&e->t90);
    tilt_filter_init(&e->t99);
    e->speed_kmh = -1.0f;
    e->state = ST_IDLE;
}

void engine_cancel(engine_t *e)
{
    e->cancel_req = true;
}

void engine_feed_speed(engine_t *e, float kmh, uint32_t t_ms)
{
    e->speed_kmh = kmh;
    e->speed_ms = t_ms;
}

/* ---- scoring ----------------------------------------------------------- */

static inline float fl1p(float x)
{
    return log1pf(x > 0.0f ? x : 0.0f);
}

float engine_score(const crash_feat_t *f)
{
    const float *v = (const float *)f;
    float s = CRASH_MODEL_BIAS;
    for (int i = 0; i < CRASH_NFEAT; i++) {
        s += CRASH_MODEL_W[i] * fl1p(v[i]);
    }
    return s;
}

/* The two physics overrides. Either one fires a CRASH regardless of the score:
 *   1. 50 ms mean horizontal accel >= 2.5 g — twice the tyre friction limit,
 *      no braking manoeuvre can do it, potholes do it vertically not horizontally.
 *   2. peak >= 4 g AND 50 ms mean horizontal >= 1.2 g — a very large impact
 *      whose horizontal part is still above what tyres can deliver (catches
 *      crashes with a big vertical component: kerbs, ditches, under-rides). */
static inline bool physics_override(const crash_feat_t *f, const engine_cfg_t *cfg)
{
    return f->sus50_h >= cfg->override_sus50_g ||
           (f->peak_a >= cfg->override_peak_g && f->sus50_h >= cfg->override_peak_sus_g);
}

uint8_t engine_tier(const crash_feat_t *f, const engine_cfg_t *cfg)
{
    if (physics_override(f, cfg) || f->peak_a >= cfg->override_peak_g) {
        return 3;
    }
    if (f->peak_a >= 3.0f) {
        return 2;
    }
    return 1;
}

/* ---- helpers ----------------------------------------------------------- */

static bool sample_plausible(const engine_cfg_t *c, const imu_sample_t *s)
{
    const float *v = (const float *)s;
    for (int i = 0; i < 6; i++) {
        if (v[i] != v[i]) {               /* NaN */
            return false;
        }
    }
    if (fabsf(s->ax) > c->max_g || fabsf(s->ay) > c->max_g || fabsf(s->az) > c->max_g) {
        return false;
    }
    if (fabsf(s->gx) > c->max_dps || fabsf(s->gy) > c->max_dps || fabsf(s->gz) > c->max_dps) {
        return false;
    }
    return true;
}

static void enter_state(engine_t *e, engine_state_t st, uint32_t t_ms)
{
    e->state = st;
    e->state_since_ms = t_ms;
}

/* ---- the per-sample update ------------------------------------------- */

engine_event_t engine_update(engine_t *e, const imu_sample_t *in, uint32_t t_ms)
{
    const engine_cfg_t *c = &e->cfg;
    engine_event_t ev = EV_NONE;
    imu_sample_t s = *in;

    e->now_ms = t_ms;
    e->n_samples++;

    /* 0. plausibility clamp ------------------------------------------- */
    if (!sample_plausible(c, &s)) {
        e->n_rejected++;
        if (!e->have_prev) {
            return EV_NONE;               /* nothing valid yet */
        }
        s = e->prev;
    } else {
        if (e->have_prev && memcmp(&s, &e->prev, sizeof(s)) == 0) {
            if (e->stuck < 0xFFFF) e->stuck++;
        } else {
            e->stuck = 0;
        }
        e->prev = s;
        e->have_prev = true;
    }
    if (e->stuck >= c->stuck_samples) {
        if (!e->fault) {
            e->fault = true;
            return EV_FAULT;
        }
    } else {
        e->fault = false;
    }

    /* 1. gravity tracker + derived signals ----------------------------- */
    float amag = imu_accel_mag(&s);
    e->a_mag = amag;
    if (!e->grav_seeded) {
        e->gx = s.ax; e->gy = s.ay; e->gz = s.az;
        e->grav_seeded = true;
    } else if (fabsf(amag - 1.0f) <= c->grav_freeze_g) {
        float a = e->grav_alpha;
        e->gx += a * (s.ax - e->gx);
        e->gy += a * (s.ay - e->gy);
        e->gz += a * (s.az - e->gz);
    }
    float gn = sqrtf(e->gx * e->gx + e->gy * e->gy + e->gz * e->gz);
    if (gn < 0.2f) gn = 0.2f;             /* never divide by ~0 */
    float ux = e->gx / gn, uy = e->gy / gn, uz = e->gz / gn;

    float lx = s.ax - e->gx, ly = s.ay - e->gy, lz = s.az - e->gz;
    float av = lx * ux + ly * uy + lz * uz;
    float hx = lx - av * ux, hy = ly - av * uy, hz = lz - av * uz;
    float ah = sqrtf(hx * hx + hy * hy + hz * hz);
    float wv = s.gx * ux + s.gy * uy + s.gz * uz;
    float wx = s.gx - wv * ux, wy = s.gy - wv * uy, wz = s.gz - wv * uz;
    float wh = sqrtf(wx * wx + wy * wy + wz * wz);
    e->a_v = av; e->a_h = ah; e->w_h = wh;

    /* a_lin ring + running 10/30 sums (for dv100 / dv300) */
    {
        uint16_t i10 = (uint16_t)((e->lh + ENG_HIST - 10) % ENG_HIST);
        uint16_t i30 = (uint16_t)((e->lh + ENG_HIST - 30) % ENG_HIST);
        if (e->ln >= 10) { e->s10x -= e->lx[i10]; e->s10y -= e->ly[i10]; e->s10z -= e->lz[i10]; }
        if (e->ln >= 30) { e->s30x -= e->lx[i30]; e->s30y -= e->ly[i30]; e->s30z -= e->lz[i30]; }
        e->lx[e->lh] = lx; e->ly[e->lh] = ly; e->lz[e->lh] = lz;
        e->s10x += lx; e->s10y += ly; e->s10z += lz;
        e->s30x += lx; e->s30y += ly; e->s30z += lz;
        e->lh = (uint16_t)((e->lh + 1) % ENG_HIST);
        if (e->ln < ENG_HIST) e->ln++;
    }
    /* a_h 5-sample moving mean */
    e->h5sum += ah - e->h5[e->h5i];
    e->h5[e->h5i] = ah;
    e->h5i = (uint8_t)((e->h5i + 1) % 5);
    float sus50 = e->h5sum * 0.2f;
    float dv100 = sqrtf(e->s10x * e->s10x + e->s10y * e->s10y + e->s10z * e->s10z) * G_MPS2 * c->dt;
    float dv300 = sqrtf(e->s30x * e->s30x + e->s30y * e->s30y + e->s30z * e->s30z) * G_MPS2 * c->dt;

    /* tilt filters (0.90 feeds the crash score, 0.99 is the rollover branch) */
    float tilt90 = tilt_filter_update(&e->tcfg90, &e->t90, &s);
    float tilt99 = tilt_filter_update(&e->tcfg99, &e->t99, &s);
    e->roll_rel = tilt99;

    /* a_v ring: running sum/sumsq over the last 50 samples (quiet gate) */
    {
        uint16_t i50 = (uint16_t)((e->avh + ENG_AV_HIST - 50) % ENG_AV_HIST);
        if (e->avn >= 50) {
            float o = e->av[i50];
            e->av_sum -= o; e->av_sq -= o * o;
        }
        e->av[e->avh] = av;
        e->av_sum += av; e->av_sq += av * av;
        e->avh = (uint16_t)((e->avh + 1) % ENG_AV_HIST);
        if (e->avn < ENG_AV_HIST) e->avn++;
    }

    /* 2. crash branch -------------------------------------------------- */
    if (!e->cap_active && amag >= c->pretrig_g) {
        e->cap_active = true;
        e->cap_left = c->capture_samples;
        memset(&e->feat, 0, sizeof(e->feat));
        e->max_ah = 0.0f;
    }
    if (e->cap_active) {
        crash_feat_t *f = &e->feat;
        if (amag > f->peak_a) f->peak_a = amag;
        if (sus50 > f->sus50_h) f->sus50_h = sus50;
        if (dv100 > f->dv100) f->dv100 = dv100;
        if (dv300 > f->dv300) f->dv300 = dv300;
        if (wh > f->peak_wh) f->peak_wh = wh;
        if (tilt90 > f->tilt90) f->tilt90 = tilt90;
        if (fabsf(av) > f->peak_av) f->peak_av = fabsf(av);
        if (ah > e->max_ah) e->max_ah = ah;
        if (--e->cap_left == 0) {
            e->cap_active = false;
            e->n_captures++;
            f->vratio = f->peak_av / (e->max_ah > 0.05f ? e->max_ah : 0.05f);
            e->last_score = engine_score(f);
            e->last_tier = engine_tier(f, c);
            e->last_override = physics_override(f, c);
            bool crash = e->last_override || e->last_score >= c->score_threshold;
            if (crash && e->state == ST_IDLE) {
                e->alarm_cause = EV_CRASH;
                enter_state(e, ST_ALARM, t_ms);
                ev = EV_CRASH;
            }
        }
    }

    /* 3. rollover branch ----------------------------------------------- */
    if (tilt99 >= c->roll_deg) {
        if (e->roll_cnt < 0xFFFF) e->roll_cnt++;
    } else {
        e->roll_cnt = 0;
    }
    if (e->roll_cnt == c->roll_hold && e->state == ST_IDLE && ev == EV_NONE) {
        e->alarm_cause = EV_ROLLOVER;
        enter_state(e, ST_ALARM, t_ms);
        ev = EV_ROLLOVER;
    }

    /* 4. road branch (runs alongside the crash capture; a crash that fires
     *    moves the state out of IDLE and drops any pending road capture) --- */
    if (e->state == ST_IDLE && ev == EV_NONE) {
        if (!e->road_active) {
            bool quiet = false;
            if (e->avn >= 50) {
                float mean = e->av_sum / 50.0f;
                float var = e->av_sq / 50.0f - mean * mean;
                /* exclude the triggering sample itself from the "before" std */
                quiet = var < c->road_quiet_std_g * c->road_quiet_std_g * 1.5f;
            }
            if (fabsf(av) >= c->road_trig_g && quiet &&
                (t_ms - e->last_pothole_ms >= c->pot_ratelimit_ms || e->last_pothole_ms == 0)) {
                e->road_active = true;
                e->road_left = c->road_capture;
                e->rd_min = 0.0f; e->rd_max = 0.0f;
                e->rd_tmin = 0; e->rd_tmax = 0;
                e->rd_first = 0x7FFFFFFF; e->rd_last = -0x7FFFFFFF;
                /* look back 150 ms so a drop that preceded the trigger counts */
                for (int k = 15; k >= 1; k--) {
                    uint16_t idx = (uint16_t)((e->avh + ENG_AV_HIST - 1 - k) % ENG_AV_HIST);
                    if (e->avn <= k) continue;
                    float x = e->av[idx];
                    int32_t tr = -(int32_t)k * 10;
                    if (x < e->rd_min) { e->rd_min = x; e->rd_tmin = tr; }
                    if (x > e->rd_max) { e->rd_max = x; e->rd_tmax = tr; }
                    if (fabsf(x) >= 0.2f) {
                        if (tr < e->rd_first) e->rd_first = tr;
                        if (tr > e->rd_last) e->rd_last = tr;
                    }
                }
            }
        }
        if (e->road_active) {
            int32_t tr = (int32_t)(c->road_capture - e->road_left) * 10;
            if (av < e->rd_min) { e->rd_min = av; e->rd_tmin = tr; }
            if (av > e->rd_max) { e->rd_max = av; e->rd_tmax = tr; }
            if (fabsf(av) >= 0.2f) {
                if (tr < e->rd_first) e->rd_first = tr;
                if (tr > e->rd_last) e->rd_last = tr;
            }
            if (--e->road_left == 0) {
                e->road_active = false;
                int32_t width = e->rd_last - e->rd_first;
                bool drop_ok = e->rd_min <= -c->pot_drop_g;
                bool imp_ok = e->rd_max >= c->pot_impact_g;
                int32_t order = e->rd_tmax - e->rd_tmin;   /* >0: drop before impact */
                if (drop_ok && imp_ok && order > 0 && order <= (int32_t)c->pot_order_ms &&
                    width <= (int32_t)c->pot_width_ms) {
                    e->last_pot_severity = e->rd_max - e->rd_min;
                    e->last_pothole_ms = t_ms;
                    e->n_potholes++;
                    if (ev == EV_NONE) ev = EV_POTHOLE;
                } else if (imp_ok && (order < 0 || width > (int32_t)c->pot_width_ms)) {
                    e->n_bumps++;
                    if (ev == EV_NONE) ev = EV_SPEED_BUMP;
                }
            }
        }
    } else {
        e->road_active = false;
    }

    /* 5. alarm state machine ------------------------------------------- */
    if (e->state == ST_ALARM) {
        if (e->cancel_req) {
            e->cancel_req = false;
            enter_state(e, ST_LOCKOUT, t_ms);
            if (ev == EV_NONE) ev = EV_ALARM_CANCELLED;
        } else if (t_ms - e->state_since_ms >= c->alarm_ms) {
            enter_state(e, ST_LOCKOUT, t_ms);
            if (ev == EV_NONE) ev = EV_ALARM_CONFIRMED;
        }
    } else if (e->state == ST_LOCKOUT) {
        e->cancel_req = false;
        if (t_ms - e->state_since_ms >= c->lockout_ms) {
            enter_state(e, ST_IDLE, t_ms);
        }
    } else {
        e->cancel_req = false;
    }

    return ev;
}

/* ---- host batch entry -------------------------------------------------- */

uint32_t engine_batch(engine_t *e, const float *acc, const float *gyr,
                      uint32_t n, uint8_t *ev_out,
                      float *feat_out, uint32_t max_feat)
{
    uint32_t nf = 0;
    uint32_t caps0 = e->n_captures;
    float step_ms = e->cfg.dt * 1000.0f;
    for (uint32_t i = 0; i < n; i++) {
        imu_sample_t s = { acc[i * 3], acc[i * 3 + 1], acc[i * 3 + 2],
                           gyr[i * 3], gyr[i * 3 + 1], gyr[i * 3 + 2] };
        uint32_t t = e->now_ms + (uint32_t)step_ms;
        engine_event_t ev = engine_update(e, &s, t);
        if (ev_out) ev_out[i] = (uint8_t)ev;
        if (e->n_captures != caps0) {
            caps0 = e->n_captures;
            if (feat_out && nf < max_feat) {
                memcpy(feat_out + nf * CRASH_NFEAT, &e->feat, sizeof(crash_feat_t));
            }
            nf++;
        }
    }
    return nf;
}
