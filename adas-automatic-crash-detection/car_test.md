# Car test: ESP32 + MPU-6050 on the dashboard

Step-by-step for getting the crash/pothole detector onto a real board, checking
it on the desk, and recording a real drive with locations. Nothing here changes
the detection algorithm; it only runs it on hardware and records what it does.

**Who does what**

| step | who | needs |
|---|---|---|
| 1–4 Wire, flash, desk checks | hardware person | Arduino IDE, the `firmware/retroadas_crash/` folder |
| 5 Desk check with the logger | whoever owns the laptop that will ride in the car | this project set up (README "Setup") |
| 6–7 The drive and the report | driver + one passenger with that laptop and a phone | the same laptop, the Sensor Logger app |

The laptop that already has this project and its `.venv` (the one the benchmark
was run on) is the easiest choice for steps 5–7.

---

## 1. Parts

| part | notes |
|---|---|
| ESP32 DevKit (38- or 30-pin "ESP32 Dev Module") | from the department |
| MPU-6050 module (GY-521 board) | the common blue breakout |
| 4–5 jumper wires (female–female if both boards have pins) | |
| USB cable that carries **data** | charge-only cables are the #1 cause of "no port" |
| optional: **active** buzzer module (3.3–5 V) | an active buzzer beeps by itself; a passive one only clicks with this code |
| optional: push button | cancel button |
| something to fix it firmly: small box + screws, zip ties, or strong double-sided foam tape | |

## 2. Wiring

| MPU-6050 pin | ESP32 pin |
|---|---|
| VCC | 3V3 |
| GND | GND |
| SCL | GPIO 22 |
| SDA | GPIO 21 |
| INT | GPIO 4 (optional: the code falls back to timing if it is missing) |
| AD0 | GND (or leave unconnected; it must not be at 3V3) |

| other | ESP32 pin |
|---|---|
| buzzer + | GPIO 25, buzzer − to GND |
| cancel button | between GPIO 27 and GND (no resistor needed) |
| alarm LED | the built-in LED (GPIO 2), nothing to wire |

The Jetson pins (GPIO 16/17) stay unconnected for this test.

## 3. Flash the firmware

1. Install **Arduino IDE 2.x**.
2. *File → Preferences → Additional boards manager URLs*: add
   `https://espressif.github.io/arduino-esp32/package_esp32_index.json`
3. *Tools → Board → Boards Manager*: install **esp32 by Espressif Systems**.
4. Open `firmware/retroadas_crash/retroadas_crash.ino` (keep the other files in
   that folder next to it; the IDE opens them as tabs, that is correct).
5. *Tools → Board*: **ESP32 Dev Module**. *Tools → Port*: the new COM port that
   appears when the board is plugged in.
   - No port appears: try another cable, then install the USB driver for the
     chip printed near the USB socket: **CP2102** (Silicon Labs CP210x driver)
     or **CH340** (WCH driver).
6. Upload (→ button). If it stalls on `Connecting.....`, hold the **BOOT**
   button on the ESP32 until the upload starts.

If it does not compile, send the **exact** error text back. (It has been
compile-checked on a laptop with stand-in Arduino headers, not yet with the
real ESP32 toolchain.)

## 4. Desk checks (hardware person)

Open *Tools → Serial Monitor* at **115200** baud and press the board's **EN**
(reset) button.

| check | what to do | pass |
|---|---|---|
| A. sensor found | reset the board | the first line says `# RetroADAS crash unit, model fit-20261116, MPU-6050 ok`, and the buzzer chirps once |
| B. alive | wait | a `> $HB,...` line every second |
| C. rollover | leave the board flat for 3 s after reset, then turn it on its side (90°) and **hold it there 5 s** | `# event 2 ...` and a `$EVT,...,ROLLOVER` line; buzzer beeps fast, LED on |
| D. cancel | while C is beeping, press the button | `# cancel button`, then `$ALM,...,CANCELLED`; beeping stops |
| E. countdown | repeat C and do **not** press the button | after 15 s: `$ALM,...,CONFIRMED` |

Wait 60 s after D or E before repeating (the unit ignores new alarms for a
minute after one, on purpose).

If A says `MPU-6050 NOT FOUND`: check the four sensor wires (SDA/SCL swapped is
common), that VCC is on 3V3, and AD0 is not on 3V3.

**F. A real recorded crash into the real board** (laptop with the datasets):
close the Serial Monitor (only one program can use the port), then

```
.venv\Scripts\python eval\replay.py vzcrash --label crash --n 0 --min-peak 4 --port COM5
```

(use the board's COM port). Pass: the buzzer goes off a few seconds in. This
is the strongest desk demo: a crash recorded in a real vehicle, decided by the
real board.

## 5. Desk check with the logger (car laptop)

Close the Serial Monitor, plug the board into the laptop that will ride in the
car, and run (VS Code: *Terminal → Run Task → Car test: 1. record a drive*):

```
.venv\Scripts\python eval\drive_logger.py --name desk
```

Pass: within a few seconds it prints `board is streaming. Recording.`; every
30 s a status line shows the seconds of data growing. Tilt the board on its
side for 5 s: you should see `*** ROLLOVER ALARM`. Press `q`. A folder appears
under `data\drives\`.

## 6. The drive

**Mounting.** Any angle is fine (the unit finds "down" by itself), but it must
be **firm**: screwed, zip-tied or strongly taped to the dashboard or centre
console so it cannot rattle or slide. Route the USB cable so nobody pulls it.

**Phone (for locations).** Install **Sensor Logger** (free; Android and iOS).
In its settings turn on **Location** (the other sensors are optional). Make
sure the phone and the laptop both use **automatic time**; the report lines
them up by clock.

**Order on the day**

1. Plug the board into the laptop. Start the phone recording.
2. Start the logger (`--name route1` or similar). Keep the car **still for 5 s**.
3. Drive. The **passenger**, never the driver, presses a key each time they
   **feel** something:

   | key | meaning |
   |---|---|
   | `p` | pothole |
   | `b` | speed bump |
   | `r` | rough road / cobblestone stretch |
   | `h` | hard braking (only somewhere safe and empty) |
   | `x` | anything else odd |
   | `c` | cancel a crash alarm (or use the button) |
   | `q` | stop and save |

   The terminal window must be the active window for the keys to register.
4. At the end press `q`, stop the phone recording, export it as a **.zip**, and
   copy the zip into the new folder under `data\drives\`.

**What to drive.** A route you know has potholes and speed bumps, a stretch of
rough road if available, and normal driving in between. Aim for **at least
1 hour in total** (several drives are fine), because "false alarms per hour"
needs hours. A crash alarm during the drive is a false alarm by definition;
cancel it and keep going (the logger records it).

## 7. The report

VS Code: *Run Task → Car test: 2. report on the newest drive*, or

```
.venv\Scripts\python eval\drive_report.py
```

It writes into the drive folder:

- `report.md`: potholes the passenger felt vs detected (correct, missed,
  unmarked), speed bumps, crash false alarms per hour, and a check that the
  board decided exactly what the laptop engine decides on the same samples.
- `events_located.csv`: every detection and mark with time, speed, latitude,
  longitude and a Google Maps link (opens in Excel; importable into Google My Maps).
- `map.html`: double-click to open the route with every detection on it
  (needs internet for the map tiles).

An "unmarked" pothole detection is not automatically wrong (the passenger may
have missed a small one). Look at it on the map before calling it false.

## What to send back

- The whole drive folder from `data\drives\` (raw.csv is about 5 MB per 10 min).
- Photos of the mounting and the wiring.
- Anything odd the passenger noticed, with the time.

The raw samples let the drive be replayed on the laptop later, so the pothole
thresholds can be re-checked on our own roads without driving again.

## How it works (for the curious)

The logger sends `$RAW,1` to the board, which then also prints every sample
(`R,…`) and every engine decision (`E,…`) on USB, at about 4.5 kB/s. The
detection is the same `src/engine.c` that the benchmark tested; the firmware
folder holds copies of those files (`firmware/sync_src.py` refreshes them).
Without a Jetson the laptop also acknowledges the board's event messages, so
it does not keep resending them.
