# AirPlay Integration

## Overview

The AirPlay protocol adapter has been integrated with the routing engine. The AirPlay server now works as a protocol adapter that can be managed through the REST API and routes audio through the router.

## Architecture

```
AirPlay Client (iOS/macOS)
        │
        ▼
RTSP Connection (TCP :49152)
        │
        ▼
AirPlay Server (apserver/)
        │
        ├─► ANNOUNCE → Extract fmtp, AES keys
        │
        ├─► SETUP → Configure transport
        │
        ├─► RECORD → Start audio streaming
        │
        ▼
UDP RTP Packets (:6000)
        │
        ▼
AirPlay Protocol Adapter (internal/protocols/airplay/)
        │
        ├─► Decrypt AES-encrypted ALAC
        ├─► Decode ALAC to PCM
        └─► Send to AudioStream channel
        │
        ▼
Router (internal/router/)
        │
        ▼
Destination Clients
```

## Integration Points

### 1. RTSP Handler Integration

The RTSP handler in `apserver/rtsp.go` now calls a global handler when ANNOUNCE is received:

```go
// In ANNOUNCE case:
if globalANNOUNCEHandler != nil {
    globalANNOUNCEHandler(id, fmtp, aesiv, aeskey)
}
```

### 2. AirPlay Server Adapter

The `internal/protocols/airplay/server.go` implements the `ProtocolServer` interface:

- **Start()** - Starts RTSP server with handler callback
- **SetDecoder()** - Called when ANNOUNCE received, sets up ALAC decoder
- **startUDPListener()** - Listens for RTP packets on UDP :6000
- **AudioStream()** - Returns channel with decoded audio packets

### 3. Flow

1. Client connects via RTSP
2. ANNOUNCE message received with fmtp, AES keys
3. Handler callback sets up ALAC decoder
4. RECORD message starts streaming
5. RTP packets arrive on UDP :6000
6. Packets are decrypted and decoded
7. Audio packets sent to AudioStream channel
8. Router picks up packets and routes to destinations

## Usage

The AirPlay server can be created and managed through the REST API:

```bash
# Create an AirPlay server
curl -X POST http://localhost:9001/api/servers \
  -H "Content-Type: application/json" \
  -d '{
    "id": "airplay-1",
    "name": "Main AirPlay Server",
    "protocol": "airplay",
    "enabled": true,
    "config": "{\"address\": \":49152\", \"interface\": \"auto\"}"
  }'
```

## Configuration

The AirPlay server uses the following configuration:
- **Address**: TCP port for RTSP (default: :49152)
- **Interface**: Network interface name (or "auto" for auto-detect)
- **UDP Port**: Fixed at :6000 for RTP packets

## Status

✅ **Completed:**
- RTSP handler integration
- Protocol adapter structure
- ALAC decoder integration
- UDP RTP packet handling
- Audio stream channel

⚠️ **Needs Testing:**
- End-to-end audio flow
- Multiple concurrent connections
- Error handling and recovery
- Performance optimization

## Future Enhancements

- Support for multiple AirPlay servers on different ports
- Dynamic UDP port assignment
- Better error handling and reconnection
- Volume control integration
- Connection state management

