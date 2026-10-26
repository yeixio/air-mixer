# Contributing to Air-Mixer

Thanks for your interest! Bug reports, device compatibility notes,
documentation and code are all welcome.

## Ways to help

- **Try it with your speakers** and open an issue with what worked and
  what did not. Compatibility reports are some of the most useful
  contributions to an audio bridge.
- **Pick up an issue** labelled `good first issue` or `help wanted`, or an
  item from the [roadmap](ROADMAP.md). For anything large, such as a new
  protocol, open an issue first so we can agree on the approach.
- **Improve the docs** when something confused you.

## Development setup

You need Go 1.26 or later. Nothing else: the build is pure Go, and the
tests run without audio hardware or network access beyond loopback.

```bash
git clone https://github.com/yeixio/air-mixer.git
cd air-mixer
make test      # tests with the race detector
make run       # build and start on http://localhost:9001
make dev       # hot reload; needs github.com/air-verse/air
make check     # vet, golangci-lint and tests: what CI runs
```

The race detector needs cgo, so `make test` turns it on for tests only.
Release builds are always `CGO_ENABLED=0`.

## Project layout

```text
cmd/airmixer/          command-line entry point
internal/app/          wires everything together; Run and graceful shutdown
internal/api/          REST API and web UI handler
internal/service/      keeps the database and the running system in step
internal/router/       routes and mixes audio between sources and sinks
internal/protocols/    adapter interfaces and the Manager that runs them
  airplay/             AirPlay (RAOP) receiver and sender
  local/               the machine's own sound output
internal/audio/        sample formats, conversion, mixer
  alac/                Apple Lossless decoder and encoder
internal/discovery/    finds speakers on the network (mDNS)
internal/db/           SQLite storage and migrations
internal/config/       configuration loading
internal/logging/      slog setup
webui/                 the embedded single-page UI
```

[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) explains how audio flows
between these.

## Adding a protocol

A protocol is an adapter in `internal/protocols/<name>` that implements
`protocols.Server` (an input), `protocols.Client` (an output), or both.

1. Implement the interface. Servers deliver `protocols.Packet`s in any
   format; the Manager converts them. Clients receive audio in
   `audio.Internal` (44.1 kHz stereo float32) and must never block in
   `Send`.
2. Return an error wrapping `protocols.ErrUnsupportedDevice` from `Connect`
   when retrying cannot help, so the Manager stops retrying.
3. Add it to the factory in `internal/service/factory.go`, with any
   options it needs in `ServerConfig` or `ClientConfig`.
4. Add a discoverer in `internal/discovery` if the protocol has one.
5. Test it without real devices: the AirPlay loopback test in
   `internal/protocols/airplay/airplay_test.go` is a good model.

## Pull requests

- Keep each PR focused on one change, and include tests for new behaviour.
- Run `make check` before pushing; CI runs the same checks on Linux and
  macOS.
- Write commit messages in the imperative ("Add Cast discovery"), with a
  body explaining why when it is not obvious.
- Update `docs/API.md` for API changes and `README.md` for user-facing
  changes.

## Style

- Format with `gofmt`; imports are grouped standard library, third party,
  then this module.
- Log with `log/slog` through the logger you are given, with structured
  key/value pairs.
- Wrap errors with context (`fmt.Errorf("open UDP port: %w", err)`) and
  compare them with `errors.Is` and `errors.As`.
- Comment why, not what. Exported identifiers get doc comments.

## Code of conduct

Everyone taking part is expected to follow the
[code of conduct](CODE_OF_CONDUCT.md).
