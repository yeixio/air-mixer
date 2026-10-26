# Air-Mixer

[![CI](https://github.com/yeixio/air-mixer/actions/workflows/ci.yml/badge.svg)](https://github.com/yeixio/air-mixer/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/yeixio/air-mixer.svg)](https://pkg.go.dev/github.com/yeixio/air-mixer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**One home for every speaker you own.**

Air-Mixer is a small self-hosted server that unifies speakers from different
ecosystems behind one web UI and one REST API. It receives audio from your
devices, mixes it, and plays it on any combination of speakers, whatever
protocol they speak.

Today it speaks classic AirPlay in both directions and plays on the
machine's own sound card. Google Cast, UPnP/DLNA, Bluetooth and AirPlay 2
are on the [roadmap](ROADMAP.md).

## What it does

- **AirPlay receiver.** Each input you create appears on iPhones and Macs as
  an AirPlay speaker. Pick it in Control Center and play.
- **AirPlay sender.** Plays to AirPort Express, shairport-sync, most
  AV receivers and other classic AirPlay (RAOP) speakers.
- **Local output.** Plays on the server's own default audio device.
- **Routing and mixing.** A route sends one input to any number of
  outputs. Several routes into one output are mixed, each with its own
  volume. Routes refer to devices by name, so they survive a device being
  removed and added again.
- **Discovery.** Finds AirPlay speakers on your network.
- **Web UI and REST API.** Everything the UI does is available over a
  [documented JSON API](docs/API.md).
- **Single static binary.** Pure Go, no cgo: the web UI, SQLite and the
  ALAC codec are all built in. Cross-compiles to Linux, macOS and Windows.

## Quick start

### Docker

```bash
git clone https://github.com/yeixio/air-mixer.git
cd air-mixer
docker compose up -d
```

Open <http://localhost:9001>. The container uses host networking because
AirPlay discovery relies on multicast DNS.

### From source

Requires Go 1.26 or later.

```bash
git clone https://github.com/yeixio/air-mixer.git
cd air-mixer
make build
./build/airmixer server
```

Or install straight from the module:

```bash
go install github.com/yeixio/air-mixer/cmd/airmixer@latest
airmixer server
```

On Linux, local playback goes through PulseAudio or PipeWire's PulseAudio
service. Headless servers that only bridge AirPlay devices do not need one.

### First steps

1. Open the web UI at <http://localhost:9001>.
2. Add an **input server** (protocol `airplay`). It shows up as a speaker
   on your Apple devices.
3. Add one or more **output clients**: AirPlay speakers (use Discover to
   find them) or `local` for the server's sound card.
4. Create a **route** from the input to the outputs, then play something.

## Configuration

Air-Mixer reads `config.yaml` from the working directory, `~/.airmixer/` or
`/etc/airmixer/`, or the file given with `--config`. Every setting can also
be set through an environment variable such as `AIRMIXER_SERVER_WEB_PORT`.
See [config.yaml.example](config.yaml.example) for all settings and their
defaults.

Servers, clients and routes are stored in SQLite under `data_dir` and are
managed through the UI or the API, not the config file.

## Command line

```text
airmixer server     Run the server (aliases: start, serve)
airmixer version    Print version information
  -c, --config      Path to a config file
```

## Current limitations

- **AirPlay 2.** Air-Mixer speaks classic AirPlay (RAOP). Devices that only
  accept AirPlay 2, such as recent Apple TVs and HomePods, need HomeKit
  pairing, which is not implemented yet; they show as `failed` in
  `/api/status`. AirPlay 2 support is the top roadmap item.
- **Synchronisation.** Each output plays with its own buffering, so
  speakers in different rooms are not sample-aligned yet.
- **Mix modes.** All routes into an output are mixed. The `mix_mode` field
  is stored but exclusive and ducking modes are not implemented yet.

## Documentation

- [Architecture](docs/ARCHITECTURE.md): how audio flows through the system
- [REST API](docs/API.md)
- [Testing](docs/TESTING.md)
- [Roadmap](ROADMAP.md)
- [Contributing](CONTRIBUTING.md)

## Contributing

Contributions are welcome, from bug reports to new protocol adapters.
Read [CONTRIBUTING.md](CONTRIBUTING.md) to get started, and look for issues
labelled `good first issue`. Please report security problems privately as
described in [SECURITY.md](SECURITY.md).

## Acknowledgements

The ALAC decoder is a Go port of David Hammerton's ALAC decoder, and the
AirPlay implementation follows the protocol work of the shairport and
shairport-sync projects. See [NOTICE](NOTICE).

## License

[MIT](LICENSE)
