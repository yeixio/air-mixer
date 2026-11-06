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
| Connection health for AirPlay outputs | S | Detect a receiver that went away (RTSP keepalive, socket errors) and report `ErrConnectionLost` from `Send`, so the Manager reconnects it. The Manager side and the pulse output already do this. |
| Adapter status in the web UI | S | Show `connecting`, `connected` and `failed` with the error from `/api/status`, and remove the leftover pairing dialog. |
| Idle handling | S | Stop streaming to outputs after a period of silence (RTSP `FLUSH`), and resume on audio, so AirPlay speakers can sleep and other senders can use them. |
| Device volume | S | Send `SET_PARAMETER volume` to AirPlay outputs and expose per-output volume in the API, separate from route volume. |
| API authentication | M | Optional token or password, and a setting to require it. Needed before anyone exposes the UI beyond a trusted LAN. |
| Release pipeline | S | Tagged releases with GoReleaser: binaries, checksums and a Homebrew tap. Multi-arch images are already published to GHCR. |

## Next: synchronised multi-room audio

| Feature | Size | Notes |
|---------|------|-------|
| Shared clock and synchronised start | L | Schedule every output against one timeline, using AirPlay sync packets and per-device latency, so rooms play in step. |
| Clock-drift compensation | M | Resample very slightly to follow each output's real clock rate, so long sessions do not drift apart. |
| Per-output latency calibration | M | A test tone and a slider for `delay_ms` (which local and pulse outputs already take), then automatic measurement where devices report latency. |
| Mix modes | M | Implement `exclusive` (highest-priority active route wins) for stored routes, and let stored routes duck others. The router already ducks for playback (announcements). |
| Better resampling | S | Replace linear interpolation with a windowed-sinc or polyphase resampler for sources not at 44.1 kHz. |

## Later: more ecosystems

| Feature | Size | Notes |
|---------|------|-------|
| AirPlay 2 output | L | HomeKit pairing (SRP-6a, Ed25519, ChaCha20-Poly1305) and buffered audio, for HomePods and recent Apple TVs. The highest-demand item, and the largest. |
| AirPlay 2 receiver, built in | L | The `airplay2` input runs shairport-sync. A built-in receiver would drop that dependency and the separate `nqptp` daemon. |
| Receivers in the Docker image | S | Ship `librespot` and an AirPlay 2 build of `shairport-sync` with `nqptp`, in the image or a `-full` variant. Alpine's packages are AirPlay 1 only and librespot is in its testing repo. |
| Cast volume and lower latency | M | Follow and set the Cast device's volume, and try a smaller buffer for the Default Media Receiver. The Cast output itself is done. |
| Sonos groups and volume | M | Join Sonos players into groups and follow their volume. Playing on a Sonos player or a group's coordinator is done, as are other DLNA renderers and the DLNA renderer input. |
| Bluetooth pairing from the web UI | S | List and forget paired phones, and pair Bluetooth speakers for `pulse` outputs, from the UI. The Bluetooth input itself is done. |
| Spotify Connect metadata | S | Show the track librespot is playing and pass Spotify's volume through. The input itself is done. |

## Platform

| Feature | Size | Notes |
|---------|------|-------|
| Plugin interface for adapters | M | Load third-party protocol adapters without forking, for example over gRPC. |
| Home Assistant integration | M | Done for outputs: a media player per output plus "all outputs", announcements, alarms and TTS ([docs](docs/HOME_ASSISTANT.md)). Next: per-route entities, device volume, and push updates once the event stream exists. |
| Metrics | S | Prometheus metrics: packets, drops, underruns and latency per adapter. |
| Event stream | S | Server-sent events for status changes, so the UI updates without polling. |
| Config import and export | S | Back up and restore servers, clients and routes as one file. |

## Good first issues

Small, self-contained tasks for a first contribution:

- Show adapter state and errors in the web UI's client and server tables.
- Remove the AirPlay 2 pairing dialog and its code from `webui/index.html`.
- Add a `--data-dir` flag that overrides `server.data_dir`.
- Add a Prometheus `/metrics` endpoint, starting with the dropped-audio
  counters the router already keeps per subscriber.
