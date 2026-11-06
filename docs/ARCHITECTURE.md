# Architecture

Air-Mixer receives audio from devices (inputs, called **servers** in the
code and API and **sources** in the web UI), routes and mixes it, and plays
it on speakers (outputs, called **clients** in the code and API and
**speakers** in the web UI).

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
therefore start playing when they come back. When `Send` reports
`ErrConnectionLost`, the Manager disconnects the client and starts
connecting again.

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
- **AirPlay 2 sender (`ClientOptions.AirPlay2`, experimental).** HomeKit
  transient pair-setup (`hap.go`: TLV8, SRP-6a over the 3072-bit group
  with SHA-512 and PIN 3939), then the RTSP connection is framed and
  encrypted with ChaCha20-Poly1305 using HKDF-derived control keys.
  `SETUP` bodies are binary plists (`bplist.go`): one for the session
  (NTP timing, which opens the encrypted event channel) and one for a
  realtime ALAC stream. Audio packets are sealed with the first 32 bytes of
  the pairing secret, the RTP timestamp and SSRC as additional data; sync,
  timing and retransmission are the classic ones. `POST /feedback` every
  two seconds keeps the session alive, and two failures report
  `ErrConnectionLost`.

### `internal/protocols/local` and `internal/protocols/pulse`

Outputs on the machine Air-Mixer runs on. `local` plays on the default
device through oto, which allows one device per process. `pulse` speaks
the PulseAudio native protocol in pure Go, so any number of outputs can
each play to their own sink, including Bluetooth speakers and sinks
reached through a socket mounted into a container. Both queue audio in an
`audio.Playout`, which can hold it back by a fixed `delay_ms` to line up
speakers. A pulse output checks every two seconds that its stream still
plays on its sink; when PulseAudio moves it because the sink went away,
it closes the stream and reports `ErrConnectionLost`, and the Manager
reconnects it once the sink is back.

### `internal/protocols/radio`, `snapcast` and the pulse input

- **radio** plays a URL as an input. It follows PLS and M3U playlists to
  their first stream, hands HLS, RTSP and RTMP to ffmpeg, decodes the rest
  with `internal/audio/decode`, and paces files to real time (live
  stations already are). A station that drops is reconnected with
  backoff; a file starts again when it ends.
- **snapcast** converts the output's audio to snapserver's rate and
  writes it as 16-bit PCM to a snapserver TCP source. A failed write
  reports `ErrConnectionLost`, so the Manager reconnects.
- **pulse** also records: `pulse.Source` opens a record stream on a
  source matched like a sink (name, description, Bluetooth address, never
  a speaker's monitor for an address), retries every two seconds while it
  is missing, and checks that PulseAudio hasn't moved the stream to
  another source.

### `internal/protocols/cast`

Google Cast output. Each output serves a live WAV stream and keeps a Cast
v2 control connection to the device: TLS to port 8009 carrying
length-prefixed protobuf `CastMessage`s with JSON payloads, encoded by
hand in `message.go`. It answers and sends heartbeats, launches the
Default Media Receiver when sound arrives, `LOAD`s the stream into it,
stops it after a minute of silence, and steps aside when a
`RECEIVER_STATUS` shows another app took the device. A dropped connection
or a device that stops answering for 20 seconds reports
`ErrConnectionLost`.

### `internal/protocols/upnp`

UPnP AV both ways, with SSDP (`ssdp.go`: M-SEARCH for discovery, and an
advertiser that answers searches and sends `NOTIFY`) and a small SOAP
client and parser (`soap.go`).

- **Client** reads the renderer's description (Sonos nests its
  AVTransport in an embedded device), then drives it like the Cast output:
  `SetAVTransportURI` and `Play` on sound, `GetTransportInfo` and
  `GetMediaInfo` every five seconds to notice another app or a renderer
  that stopped, `Stop` after a minute of silence. Three failed polls in a
  row report `ErrConnectionLost`.
- **Server** is a media renderer with AVTransport, RenderingControl and
  ConnectionManager (`scpd.go` holds their descriptions) and GENA
  eventing. Each track plays through a `radio.Server` with `Once` set, at
  the renderer's volume.

### `internal/protocols/bluetooth` and `internal/protocols/pipe`

- **bluetooth** turns the host into a Bluetooth speaker over BlueZ's
  D-Bus API (`github.com/godbus/dbus/v5`): it sets the adapter's alias,
  makes it discoverable and pairable, and exports a `NoInputNoOutput`
  agent that trusts phones and authorises only the A2DP and AVRCP
  profiles. The audio comes from a `pulse.Source` that follows whichever
  Bluetooth source appears. D-Bus failures go to `Err()` without
  stopping capture.
- **pipe** runs an external receiver that writes 16-bit stereo PCM to
  stdout (`librespot --backend pipe`, `shairport-sync --output stdout`),
  paces it to real time, and restarts the program with backoff when it
  exits, keeping the last lines of stderr for `Err()`.

### `internal/audio/decode`

Turns encoded audio into samples for playback and radio inputs: MP3 and
WAV in pure Go, anything else through an `ffmpeg` process when one is on
`$PATH`.

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

### `internal/playback`

Plays a URL on chosen outputs for announcements and alarms. Each playback
fetches an MP3 or WAV, decodes it in pure Go, converts it to the internal
format, and feeds it to the router in real time (never more than 100 ms
ahead) as a temporary source with a **transient** route to its outputs.
Announcement routes carry a `duck` level: the router multiplies every
other route on the same destinations by it, and the mixer fades volume
changes over 250 ms. Other audio therefore keeps flowing underneath and
returns when the route is removed. When an input's channel closes the
mixer still plays what it already holds, so the end of a clip is not cut
off.

### `internal/service`

Every change goes through the service, which saves it to SQLite and
applies it to the Manager and router in one place. For example, creating
an enabled server builds its adapter through the `Factory`, starts it, and
rolls the database insert back if it cannot start. `Load` restores the
saved setup on startup, then `Apply` creates or updates whatever the
config file's `setup` section declares.

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
