#!/usr/bin/env python3
"""Download the VZCrash shards used by the evaluation into data/vzcrash/.

Needs a Hugging Face login that has accepted the dataset terms:
    .venv\\Scripts\\hf auth login          (Windows)
    .venv/bin/hf auth login                (Linux/macOS)

Then:
    .venv\\Scripts\\python eval\\download_vzcrash.py            # test split (2 shards, ~1.8 GB)
    .venv\\Scripts\\python eval\\download_vzcrash.py --train 2  # + first 2 train shards (for fitting)

VZCrash is CC-BY-NC-4.0 (non-commercial). Companion paper: arXiv:2606.06074.
"""
import argparse
import shutil
import sys
from pathlib import Path

from huggingface_hub import HfApi, hf_hub_download

REPO = "vzc-research-chapter/VZCrash"
ROOT = Path(__file__).resolve().parent.parent
DST = ROOT / "data" / "vzcrash"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--train", type=int, default=0, help="number of train shards to fetch")
    ap.add_argument("--val", type=int, default=0, help="number of validation shards to fetch")
    args = ap.parse_args()

    api = HfApi()
    files = api.list_repo_files(REPO, repo_type="dataset")
    parquet = sorted(f for f in files if f.endswith(".parquet"))
    print(f"{len(parquet)} parquet files in {REPO}")
    for f in parquet:
        print("  ", f)

    def pick(prefix: str, k: int) -> list:
        return [f for f in parquet if Path(f).name.startswith(prefix)][:k]

    want = pick("test-", 99) + pick("train-", args.train) + pick("validation-", args.val)
    DST.mkdir(parents=True, exist_ok=True)
    for f in want:
        out = DST / Path(f).name
        if out.exists() and out.stat().st_size > 0:
            print(f"have {out.name}")
            continue
        print(f"fetch {f} ...")
        p = hf_hub_download(REPO, f, repo_type="dataset", local_dir=ROOT / "data" / "vzcrash-raw")
        shutil.move(p, out)
        print(f"  -> {out} ({out.stat().st_size / 1e6:.0f} MB)")
    print("done. files in", DST)
    for p in sorted(DST.glob("*.parquet")):
        print(f"  {p.name:40s} {p.stat().st_size / 1e6:8.0f} MB")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001
        print("ERROR:", e, file=sys.stderr)
        print("If this is a 401/403: log in (hf auth login) and accept the dataset terms at\n"
              f"https://huggingface.co/datasets/{REPO}", file=sys.stderr)
        sys.exit(1)
