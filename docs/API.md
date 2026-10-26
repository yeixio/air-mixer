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
| 501 | Not implemented (AirPlay 2 pairing) |
| 502 | Discovery failed |

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
| `local` | ignored | none |

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
