# Audio Engine

This package provides cross-platform audio processing capabilities for Air-Mixer.

## Components

### Interfaces

- **AudioDecoder**: Decodes audio from various formats (ALAC, PCM, etc.)
- **AudioEncoder**: Encodes audio to various formats
- **AudioMixer**: Mixes multiple audio streams
- **AudioOutput**: Handles audio playback
- **SampleRateConverter**: Converts between sample rates
- **ChannelConverter**: Converts between channel configurations

### Implementations

#### Audio Output
- **OtoOutput**: Uses `github.com/ebitengine/oto/v3` for cross-platform audio playback
  - Pure Go implementation (no CGO required)
  - Supports macOS, Linux, Windows
  - Low latency audio playback

#### Decoders
- **PCMDecoder**: Decodes PCM audio data
  - Supports 16-bit, 24-bit, and 32-bit samples
  - Supports both little-endian and big-endian formats
  - Normalizes samples to [-1.0, 1.0] range

#### Converters
- **LinearSampleRateConverter**: Converts sample rates using linear interpolation
- **ChannelConverter**: Converts between mono, stereo, and multi-channel audio

#### Mixer
- **Mixer**: Real-time audio mixing
  - Supports multiple input sources
  - Per-source volume control
  - Automatic clipping prevention
  - Thread-safe operations

## Usage Example

```go
package main

import (
    "github.com/yeixio/air-mixer/internal/audio"
)

func main() {
    // Create audio output
    output, err := audio.NewOtoOutput(44100, 2) // 44.1kHz, stereo
    if err != nil {
        panic(err)
    }
    defer output.Close()

    // Create a stream channel
    stream := make(chan []float32, 100)

    // Start playback
    if err := output.Play(stream, 44100, 2); err != nil {
        panic(err)
    }

    // Send audio samples
    samples := make([]float32, 1024)
    // ... fill samples with audio data ...
    stream <- samples

    // Stop playback
    output.Stop()
}
```

## Future Work

- [ ] ALAC decoder implementation (pure Go or bindings)
- [ ] Higher-quality sample rate conversion (cubic/sinc interpolation)
- [ ] Audio effects (EQ, reverb, etc.)
- [ ] Audio recording support
- [ ] Platform-specific optimizations

## Dependencies

- `github.com/ebitengine/oto/v3` - Cross-platform audio I/O

