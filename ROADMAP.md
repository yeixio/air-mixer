# Roadmap

Air-Mixer's goal is one place to control every speaker in a home,
whatever ecosystem it belongs to. This roadmap is a guide, not a promise;
priorities follow what users need. Comment on or open an issue to weigh
in, and see [CONTRIBUTING.md](CONTRIBUTING.md) to help build it.

Sizes are rough: **S** is a few days, **M** one to three weeks, **L**
longer.

## Now: make what exists dependable

| Feature | Size | Notes |
|---------|------|-------|
| Receiver jitter buffer | M | Reorder late packets and request retransmission (the sender side already answers requests), instead of dropping anything out of order. Fixes clicks on busy Wi-Fi. |
| Connection health for AirPlay outputs | S | Detect a receiver that went away (RTSP keepalive, socket errors) and hand it back to the Manager to reconnect. Today only the first connection is retried. |
| Adapter status in the web UI | S | Show `connecting`, `connected` and `failed` with the error from `/api/status`, and remove the leftover pairing dialog. |
| Idle handling | S | Stop streaming to outputs after a period of silence (RTSP `FLUSH`), and resume on audio, so AirPlay speakers can sleep and other senders can use them. |
| Device volume | S | Send `SET_PARAMETER volume` to AirPlay outputs and expose per-output volume in the API, separate from route volume. |
| API authentication | M | Optional token or password, and a setting to require it. Needed before anyone exposes the UI beyond a trusted LAN. |
| Release pipeline | S | Tagged releases with GoReleaser: binaries, checksums, multi-arch Docker images on GHCR, and a Homebrew tap. |

## Next: synchronised multi-room audio

| Feature | Size | Notes |
|---------|------|-------|
| Shared clock and synchronised start | L | Schedule every output against one timeline, using AirPlay sync packets and per-device latency, so rooms play in step. |
| Clock-drift compensation | M | Resample very slightly to follow each output's real clock rate, so long sessions do not drift apart. |
| Per-output latency calibration | M | A test tone and a slider, then automatic measurement where devices report latency. |
| Mix modes | M | Implement `exclusive` (highest-priority active route wins) and `duck` (lower others while a priority source plays, for announcements and doorbells), which are already stored on routes. |
| Better resampling | S | Replace linear interpolation with a windowed-sinc or polyphase resampler for sources not at 44.1 kHz. |

## Later: more ecosystems

| Feature | Size | Notes |
|---------|------|-------|
| AirPlay 2 output | L | HomeKit pairing (SRP-6a, Ed25519, ChaCha20-Poly1305) and buffered audio, for HomePods and recent Apple TVs. The highest-demand item, and the largest. |
| AirPlay 2 receiver | L | Appear as an AirPlay 2 speaker, so Apple devices can include Air-Mixer in their own multi-room groups. |
| Google Cast output | L | Cast to Chromecast Audio, Nest speakers and Cast-enabled TVs by serving a live stream to the default media receiver. |
| UPnP/DLNA renderer output | M | Push a stream to Sonos (via UPnP) and other DLNA renderers. Includes SSDP discovery. |
| UPnP/DLNA renderer input | M | Appear as a renderer so DLNA apps can play to Air-Mixer. |
| Bluetooth output and input | L | A2DP through BlueZ on Linux. Platform-specific, so it would be an optional build. |
| Snapcast and Spotify Connect | M | Snapcast as an output for open-source multi-room setups; Spotify Connect input through librespot. |
| Internet radio and file sources | S | Play a stream URL or a local file as an input, useful for alarms and testing. |

## Platform

| Feature | Size | Notes |
|---------|------|-------|
| Plugin interface for adapters | M | Load third-party protocol adapters without forking, for example over gRPC. |
| Home Assistant integration | M | A media-player entity per route and output. |
| Metrics | S | Prometheus metrics: packets, drops, underruns and latency per adapter. |
| Event stream | S | Server-sent events for status changes, so the UI updates without polling. |
| Config import and export | S | Back up and restore servers, clients and routes as one file. |

## Good first issues

Small, self-contained tasks for a first contribution:

- Show adapter state and errors in the web UI's client and server tables.
- Remove the AirPlay 2 pairing dialog and its code from `webui/index.html`.
- Add `GET /api/protocols` listing supported protocols and their config
  keys, and use it to build the UI's protocol dropdowns.
- Add a `--data-dir` flag that overrides `server.data_dir`.
- Add a Prometheus `/metrics` endpoint, starting with the dropped-audio
  counters the router already keeps per subscriber.
