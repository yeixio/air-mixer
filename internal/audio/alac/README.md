# Pure Go ALAC Decoder

This package contains a pure Go implementation of the Apple Lossless Audio Codec (ALAC) decoder, ported from Apple's open-source implementation.

## Status

🚧 **In Development** - Foundational implementation complete. Needs refinement based on Apple's actual ALAC specification and testing with real AirPlay streams.

**Current Status:**
- ✅ Bitstream reader implemented
- ✅ Rice decoder implemented  
- ✅ Magic cookie parser implemented
- ✅ Basic predictor implemented
- ✅ Frame decoder structure in place
- ⚠️ **Needs enhancement**: The actual ALAC format is more complex than this initial implementation. This provides a foundation that will be refined based on:
  - Apple's official ALAC source code analysis
  - Testing with real AirPlay streams
  - Edge case handling
  - Performance optimization

## Source

Based on Apple's open-source ALAC implementation, released under Apache License 2.0.

## Architecture

The ALAC decoder consists of several components:

1. **Bitstream Reader** - Reads compressed ALAC data bit-by-bit
2. **Entropy Decoder** - Decodes entropy-coded data (Rice coding)
3. **Predictor** - Linear prediction for sample reconstruction
4. **Sample Reconstruction** - Reconstructs PCM samples from decoded data

## Implementation Plan

- [x] Research and locate Apple's ALAC source
- [ ] Port bitstream reader
- [ ] Port entropy decoder (Rice decoder)
- [ ] Port predictor (linear prediction)
- [ ] Port sample reconstruction
- [ ] Integrate with audio engine
- [ ] Test with AirPlay streams
- [ ] Optimize performance

## References

- ALAC format specification
- Apple's open-source ALAC implementation
- ALACMagicCookie format documentation

