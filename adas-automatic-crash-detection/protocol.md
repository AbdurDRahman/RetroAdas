# ESP32 ↔ Jetson link protocol (Feature 3)

Plain ASCII lines over UART, NMEA-style, so both ends can be debugged with a
terminal and the Jetson side is ~50 lines of `pyserial`. Chosen over a binary
protocol because the data rate is tiny (a few lines per second) and
readability matters more than bytes.

## Physical

- ESP32 `UART2`: TX = GPIO17, RX = GPIO16 (both 3.3 V; the Jetson's UART
  pins are 3.3 V too — no level shifter). Jetson side: `/dev/ttyTHS1` on the
  40-pin header, or any USB-TTL adapter (`/dev/ttyUSB0`) if the header UART
  is taken.
- **115200 8N1**, no flow control.
- One line per message, `\n` terminated, max 120 characters.

## Framing

```
$<TYPE>,<field>,<field>,...*<XX>\n
```

`XX` = two uppercase hex digits of the XOR of every byte between `$` and `*`
(exactly NMEA 0183). A receiver drops any line whose checksum does not
match, and any line longer than 120 bytes. Fields never contain `,`, `*`
or `$`. Unknown message types are ignored, so either side can be upgraded
alone.

## ESP32 → Jetson

| line | when | fields |
|---|---|---|
| `$HB,seq,uptime_s,state,fault,n_samples` | every 1 s | `state` 0 idle / 1 alarm / 2 lockout; `fault` 0/1 (sensor stuck or implausible) |
| `$EVT,seq,CRASH,uptime_ms,peak_g,sus50_g,dv100_mps,score,tier,speed_kmh` | alarm starts | `tier` 1 minor / 2 moderate / 3 severe; `speed_kmh` = last `$SPD` received or `-1` |
| `$EVT,seq,ROLLOVER,uptime_ms,tilt_deg,speed_kmh` | alarm starts | |
| `$EVT,seq,POTHOLE,uptime_ms,severity_g,speed_kmh` | pothole recognised | **no alarm**; Jetson attaches GPS and posts `event_tag: "pothole"` |
| `$ALM,seq,CANCELLED,uptime_ms,cause` | driver cancelled | `cause` CRASH / ROLLOVER |
| `$ALM,seq,CONFIRMED,uptime_ms,cause,peak_g,speed_kmh` | countdown elapsed | Jetson reports the accident (cloud `accident` hazard + emergency-contact stub) |
| `$LOG,seq,i,n,ax,ay,az,gx,gy,gz` | after CONFIRMED/CANCELLED | the last 3 s of raw samples (n = 300 lines), for the local event log required by the spec |
| `$ACK,seq` | reply to `$CFG` / `$CANCEL` | |

`seq` is a 16-bit counter per ESP32 boot. The Jetson acknowledges every
`$EVT` and `$ALM` with `$ACK,seq`; the ESP32 resends an unacknowledged
`$EVT`/`$ALM` every 2 s, up to 5 times, then gives up (the event is still in
the local log). Heartbeats are never acknowledged.

## Jetson → ESP32

| line | when | fields |
|---|---|---|
| `$SPD,kmh,valid` | every 1 s while the Jetson is awake | GPS speed; `valid` 0/1. The ESP32 attaches the last value to events; it is never a detection gate (see `results.md` Test 4) |
| `$CANCEL` | on-screen cancel tapped | same effect as the physical button |
| `$CFG,key,value` | optional | runtime tuning: `alarm_ms`, `pretrig_g`, `score_thr`, `pot_trig_g`, `buzzer` (0/1 mute) — persists until reboot |
| `$ACK,seq` | reply to `$EVT` / `$ALM` | |
| `$PING` | any time | ESP32 answers `$HB` immediately |

## Behaviour when the Jetson is absent

Nothing changes on the ESP32. Detection, buzzer, cancel button and the
countdown are all local. Events are queued (last 8) and flushed when
`$ACK`s start arriving again, so a crash during a Jetson reboot is reported
once the Jetson is back — this is the reason the detector lives on its own
microcontroller (see `handover.md` §1).

## Jetson responsibilities (`jetson/crash_link.py`)

1. Open the port, parse lines, verify checksums, `$ACK` events.
2. Send `$SPD` once a second from the GPS it already has.
3. On `$ALM,CONFIRMED`: build the accident report (device_id, timestamp,
   lat/lon, heading, speed, peak_g, tier), `POST /v1/hazards` with
   `event_tag: "accident"` (auth: the same `device_id` + API key as the
   perception stack — `adas-cloud/internal/devices/authentication.go`), and
   call the emergency-contact stub.
4. On `$EVT,POTHOLE`: `POST /v1/hazards` with `event_tag: "pothole"`,
   `confidence` from the severity (cloud deduplicates on position).
5. Write every line to `crash_link.log` (the local event log of the spec).

The cloud calls are behind `--cloud-url`; without it the script logs what it
*would* post, which is how it is demonstrated on a laptop.

## Example session

```
$HB,1,12,0,0,1200*5C
$SPD,43.2,1*6A                      (Jetson → ESP32)
$EVT,2,POTHOLE,13410,1.31,43.2*7B
$ACK,2*47                           (Jetson → ESP32)
$EVT,3,CRASH,20150,4.62,3.10,2.81,2.44,3,41.0*61
$ACK,3*46
$HB,4,21,1,0,2100*5E
$ALM,5,CONFIRMED,35150,CRASH,4.62,0.0*0A
$ACK,5*40
$LOG,6,0,300,0.02,-0.01,1.00,0.3,-0.1,0.2*2F
...
```
