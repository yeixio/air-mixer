# REST API

The API is served on the web port (9001 by default) under `/api`. Requests
and responses are JSON. Errors use the matching HTTP status and a body of
the form:

```json
{"error": "name is required", "code": "name_required"}
```

`code` is a stable name for the error and `params` fills in the names in
its message, such as `{"protocol": "dlna"}` for
`unsupported_client_protocol`. Clients can show the code in their reader's
language from [i18n/locales](../i18n/README.md#errors); `error` is always
the English text. Errors without a code, such as a failed discovery, carry
only `error`.

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

| Protocol | `config` keys |
|----------|---------------|
| `airplay` | `address` (RTSP listen address; default: the first free port in `server.airplay_port_range`, 49152-49199 unless set), `interface` (interface to advertise on, default `server.interface`) |
| `radio` | `url` (required): an internet radio station, a PLS or M3U playlist, an HLS stream (`.m3u8`) or an audio file, over http, https, rtsp or rtmp |
| `pulse` | `source` (name, description, a phone's Bluetooth address, or `<sink>.monitor`; default source if empty), `server` (PulseAudio server string; default `$PULSE_SERVER`) |
| `bluetooth` | `device` (a phone's Bluetooth address: record only that phone; default whichever device plays), `adapter` (default `hci0`), `hidden` (`true`: stay out of scans, for phones already paired), `server` (PulseAudio server string) |
| `spotify` | `bitrate`: 96, 160 or 320 (default 320). Needs `librespot` |
| `airplay2` | `address`: the RTSP port, such as `:7000` (default shairport-sync's 7000). Needs `shairport-sync` built with AirPlay 2 |
| `upnp` | `address`: the HTTP listen address for control points, such as `:49494` (default: a free port, which SSDP advertises) |
| `matinee` | none: it shows up in Matinee as a player named after the server (see [Matinee](#matinee)) |

A `radio` server plays its URL for as long as it is on. A station that
drops is reconnected with backoff (1 s, doubling up to a minute) and a
file starts again when it ends. MP3 and WAV are decoded in Go; AAC, Ogg,
Opus, FLAC, HLS, RTSP and RTMP need `ffmpeg` on the server's `$PATH`,
which the Docker image includes.

A `bluetooth` server makes the host a Bluetooth speaker named after the
server. Over BlueZ's D-Bus API it powers the adapter, sets its alias,
makes it discoverable and pairable with no timeout, and registers a
pairing agent that accepts any phone without a PIN, trusts it, and
authorises only audio (A2DP and AVRCP). It records whichever Bluetooth
source PipeWire or PulseAudio creates when a phone plays, so the host
needs BlueZ and PipeWire (or PulseAudio with its Bluetooth module), and
the server needs access to the D-Bus system bus. Without BlueZ, it still
records a phone that is already paired, and `/api/status` shows why
pairing is off.

A `spotify` server shows up in the Spotify app as a Connect speaker,
through `librespot`. It keeps librespot's credentials under
`<data_dir>/spotify/<id>`, so it stays signed in to the account that last
played to it. An `airplay2` server shows up on Apple devices as an
AirPlay 2 speaker, which they can add to multi-room groups, through
`shairport-sync` built with AirPlay 2 support and with `nqptp` running.
Each runs its program, reads its audio from the program's output, and
starts it again when it exits. Creating either fails with
`program_missing` when the program is not on the server's `$PATH`, and
`GET /api/protocols` reports them with `"available": false`.

A `upnp` server is a UPnP/DLNA media renderer named after the server:
apps such as BubbleUPnP, Hi-Fi Cast, foobar2000 or Windows' "Cast to
Device" list it as a speaker and send it a URL, which it plays like a
`radio` server, once per track (with `SetNextAVTransportURI` for gapless
queues). It handles play, pause, stop, volume and mute, and sends GENA
events so apps show its state.

A `pulse` server records from a PulseAudio or PipeWire source: a line-in,
a USB audio interface, the monitor of a sink, or a phone playing to the
host over Bluetooth (pair it with the host and give its address). It waits
while the source is missing, such as while no phone is connected, and
starts again when the source comes back.

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
| `airplay2` | `host` or `host:port` (default port 7000), as discovery gives it | `latency_ms` (default 2000) |
| `local` | ignored | `delay_ms` |
| `pulse` | ignored | `sink` (name, description or Bluetooth address; default sink if empty), `server` (PulseAudio server string; default `$PULSE_SERVER`), `delay_ms` |
| `cast` | the Cast device's `host` or `host:port` (default port 8009; speaker groups have their own port, which discovery gives) | none |
| `upnp` | the renderer's device description URL, as discovery gives it; a bare host or `host:port` means a Sonos player | `format`: `wav`, `mp3` (needs ffmpeg), or empty for MP3 on Sonos with ffmpeg installed and WAV otherwise |
| `snapcast` | snapserver's `host` or `host:port` of its TCP source (default port 4953) | `sample_rate` (default 48000, snapserver's default) |
| `stream` | ignored | none; see [Streams](#streams) |
| `matinee` | the Matinee player's id, from `/api/matinee/players` | `library_id`: the music library its station goes in (default: the first) |

`delay_ms` (0 to 5000) holds audio back on that output, to line up
speakers with different latencies, such as several Bluetooth speakers in
one room. A `pulse` client keeps retrying while its sink is missing, for
example while a Bluetooth speaker is off, and reconnects by itself when
the speaker drops off and comes back.

An `airplay2` client is **experimental** and has not been tried on
Apple hardware yet. It plays on receivers that refuse classic AirPlay,
such as HomePods and recent Apple TVs: it pairs with HomeKit's transient
pairing (the receiver must allow "Anyone on the same network", without a
password), encrypts the RTSP connection with ChaCha20-Poly1305, and sends
ALAC audio as a realtime stream, each packet sealed with the session key.
It uses NTP timing, so receivers that only accept PTP, such as
shairport-sync's AirPlay 2 mode, do not play; those speak classic AirPlay
anyway. A receiver that wants a PIN fails with "refused transient
pairing". Please report what works with the device report form.

A `cast` client plays on a Google Cast device (Chromecast, Nest and
Google Home speakers, speaker groups, TVs with Chromecast built in). It
serves its audio as a stream (see [Streams](#streams)) and has the device
play it in Google's Default Media Receiver. The receiver starts when
audio starts and stops after a minute of silence. If someone casts
something else in between, Air-Mixer leaves it alone until its own audio
stops and starts again. Cast devices buffer a live stream for a few
seconds, so give the outputs that play alongside one a `delay_ms` to
match. The device must be able to reach Air-Mixer's web port; set
`server.public_url` if it can't be worked out.

A `upnp` client plays on a UPnP/DLNA media renderer: Sonos, AV
receivers, smart TVs, Kodi and the like. Like `cast`, it starts the
renderer when audio starts (`SetAVTransportURI` and `Play`), stops it
after a minute of silence, plays again if the renderer gives up on the
stream, and steps aside when another app sets a different URI. Sonos gets
MP3 through its `x-rincon-mp3radio` scheme, which it needs for endless
streams; use a group's coordinator as the address. Renderers fetch the
stream with `?raw=1`, a close-delimited body with DLNA's streaming
headers, and `?format=mp3` when they get MP3.

A `snapcast` client writes 16-bit stereo PCM into a TCP source on
snapserver, which every Snapcast client then plays in sync. Add the source
to `snapserver.conf`:

```ini
[stream]
source = tcp://0.0.0.0:4953?name=Air-Mixer&mode=server
```

and set `sample_rate` if the source names another rate with
`sampleformat`.

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

## Protocols

`GET /api/protocols` lists the protocols this build supports, inputs
(`"kind": "server"`) first, with the fields each takes, so a client can
build its forms. `label`, `hint` and `placeholder_key` are keys in the web
UI's translations (`i18n/locales/<lang>/web.json`); `placeholder` is shown
as it is. A client field with `"key": "address"` is the client's `address`; every
other key goes in `config`. `available` is `false` when the program
named in `requires` is not installed, and creating that protocol fails.

```json
[
  {
    "name": "radio", "kind": "server", "title": "Internet radio",
    "description": "protocols.radioServer", "available": true,
    "fields": [
      {"key": "url", "type": "url", "required": true, "label": "fields.url",
       "hint": "fields.urlHint", "placeholder": "https://stream.example.com/radio.mp3"}
    ]
  },
  {
    "name": "spotify", "kind": "server", "title": "Spotify Connect",
    "description": "protocols.spotifyServer",
    "requires": "librespot", "available": false,
    "fields": [
      {"key": "bitrate", "type": "number", "label": "fields.bitrate", "placeholder": "320"}
    ]
  },
  {
    "name": "snapcast", "kind": "client", "title": "Snapcast",
    "description": "protocols.snapcastClient", "discovery": true, "available": true,
    "fields": [
      {"key": "address", "type": "text", "required": true, "label": "common.address", "placeholder": "snapserver.local:4953"},
      {"key": "sample_rate", "type": "number", "label": "fields.sampleRate", "placeholder": "48000"}
    ]
  }
]
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
     "error": "airplay: device requires AirPlay 2; add it as an AirPlay 2 output instead: unsupported device"}
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
most 30s. AirPlay speakers (`_raop._tcp`), Google Cast devices and groups
(`_googlecast._tcp`, named as in the Google Home app) and Snapcast servers
(`_snapcast._tcp`, given with the TCP source port 4953) are found over
mDNS, over IPv4 alone on hosts without IPv6. UPnP/DLNA renderers and Sonos
players are found with an SSDP search for `MediaRenderer:1`; their
address is their description URL.

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

## Streams

`GET /api/clients/{id}/stream` plays a `stream` or `matinee` output live,
as a WAV of 16-bit stereo PCM at 44.1 kHz whose sizes are `0xFFFFFFFF`.
It plays silence while nothing is routed to the output. Each listener gets
its own copy; one that falls a second behind loses the oldest audio. 404
means the output isn't connected or isn't a stream. Open it in VLC or a
browser, or let a Matinee output hand it to a Matinee player.

## Matinee

Air-Mixer works with [Matinee](https://github.com/yeixio/matinee), the
self-hosted media server, both ways:

- A **`matinee` server** (input) shows up in Matinee, and in Matinee's Home
  Assistant integration, as a player. Whatever is played on it comes into
  Air-Mixer and goes wherever its routes send it. It takes play, pause,
  seek, stop, previous, next, volume and mute; a song goes on to the next
  song of its album, and the place is saved in Matinee.
- A **`matinee` client** (output) plays on a Matinee player: a TV, a phone
  or a browser with Matinee open. Air-Mixer adds the output's
  [stream](#streams) to the first music library as a radio station named
  "Air-Mixer · <output name>" and asks the player to play it. Matinee
  converts it to AAC for the player. When the player is closed the client
  stays `connecting` and starts again once the player is back. Matinee
  players buffer a few seconds, so they lag behind AirPlay speakers.

Both need Matinee 0.2.3 or later (`GET /items/{id}/audio` and stations
on the home network) and one sign-in, approved by a Matinee admin.

| Method | Path | |
|--------|------|-|
| GET | `/api/matinee` | The server, whether Air-Mixer is signed in, and the code while it signs in |
| PUT | `/api/matinee` | Set the server: `{"url": "http://192.168.1.20:7171"}`. A different server signs out. |
| POST | `/api/matinee/link` | Ask for a sign-in code; returns the state with `link` |
| DELETE | `/api/matinee/link` | Sign out; returns 204 |
| GET | `/api/matinee/players` | Matinee players to play on; 409 until signed in |

```json
{
  "url": "http://192.168.1.20:7171",
  "signed_in": false,
  "link": {
    "user_code": "WXYZ-2345",
    "verification_url": "http://192.168.1.20:7171/link",
    "verification_url_complete": "http://192.168.1.20:7171/link?code=WXYZ2345",
    "expires_at": "2026-11-05T18:00:00Z"
  }
}
```

Open `verification_url` as a Matinee admin and enter the code; Air-Mixer
notices within a few seconds and `signed_in` turns true. `link.error`
says why a code stopped working (it expired or was turned down).

Players:

```json
[{"id": "3f0c…", "name": "Living room TV", "platform": "tvos", "online": true, "user": "mike"}]
```

Matinee fetches an output's stream from Air-Mixer, so it has to reach
Air-Mixer's web port. Air-Mixer uses its own address on the way to the
Matinee server; set `server.public_url` (`AIRMIXER_SERVER_PUBLIC_URL`)
when that isn't reachable, such as behind NAT or in Kubernetes.
