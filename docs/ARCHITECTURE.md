# Architecture

Air-Mixer receives audio from devices (inputs, called **servers**), routes
and mixes it, and plays it on speakers (outputs, called **clients**).

```text
  senders                 inputs                                   outputs                speakers

  iPhone ──AirPlay──▶ ┌──────────────┐               ┌────────┐               ┌────────────────┐ ──▶ AirPort Express
  Mac ─────AirPlay──▶ │ AirPlay      │──convert──▶   │ Router │ ──mixed──▶    │ AirPlay client │
                      │ server       │  (Manager)    │        │  (Manager)    │ Local client   │ ──▶ sound card
                      └──────────────┘               └────────┘               └────────────────┘
                                                         ▲
                     web UI ──▶ REST API ──▶ Service ────┘ routes
                                               │
                                             SQLite
```

## Audio format

Everything inside the router is interleaved `float32` samples in [-1, 1] at
44.1 kHz stereo (`audio.Internal`). Converting at the edges keeps the
router and mixer simple; the `protocols.Manager` converts each server's
audio with `audio.Converter` (channel remixing plus linear-interpolation
resampling) before it reaches the router.

## Packages

### `internal/protocols`

Defines the two adapter interfaces:

- `Server` is an input. `Start` begins accepting connections and
  `Audio()` delivers decoded `Packet`s.
- `Client` is an output. `Connect` opens it and `Send` queues audio
  without blocking, returning `ErrBufferFull` when it has to drop.

The `Manager` runs adapters and connects them to the router. Each server
gets a goroutine that converts its audio and feeds a router source. Each
client gets a router sink and a goroutine that connects it, retrying with
exponential backoff (1 s up to 30 s) until it succeeds, is removed, or
fails with `ErrUnsupportedDevice`. Speakers that are switched off
therefore start playing when they come back.

### `internal/protocols/airplay`

Classic AirPlay (RAOP), both directions.

- **Receiver (`Server`).** Advertises `_raop._tcp` over mDNS as
  `<MAC>@<name>` and handles the RTSP session: `OPTIONS` (answering the
  Apple-Challenge with the public AirPort key), `ANNOUNCE` (SDP with ALAC
  or L16 and an RSA-OAEP-wrapped AES key), `SETUP` (three UDP ports for
  audio, control and timing), `RECORD`, `FLUSH`, `SET_PARAMETER` for
  volume, and `TEARDOWN`. Audio packets are RTP payload type 0x60,
  AES-128-CBC encrypted per packet with any trailing partial block in the
  clear, then ALAC-decoded. One sender plays at a time; a new one takes
  over, as on real AirPlay speakers.
- **Sender (`Client`).** Runs the same handshake from the other side,
  then streams 352-frame ALAC packets paced against the wall clock. It
  pre-buffers 100 ms, sends silence on underrun, sends a sync packet
  every second, keeps the last 1024 packets to answer retransmit requests,
  and answers timing requests. Devices that demand AirPlay 2 pairing are
  reported with `ErrAirPlay2Required`.

### `internal/audio/alac`

A pure-Go Apple Lossless codec. The decoder is a port of David Hammerton's
decoder and is checked bit-for-bit against ffmpeg on 16- and 24-bit, mono
and stereo fixtures, and fuzzed. The encoder writes uncompressed ALAC
frames, which every receiver accepts and which cost nothing to produce.

### `internal/router`

Routes are stored by name: a route names one source and any number of
destinations. Whenever a route, source or sink changes, `reconcile`
compares the links that should exist with those that do, and adds or
removes the difference, so registration order never matters.

- Every source has a **hub** that fans its stream out to every link,
  never blocking: a slow subscriber loses audio rather than stalling the
  others.
- Every sink has an `audio.Mixer`. Each route into it is a mixer input
  with its own volume. The mixer emits as much audio as every input has
  ready; an input that falls more than 250 ms behind is treated as silent
  so one stalled source cannot stall the rest. Output is clamped to
  [-1, 1].
- A forwarder moves mixed audio to the sink's channel without blocking.

### `internal/service`

Every change goes through the service, which saves it to SQLite and
applies it to the Manager and router in one place. For example, creating
an enabled server builds its adapter through the `Factory`, starts it, and
rolls the database insert back if it cannot start. `Load` restores the
saved setup on startup.

### `internal/api` and `webui`

A standard-library `http.ServeMux` serves the JSON API under `/api`,
`/health`, and the embedded single-page UI for every other path. See
[API.md](API.md).

### `internal/db`

SQLite through `modernc.org/sqlite` (pure Go). The schema version is kept
in `PRAGMA user_version`, and migrations run on open. Migration 1 also
converts databases from the original prototype.

## Concurrency rules

- Nothing on the audio path blocks on a consumer. Channels between stages
  are buffered and full buffers drop audio; dropping is always better than
  stalling every speaker.
- `Router.UnregisterSink` guarantees that no send happens after it
  returns, so callers may close their channel.
- Adapters own their goroutines and stop them in `Stop` or `Disconnect`.
  All tests run with the race detector.
