# Air-Mixer Implementation Plan

## Phase 1: Foundation ✅ (COMPLETED)

### Completed Tasks

1. **Project Structure & Documentation**
   - ✅ Created comprehensive architecture document (ARCHITECTURE.md)
   - ✅ Set up modern Go module structure
   - ✅ Created directory structure for new architecture
   - ✅ Updated import paths to new module name
   - ✅ Created MIT LICENSE
   - ✅ Created CONTRIBUTING.md with guidelines
   - ✅ Created comprehensive README.md
   - ✅ Created .gitignore

2. **Build System**
   - ✅ Created Makefile with common tasks
   - ✅ Created Dockerfile for containerized deployment
   - ✅ Created docker-compose.yml
   - ✅ Updated CLI to use Cobra (modern CLI framework)
   - ✅ Moved main.go to cmd/airmixer/main.go

3. **Code Modernization**
   - ✅ Removed deprecated codegangsta/cli dependency
   - ✅ Updated error handling to return errors instead of panicking
   - ✅ Fixed import paths

## Phase 2: Core Infrastructure (IN PROGRESS)

### Next Steps

1. **Database Layer** (`internal/db/`)
   - [ ] Create SQLite database schema
   - [ ] Implement migrations system
   - [ ] Create repository pattern for data access
   - [ ] Models: Server, Client, Route, Settings

2. **Configuration Management** (`internal/config/`)
   - [ ] Configuration file support (YAML/JSON)
   - [ ] Environment variable support
   - [ ] Default configuration
   - [ ] Configuration validation

3. **Cross-Platform Audio Engine** (`internal/audio/`)
   - [ ] Research and choose audio library (PortAudio or similar)
   - [ ] Create audio decoder interface
   - [ ] Implement ALAC decoder (pure Go or bindings)
   - [ ] Implement PCM decoder
   - [ ] Create audio mixer
   - [ ] Create cross-platform audio output
   - [ ] Remove macOS-specific Core Audio CGO code

## Phase 3: Protocol Adapters

### AirPlay Adapter (`internal/protocols/airplay/`)
- [ ] Refactor existing AirPlay server code
- [ ] Create AirPlay client (for output)
- [ ] Implement protocol interface
- [ ] Add proper error handling
- [ ] Add connection management

### Bluetooth Adapter (`internal/protocols/bluetooth/`)
- [ ] Research Bluetooth libraries for each platform
- [ ] Implement A2DP server (input)
- [ ] Implement A2DP client (output)
- [ ] Handle platform-specific implementations

### Google Cast Adapter (`internal/protocols/chromecast/`)
- [ ] Research go-chromecast or similar
- [ ] Implement Cast receiver (input)
- [ ] Implement Cast sender (output)
- [ ] Handle authentication

### UPnP/DLNA Adapter (`internal/protocols/upnp/`)
- [ ] Research goupnp or similar
- [ ] Implement UPnP MediaServer (input)
- [ ] Implement UPnP MediaRenderer (output)
- [ ] Handle device discovery

## Phase 4: Routing & Mixing Engine

### Router (`internal/router/`)
- [ ] Define routing interface
- [ ] Implement 1-to-1 routing
- [ ] Implement 1-to-many routing
- [ ] Implement many-to-1 routing (mixing)
- [ ] Implement many-to-many routing
- [ ] Add volume control per route
- [ ] Add route enable/disable
- [ ] Add route priority/ordering

### Mixer (`internal/audio/mixer/`)
- [ ] Implement audio stream mixing
- [ ] Handle sample rate conversion
- [ ] Handle channel conversion (mono/stereo)
- [ ] Add per-source volume control
- [ ] Add mixing algorithms (linear, weighted, etc.)

## Phase 5: REST API

### API Layer (`internal/api/`)
- [ ] Choose HTTP router (Gorilla Mux or Gin)
- [ ] Implement server endpoints (CRUD)
- [ ] Implement client endpoints (CRUD)
- [ ] Implement route endpoints (CRUD)
- [ ] Implement status/health endpoints
- [ ] Add request validation
- [ ] Add error handling middleware
- [ ] Add CORS support
- [ ] Add authentication (optional)

## Phase 6: Web UI

### Frontend (`webui/`)
- [ ] Choose framework (React or Vue)
- [ ] Set up build system
- [ ] Create component structure
- [ ] Implement server management UI
- [ ] Implement client management UI
- [ ] Implement route management UI
- [ ] Add real-time status updates (WebSocket or polling)
- [ ] Add responsive design
- [ ] Embed static assets in binary (go:embed)

## Phase 7: Testing & Polish

### Testing
- [ ] Unit tests for core components
- [ ] Integration tests for protocols
- [ ] End-to-end tests
- [ ] Performance testing
- [ ] Cross-platform testing

### Documentation
- [ ] API documentation
- [ ] User guide
- [ ] Developer guide
- [ ] Deployment guide
- [ ] Troubleshooting guide

### Deployment
- [ ] Systemd service file
- [ ] LaunchAgent for macOS
- [ ] Windows service support
- [ ] Release automation
- [ ] CI/CD pipeline

## Current Status

**Completed**: Phase 1 (Foundation)
**In Progress**: Planning Phase 2
**Next**: Database layer and configuration management

## Notes

- The existing AirPlay server code works but needs refactoring
- Core Audio CGO code needs to be replaced with cross-platform solution
- Web UI is currently just a placeholder
- No routing/mixing logic exists yet
- Need to research best libraries for each protocol

## Dependencies to Research

1. **Audio Processing**
   - PortAudio bindings for Go
   - Pure Go ALAC decoder
   - Audio mixing libraries

2. **Protocol Libraries**
   - Bluetooth: Platform-specific (BlueZ, Core Bluetooth, etc.)
   - Google Cast: `github.com/vishen/go-chromecast`
   - UPnP: `github.com/huin/goupnp`

3. **Web Framework**
   - HTTP Router: `github.com/gorilla/mux` or `github.com/gin-gonic/gin`
   - WebSocket: `github.com/gorilla/websocket`

4. **Database**
   - SQLite: `github.com/mattn/go-sqlite3` or `modernc.org/sqlite` (pure Go)

5. **Configuration**
   - YAML: `gopkg.in/yaml.v3`
   - Environment: `github.com/spf13/viper`

