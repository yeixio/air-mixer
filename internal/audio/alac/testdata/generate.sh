#!/usr/bin/env bash
# Regenerates the ALAC decoder fixtures with ffmpeg's ALAC encoder.
# Each fixture is three files:
#   NAME.cookie  ALAC magic cookie (MP4 'alac' atom payload)
#   NAME.frames  frames, each prefixed with a big-endian uint32 length
#   NAME.pcm     reference decode by ffmpeg, interleaved little-endian
#                int16 (16-bit) or int32 (24-bit, left-justified)
set -euo pipefail
cd "$(dirname "$0")"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

gen() {
  local name=$1 channels=$2 fmt=$3 pcmfmt=$4
  ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "sine=frequency=440:duration=0.2" \
    -f lavfi -i "anoisesrc=d=0.2:c=pink:a=0.3:seed=42" \
    -filter_complex "[0][1]amerge=inputs=2,aresample=44100,aformat=sample_fmts=${fmt}:channel_layouts=$([ "$channels" = 1 ] && echo mono || echo stereo)" \
    -c:a alac "$work/$name.m4a"
  ffprobe -v error -select_streams a:0 -show_packets -show_data -of json "$work/$name.m4a" > "$work/$name.packets"
  ffprobe -v error -select_streams a:0 -show_streams -show_data -of json "$work/$name.m4a" > "$work/$name.json"
  ffmpeg -hide_banner -loglevel error -y -i "$work/$name.m4a" -f "$pcmfmt" "$name.pcm"
  python3 - "$work/$name" "$name" <<'PY'
import json, struct, sys
src, name = sys.argv[1], sys.argv[2]
stream = json.load(open(src + ".json"))["streams"][0]
def unhex(dump):
    # ffprobe prints "offset: hex groups  ascii"; keep only the hex groups.
    return bytes.fromhex("".join(line[10:49].replace(" ", "") for line in dump.splitlines() if line.strip()))
open(name + ".cookie", "wb").write(unhex(stream["extradata"]))
out = bytearray()
for pkt in json.load(open(src + ".packets"))["packets"]:
    data = unhex(pkt["data"])
    assert len(data) == int(pkt["size"]), (len(data), pkt["size"])
    out += struct.pack(">I", len(data)) + data
open(name + ".frames", "wb").write(out)
PY
}

gen stereo16 2 s16 s16le
gen mono16 1 s16 s16le
gen stereo24 2 s32 s32le
