# Changelog

Notable changes to Air-Mixer. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [semantic versioning](https://semver.org/). Each release on
[GitHub](https://github.com/yeixio/air-mixer/releases) also has notes
generated from its pull requests.

## [Unreleased]

### Added

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
