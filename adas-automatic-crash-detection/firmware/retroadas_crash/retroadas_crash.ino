/*
 * retroadas_crash.ino — RetroADAS Feature 3 firmware: ESP32 DevKit + MPU-6050.
 *
 * Runs src/engine.c unchanged (the .c/.h files next to this sketch are copies
 * made by firmware/sync_src.py — do not edit them here). Design: algorithm.md,
 * link protocol: protocol.md.
 *
 * Wiring (ESP32 DevKit v1, 3.3 V logic):
 *   MPU-6050  VCC→3V3  GND→GND  SCL→GPIO22  SDA→GPIO21  INT→GPIO4  AD0→GND
 *   Buzzer    +→GPIO25 (active buzzer module; use a transistor for >12 mA)
 *   Cancel    GPIO27 → button → GND (internal pull-up)
 *   Jetson    UART2: ESP32 TX GPIO17 → Jetson RX, ESP32 RX GPIO16 ← Jetson TX, GND↔GND
 *   LED       built-in GPIO2 mirrors the alarm state
 *
 * Build: Arduino IDE 2.x, board "ESP32 Dev Module", core esp32 ≥ 2.0.14.
 * Monitor: 115200 baud on USB. Every line of the Jetson link is echoed on USB
 * prefixed with "> " (out) / "< " (in) so one terminal shows everything.
 *
 * Bench test without a car: send "$SIM,ax,ay,az,gx,gy,gz" lines (g, deg/s)
 * on USB at 100 Hz (eval/replay.py --port COMx does this with real recorded
 * crash clips); while SIM lines arrive the IMU is ignored.
 */
#include <Wire.h>
#include <HardwareSerial.h>
#include "engine.h"

/* ---- pins / constants ------------------------------------------------ */
#define PIN_SDA 21
#define PIN_SCL 22
#define PIN_IMU_INT 4
#define PIN_BUZZER 25
#define PIN_CANCEL 27
#define PIN_LED 2
#define JETSON_RX 16
#define JETSON_TX 17
#define JETSON_BAUD 115200
#define USB_BAUD 115200

#define MPU_ADDR 0x68
#define IMU_HZ 100
#define ACCEL_LSB_PER_G 2048.0f      /* ±16 g */
#define GYRO_LSB_PER_DPS 16.4f       /* ±2000 °/s */

#define LOG_SAMPLES 300              /* 3 s raw ring for $LOG */
#define QUEUE_LEN 8                  /* unacked outgoing events */
#define RETRY_MS 2000
#define RETRY_MAX 5
#define SIM_TIMEOUT_MS 500

HardwareSerial Jetson(2);

/* ---- state ------------------------------------------------------------ */
static engine_t eng;
static engine_cfg_t cfg;
static volatile bool imu_ready = false;
static uint16_t seq = 0;
static bool buzzer_enabled = true;
static float last_speed = -1.0f;
static uint32_t last_hb_ms = 0;
static uint32_t last_imu_ms = 0;
static uint32_t last_sim_ms = 0;
static bool sim_mode = false;
static imu_sample_t sim_sample;
static bool sim_pending = false;

/* raw-sample ring for the post-event log */
static imu_sample_t logring[LOG_SAMPLES];
static uint16_t loghead = 0;
static bool log_dump_pending = false;
static uint16_t log_dump_i = 0;

/* outgoing queue with ack/retry */
struct pending_t { char line[120]; uint16_t seq; uint32_t sent_ms; uint8_t tries; bool used; };
static pending_t queue[QUEUE_LEN];

/* cancel button debounce */
static uint32_t cancel_low_since = 0;
static bool cancel_latched = false;

/* ---- MPU-6050 register driver --------------------------------------- */
static void mpu_write(uint8_t reg, uint8_t val)
{
    Wire.beginTransmission(MPU_ADDR);
    Wire.write(reg);
    Wire.write(val);
    Wire.endTransmission();
}

static uint8_t mpu_read(uint8_t reg)
{
    Wire.beginTransmission(MPU_ADDR);
    Wire.write(reg);
    Wire.endTransmission(false);
    Wire.requestFrom((uint8_t)MPU_ADDR, (uint8_t)1);
    return Wire.read();
}

static bool mpu_init(void)
{
    Wire.begin(PIN_SDA, PIN_SCL, 400000);
    delay(50);
    if (mpu_read(0x75) != 0x68) {           /* WHO_AM_I */
        return false;
    }
    mpu_write(0x6B, 0x80); delay(100);      /* reset */
    mpu_write(0x6B, 0x01);                  /* clock = PLL gyro X (stable rate) */
    mpu_write(0x1A, 0x03);                  /* DLPF 44 Hz accel / 42 Hz gyro, 1 kHz base */
    mpu_write(0x19, 1000 / IMU_HZ - 1);     /* sample rate divider -> 100 Hz */
    mpu_write(0x1B, 0x18);                  /* gyro ±2000 °/s */
    mpu_write(0x1C, 0x18);                  /* accel ±16 g */
    mpu_write(0x37, 0x10);                  /* INT cleared on any read */
    mpu_write(0x38, 0x01);                  /* data-ready interrupt */
    return true;
}

static bool mpu_read_sample(imu_sample_t *s)
{
    Wire.beginTransmission(MPU_ADDR);
    Wire.write(0x3B);
    if (Wire.endTransmission(false) != 0) return false;
    if (Wire.requestFrom((uint8_t)MPU_ADDR, (uint8_t)14) != 14) return false;
    int16_t r[7];
    for (int i = 0; i < 7; i++) {
        r[i] = (int16_t)((Wire.read() << 8) | Wire.read());
    }
    s->ax = r[0] / ACCEL_LSB_PER_G;
    s->ay = r[1] / ACCEL_LSB_PER_G;
    s->az = r[2] / ACCEL_LSB_PER_G;
    /* r[3] = temperature */
    s->gx = r[4] / GYRO_LSB_PER_DPS;
    s->gy = r[5] / GYRO_LSB_PER_DPS;
    s->gz = r[6] / GYRO_LSB_PER_DPS;
    return true;
}

static void IRAM_ATTR on_imu_int(void)
{
    imu_ready = true;
}

/* ---- link helpers ----------------------------------------------------- */
static uint8_t nmea_sum(const char *body)
{
    uint8_t x = 0;
    for (const char *p = body; *p; p++) x ^= (uint8_t)*p;
    return x;
}

/* body = everything between '$' and '*' */
static void send_line(const char *body, bool queue_it, uint16_t s)
{
    char line[128];
    snprintf(line, sizeof(line), "$%s*%02X", body, nmea_sum(body));
    Jetson.println(line);
    Serial.print("> "); Serial.println(line);
    if (queue_it) {
        for (int i = 0; i < QUEUE_LEN; i++) {
            if (!queue[i].used) {
                strncpy(queue[i].line, line, sizeof(queue[i].line) - 1);
                queue[i].seq = s;
                queue[i].sent_ms = millis();
                queue[i].tries = 1;
                queue[i].used = true;
                return;
            }
        }
        /* queue full: drop the oldest */
        int oldest = 0;
        for (int i = 1; i < QUEUE_LEN; i++) if (queue[i].sent_ms < queue[oldest].sent_ms) oldest = i;
        strncpy(queue[oldest].line, line, sizeof(queue[oldest].line) - 1);
        queue[oldest].seq = s; queue[oldest].sent_ms = millis(); queue[oldest].tries = 1;
    }
}

static void retry_queue(void)
{
    uint32_t now = millis();
    for (int i = 0; i < QUEUE_LEN; i++) {
        if (!queue[i].used) continue;
        if (now - queue[i].sent_ms < RETRY_MS) continue;
        if (queue[i].tries >= RETRY_MAX) { queue[i].used = false; continue; }
        Jetson.println(queue[i].line);
        Serial.print("> (retry) "); Serial.println(queue[i].line);
        queue[i].sent_ms = now;
        queue[i].tries++;
    }
}

static void ack_received(uint16_t s)
{
    for (int i = 0; i < QUEUE_LEN; i++) {
        if (queue[i].used && queue[i].seq == s) queue[i].used = false;
    }
}

static void send_heartbeat(void)
{
    char body[80];
    snprintf(body, sizeof(body), "HB,%u,%lu,%d,%d,%lu", ++seq, millis() / 1000,
             (int)eng.state, eng.fault ? 1 : 0, (unsigned long)eng.n_samples);
    send_line(body, false, seq);
}

static void send_event(engine_event_t ev)
{
    char body[110];
    uint32_t t = millis();
    switch (ev) {
    case EV_CRASH:
        snprintf(body, sizeof(body), "EVT,%u,CRASH,%lu,%.2f,%.2f,%.2f,%.2f,%d,%.1f", ++seq, t,
                 eng.feat.peak_a, eng.feat.sus50_h, eng.feat.dv100, eng.last_score,
                 (int)eng.last_tier, last_speed);
        send_line(body, true, seq);
        break;
    case EV_ROLLOVER:
        snprintf(body, sizeof(body), "EVT,%u,ROLLOVER,%lu,%.1f,%.1f", ++seq, t, eng.roll_rel, last_speed);
        send_line(body, true, seq);
        break;
    case EV_POTHOLE:
        snprintf(body, sizeof(body), "EVT,%u,POTHOLE,%lu,%.2f,%.1f", ++seq, t, eng.last_pot_severity, last_speed);
        send_line(body, true, seq);
        break;
    case EV_ALARM_CANCELLED:
        snprintf(body, sizeof(body), "ALM,%u,CANCELLED,%lu,%s", ++seq, t,
                 eng.alarm_cause == EV_ROLLOVER ? "ROLLOVER" : "CRASH");
        send_line(body, true, seq);
        log_dump_pending = true; log_dump_i = 0;
        break;
    case EV_ALARM_CONFIRMED:
        snprintf(body, sizeof(body), "ALM,%u,CONFIRMED,%lu,%s,%.2f,%.1f", ++seq, t,
                 eng.alarm_cause == EV_ROLLOVER ? "ROLLOVER" : "CRASH", eng.feat.peak_a, last_speed);
        send_line(body, true, seq);
        log_dump_pending = true; log_dump_i = 0;
        break;
    case EV_FAULT:
        snprintf(body, sizeof(body), "HB,%u,%lu,%d,1,%lu", ++seq, t / 1000, (int)eng.state,
                 (unsigned long)eng.n_samples);
        send_line(body, false, seq);
        break;
    default:
        break;
    }
}

/* dump the raw ring a few lines per loop so the 100 Hz loop is not starved */
static void pump_log_dump(void)
{
    if (!log_dump_pending) return;
    for (int k = 0; k < 10 && log_dump_i < LOG_SAMPLES; k++, log_dump_i++) {
        const imu_sample_t *s = &logring[(loghead + log_dump_i) % LOG_SAMPLES];
        char body[110];
        snprintf(body, sizeof(body), "LOG,%u,%u,%u,%.3f,%.3f,%.3f,%.1f,%.1f,%.1f", ++seq,
                 log_dump_i, LOG_SAMPLES, s->ax, s->ay, s->az, s->gx, s->gy, s->gz);
        send_line(body, false, seq);
    }
    if (log_dump_i >= LOG_SAMPLES) log_dump_pending = false;
}

/* ---- incoming lines (Jetson UART and USB share one parser) ----------- */
static bool verify_line(char *line, char **body_out)
{
    if (line[0] != '$') return false;
    char *star = strrchr(line, '*');
    if (!star || strlen(star) < 3) return false;
    *star = 0;
    uint8_t want = (uint8_t)strtol(star + 1, NULL, 16);
    if (nmea_sum(line + 1) != want) return false;
    *body_out = line + 1;
    return true;
}

static void handle_line(char *line, bool from_usb)
{
    char *body;
    if (!from_usb) { Serial.print("< "); Serial.println(line); }
    if (!verify_line(line, &body)) return;
    if (strncmp(body, "SPD,", 4) == 0) {
        float kmh = atof(body + 4);
        const char *v = strchr(body + 4, ',');
        if (!v || atoi(v + 1) != 0) {
            last_speed = kmh;
            engine_feed_speed(&eng, kmh, millis());
        } else {
            last_speed = -1.0f;
        }
    } else if (strcmp(body, "CANCEL") == 0) {
        engine_cancel(&eng);
    } else if (strncmp(body, "ACK,", 4) == 0) {
        ack_received((uint16_t)atoi(body + 4));
    } else if (strcmp(body, "PING") == 0) {
        send_heartbeat();
    } else if (strncmp(body, "CFG,", 4) == 0) {
        char key[24]; float val = 0;
        if (sscanf(body + 4, "%23[^,],%f", key, &val) == 2) {
            if (!strcmp(key, "alarm_ms")) eng.cfg.alarm_ms = (uint32_t)val;
            else if (!strcmp(key, "pretrig_g")) eng.cfg.pretrig_g = val;
            else if (!strcmp(key, "score_thr")) eng.cfg.score_threshold = val;
            else if (!strcmp(key, "pot_trig_g")) eng.cfg.road_trig_g = val;
            else if (!strcmp(key, "buzzer")) buzzer_enabled = val != 0;
            char b[40]; snprintf(b, sizeof(b), "ACK,%u", seq);
            send_line(b, false, seq);
        }
    } else if (strncmp(body, "SIM,", 4) == 0) {
        float v[6];
        if (sscanf(body + 4, "%f,%f,%f,%f,%f,%f", &v[0], &v[1], &v[2], &v[3], &v[4], &v[5]) == 6) {
            sim_sample.ax = v[0]; sim_sample.ay = v[1]; sim_sample.az = v[2];
            sim_sample.gx = v[3]; sim_sample.gy = v[4]; sim_sample.gz = v[5];
            sim_pending = true;
            sim_mode = true;
            last_sim_ms = millis();
        }
    }
}

static void poll_serial(Stream &port, char *buf, size_t cap, size_t *len, bool from_usb)
{
    while (port.available()) {
        char c = (char)port.read();
        if (c == '\n' || c == '\r') {
            if (*len) { buf[*len] = 0; handle_line(buf, from_usb); *len = 0; }
        } else if (*len < cap - 1) {
            buf[(*len)++] = c;
        } else {
            *len = 0;   /* overlong line: drop */
        }
    }
}

/* ---- alarm outputs ----------------------------------------------------- */
static void drive_outputs(void)
{
    bool alarm = eng.state == ST_ALARM;
    digitalWrite(PIN_LED, alarm ? HIGH : LOW);
    /* 4 Hz beep pattern while the countdown runs */
    bool on = alarm && buzzer_enabled && ((millis() / 125) % 2 == 0);
    digitalWrite(PIN_BUZZER, on ? HIGH : LOW);
}

static void poll_cancel_button(void)
{
    bool low = digitalRead(PIN_CANCEL) == LOW;
    uint32_t now = millis();
    if (!low) { cancel_low_since = 0; cancel_latched = false; return; }
    if (!cancel_low_since) cancel_low_since = now;
    if (!cancel_latched && now - cancel_low_since >= 50) {   /* 50 ms debounce */
        cancel_latched = true;
        engine_cancel(&eng);
        Serial.println("# cancel button");
    }
}

/* ---- per-sample work --------------------------------------------------- */
static void process_sample(const imu_sample_t *s)
{
    logring[loghead] = *s;
    loghead = (uint16_t)((loghead + 1) % LOG_SAMPLES);
    engine_event_t ev = engine_update(&eng, s, millis());
    if (ev != EV_NONE && ev != EV_SPEED_BUMP) {
        Serial.printf("# event %d state %d\n", (int)ev, (int)eng.state);
        send_event(ev);
    }
}

/* ---- Arduino ----------------------------------------------------------- */
static char usb_buf[160]; static size_t usb_len = 0;
static char jet_buf[160]; static size_t jet_len = 0;

void setup()
{
    Serial.begin(USB_BAUD);
    Jetson.begin(JETSON_BAUD, SERIAL_8N1, JETSON_RX, JETSON_TX);
    pinMode(PIN_BUZZER, OUTPUT);
    pinMode(PIN_LED, OUTPUT);
    pinMode(PIN_CANCEL, INPUT_PULLUP);
    pinMode(PIN_IMU_INT, INPUT);

    engine_cfg_default(&cfg);
    cfg.dt = 1.0f / IMU_HZ;
    engine_init(&eng, &cfg);

    bool ok = mpu_init();
    Serial.printf("# RetroADAS crash unit, model %s, MPU-6050 %s\n", CRASH_MODEL_VERSION, ok ? "ok" : "NOT FOUND");
    if (ok) attachInterrupt(digitalPinToInterrupt(PIN_IMU_INT), on_imu_int, RISING);
    /* short beep: alive */
    digitalWrite(PIN_BUZZER, HIGH); delay(80); digitalWrite(PIN_BUZZER, LOW);
}

void loop()
{
    uint32_t now = millis();

    poll_serial(Serial, usb_buf, sizeof(usb_buf), &usb_len, true);
    poll_serial(Jetson, jet_buf, sizeof(jet_buf), &jet_len, false);
    poll_cancel_button();

    if (sim_mode && now - last_sim_ms > SIM_TIMEOUT_MS) {
        sim_mode = false;
        Serial.println("# SIM ended, back to IMU");
    }

    if (sim_mode) {
        if (sim_pending) { sim_pending = false; process_sample(&sim_sample); }
    } else if (imu_ready || (now - last_imu_ms) >= (1000 / IMU_HZ + 5)) {
        /* interrupt-driven; the time check is a fallback if INT is not wired */
        imu_ready = false;
        imu_sample_t s;
        if (mpu_read_sample(&s)) {
            last_imu_ms = now;
            process_sample(&s);
        }
    }

    if (now - last_hb_ms >= 1000) {
        last_hb_ms = now;
        send_heartbeat();
        if (eng.state == ST_ALARM) {
            Serial.printf("# ALARM %lu s left\n", (eng.cfg.alarm_ms - (now - eng.state_since_ms)) / 1000);
        }
    }
    retry_queue();
    pump_log_dump();
    drive_outputs();
}
