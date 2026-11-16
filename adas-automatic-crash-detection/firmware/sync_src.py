#!/usr/bin/env python3
"""Copy src/*.c, src/*.h next to the Arduino sketch (the Arduino IDE only
compiles files inside the sketch folder). Run after any change to src/:

    python firmware/sync_src.py
"""
import filecmp
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "src"
SKETCH = ROOT / "firmware" / "retroadas_crash"

for f in sorted(SRC.glob("*.[ch]")):
    dst = SKETCH / f.name
    if dst.exists() and filecmp.cmp(f, dst, shallow=False):
        print(f"same    {f.name}")
        continue
    shutil.copy2(f, dst)
    print(f"copied  {f.name}")
