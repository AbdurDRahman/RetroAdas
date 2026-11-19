#!/usr/bin/env python3
"""Car test, after the drive: turn a drive_logger.py folder into counts, a
table of every detection with its location, and a map.

    .venv\\Scripts\\python eval\\drive_report.py                      (newest drive)
    .venv\\Scripts\\python eval\\drive_report.py data\\drives\\2026-11-25_101500_route1
    .venv\\Scripts\\python eval\\drive_report.py <folder> --phone path\\to\\SensorLogger.zip

Location comes from the phone: run the Sensor Logger app (Location switched
on) during the drive, export the recording as a .zip, and drop it into the
drive folder (or pass --phone). Phone and laptop clocks are matched by their
own time, so set both to automatic time before driving; --phone-offset fixes
a known difference.

Writes into the drive folder:
  report.md            the counts (true / false / missed potholes, false alarms per hour)
  events_located.csv   every detection and mark with time, lat/lon, speed, map link
  map.html             open in a browser: the route and every detection on a map

It also replays the recorded samples through the same C engine on the laptop
and checks that the board made exactly the same decisions.
"""
from __future__ import annotations

import argparse
import json
import sys
import zipfile
from datetime import datetime
from io import BytesIO
from pathlib import Path

import numpy as np
import pandas as pd

sys.path.insert(0, str(Path(__file__).resolve().parent))
from engine import Engine  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
DRIVES = ROOT / "data" / "drives"
NAMES = {1: "CRASH", 2: "ROLLOVER", 3: "POTHOLE", 4: "SPEED_BUMP",
         5: "ALARM_CANCELLED", 6: "ALARM_CONFIRMED", 7: "SENSOR_FAULT"}


# ---------------------------------------------------------------------------
# loading
# ---------------------------------------------------------------------------

def newest_drive() -> Path:
    runs = sorted(p for p in DRIVES.glob("*") if (p / "raw.csv").exists())
    if not runs:
        raise SystemExit(f"no drives in {DRIVES}; record one with eval/drive_logger.py")
    return runs[-1]


def read_phone(path: Path) -> pd.DataFrame | None:
    """Location.csv from a Sensor Logger export (zip or folder)."""
    def pick(names):
        for n in names:
            if Path(n).stem.lower() == "location" and n.lower().endswith(".csv"):
                return n
        return None
    if path.is_dir():
        n = pick([str(p) for p in path.rglob("*.csv")])
        df = pd.read_csv(n) if n else None
    else:
        with zipfile.ZipFile(path) as z:
            n = pick(z.namelist())
            df = pd.read_csv(BytesIO(z.read(n))) if n else None
    if df is None or not len(df):
        return None
    need = {"time", "latitude", "longitude"}
    if not need <= set(df.columns):
        raise SystemExit(f"{path}: Location.csv lacks {need - set(df.columns)}")
    df = df.dropna(subset=["latitude", "longitude"]).copy()
    df["wall"] = df["time"].astype(np.float64) / 1e9          # ns since 1970 (UTC)
    return df.sort_values("wall").reset_index(drop=True)


def find_phone(folder: Path) -> Path | None:
    for p in sorted(folder.glob("*.zip")):
        return p
    for p in sorted(folder.iterdir()):
        if p.is_dir() and (any(p.rglob("Location.csv")) or any(p.rglob("location.csv"))):
            return p
    return None


def segments(esp_ms: np.ndarray) -> np.ndarray:
    """Segment id per row; a new segment starts when the board restarted."""
    seg = np.zeros(len(esp_ms), dtype=int)
    if len(esp_ms) > 1:
        seg[1:] = np.cumsum(np.diff(esp_ms) < -1000)
    return seg


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def hhmmss(t: float) -> str:
    return datetime.fromtimestamp(t).strftime("%H:%M:%S")


def match(det: np.ndarray, marks: np.ndarray, before: float, after: float):
    """One-to-one greedy matching of detection times to mark times.
    A mark may come up to `before` s before the detection (the engine decides
    ~0.6 s after the hit) or up to `after` s after it (reaction time).
    Returns (det_idx -> mark_idx) dict."""
    pairs = []
    for i, td in enumerate(det):
        for j, tm in enumerate(marks):
            d = tm - td
            if -before <= d <= after:
                pairs.append((abs(d - 0.8), i, j))   # people press ~0.8 s after
    pairs.sort()
    used_d, used_m, out = set(), set(), {}
    for _, i, j in pairs:
        if i not in used_d and j not in used_m:
            used_d.add(i)
            used_m.add(j)
            out[i] = j
    return out


def per_hour(n: int, hours: float) -> str:
    return f"{n / hours:.2f}" if hours > 0 else "—"


def pct(a: int, b: int) -> str:
    return f"{100 * a / b:.0f} %" if b else "—"


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("folder", nargs="?", help="drive folder (default: the newest in data/drives)")
    ap.add_argument("--phone", help="Sensor Logger export (.zip or folder); default: a .zip inside the drive folder")
    ap.add_argument("--phone-offset", type=float, default=0.0,
                    help="seconds to ADD to phone time to match the laptop clock")
    ap.add_argument("--before", type=float, default=2.0, help="a mark may precede its detection by this much (s)")
    ap.add_argument("--after", type=float, default=4.0, help="a mark may follow its detection by this much (s)")
    a = ap.parse_args()

    folder = Path(a.folder) if a.folder else newest_drive()
    raw = pd.read_csv(folder / "raw.csv")
    if not len(raw):
        raise SystemExit(f"{folder}/raw.csv is empty: the board never streamed (see console.log)")
    ev = pd.read_csv(folder / "events.csv")
    mk = pd.read_csv(folder / "marks.csv")
    meta = json.loads((folder / "session.json").read_text()) if (folder / "session.json").exists() else {}

    # --- clock: esp_ms -> laptop time, per board power-cycle -------------------
    raw["seg"] = segments(raw["esp_ms"].to_numpy(np.int64))
    offs = {}
    for s, g in raw.groupby("seg"):
        # USB latency only ever delays a line, so the low percentile is the true offset
        offs[s] = float(np.percentile(g["host_t"] - g["esp_ms"] / 1000.0, 2))
    raw["wall"] = raw["esp_ms"] / 1000.0 + raw["seg"].map(offs)

    def wall_of(esp_ms: float, host_t: float) -> float:
        # the segment whose offset puts this event closest to when it arrived
        best = min(offs.values(), key=lambda o: abs(esp_ms / 1000.0 + o - host_t))
        return esp_ms / 1000.0 + best
    ev["wall"] = [wall_of(m, h) for m, h in zip(ev["esp_ms"], ev["host_t"])]
    mk = mk[mk["key"] != "c"].copy()
    mk["wall"] = mk["host_t"].astype(float)

    n = len(raw)
    hours = n / 100.0 / 3600.0
    lost = int(meta.get("samples_lost", 0))

    # --- replay through the laptop engine --------------------------------------
    replay_events = []
    replay_error = ""
    try:
        for s, g in raw.groupby("seg"):
            acc = g[["ax", "ay", "az"]].to_numpy(np.float32)
            gyr = g[["gx", "gy", "gz"]].to_numpy(np.float32)
            e = Engine()
            r, _ = e.run(acc, gyr, max_feat=1)
            for i in np.nonzero(r)[0]:
                replay_events.append((int(r[i]), float(g["esp_ms"].iloc[i]), s))
    except Exception as exc:   # no gcc on this laptop, etc.: the rest still works
        replay_error = f"{type(exc).__name__}: {exc}"
    # cancelled / confirmed depend on whether someone pressed cancel, which the
    # replay cannot know, so the comparison covers the detections themselves
    board = [(int(c), float(m)) for c, m in zip(ev["code"], ev["esp_ms"]) if int(c) not in (5, 6)]
    left = [x for x in replay_events if x[0] not in (5, 6)]
    same = 0
    for c, m in board:
        hit = next((x for x in left if x[0] == c and abs(x[1] - m) <= 30), None)
        if hit:
            same += 1
            left.remove(hit)
    only_replay = left
    consistent = not only_replay and same == len(board)

    # --- ground truth matching ---------------------------------------------------
    def times(code):
        return ev.loc[ev["code"] == code, "wall"].to_numpy(float)

    def mtimes(label):
        return mk.loc[mk["label"] == label, "wall"].to_numpy(float)

    pot_t, bump_t = times(3), times(4)
    alarm_rows = ev[ev["code"].isin([1, 2])]
    m_pot, m_bump, m_rough, m_brake = (mtimes(x) for x in ("pothole", "speed_bump", "rough_road", "hard_brake"))

    pot_vs_pot = match(pot_t, m_pot, a.before, a.after)
    tp = len(pot_vs_pot)
    fp_idx = [i for i in range(len(pot_t)) if i not in pot_vs_pot]
    fn = len(m_pot) - tp
    fp_at_bump = match(pot_t[fp_idx], m_bump, a.before, a.after)
    fp_at_rough = match(pot_t[fp_idx], m_rough, a.before, 6.0)
    bump_ok = len(match(bump_t, m_bump, a.before, a.after))
    brake_alarm = sum(1 for tb in m_brake if any(-3 <= t - tb <= 4 for t in alarm_rows["wall"]))
    fp = len(fp_idx)

    # --- location ------------------------------------------------------------------
    phone_path = Path(a.phone) if a.phone else find_phone(folder)
    loc = read_phone(phone_path) if phone_path else None
    gps_note = ""
    if loc is not None:
        loc["wall"] = loc["wall"] + a.phone_offset
        lo, hi = loc["wall"].iloc[0], loc["wall"].iloc[-1]
        overlap = min(hi, raw["wall"].iloc[-1]) - max(lo, raw["wall"].iloc[0])
        if overlap <= 0:
            gps_note = ("**The phone recording does not overlap the drive in time** "
                        f"(phone {hhmmss(lo)}–{hhmmss(hi)}, board {hhmmss(raw['wall'].iloc[0])}–"
                        f"{hhmmss(raw['wall'].iloc[-1])}). Check the clocks or pass --phone-offset.")
            loc = None

    def where(t: float):
        if loc is None or t < loc["wall"].iloc[0] - 5 or t > loc["wall"].iloc[-1] + 5:
            return None, None, None
        lat = float(np.interp(t, loc["wall"], loc["latitude"]))
        lon = float(np.interp(t, loc["wall"], loc["longitude"]))
        spd = float(np.interp(t, loc["wall"], loc["speed"])) * 3.6 if "speed" in loc else None
        return lat, lon, (spd if spd is not None and spd >= 0 else None)

    pot_verdict = {}
    for i in range(len(pot_t)):
        if i in pot_vs_pot:
            pot_verdict[i] = "correct (passenger felt a pothole)"
        else:
            k = fp_idx.index(i)
            pot_verdict[i] = ("false: was a speed bump" if k in fp_at_bump else
                              "false: rough road" if k in fp_at_rough else
                              "unconfirmed: no pothole mark")
    rows = []
    pot_i = 0
    for _, r in ev.iterrows():           # events.csv is in time order, like pot_t
        code = int(r["code"])
        what = NAMES.get(code, str(code))
        verdict = ""
        if code == 3:
            verdict = pot_verdict[pot_i]
            pot_i += 1
        elif code in (1, 2):
            verdict = "false alarm (no crash happened)"
        elif code == 4:
            verdict = "speed bump recognised"
        rows.append({"time": hhmmss(r["wall"]), "wall": r["wall"], "what": what, "verdict": verdict})
    missed = []
    matched_marks = set(pot_vs_pot.values())
    for j, t in enumerate(m_pot):
        if j not in matched_marks:
            missed.append(t)
            rows.append({"time": hhmmss(t), "wall": t, "what": "MISSED_POTHOLE",
                         "verdict": "passenger felt a pothole, no detection"})
    for t, lab in zip(mk["wall"], mk["label"]):
        if lab != "pothole":
            rows.append({"time": hhmmss(t), "wall": t, "what": f"mark:{lab}", "verdict": ""})
    rows.sort(key=lambda x: x["wall"])
    for x in rows:
        lat, lon, spd = where(x["wall"])
        x["lat"] = round(lat, 6) if lat is not None else ""
        x["lon"] = round(lon, 6) if lon is not None else ""
        x["speed_kmh"] = round(spd) if spd is not None else ""
        x["map_link"] = f"https://maps.google.com/?q={lat:.6f},{lon:.6f}" if lat is not None else ""
    out = pd.DataFrame(rows).drop(columns=["wall"])
    out.to_csv(folder / "events_located.csv", index=False)

    # --- map --------------------------------------------------------------------------
    if loc is not None:
        step = max(1, len(loc) // 2000)
        track = loc.iloc[::step][["latitude", "longitude"]].round(6).values.tolist()
        pts = [{"lat": x["lat"], "lon": x["lon"], "what": x["what"], "verdict": x["verdict"],
                "time": x["time"], "spd": x["speed_kmh"]} for x in rows if x["lat"] != ""]
        write_map(folder / "map.html", track, pts, folder.name)

    # --- report -----------------------------------------------------------------------
    alarms = len(alarm_rows)
    L = []
    L.append(f"# Car test report — {folder.name}\n")
    L.append(f"Board: `{meta.get('board', '?')}`  ")
    L.append(f"Recorded: {hhmmss(raw['wall'].iloc[0])} to {hhmmss(raw['wall'].iloc[-1])}, "
             f"**{n / 100 / 60:.1f} min of sensor data** ({hours:.2f} h)"
             + (f", {lost} samples lost on the cable" if lost else "") + ".\n")
    if hours < 0.5:
        L.append("> Short drive: the per-hour numbers below are not meaningful yet. "
                 "Aim for at least 1 hour of driving in total (several drives can be added up).\n")
    L.append("## Potholes\n")
    L.append("| | count |\n|---|---|")
    L.append(f"| potholes the passenger marked | {len(m_pot)} |")
    L.append(f"| detected **and** marked (correct) | **{tp}** |")
    L.append(f"| marked but **not** detected (missed) | {fn} |")
    L.append(f"| detected but **not** marked | {fp} |")
    L.append(f"| … of which at a marked speed bump | {len(fp_at_bump)} |")
    L.append(f"| … of which on marked rough road | {len(fp_at_rough)} |")
    L.append(f"| detection rate (correct ÷ marked) | {pct(tp, len(m_pot))} |")
    L.append(f"| precision (correct ÷ detected) | {pct(tp, len(pot_t))} |")
    L.append(f"| unmarked detections per hour | {per_hour(fp, hours)} |\n")
    L.append("An unmarked detection is not automatically wrong: the passenger may have "
             "missed a small pothole. Look each one up on the map before calling it false.\n")
    L.append("## Speed bumps\n")
    L.append(f"{len(m_bump)} marked; {bump_ok} recognised as a speed bump; "
             f"{len(fp_at_bump)} reported as a pothole instead.\n")
    L.append("## Crash / rollover alarms\n")
    L.append("No crash happened on this drive, so **every alarm is a false alarm**.\n")
    L.append("| | count |\n|---|---|")
    L.append(f"| crash / rollover alarms | **{alarms}** |")
    L.append(f"| false alarms per hour | **{per_hour(alarms, hours)}** (target ≤ 1) |")
    L.append(f"| hard brakes marked | {len(m_brake)} |")
    L.append(f"| alarms during a marked hard brake | {brake_alarm} |\n")
    L.append("## Board vs laptop\n")
    if replay_error:
        L.append(f"Skipped: the engine could not be built on this laptop ({replay_error}). "
                 "Run the report on a laptop with gcc (see README setup) for this check.\n")
    elif consistent:
        L.append(f"The recorded samples were replayed through the same C engine on the laptop: "
                 f"**identical decisions** ({same} of {len(board)} events, same type, within 30 ms).\n")
    else:
        L.append(f"Replay on the laptop matched {same} of {len(board)} board events; "
                 f"{len(only_replay)} events only on the laptop. Usually this means samples were lost "
                 f"on the cable ({lost}); otherwise the firmware copy of the engine differs from src/ "
                 f"(run firmware/sync_src.py and reflash).\n")
    L.append("## Location\n")
    if loc is not None:
        L.append(f"From `{phone_path.name}`. Every row in `events_located.csv` has a map link; "
                 f"`map.html` shows the route and all detections.\n")
    else:
        L.append(gps_note or "No phone recording found, so no locations. Put the Sensor Logger "
                 ".zip into this folder and run the report again.")
        L.append("")
    L.append("## Every detection and mark\n")
    L.append("| time | what | verdict | speed km/h | location |\n|---|---|---|---|---|")
    for x in rows:
        link = f"[map]({x['map_link']})" if x["map_link"] else ""
        L.append(f"| {x['time']} | {x['what']} | {x['verdict']} | {x['speed_kmh']} | {link} |")
    (folder / "report.md").write_text("\n".join(L) + "\n", encoding="utf-8")

    # --- console -----------------------------------------------------------------------
    print(f"drive {folder.name}: {n / 100 / 60:.1f} min of data" + (f", {lost} samples lost" if lost else ""))
    print(f"potholes: marked {len(m_pot)}, correct {tp}, missed {fn}, unmarked detections {fp}")
    print(f"speed bumps: marked {len(m_bump)}, recognised {bump_ok}")
    print(f"crash/rollover false alarms: {alarms}  ({per_hour(alarms, hours)} per hour)")
    print("board vs laptop replay: " + ("skipped (" + replay_error + ")" if replay_error else
                                         f"{'identical' if consistent else 'DIFFERENT'} ({same}/{len(board)})"))
    print("location: " + (f"yes ({phone_path.name})" if loc is not None else (gps_note or "no phone recording")))
    print(f"wrote {folder / 'report.md'}")
    if loc is not None:
        print(f"wrote {folder / 'map.html'}  (open it in a browser)")


def write_map(path: Path, track, pts, title: str) -> None:
    colours = {"POTHOLE": "#1a9850", "MISSED_POTHOLE": "#d73027", "SPEED_BUMP": "#4575b4",
               "CRASH": "#000000", "ROLLOVER": "#000000"}
    html = f"""<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Car test map</title>
<link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css">
<script src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"></script>
<style>html,body,#m{{height:100%;margin:0;font:14px system-ui,sans-serif}}
#k{{position:absolute;z-index:999;top:10px;right:10px;background:#fff;padding:8px 10px;border-radius:6px;
box-shadow:0 1px 4px #0004;line-height:1.6}} .d{{display:inline-block;width:11px;height:11px;border-radius:50%;
margin-right:6px;vertical-align:middle}}
@media (max-width:700px){{#k{{font-size:11px;top:auto;bottom:22px;right:6px;padding:5px 7px;line-height:1.4}}}}</style></head><body><div id="m"></div>
<div id="k"><b>{title}</b><br>
<span class="d" style="background:#1a9850"></span>pothole, confirmed by passenger<br>
<span class="d" style="background:#fdae61"></span>pothole, not marked by passenger<br>
<span class="d" style="background:#d73027"></span>pothole felt, not detected<br>
<span class="d" style="background:#4575b4"></span>speed bump recognised<br>
<span class="d" style="background:#000"></span>crash / rollover alarm<br>
<span class="d" style="background:#999"></span>other passenger mark</div>
<script>
const track = {json.dumps(track)};
const pts = {json.dumps(pts)};
const col = {json.dumps(colours)};
const m = L.map('m');
L.tileLayer('https://{{s}}.tile.openstreetmap.org/{{z}}/{{x}}/{{y}}.png',
  {{maxZoom: 19, attribution: '&copy; OpenStreetMap'}}).addTo(m);
const line = L.polyline(track, {{color: '#555', weight: 3, opacity: .6}}).addTo(m);
m.fitBounds(line.getBounds(), {{padding: [20, 20]}});
for (const p of pts) {{
  let c = col[p.what] || '#999';
  if (p.what === 'POTHOLE' && !p.verdict.startsWith('correct')) c = '#fdae61';
  L.circleMarker([p.lat, p.lon], {{radius: 7, color: '#222', weight: 1, fillColor: c, fillOpacity: .9}})
   .bindPopup(`<b>${{p.what}}</b><br>${{p.time}}${{p.spd !== '' ? ', ' + p.spd + ' km/h' : ''}}<br>${{p.verdict}}`)
   .addTo(m);
}}
</script></body></html>
"""
    path.write_text(html, encoding="utf-8")


if __name__ == "__main__":
    main()
