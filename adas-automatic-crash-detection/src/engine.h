/*
 * engine.h — RetroADAS Feature 3 detection engine (crash / rollover / pothole).
 *
 * One struct, one call per IMU sample, O(1) work, float only, no heap, no
 * globals. Same rules as crash_detect.h so the file compiles unchanged in
 * Arduino / ESP-IDF and in the host-side harness (eval/). Design: algorithm.md.
 *
 * Units: accelerometer in g (gravity included, ~1 at rest), gyroscope in
 * deg/s, time in milliseconds from the caller's monotonic clock.
 */
#ifndef ENGINE_H
#define ENGINE_H

#include <stdbool.h>
#include <stdint.h>

#include "crash_detect.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ---- events returned by engine_update() ------------------------------- */
typedef enum {
    EV_NONE = 0,
    EV_CRASH = 1,            /* collision detected -> alarm started            */
    EV_ROLLOVER = 2,         /* sustained tilt -> alarm started                */
    EV_POTHOLE = 3,          /* pothole signature (report to Jetson, no alarm) */
    EV_SPEED_BUMP = 4,       /* recognised and suppressed (diagnostic only)    */
    EV_ALARM_CANCELLED = 5,  /* driver pressed cancel during the countdown     */
    EV_ALARM_CONFIRMED = 6,  /* countdown elapsed -> notify Jetson             */
    EV_FAULT = 7,            /* sensor stuck / implausible for too long        */
} engine_event_t;

typedef enum {
    ST_IDLE = 0,
    ST_ALARM = 1,     /* buzzer on, countdown running */
    ST_LOCKOUT = 2,   /* after cancel/confirm; no new alarms for lockout_ms */
} engine_state_t;

/* ---- crash features (one row per capture) ------------------------------ */
#define CRASH_NFEAT 8
typedef struct {
    float peak_a;    /* max ‖a‖ (g)                                             */
    float sus50_h;   /* max 50 ms moving mean of horizontal linear accel (g)   */
    float dv100;     /* max ‖∫ a_lin dt‖ over 100 ms (m/s)                     */
    float dv300;     /* same over 300 ms (m/s)                                  */
    float vratio;    /* max |a_v| / max a_h  (vertical-dominated -> pothole)   */
    float peak_wh;   /* max pitch/roll rate (deg/s)                             */
    float tilt90;    /* peak relative tilt from the alpha=0.90 filter (deg)    */
    float peak_av;   /* max |a_v| (g)                                            */
} crash_feat_t;

/* ---- configuration ------------------------------------------------------ */
typedef struct {
    float dt;                  /* seconds per sample (0.01 at 100 Hz)          */

    /* plausibility clamp */
    float max_g;               /* reject |a_axis| above this (20)               */
    float max_dps;             /* reject |w_axis| above this (2500)             */
    uint16_t stuck_samples;    /* identical samples before FAULT (100)          */

    /* gravity tracker */
    float grav_tau_s;          /* LPF time constant (1.0)                       */
    float grav_freeze_g;       /* freeze update when |‖a‖-1| exceeds this (0.5) */

    /* crash branch */
    float pretrig_g;           /* ‖a‖ that opens a capture (1.8)                */
    uint16_t capture_samples;  /* capture length (50 = 500 ms)                  */
    float override_sus50_g;    /* physics override 1: sus50_h >= this (2.5)      */
    float override_peak_g;     /* physics override 2: peak_a >= this (4.0) ...  */
    float override_peak_sus_g; /*   ... AND sus50_h >= this (1.2, tyre limit)   */
    float score_threshold;     /* fitted score threshold (crash_model.h)        */

    /* rollover branch */
    float roll_alpha;          /* 0.99                                          */
    float roll_deg;            /* 60                                            */
    uint16_t roll_hold;        /* samples the angle must hold (100 = 1 s)       */
    uint16_t roll_calib;       /* samples before mounting baseline (200)        */

    /* road branch */
    float road_trig_g;         /* |a_v| that opens a road capture (0.30)        */
    float road_quiet_std_g;    /* pre-trigger std(a_v) must be below (0.15)     */
    float pot_drop_g;          /* min a_v must be <= -this (0.35)                */
    float pot_impact_g;        /* max a_v must be >= this (0.40)                 */
    uint16_t pot_order_ms;     /* drop must precede impact by <= this (250)     */
    uint16_t pot_width_ms;     /* event width must be <= this (400)             */
    uint16_t road_capture;     /* samples (60 = 600 ms)                         */
    uint32_t pot_ratelimit_ms; /* one pothole per (2000)                        */

    /* state machine */
    uint32_t alarm_ms;         /* cancel countdown (15000)                      */
    uint32_t lockout_ms;       /* after an alarm (60000)                        */
} engine_cfg_t;

/* Defaults from algorithm.md. score_threshold is filled from crash_model.h. */
void engine_cfg_default(engine_cfg_t *cfg);

/* ---- engine state ------------------------------------------------------- */
#define ENG_HIST 32            /* a_lin ring (>= 30 samples for dv300)         */
#define ENG_AV_HIST 64         /* a_v ring (>= 50 for quiet gate + 15 lookback)*/

typedef struct {
    engine_cfg_t cfg;

    /* clamp */
    imu_sample_t prev;
    bool have_prev;
    uint16_t stuck;
    uint32_t n_rejected;
    bool fault;

    /* gravity */
    float gx, gy, gz;          /* g_est vector (g)                              */
    bool grav_seeded;
    float grav_alpha;

    /* derived, last sample */
    float a_mag, a_v, a_h, w_h;

    /* a_lin ring for windowed dV */
    float lx[ENG_HIST], ly[ENG_HIST], lz[ENG_HIST];
    uint16_t lh;               /* head index                                    */
    uint16_t ln;               /* samples seen (saturating at ENG_HIST)        */
    float s10x, s10y, s10z;    /* running 10-sample sums                        */
    float s30x, s30y, s30z;    /* running 30-sample sums                        */
    float h5[5];               /* a_h 5-sample ring                             */
    uint8_t h5i; float h5sum;

    /* crash capture */
    bool cap_active;
    uint16_t cap_left;
    crash_feat_t feat;
    float max_ah;              /* for vratio                                    */
    float last_score;
    uint8_t last_tier;         /* 1 minor, 2 moderate, 3 severe                 */
    bool last_override;
    uint32_t n_captures;

    /* rollover */
    tilt_cfg_t tcfg90, tcfg99;
    tilt_state_t t90, t99;
    uint16_t roll_cnt;
    float roll_rel;            /* last relative tilt (deg), alpha=0.99          */

    /* road branch */
    float av[ENG_AV_HIST];
    uint16_t avh, avn;
    float av_sum, av_sq;       /* over last 50 samples                          */
    bool road_active;
    uint16_t road_left;
    float rd_min, rd_max; int32_t rd_tmin, rd_tmax;   /* ms rel. to trigger    */
    int32_t rd_first, rd_last;                        /* ±0.2 g crossings, ms  */
    uint32_t last_pothole_ms;
    float last_pot_severity;   /* max_av - min_av (g)                           */
    uint32_t n_potholes, n_bumps;

    /* state machine */
    engine_state_t state;
    uint32_t state_since_ms;
    engine_event_t alarm_cause;
    bool cancel_req;

    /* externally fed */
    float speed_kmh;           /* <0 = unknown                                  */
    uint32_t speed_ms;

    uint32_t now_ms;
    uint32_t n_samples;
} engine_t;

void engine_init(engine_t *e, const engine_cfg_t *cfg);

/* Feed one IMU sample. t_ms is the caller's monotonic millisecond clock.
 * Returns the event raised on this sample (at most one), EV_NONE otherwise. */
engine_event_t engine_update(engine_t *e, const imu_sample_t *s, uint32_t t_ms);

/* Driver pressed the physical cancel button / Jetson sent $CANCEL. */
void engine_cancel(engine_t *e);

/* Latest GPS speed from the Jetson (km/h). Attached to events, never a gate. */
void engine_feed_speed(engine_t *e, float kmh, uint32_t t_ms);

/* Score the current feature set with crash_model.h (exposed for the harness). */
float engine_score(const crash_feat_t *f);

/* Severity tier for a feature set: 3 severe, 2 moderate, 1 minor. */
uint8_t engine_tier(const crash_feat_t *f, const engine_cfg_t *cfg);

/* Host harness entry point: run n samples (acc[n*3] in g, gyr[n*3] in deg/s,
 * uniformly spaced by cfg.dt), write one event code per sample to ev_out
 * (may be NULL), and append the feature row of every crash capture that
 * completes to feat_out (max_feat rows of CRASH_NFEAT floats, may be NULL).
 * Returns the number of capture rows written. The engine is NOT reset. */
uint32_t engine_batch(engine_t *e, const float *acc, const float *gyr,
                      uint32_t n, uint8_t *ev_out,
                      float *feat_out, uint32_t max_feat);

#ifdef __cplusplus
}
#endif

#endif /* ENGINE_H */
