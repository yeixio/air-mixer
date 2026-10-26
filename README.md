# Air-Mixer 🎵

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8.svg)](https://golang.org)

**Universal Audio Protocol Translator & Mixer**

Air-Mixer is a home server application that translates between different audio streaming protocols, allowing you to mix and route audio from multiple sources (AirPlay, Bluetooth, Google Cast, UPnP/DLNA) to multiple destinations. All your speakers appear as a unified set of AirPlay speakers on your network.

## Features

- 🔄 **Protocol Translation**: Convert between AirPlay, Bluetooth, Google Cast, and UPnP/DLNA
- 🎚️ **Audio Mixing**: Mix multiple audio sources into a single output
- 🔀 **Flexible Routing**: Route one source to many destinations, or mix many sources to one
- 🌐 **Web UI**: Modern web interface for easy configuration and management
- 🐳 **Docker Support**: Easy deployment with Docker
- 📦 **Single Binary**: Standalone executable with embedded web UI
- 🔒 **Cross-Platform**: Works on macOS, Linux, and Windows

## Architecture

Air-Mixer acts as a central hub for audio streaming:

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  AirPlay    │     │  Bluetooth  │     │ Google Cast │
│   Source    │────▶│   Source    │────▶│   Source    │
└─────────────┘     └─────────────┘     └─────────────┘
       │                  │                    │
       └──────────────────┼────────────────────┘
                         │
                  ┌──────▼──────┐
                  │  Air-Mixer  │
                  │   Router    │
                  └──────┬──────┘
                         │
       ┌─────────────────┼─────────────────┐
       │                 │                 │
┌──────▼──────┐   ┌──────▼──────┐   ┌──────▼──────┐
│  AirPlay    │   │  Bluetooth  │   │ Google Cast │
│ Destination │   │ Destination │   │ Destination │
└─────────────┘   └─────────────┘   └─────────────┘
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for detailed architecture documentation.

## Quick Start

### Prerequisites

- Go 1.21 or later (for building from source)
- Docker (for Docker deployment)

### Installation

#### Using Docker (Recommended)

```bash
# Clone the repository
git clone https://github.com/yeixio/air-mixer.git
cd air-mixer

# Build and run with docker-compose
docker-compose up -d

# Or build and run manually
docker build -t airmixer .
docker run --network host -v $(pwd)/data:/app/data airmixer
```

#### Building from Source

```bash
# Clone the repository
git clone https://github.com/yeixio/air-mixer.git
cd air-mixer

# Install dependencies
go mod download

# Build
make build
# Or manually:
go build -o airmixer ./cmd/airmixer

# Run
./airmixer start
```

#### Using Make

```bash
# Build
make build

# Run
make run

# Run tests
make test

# Build for all platforms
make build-all

# See all available commands
make help
```

### Configuration

1. Start the application: `./airmixer start`
2. Open your browser: `http://localhost:9001`
3. Configure input servers (AirPlay, Bluetooth, etc.)
4. Configure output clients (speakers, devices)
5. Create routes to connect sources to destinations

## Usage

### Command Line

```bash
# Start the server
airmixer start

# Start with custom port
airmixer start --web-port 8080

# Show version
airmixer version

# Show help
airmixer help
```

### Web Interface

Once running, access the web UI at `http://localhost:9001` (default port).

The web interface allows you to:
- Add/remove input servers (AirPlay, Bluetooth, etc.)
- Add/remove output clients (speakers, devices)
- Create and manage routing rules
- Monitor active audio streams
- Adjust volume and mixing settings

## Supported Protocols

### Input Protocols (Sources)
- ✅ **AirPlay** - Receive audio from iOS/macOS devices
- 🚧 **Bluetooth** - A2DP audio input (in progress)
- 🚧 **Google Cast** - Receive from Cast-enabled apps (in progress)
- 🚧 **UPnP/DLNA** - Media server input (in progress)

### Output Protocols (Destinations)
- ✅ **AirPlay** - Send to AirPlay speakers
- 🚧 **Bluetooth** - Send to Bluetooth speakers (in progress)
- 🚧 **Google Cast** - Send to Chromecast devices (in progress)
- 🚧 **UPnP/DLNA** - Send to DLNA renderers (in progress)
- 🚧 **Local Audio** - Play on system audio output (in progress)

## Development

### Project Structure

```
air-mixer/
├── cmd/airmixer/          # Main application entry point
├── internal/               # Internal packages
│   ├── audio/             # Audio engine (decoders, mixer, output)
│   ├── protocols/         # Protocol adapters
│   ├── router/            # Routing engine
│   ├── api/               # REST API handlers
│   ├── config/            # Configuration management
│   └── db/                # Database layer
├── webui/                 # Web UI source and assets
├── migrations/            # Database migrations
└── docs/                  # Documentation
```

### Running Tests

```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run specific package tests
go test ./internal/router/...
```

For comprehensive testing instructions, see [docs/TESTING.md](docs/TESTING.md).

### Code Style

```bash
# Format code
make fmt

# Run linter
make lint
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed contribution guidelines.

## Documentation

All documentation is organized in the [docs/](docs/) folder:

- [Architecture](docs/ARCHITECTURE.md) - System architecture and design
- [Testing Guide](docs/TESTING.md) - Comprehensive testing instructions
- [Implementation Plan](docs/PLAN.md) - Development roadmap
- [Contributing](CONTRIBUTING.md) - How to contribute
- [Completion Summary](docs/COMPLETION_SUMMARY.md) - Current implementation status

## Roadmap

### MVP (Current Focus)
- [x] Basic AirPlay server
- [x] Project structure and architecture
- [ ] Cross-platform audio engine
- [ ] Web UI for configuration
- [ ] Routing engine
- [ ] SQLite persistence
- [ ] Docker support

### Future Enhancements
- [ ] Additional protocols (Spotify Connect, Sonos, etc.)
- [ ] Audio effects (EQ, reverb, delay)
- [ ] Multi-room synchronization
- [ ] Scheduled routing rules
- [ ] Audio recording and streaming
- [ ] REST API for automation
- [ ] Plugin system for custom protocols

## Troubleshooting

### mDNS/Bonjour Not Working

On Linux, you may need to install Avahi:
```bash
# Ubuntu/Debian
sudo apt-get install avahi-daemon avahi-utils

# Fedora/RHEL
sudo dnf install avahi avahi-tools
```

### Audio Not Playing

- Check that audio devices are properly configured
- Verify firewall settings allow traffic on required ports
- Check logs for error messages: `./airmixer start --verbose`

### Docker Network Issues

Use `--network host` mode for mDNS/Bonjour to work properly:
```bash
docker run --network host airmixer
```

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Inspired by projects like Shairport, AirConnect, and others
- Built with Go and modern web technologies
- Thanks to all contributors!

## Support

- 📖 [Documentation](ARCHITECTURE.md)
- 🐛 [Issue Tracker](https://github.com/yeixio/air-mixer/issues)
- 💬 [Discussions](https://github.com/yeixio/air-mixer/discussions)

---

**Note**: This project is in active development. Some features may not be fully implemented yet. See the roadmap above for current status.
