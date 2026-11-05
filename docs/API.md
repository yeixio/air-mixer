# REST API

The API is served on the web port (9001 by default) under `/api`. Requests
and responses are JSON. Errors use the matching HTTP status and a body of
the form:

```json
{"error": "name is required"}
```

| Status | Meaning |
|--------|---------|
| 400 | Invalid input or JSON |
| 404 | No such item or endpoint |
| 409 | An item with that ID already exists |
| 413 | Request body over 1 MB |
| 429 | Too many playbacks at once |
| 501 | Not implemented (AirPlay 2 pairing) |
| 502 | Discovery failed, or a playback URL could not be fetched or decoded |

There is no authentication yet, so keep the port on a trusted network.

## Servers (inputs)

```json
{
  "id": "server-8c1d0f2b9a3e4d5c",
  "name": "Living room",
  "protocol": "airplay",
  "enabled": true,
  "config": "{\"address\":\":49152\",\"interface\":\"en0\"}",
  "created_at": "2026-10-26T18:00:00Z",
  "updated_at": "2026-10-26T18:00:00Z"
}
```

| Method | Path | |
|--------|------|-|
| GET | `/api/servers` | List servers |
| POST | `/api/servers` | Create; returns 201 |
| GET | `/api/servers/{id}` | Get one |
| PUT | `/api/servers/{id}` | Update; omitted fields keep their value |
| DELETE | `/api/servers/{id}` | Stop and delete; returns 204 |

On create, `id` is generated when omitted and `enabled` defaults to
`true`. An enabled server is started immediately; if it cannot start (for
example the port is taken) nothing is saved and the error is returned.

`config` may be sent as a JSON string, as stored, or as an object.
AirPlay servers accept:

| Key | Default | |
|-----|---------|-|
| `address` | `:<airplay_port>` | RTSP listen address. Each receiver needs its own port. |
| `interface` | `server.interface` | Interface to advertise on |

## Clients (outputs)

```json
{
  "id": "client-1",
  "name": "Kitchen",
  "protocol": "airplay",
  "address": "192.168.1.20:7000",
  "enabled": true,
  "config": "{\"latency_ms\":2000}",
  "created_at": "2026-10-26T18:00:00Z",
  "updated_at": "2026-10-26T18:00:00Z"
}
```

Same endpoints as servers, under `/api/clients`. Creating an enabled
client returns at once and connects in the background; its state is
reported by `/api/status`.

| Protocol | `address` | `config` keys |
|----------|-----------|---------------|
| `airplay` | `host` or `host:port` (default port 5000) | `latency_ms` (default 2000), `disable_encryption` |
| `local` | ignored | `delay_ms` |
| `pulse` | ignored | `sink` (name, description or Bluetooth address; default sink if empty), `server` (PulseAudio server string; default `$PULSE_SERVER`), `delay_ms` |

`delay_ms` (0 to 5000) holds audio back on that output, to line up
speakers with different latencies, such as several Bluetooth speakers in
one room. A `pulse` client keeps retrying while its sink is missing, for
example while a Bluetooth speaker is off, and reconnects by itself when
the speaker drops off and comes back.

`POST /api/clients/{id}/pair` returns 501: AirPlay 2 pairing is not
implemented yet.

## Routes

```json
{
  "id": "route-3f2a9c0d1e4b5a6f",
  "name": "Party",
  "source_name": "Living room",
  "dest_names": ["Kitchen", "Patio"],
  "mix_mode": "replace",
  "volume": 0.8,
  "enabled": true,
  "priority": 0
}
```

Routes refer to servers and clients by **name**. A route is active while
its source and at least one destination are running.

| Method | Path | |
|--------|------|-|
| GET | `/api/routes` | List with live status, highest priority first |
| POST | `/api/routes` | Create; returns 201 |
| GET | `/api/routes/{id}` | Get one with live status |
| PUT | `/api/routes/{id}` | Update; omitted fields keep their value |
| DELETE | `/api/routes/{id}` | Delete; returns 204 |

On create, `volume` defaults to 1, `enabled` to `true` and `mix_mode` to
`replace`. `volume` must be between 0 and 1. All routes into one
destination are mixed regardless of `mix_mode` for now.

Listing and getting add live fields:

```json
{
  "...": "route fields as above",
  "active": true,
  "active_dests": ["Kitchen"],
  "source_available": true,
  "samples_routed": 1764000
}
```

Playback (below) adds **transient** routes while it plays. They carry
`"transient": true` and a `duck` level, are not saved, disappear when the
playback ends, and cannot be updated through `/api/routes`.

## Playback

Plays audio from a URL on any set of outputs: announcements,
text-to-speech, doorbells, alarms or a stream. This is what the
[Home Assistant integration](HOME_ASSISTANT.md) uses.

`POST /api/play`

```json
{
  "url": "http://homeassistant.local:8123/api/tts_proxy/abc123.mp3",
  "outputs": ["Kitchen", "client-2"],
  "mode": "duck",
  "volume": 0.9,
  "duck_level": 0.2,
  "repeat": 1,
  "loop": false,
  "max_duration": 60,
  "title": "Dinner is ready",
  "wait": false
}
```

| Field | | Default |
|-------|-|---------|
| `url` | An `http` or `https` URL of an MP3 or WAV file or stream. Required. | |
| `outputs` | Client IDs or names (names match ignoring case). Empty or omitted means every enabled client. | all |
| `mode` | `duck` lowers other audio on those outputs to `duck_level`; `interrupt` silences it; `mix` plays alongside it. | `duck` |
| `volume` | Playback volume, 0 to 1. | 1 |
| `duck_level` | Volume of other audio under `duck`, 0 to 1. | 0.2 |
| `repeat` | Number of times to play, up to 100. | 1 |
| `loop` | Repeat until stopped or `max_duration` passes. | `false` |
| `max_duration` | Stop after this many seconds. Every playback stops after an hour. | |
| `title` | Shown in status. | |
| `wait` | Respond when playback ends (200) instead of when it starts (202). | `false` |

Air-Mixer downloads the URL itself, so it must be reachable from the
Air-Mixer server. It is fetched once per repeat. MP3 (any bitrate and
sample rate) and WAV (8, 16, 24 or 32-bit integer PCM, or 32-bit float)
are supported; other formats return 502.

Other audio is never paused: it keeps playing underneath at the ducked
volume, fading down over a quarter of a second and back up when the
playback ends. Announcements (`duck` and `interrupt`) also duck `mix`
playbacks; several announcements on one output play together.

The response is the playback:

```json
{
  "id": "play-3",
  "url": "http://homeassistant.local:8123/api/tts_proxy/abc123.mp3",
  "title": "Dinner is ready",
  "outputs": ["Kitchen", "Den"],
  "mode": "duck",
  "volume": 0.9,
  "duck_level": 0.2,
  "repeat": 1,
  "state": "playing",
  "started_at": "2026-11-05T18:00:00Z",
  "position": 0
}
```

`state` is `playing`, then `finished`, `stopped` or `failed` (with
`error`). `position` is seconds played, across repeats. The query
string of `url` is left out of logs, since it often carries a token.

| Method | Path | |
|--------|------|-|
| POST | `/api/play` | Start a playback; 202, or 200 with `wait` |
| GET | `/api/playback` | `{"playback": [...]}`: playbacks in progress, oldest first |
| GET | `/api/playback/{id}` | One playback in progress |
| DELETE | `/api/playback/{id}` | Stop it; 204 |
| DELETE | `/api/playback?output=Kitchen` | Stop playback on one output (ID or name); a playback on other outputs too keeps playing there. Returns `{"stopped": 1}` |
| DELETE | `/api/playback` | Stop every playback; returns `{"stopped": n}` |

For example, a doorbell from a shell:

```sh
curl -X POST http://airmixer.local:9001/api/play \
  -d '{"url": "http://nas.local/sounds/doorbell.wav", "outputs": ["Kitchen", "Den"]}'
```

## Status and health

`GET /api/status`

```json
{
  "router_running": true,
  "routes_active": 1,
  "adapters": [
    {"id": "client-1", "name": "Kitchen", "protocol": "airplay", "kind": "client", "state": "connected"},
    {"id": "client-2", "name": "Apple TV", "protocol": "airplay", "kind": "client", "state": "failed",
     "error": "airplay: device requires AirPlay 2 pairing, which is not supported yet: unsupported device"}
  ],
  "version": "v0.2.0",
  "timestamp": 1793037600
}
```

Adapter states are `running` (servers), `connecting`, `connected` and
`failed` (clients). A connecting client may carry the last connection
error.

`GET /health` returns `{"status": "ok", "version": "..."}`, or 503 when
the database is unavailable.

## Discovery

`GET /api/discovery?timeout=5s` browses every protocol;
`GET /api/discovery/{protocol}` browses one. `timeout` is optional, at
most 30s.

```json
{
  "devices": [
    {
      "name": "Kitchen",
      "protocol": "airplay",
      "address": "192.168.1.20:7000",
      "port": 7000,
      "description": "AirPlay device: Kitchen",
      "metadata": {"am": "AirPort10,115", "et": "0,1", "service_name": "A1B2C3D4E5F6@Kitchen"}
    }
  ],
  "count": 1
}
```
