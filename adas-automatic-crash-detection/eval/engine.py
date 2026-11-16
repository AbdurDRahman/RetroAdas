"""ctypes wrapper around src/engine.c so the laptop harness runs the exact
C code the ESP32 runs. Builds build/libcrash.{dll,so} with gcc on demand.

    from engine import Engine
    e = Engine()
    events, feats = e.run(acc_g, gyro_dps)   # arrays of shape (n, 3)
"""
import ctypes as C
import os
import platform
import subprocess
import sys
from pathlib import Path

import numpy as np

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "src"
BUILD = ROOT / "build"
LIB = BUILD / ("libcrash.dll" if platform.system() == "Windows" else "libcrash.so")
CFLAGS = ["-std=c99", "-O2", "-Wall", "-Wextra"]

EV_NAMES = {0: "none", 1: "CRASH", 2: "ROLLOVER", 3: "POTHOLE", 4: "SPEED_BUMP",
            5: "ALARM_CANCELLED", 6: "ALARM_CONFIRMED", 7: "FAULT"}
EV_CRASH, EV_ROLLOVER, EV_POTHOLE, EV_SPEED_BUMP = 1, 2, 3, 4
FEAT_NAMES = ["peak_a", "sus50_h", "dv100", "dv300", "vratio", "peak_wh", "tilt90", "peak_av"]
NFEAT = len(FEAT_NAMES)


def build(force: bool = False) -> Path:
    srcs = [SRC / "engine.c", SRC / "crash_detect.c"]
    deps = srcs + [SRC / "engine.h", SRC / "crash_detect.h", SRC / "crash_model.h"]
    if not force and LIB.exists() and LIB.stat().st_mtime > max(d.stat().st_mtime for d in deps):
        return LIB
    BUILD.mkdir(exist_ok=True)
    cmd = ["gcc", *CFLAGS, "-shared", "-o", str(LIB), *map(str, srcs), "-lm"]
    print("$", " ".join(cmd), file=sys.stderr)
    subprocess.run(cmd, check=True)
    return LIB


class _Cfg(C.Structure):
    _fields_ = [
        ("dt", C.c_float),
        ("max_g", C.c_float), ("max_dps", C.c_float), ("stuck_samples", C.c_uint16),
        ("grav_tau_s", C.c_float), ("grav_freeze_g", C.c_float),
        ("pretrig_g", C.c_float), ("capture_samples", C.c_uint16),
        ("override_sus50_g", C.c_float), ("override_peak_g", C.c_float),
        ("override_peak_sus_g", C.c_float), ("score_threshold", C.c_float),
        ("roll_alpha", C.c_float), ("roll_deg", C.c_float),
        ("roll_hold", C.c_uint16), ("roll_calib", C.c_uint16),
        ("road_trig_g", C.c_float), ("road_quiet_std_g", C.c_float),
        ("pot_drop_g", C.c_float), ("pot_impact_g", C.c_float),
        ("pot_order_ms", C.c_uint16), ("pot_width_ms", C.c_uint16),
        ("road_capture", C.c_uint16), ("pot_ratelimit_ms", C.c_uint32),
        ("alarm_ms", C.c_uint32), ("lockout_ms", C.c_uint32),
    ]


CFG_FIELDS = [f[0] for f in _Cfg._fields_]

# engine_t is opaque to Python; allocate generously and let C own the layout.
_ENGINE_BYTES = 8192


class Engine:
    _lib = None

    def __init__(self, **overrides):
        if Engine._lib is None:
            lib = C.CDLL(str(build()))
            lib.engine_cfg_default.argtypes = [C.POINTER(_Cfg)]
            lib.engine_init.argtypes = [C.c_void_p, C.POINTER(_Cfg)]
            lib.engine_batch.argtypes = [C.c_void_p, C.POINTER(C.c_float), C.POINTER(C.c_float),
                                         C.c_uint32, C.POINTER(C.c_uint8),
                                         C.POINTER(C.c_float), C.c_uint32]
            lib.engine_batch.restype = C.c_uint32
            lib.engine_cancel.argtypes = [C.c_void_p]
            lib.engine_feed_speed.argtypes = [C.c_void_p, C.c_float, C.c_uint32]
            Engine._lib = lib
        self.lib = Engine._lib
        self.cfg = _Cfg()
        self.lib.engine_cfg_default(C.byref(self.cfg))
        for k, v in overrides.items():
            if k not in CFG_FIELDS:
                raise KeyError(k)
            setattr(self.cfg, k, v)
        self._mem = C.create_string_buffer(_ENGINE_BYTES)
        self.reset()

    def reset(self) -> None:
        self.lib.engine_init(self._mem, C.byref(self.cfg))

    def cancel(self) -> None:
        self.lib.engine_cancel(self._mem)

    def feed_speed(self, kmh: float, t_ms: int) -> None:
        self.lib.engine_feed_speed(self._mem, float(kmh), int(t_ms))

    def run(self, acc: np.ndarray, gyr: np.ndarray, max_feat: int = 64):
        """Run n samples. Returns (events uint8[n], feats float32[k, NFEAT])."""
        acc = np.ascontiguousarray(acc, dtype=np.float32)
        gyr = np.ascontiguousarray(gyr, dtype=np.float32)
        n = acc.shape[0]
        assert acc.shape == (n, 3) and gyr.shape == (n, 3)
        ev = np.zeros(n, dtype=np.uint8)
        feats = np.zeros((max_feat, NFEAT), dtype=np.float32)
        k = self.lib.engine_batch(self._mem,
                                  acc.ctypes.data_as(C.POINTER(C.c_float)),
                                  gyr.ctypes.data_as(C.POINTER(C.c_float)),
                                  n, ev.ctypes.data_as(C.POINTER(C.c_uint8)),
                                  feats.ctypes.data_as(C.POINTER(C.c_float)), max_feat)
        return ev, feats[:min(k, max_feat)]

    def cfg_dict(self) -> dict:
        return {k: getattr(self.cfg, k) for k in CFG_FIELDS}


if __name__ == "__main__":
    # quick self-test with synthetic signals
    import sys
    e = Engine()
    print("cfg:", e.cfg_dict())
    n = 1500
    t = np.arange(n) / 100.0
    # mounting: gravity along +z with a 20 deg pitch offset
    g = np.array([0.0, np.sin(np.radians(20)), np.cos(np.radians(20))])
    acc = np.tile(g, (n, 1)) + np.random.default_rng(0).normal(0, 0.02, (n, 3))
    gyr = np.random.default_rng(1).normal(0, 1.0, (n, 3))

    def pulse(center_s, width_s, amp, direction):
        idx = (t >= center_s) & (t < center_s + width_s)
        acc[idx] += amp * np.asarray(direction)

    # hard braking: -1.0 g along x for 1.2 s
    pulse(3.0, 1.2, -1.0, [1, 0, 0])
    # pothole: drop 0.5 g then hit +1.2 g, vertical, 150 ms total
    pulse(6.0, 0.06, -0.5, g); pulse(6.06, 0.06, 1.2, g)
    # speed bump: lift then drop, 500 ms
    pulse(8.0, 0.25, 0.8, g); pulse(8.25, 0.25, -0.5, g)
    # crash: 3.5 g along x for 80 ms
    pulse(11.0, 0.08, 3.5, [1, 0, 0])
    ev, feats = e.run(acc, gyr)
    for i in np.nonzero(ev)[0]:
        print(f"t={t[i]:6.2f}s  {EV_NAMES[ev[i]]}")
    for f in feats:
        print("capture:", dict(zip(FEAT_NAMES, np.round(f, 3))))
