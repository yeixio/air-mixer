# ALAC Decoder Decision

## Recommendation: **Build Our Own Pure Go Implementation**

After researching the options, I recommend **porting Apple's open-source ALAC implementation to pure Go** rather than using the existing `carterpeel/alac` library long-term.

## Why Build Our Own?

### Current Options Analysis

1. **carterpeel/alac** (Existing Go Library)
   - ✅ Works cross-platform
   - ✅ Quick to integrate
   - ❌ **Requires CGO** (defeats purpose of pure Go audio engine)
   - ❌ **Not maintained** (last updated Jan 2022, 2+ years old)
   - ❌ Wrapper around older C code
   - ❌ Adds CGO dependency to entire project

2. **Port Apple's Open-Source ALAC** (Recommended)
   - ✅ **Pure Go** (no CGO)
   - ✅ **Official implementation** (Apple's own code)
   - ✅ **Apache 2.0 license** (compatible with MIT)
   - ✅ **Well-tested** (used by Apple)
   - ✅ **Maintainable** (we control it)
   - ✅ **Modern** (can use Go best practices)
   - ⚠️ Requires development effort (but worth it)

## Implementation Strategy

### Phase 1: Temporary Solution (Now)
- Use `carterpeel/alac` as a **stopgap** to get AirPlay working
- This allows us to continue development while building the pure Go version
- Mark it clearly as temporary in code comments

### Phase 2: Pure Go Port (Next)
- Port Apple's open-source ALAC implementation to pure Go
- Start with core decoding logic
- Test thoroughly with AirPlay streams
- Replace temporary CGO solution

## Benefits of Pure Go ALAC

1. **No CGO**: Simplifies builds, cross-compilation, and deployment
2. **Maintainability**: We understand and control the code
3. **Modern**: Can use Go idioms and best practices
4. **Performance**: Go compiler optimizations, no C/Go boundary overhead
5. **Portability**: Works everywhere Go works

## Development Plan

### Step 1: Get Apple's ALAC Source
- Apple has open-sourced ALAC under Apache 2.0
- Find the official source code
- Understand the structure

### Step 2: Port Incrementally
- Start with core decoder
- Port bitstream reading
- Port entropy decoding
- Port sample reconstruction
- Test each component

### Step 3: Integration
- Integrate with our audio engine
- Replace carterpeel/alac
- Test with real AirPlay streams

### Step 4: Optimization
- Profile and optimize hot paths
- Add tests for edge cases
- Document the implementation

## Timeline Estimate

- **Temporary solution** (carterpeel/alac): 1-2 days
- **Pure Go port**: 2-4 weeks (depending on complexity)
- **Testing & optimization**: 1 week

## Conclusion

**Yes, we should build our own decoder.** While it requires more upfront work, it gives us:
- A modern, maintainable solution
- No CGO dependencies
- Full control over the implementation
- Better long-term maintainability

The temporary use of `carterpeel/alac` allows us to continue development while we build the proper solution.

