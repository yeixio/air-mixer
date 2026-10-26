# Testing

```bash
make test           # every test, with the race detector
make test-coverage  # writes coverage.html
make fuzz           # fuzz the ALAC decoder for 30 seconds
make check          # vet + golangci-lint + tests, as in CI
```

The suite needs no audio hardware, no speakers and no network beyond
loopback, so it runs the same on a laptop and in CI.

## What is covered

| Package | How |
|---------|-----|
| `audio/alac` | Decoder output compared bit-for-bit with ffmpeg for 16-bit stereo, 16-bit mono and 24-bit stereo; encoder round trips; malformed input; a fuzz test. |
| `audio` | Conversions, resampling, channel remixing and the mixer's lag and volume handling. |
| `router` | Routes, registration in any order, fan-out, mixing, removal while audio flows, and the no-send-after-unregister guarantee. |
| `protocols` | The Manager with fake adapters: format conversion, retry with backoff, permanent failures, removal while connecting. |
| `protocols/airplay` | RTSP, SDP, RTP and crypto units, and a loopback test that streams through the real sender into the real receiver, encrypted and not, and checks every sample arrives intact. |
| `protocols/local` | Queueing, silence padding and buffer limits, with the sound card replaced by a fake. |
| `service` | Database and runtime kept in step, rollbacks, and a restart that restores audio flow from the database. |
| `api` | Every endpoint over `httptest`, including the request bodies the web UI sends. |
| `app` | Starts the whole server, uses it over HTTP, shuts down and restarts. |
| `db`, `config`, `logging`, `discovery` | Unit tests, including migration of a database from the original prototype. |

## ALAC fixtures

`internal/audio/alac/testdata` holds short ALAC streams and the PCM ffmpeg
decodes them to. Regenerate them with `testdata/generate.sh` (needs ffmpeg
and ffprobe) if you add cases.

## Testing with real devices

1. `make run`, then open <http://localhost:9001>.
2. Add an AirPlay server and a `local` client, and route one to the other.
3. Play to the new speaker from an iPhone or Mac; it should come out of
   the server's speakers.
4. For AirPlay output, add a client for a receiver on your network. An
   easy one to test against is shairport-sync:

   ```bash
   docker run --rm --net host mikebrady/shairport-sync
   ```

Run with `AIRMIXER_LOGGING_LEVEL=debug` to see every RTSP request.
