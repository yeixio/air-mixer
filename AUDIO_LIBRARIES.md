# Audio Library Research

This document tracks research on audio libraries for cross-platform audio processing in Go.

## Requirements

1. **Cross-platform audio I/O**: Play audio on macOS, Linux, and Windows
2. **ALAC decoder**: Decode Apple Lossless Audio Codec
3. **Audio mixing**: Mix multiple audio streams in real-time
4. **Sample rate conversion**: Convert between different sample rates
5. **Channel conversion**: Convert between mono/stereo/multi-channel

## Library Options

### Audio I/O (Playback)

#### 1. PortAudio Bindings
- **Library**: `github.com/gordonklaus/portaudio`
- **Status**: ⚠️ Requires CGO, needs PortAudio library installed
- **Pros**: 
  - Cross-platform (macOS, Linux, Windows)
  - Well-established library
  - Good performance
- **Cons**: 
  - Requires CGO compilation
  - Requires PortAudio to be installed on system
  - More complex build process
- **Recommendation**: Good option if CGO is acceptable

#### 2. Oto (Pure Go)
- **Library**: `github.com/hajimehoshi/oto`
- **Status**: ✅ Pure Go, no CGO required
- **Pros**:
  - Pure Go implementation
  - No external dependencies
  - Easy cross-compilation
- **Cons**:
  - Primarily designed for games
  - May have higher latency
  - Less control over audio parameters
- **Recommendation**: Good for simple use cases, may need testing for low-latency requirements

#### 3. Beep
- **Library**: `github.com/faiface/beep`
- **Status**: ✅ Pure Go, uses Oto for output
- **Pros**:
  - Pure Go
  - Higher-level API
  - Built-in resampling
  - Good documentation
- **Cons**:
  - Uses Oto underneath (same limitations)
  - May not support all audio formats
- **Recommendation**: Good option for higher-level audio processing

#### 4. Platform-Specific Libraries
- **macOS**: Core Audio (via CGO) - what we currently use
- **Linux**: ALSA/PulseAudio bindings
- **Windows**: DirectSound/WASAPI bindings
- **Status**: ⚠️ Requires platform-specific code
- **Recommendation**: Only if we need platform-specific optimizations

### ALAC Decoder

#### 1. Pure Go ALAC
- **Status**: ❓ Need to research if pure Go implementation exists
- **Options**:
  - Port existing C implementation to Go
  - Use CGO bindings to libalac
  - Use FFmpeg bindings

#### 2. FFmpeg Bindings
- **Library**: `github.com/xfr/goffmpeg` or similar
- **Status**: ⚠️ Requires FFmpeg installed
- **Pros**:
  - Supports many codecs including ALAC
  - Well-tested
- **Cons**:
  - Large dependency
  - Requires FFmpeg installation
  - May be overkill

#### 3. CGO Bindings to libalac
- **Status**: ⚠️ Requires CGO and libalac
- **Pros**:
  - Direct ALAC support
  - Good performance
- **Cons**:
  - Requires CGO
  - Need to find/maintain bindings

### Audio Mixing

Audio mixing can be implemented in pure Go without external libraries:
- Simple linear mixing: `mixed[i] = (source1[i] + source2[i] + ...) / numSources`
- Weighted mixing: `mixed[i] = (source1[i] * vol1 + source2[i] * vol2 + ...)`
- Clipping protection: Ensure values stay in [-1.0, 1.0] range

### Sample Rate Conversion

#### 1. Beep's Resampler
- **Library**: Part of `github.com/faiface/beep`
- **Status**: ✅ Pure Go
- **Pros**: Already implemented, good quality
- **Cons**: Tied to Beep library

#### 2. Pure Go Implementation
- **Status**: ✅ Can implement ourselves
- **Options**:
  - Linear interpolation (simple, fast)
  - Cubic interpolation (better quality)
  - Sinc interpolation (best quality, slower)

### Channel Conversion

Can be implemented in pure Go:
- Mono to Stereo: Duplicate channel
- Stereo to Mono: Average channels
- Multi-channel: More complex but doable

## Recommended Approach

### Phase 1: MVP
1. **Audio I/O**: Use `github.com/hajimehoshi/oto` (pure Go, simple)
2. **ALAC Decoder**: Use CGO bindings to existing ALAC library (temporary)
3. **Mixing**: Implement in pure Go
4. **Sample Rate Conversion**: Use Beep's resampler or implement simple linear interpolation
5. **Channel Conversion**: Implement in pure Go

### Phase 2: Optimization
1. **Audio I/O**: Evaluate PortAudio if latency becomes an issue
2. **ALAC Decoder**: Port to pure Go or find better bindings
3. **Sample Rate Conversion**: Implement higher-quality resampling if needed

## Implementation Notes

- Start with pure Go solutions where possible
- Use CGO only when necessary (ALAC decoder initially)
- Plan to replace CGO dependencies with pure Go alternatives
- Test on all target platforms early

## Action Items

- [ ] Test Oto library for audio playback
- [ ] Research pure Go ALAC decoder options
- [ ] Implement basic audio mixing in pure Go
- [ ] Implement sample rate conversion
- [ ] Implement channel conversion
- [ ] Benchmark performance on target platforms

