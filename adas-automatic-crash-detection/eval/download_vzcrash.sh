#!/usr/bin/env bash
# Fallback downloader for VZCrash using curl with resume + retries (the
# huggingface_hub client stalled on some connections). Reads the token that
# `hf auth login` stored in ~/.cache/huggingface/token; never prints it.
#
#   bash eval/download_vzcrash.sh                 # test split
#   bash eval/download_vzcrash.sh --train 2       # + first N train shards
set -u
cd "$(dirname "$0")/.."
TOKEN_FILE="$HOME/.cache/huggingface/token"
[ -s "$TOKEN_FILE" ] || { echo "no HF token at $TOKEN_FILE — run: .venv/Scripts/hf auth login"; exit 1; }
REPO="https://huggingface.co/datasets/vzc-research-chapter/VZCrash/resolve/main/data"
NTRAIN=0
[ "${1:-}" = "--train" ] && NTRAIN="${2:-0}"
FILES="test-00000-of-00002.parquet test-00001-of-00002.parquet"
for ((i = 0; i < NTRAIN; i++)); do FILES="$FILES train-0000${i}-of-00011.parquet"; done
mkdir -p data/vzcrash
for f in $FILES; do
  out="data/vzcrash/$f"
  for try in $(seq 1 30); do
    echo "[$(date +%H:%M:%S)] $f attempt $try"
    curl -L -sS -C - --retry 5 --retry-all-errors --speed-limit 20000 --speed-time 30 \
         -H "Authorization: Bearer $(cat "$TOKEN_FILE")" -o "$out" "$REPO/$f"
    rc=$?
    size=$(stat -c %s "$out" 2>/dev/null || echo 0)
    if [ $rc -eq 0 ] && head -c 4 "$out" | grep -q PAR1 && [ "$size" -gt 300000000 ]; then
      echo "[$(date +%H:%M:%S)] done $f ($((size / 1000000)) MB)"
      break
    fi
    echo "  rc=$rc size=$size, retrying"
    sleep 3
  done
done
ls -la data/vzcrash
