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

### Changed

- Rewrote Air-Mixer as a single CGO-free Go binary with an embedded web UI,
  SQLite storage, a documented REST API, classic AirPlay in both
  directions and local playback. AirPlay 2 pairing, which never worked,
  was removed until it can be done properly.

[Unreleased]: https://github.com/yeixio/air-mixer/commits/master
