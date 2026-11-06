# Changelog

Notable changes to Air-Mixer. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [semantic versioning](https://semver.org/). Each release on
[GitHub](https://github.com/yeixio/air-mixer/releases) also has notes
generated from its pull requests.

## [Unreleased]

### Added

- `GET /api/protocols`, listing each protocol with the fields it takes;
  the web UI builds its protocol menus and settings from it, so PipeWire
  and Bluetooth outputs can be set up there now.
- An internet radio input: a station, playlist, HLS stream or audio file
  from a URL, kept playing and reconnected when it drops.
- A PulseAudio/PipeWire input that records a line-in, a USB interface, a
  sink's monitor, or a phone playing to the host over Bluetooth.
- A Google Cast output for Chromecast, Nest and Google Home speakers,
  speaker groups and Cast TVs, with discovery.
- UPnP/DLNA in both directions: an output for Sonos (MP3 through its
  radio scheme), AV receivers, smart TVs and other renderers, found with
  SSDP; and an input that is a DLNA media renderer apps can play to.
- AirPlay inputs without their own address take the first free port in
  `server.airplay_port_range` (default 49152-49199), so several can run
  side by side; the web UI also lets an input set its own port. Before,
  a second input failed with "address already in use".
- An experimental AirPlay 2 output for HomePods, Apple TVs and other
  receivers that refuse classic AirPlay: HomeKit transient pairing, an
  encrypted control channel and ChaCha20-Poly1305 audio, with discovery
  over `_airplay._tcp`. Untested on Apple hardware.
- A Bluetooth input: Air-Mixer shows up as a Bluetooth speaker that
  phones pair with, accepting pairings through BlueZ and recording
  through PipeWire or PulseAudio.
- A Spotify Connect input through librespot and an AirPlay 2 input
  through shairport-sync; the web UI greys them out until the program is
  installed.
- `controller.hostAudio` in the Helm chart, mounting the host's
  PipeWire/PulseAudio socket and D-Bus system bus for line-in and
  Bluetooth inputs.
- Streams take `?raw=1` (close-delimited, DLNA headers) and
  `?format=mp3` (through ffmpeg).
- A Snapcast output that feeds a snapserver TCP source, and discovery of
  Snapcast servers.
- AAC, Ogg, Opus and FLAC through ffmpeg for playback and radio, when
  ffmpeg is installed; the Docker image includes it.
- A PulseAudio/PipeWire output that can drive paired Bluetooth speakers,
  with a per-output delay, and declarative setup from the config file.
- A Helm chart with a per-room speaker agent, and multi-arch images on
  `ghcr.io/yeixio/air-mixer`.
- Release automation: every `v*` tag publishes binaries for Linux (amd64,
  arm64, armv7), macOS (amd64, arm64) and Windows (amd64) with checksums.
- A logo, a web UI screenshot and a social preview image.
- A device compatibility issue form, `SUPPORT.md` and `CODEOWNERS`.
- Playback of MP3 and WAV URLs on chosen outputs (`/api/play`), ducking or
  silencing other audio and restoring it afterwards, for announcements,
  text-to-speech and alarms.
- A Home Assistant integration: a media player per output plus "All
  outputs", with `airmixer.announce` and `airmixer.stop` actions.
- Matinee in both directions: a Matinee input shows up in Matinee and
  Home Assistant as a player and plays what it's sent, and a Matinee output
  plays on a Matinee player such as a TV. Sign in once with a code
  approved on Matinee's /link page.
- A stream output: any output as a live WAV at `/api/clients/{id}/stream`,
  and `server.public_url` for the address other machines use to reach it.

### Changed

- Discovery falls back to IPv4 alone on hosts and pods without IPv6.
- The web UI wears Matinee's look: its warm dark theme with the coral
  accent, or its Light theme when the system asks for light, Inter and
  Fraunces (served by Air-Mixer, no internet needed), Matinee's cards,
  buttons, toggles and dialogs, and Box Office's ticket edge under the
  top bar.
- Rewrote Air-Mixer as a single CGO-free Go binary with an embedded web UI,
  SQLite storage, a documented REST API, classic AirPlay in both
  directions and local playback. AirPlay 2 pairing, which never worked,
  was removed until it can be done properly.

[Unreleased]: https://github.com/yeixio/air-mixer/commits/master
