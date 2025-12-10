# ALAC Decoder Research & Implementation Plan

## Current Situation

The existing codebase uses **macOS Core Audio** for ALAC decoding, which:
- ❌ Only works on macOS
- ❌ Requires CGO
- ❌ Uses deprecated/old APIs
- ❌ Not cross-platform
- ✅ Works, but needs replacement

## Available Options

### Option 1: Use Existing Go Library (carterpeel/alac)

**Library**: `github.com/carterpeel/alac`
- **Status**: ⚠️ Wrapper around C library (David Hammerton's ALAC decoder)
- **Last Updated**: January 2022 (2+ years old)
- **License**: Apache 2.0
- **Pros**:
  - Already exists, ready to use
  - Cross-platform (uses C library)
  - Apache 2.0 license (compatible with MIT)
- **Cons**:
  - Requires CGO
  - Not pure Go
  - May not be actively maintained
  - Wrapper around older C implementation

**Verdict**: Good short-term solution, but still requires CGO.

### Option 2: Port Apple's Open-Source ALAC Implementation

**Source**: Apple has open-sourced ALAC under Apache 2.0
- **Language**: C/C++
- **Status**: Official Apple implementation
- **License**: Apache 2.0
- **Pros**:
  - Official implementation
  - Well-tested
  - Apache 2.0 license
  - Could port to pure Go
- **Cons**:
  - Significant development effort (porting C/C++ to Go)
  - Requires understanding ALAC format deeply
  - Time-consuming

**Verdict**: Best long-term solution if we want pure Go, but requires significant work.

### Option 3: Use FFmpeg/libavcodec

**Library**: FFmpeg's libavcodec (via CGO)
- **Status**: Well-maintained, widely used
- **Pros**:
  - Supports many codecs (not just ALAC)
  - Well-tested and optimized
  - Actively maintained
- **Cons**:
  - Large dependency
  - Requires FFmpeg installation
  - Requires CGO
  - Overkill if we only need ALAC

**Verdict**: Too heavy for our use case.

### Option 4: Create Pure Go Implementation from Scratch

**Status**: Would need to implement ALAC decoder from specification
- **Pros**:
  - Pure Go (no CGO)
  - Full control
  - No external dependencies
- **Cons**:
  - Very time-consuming
  - Requires deep ALAC format knowledge
  - Need to handle edge cases
  - Testing and validation required

**Verdict**: Not recommended unless other options fail.

## Recommendation

### Phase 1: Use carterpeel/alac (Short-term)
1. **Why**: Quick to integrate, works cross-platform
2. **Trade-off**: Still requires CGO, but better than current macOS-only solution
3. **Action**: Integrate and test

### Phase 2: Evaluate Pure Go Port (Long-term)
1. **Why**: Eliminate CGO dependency completely
2. **When**: After MVP is working, if CGO becomes a problem
3. **Action**: Port Apple's open-source ALAC to pure Go

## Implementation Plan

### Step 1: Try carterpeel/alac
- Add as dependency
- Create ALAC decoder wrapper
- Test with AirPlay streams
- If it works well, keep it
- If issues arise, move to Step 2

### Step 2: Port Apple's ALAC (if needed)
- Find Apple's open-source ALAC code
- Understand the implementation
- Port to pure Go incrementally
- Test thoroughly
- Replace carterpeel/alac

## Decision Criteria

**Use carterpeel/alac if**:
- ✅ It compiles and works
- ✅ Performance is acceptable
- ✅ No major bugs
- ✅ CGO is acceptable for this component

**Port to pure Go if**:
- ❌ carterpeel/alac has issues
- ❌ CGO becomes a deployment problem
- ❌ We want to eliminate all CGO dependencies
- ✅ We have time/resources for porting

## Next Steps

1. Try integrating `github.com/carterpeel/alac`
2. Test with real AirPlay streams
3. Document any issues
4. Decide on long-term approach based on results

