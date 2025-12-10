# Air-Mixer Architecture

## Overview

Air-Mixer is a universal audio protocol translator and mixer that allows multiple audio input protocols (AirPlay, Bluetooth, Google Cast, UPnP/DLNA) to be routed and mixed to multiple output protocols. All speakers appear as a unified set of AirPlay speakers on the network.

## Core Architecture

### Component Layers

```
┌─────────────────────────────────────────────────────────┐
│                    Web UI (Embedded)                    │
│              React/Vue SPA (Static Assets)              │
└─────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────┐
│                    REST API Layer                        │
│         HTTP/JSON API for Configuration                  │
└─────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────┐
│                  Core Engine                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │   Routing    │  │    Mixing    │  │   Config     │  │
│  │   Engine     │  │    Engine    │  │   Manager   │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  │
└─────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────┐
│              Protocol Adapters (Input)                   │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────┐ │
│  │ AirPlay  │  │Bluetooth │  │GoogleCast│  │  UPnP  │ │
│  └──────────┘  └──────────┘  └──────────┘  └────────┘ │
└─────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────┐
│              Audio Engine (Cross-Platform)               │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │  ALAC    │  │   PCM    │  │  Mixer   │             │
│  │ Decoder  │  │  Buffer  │  │          │             │
│  └──────────┘  └──────────┘  └──────────┘             │
└─────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────┐
│            Protocol Adapters (Output)                    │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────┐ │
│  │ AirPlay  │  │Bluetooth │  │GoogleCast│  │  UPnP  │ │
│  └──────────┘  └──────────┘  └──────────┘  └────────┘ │
└─────────────────────────────────────────────────────────┘
                            │
┌─────────────────────────────────────────────────────────┐
│                    Persistence Layer                     │
│                    SQLite Database                       │
└─────────────────────────────────────────────────────────┘
```

## Directory Structure

```
air-mixer/
├── cmd/
│   └── airmixer/          # Main application entry point
├── internal/
│   ├── audio/             # Cross-platform audio engine
│   │   ├── decoder/       # ALAC, PCM decoders
│   │   ├── mixer/         # Audio mixing logic
│   │   └── output/        # Platform-specific output (PortAudio/ALSA/etc)
│   ├── protocols/         # Protocol adapters
│   │   ├── airplay/       # AirPlay server/client
│   │   ├── bluetooth/     # Bluetooth A2DP
│   │   ├── chromecast/    # Google Cast
│   │   └── upnp/          # UPnP/DLNA
│   ├── router/            # Routing engine
│   ├── api/               # REST API handlers
│   ├── config/            # Configuration management
│   └── db/                # Database layer (SQLite)
├── webui/                 # Embedded web UI
│   ├── dist/              # Built static assets (gitignored)
│   └── src/               # Source files (if building UI separately)
├── pkg/                   # Public packages (if any)
├── migrations/            # Database migrations
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── Makefile
├── README.md
├── ARCHITECTURE.md
├── CONTRIBUTING.md
├── LICENSE
└── .gitignore
```

## Key Components

### 1. Audio Engine (`internal/audio/`)

**Purpose**: Cross-platform audio processing and playback

**Responsibilities**:
- Decode audio streams (ALAC, PCM, etc.)
- Mix multiple audio streams
- Output to system audio (PortAudio for cross-platform)
- Buffer management
- Sample rate conversion

**Key Interfaces**:
```go
type AudioDecoder interface {
    Decode(data []byte) ([]float32, error)
    SampleRate() int
    Channels() int
}

type AudioMixer interface {
    AddSource(id string, source <-chan []float32)
    RemoveSource(id string)
    Output() <-chan []float32
}

type AudioOutput interface {
    Play(stream <-chan []float32, sampleRate, channels int) error
    Stop() error
    SetVolume(volume float32) error
}
```

### 2. Protocol Adapters (`internal/protocols/`)

**Purpose**: Handle different audio streaming protocols

**Structure**: Each protocol has:
- Server (for receiving audio)
- Client (for sending audio)
- Common interface

**Interfaces**:
```go
type ProtocolServer interface {
    Start() error
    Stop() error
    AudioStream() <-chan AudioPacket
    Name() string
    Protocol() string
}

type ProtocolClient interface {
    Connect(target string) error
    Disconnect() error
    SendAudio(packet AudioPacket) error
    Name() string
    Protocol() string
}
```

**Protocols**:
- **AirPlay**: RTSP server/client, mDNS, ALAC decoding
- **Bluetooth**: A2DP profile, SBC/AAC decoding
- **Google Cast**: Cast protocol, MP3/AAC support
- **UPnP/DLNA**: UPnP MediaRenderer, various codecs

### 3. Routing Engine (`internal/router/`)

**Purpose**: Route audio streams between sources and destinations

**Features**:
- 1-to-1 routing (single source to single destination)
- 1-to-many routing (single source to multiple destinations)
- Many-to-1 routing (multiple sources mixed to single destination)
- Many-to-many routing (multiple sources mixed to multiple destinations)

**Key Types**:
```go
type Route struct {
    ID          string
    SourceID    string
    SourceType  string
    DestIDs     []string
    DestTypes   []string
    MixMode     MixMode  // Replace, Mix, etc.
    Volume      float32
    Enabled     bool
}

type Router interface {
    AddRoute(route Route) error
    RemoveRoute(routeID string) error
    UpdateRoute(routeID string, route Route) error
    ListRoutes() []Route
    Start() error
    Stop() error
}
```

### 4. Configuration Management (`internal/config/`)

**Purpose**: Manage application configuration and persistence

**Storage**: SQLite database

**Schema**:
- `servers`: Protocol server instances
- `clients`: Protocol client instances (output targets)
- `routes`: Routing configurations
- `settings`: Application settings

### 5. REST API (`internal/api/`)

**Purpose**: HTTP API for web UI

**Endpoints**:
- `GET /api/servers` - List input servers
- `POST /api/servers` - Create input server
- `DELETE /api/servers/:id` - Remove input server
- `GET /api/clients` - List output clients
- `POST /api/clients` - Create output client
- `DELETE /api/clients/:id` - Remove output client
- `GET /api/routes` - List routes
- `POST /api/routes` - Create route
- `PUT /api/routes/:id` - Update route
- `DELETE /api/routes/:id` - Remove route
- `GET /api/status` - System status

### 6. Web UI (`webui/`)

**Purpose**: User interface for managing the system

**Technology**: Modern SPA framework (React or Vue)
- Embedded as static assets in binary
- Served via `go:embed`
- Responsive design
- Real-time status updates (WebSocket or polling)

## Data Flow

### Example: AirPlay Input → Multiple Outputs

1. **AirPlay Server** receives RTSP connection
2. **AirPlay Adapter** decodes ALAC audio packets
3. **Audio Engine** processes decoded audio
4. **Router** receives audio stream and routes to configured destinations
5. **Protocol Clients** (Bluetooth, Google Cast, etc.) encode and send audio
6. **Audio Output** plays on local system (if configured)

### Example: Multiple Inputs → Mixed Output

1. **Multiple Protocol Servers** receive audio streams
2. **Audio Engine** decodes all streams
3. **Mixer** combines audio streams with volume control
4. **Router** sends mixed stream to destination
5. **Protocol Client** or **Audio Output** plays mixed audio

## Technology Choices

### Audio Processing
- **PortAudio**: Cross-platform audio I/O library
- **Pure Go ALAC decoder**: Replace CGO dependency
- **Native audio libraries**: Platform-specific optimizations

### Protocol Libraries
- **AirPlay**: Custom implementation (existing)
- **Bluetooth**: `github.com/go-ole/go-ole` (Windows) + BlueZ (Linux) + Core Bluetooth (macOS)
- **Google Cast**: `github.com/vishen/go-chromecast`
- **UPnP**: `github.com/huin/goupnp`

### Web Framework
- **HTTP Router**: `github.com/gorilla/mux` or `github.com/gin-gonic/gin`
- **WebSocket**: `github.com/gorilla/websocket` (for real-time updates)

### Database
- **SQLite**: `github.com/mattn/go-sqlite3` or `modernc.org/sqlite` (pure Go)

### CLI
- **Cobra**: Modern CLI framework (`github.com/spf13/cobra`)

## Security Considerations

1. **RSA Keys**: Generate per-instance keys, store securely
2. **Authentication**: Optional password protection for web UI
3. **Network**: Bind to localhost by default, allow configuration
4. **TLS**: Optional HTTPS for web UI

## Deployment

### Standalone Binary
- Single executable with embedded web UI
- Cross-compile for all platforms
- Systemd service file for Linux
- LaunchAgent for macOS

### Docker
- Multi-stage build
- Minimal base image (Alpine)
- Volume mounts for configuration
- Network mode: host (for mDNS/Bonjour)

## Future Enhancements

1. **Additional Protocols**: Spotify Connect, Sonos, etc.
2. **Audio Effects**: EQ, reverb, delay
3. **Scheduling**: Time-based routing rules
4. **Multi-room Sync**: Synchronized playback across outputs
5. **Recording**: Capture and save audio streams
6. **Streaming**: Stream to external services (Icecast, etc.)

