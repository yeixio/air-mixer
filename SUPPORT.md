# Getting help with Air-Mixer

## Setting it up

The [README](README.md#quick-start) covers Docker, release binaries and
building from source, then the [first steps](README.md#first-steps) in the
web UI. [Configuration](README.md#configuration) lists where settings live,
and [config.yaml.example](config.yaml.example) documents every one.

## Something's wrong

- **Check the status first.** `GET /api/status` (or the web UI) shows each
  input and output as `running`, `connecting`, `connected` or `failed`,
  with the error that stopped it. A speaker that only speaks AirPlay 2
  shows as `failed`; see [current limitations](README.md#current-limitations).
- **Turn on debug logs** with `AIRMIXER_LOGGING_LEVEL=debug`.
- **Search the [issues](https://github.com/yeixio/air-mixer/issues)**:
  someone may have hit it already.
- **Open an issue.** Use the bug report for something broken, or the device
  compatibility report to tell us how a speaker behaves, good or bad. Leave
  out passwords and anything private from logs.
- **Security problems** go through private reporting instead; see
  [SECURITY.md](SECURITY.md).

You can also email hello@yeix.io.
