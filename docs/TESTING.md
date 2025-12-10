# Testing Guide

This document provides comprehensive guidance for developers on how to test Air-Mixer at various levels, from unit tests to end-to-end integration testing.

## Table of Contents

- [Quick Start](#quick-start)
- [Running Tests](#running-tests)
- [Test Types](#test-types)
- [Testing Scenarios](#testing-scenarios)
- [Manual Testing](#manual-testing)
- [Integration Testing](#integration-testing)
- [Performance Testing](#performance-testing)
- [Troubleshooting Tests](#troubleshooting-tests)

## Quick Start

```bash
# Run all tests
make test

# Run tests with coverage report
make test-coverage

# Run specific package tests
go test ./internal/router/...

# Run tests with verbose output
go test -v ./...

# Run a specific test
go test -v ./internal/router -run TestAddRoute
```

## Running Tests

### Using Make

The Makefile provides convenient test targets:

```bash
# Run all tests with race detection
make test

# Run tests and generate HTML coverage report
make test-coverage

# Clean, format, lint, test, and build (common workflow)
make clean fmt lint test build
```

### Using Go Directly

```bash
# Run all tests
go test ./...

# Run tests with race detector (recommended)
go test -race ./...

# Run tests with coverage
go test -cover ./...

# Run tests with detailed coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

# Run tests in a specific package
go test ./internal/router

# Run a specific test function
go test -run TestAddRoute ./internal/router

# Run tests with verbose output
go test -v ./...

# Run tests in parallel (default)
go test -parallel 4 ./...

# Run tests sequentially
go test -parallel 1 ./...
```

## Test Types

### Unit Tests

Unit tests test individual functions and methods in isolation.

**Example: Testing the Router**

```bash
# Run router unit tests
go test -v ./internal/router

# Run with coverage
go test -cover ./internal/router
```

**Example: Testing Audio Components**

```bash
# Test audio mixer
go test -v ./internal/audio -run TestMixer

# Test PCM decoder
go test -v ./internal/audio -run TestPCMDecoder

# Test ALAC decoder
go test -v ./internal/audio/alac
```

### Integration Tests

Integration tests verify that multiple components work together correctly.

**Example: Testing Router with Database**

```bash
# Run integration tests (if they exist)
go test -v ./internal/router -tags=integration

# Test API endpoints (requires running server)
go test -v ./internal/api -tags=integration
```

### End-to-End Tests

E2E tests verify the entire system works from user input to output.

**Example: Full System Test**

```bash
# Start the server in test mode
./build/airmixer server --test-mode

# In another terminal, run E2E tests
go test -v ./tests/e2e
```

## Testing Scenarios

### 1. Testing Audio Routing

**Setup:**
1. Start the server: `make run`
2. Access web UI: `http://localhost:9001`
3. Create an AirPlay input server
4. Create a route connecting the server to an output

**Test Steps:**
```bash
# 1. Check server is running
curl http://localhost:9001/api/status

# 2. Create an input server
curl -X POST http://localhost:9001/api/servers \
  -H "Content-Type: application/json" \
  -d '{
    "id": "test-airplay-1",
    "name": "Test AirPlay Server",
    "protocol": "airplay",
    "enabled": true
  }'

# 3. Verify server was created
curl http://localhost:9001/api/servers/test-airplay-1

# 4. Create a route
curl -X POST http://localhost:9001/api/routes \
  -H "Content-Type: application/json" \
  -d '{
    "id": "test-route-1",
    "name": "Test Route",
    "source_id": "test-airplay-1",
    "dest_ids": ["output-1"],
    "enabled": true,
    "volume": 1.0
  }'

# 5. Verify route is active
curl http://localhost:9001/api/routes/test-route-1
```

### 2. Testing AirPlay Integration

**Prerequisites:**
- iOS or macOS device with AirPlay capability
- Air-Mixer server running on the same network

**Test Steps:**

1. **Start the server:**
   ```bash
   make run
   # Or with custom port:
   ./build/airmixer server --listener :49152
   ```

2. **Verify AirPlay service is discoverable:**
   ```bash
   # On macOS, check with dns-sd
   dns-sd -B _raop._tcp
   
   # On Linux, use avahi-browse
   avahi-browse -r _raop._tcp
   ```

3. **Connect from iOS/macOS:**
   - Open Control Center (iOS) or System Preferences (macOS)
   - Select AirPlay
   - Look for "airmixer" or your configured name
   - Connect and play audio

4. **Verify audio is received:**
   ```bash
   # Check server logs for connection messages
   # Check API for active routes
   curl http://localhost:9001/api/routes
   ```

### 3. Testing Audio Mixing

**Test Scenario:** Mix two AirPlay sources into one output

```bash
# Create two input servers
curl -X POST http://localhost:9001/api/servers \
  -H "Content-Type: application/json" \
  -d '{"id": "source1", "name": "Source 1", "protocol": "airplay", "enabled": true}'

curl -X POST http://localhost:9001/api/servers \
  -H "Content-Type: application/json" \
  -d '{"id": "source2", "name": "Source 2", "protocol": "airplay", "enabled": true}'

# Create a route mixing both sources
curl -X POST http://localhost:9001/api/routes \
  -H "Content-Type: application/json" \
  -d '{
    "id": "mix-route",
    "name": "Mixed Route",
    "source_id": "source1",
    "dest_ids": ["output-1"],
    "mix_mode": "mix",
    "enabled": true
  }'

# Connect both sources and verify mixed output
```

### 4. Testing Protocol Adapters

**Test AirPlay Server Adapter:**

```bash
# Unit test the adapter
go test -v ./internal/protocols/airplay

# Test with mock audio stream
go test -v ./internal/protocols/airplay -run TestServer
```

**Test Protocol Manager:**

```bash
# Test manager registration
go test -v ./internal/protocols -run TestManager
```

## Manual Testing

### Testing the Web UI

1. **Start the server:**
   ```bash
   make run
   ```

2. **Open browser:**
   - Navigate to `http://localhost:9001`
   - Verify UI loads correctly
   - Check dark theme is applied

3. **Test Server Management:**
   - Click "Add Server"
   - Fill in form (name: "Test Server", protocol: "AirPlay")
   - Submit and verify server appears in table
   - Click "Delete" and verify removal

4. **Test Client Management:**
   - Add a client
   - Verify it appears in the list
   - Test deletion

5. **Test Route Management:**
   - Create a route connecting a server to a client
   - Verify route appears as "Active"
   - Test route deletion

### Testing the REST API

**Using curl:**

```bash
# Get status
curl http://localhost:9001/api/status

# List all servers
curl http://localhost:9001/api/servers

# Create a server
curl -X POST http://localhost:9001/api/servers \
  -H "Content-Type: application/json" \
  -d '{
    "id": "test-1",
    "name": "Test Server",
    "protocol": "airplay",
    "enabled": true
  }'

# Get specific server
curl http://localhost:9001/api/servers/test-1

# Update server
curl -X PUT http://localhost:9001/api/servers/test-1 \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'

# Delete server
curl -X DELETE http://localhost:9001/api/servers/test-1
```

**Using httpie (if installed):**

```bash
# Get status
http GET localhost:9001/api/status

# Create server
http POST localhost:9001/api/servers \
  id=test-1 \
  name="Test Server" \
  protocol=airplay \
  enabled:=true
```

## Integration Testing

### Full System Test

**Test Script:**

```bash
#!/bin/bash
# tests/e2e/full-system-test.sh

set -e

echo "Starting Air-Mixer server..."
./build/airmixer server &
SERVER_PID=$!
sleep 2

echo "Testing API endpoints..."

# Test status
curl -f http://localhost:9001/api/status || exit 1

# Create server
curl -f -X POST http://localhost:9001/api/servers \
  -H "Content-Type: application/json" \
  -d '{"id": "e2e-test", "name": "E2E Test", "protocol": "airplay", "enabled": true}' || exit 1

# Verify server exists
curl -f http://localhost:9001/api/servers/e2e-test || exit 1

# Cleanup
kill $SERVER_PID
echo "E2E test passed!"
```

### Database Integration Test

```bash
# Test database operations
go test -v ./internal/db -run TestDatabase

# Test with in-memory database
go test -v ./internal/db -run TestInMemory
```

## Performance Testing

### Load Testing

**Test concurrent connections:**

```bash
# Use Apache Bench
ab -n 1000 -c 10 http://localhost:9001/api/status

# Use wrk
wrk -t4 -c100 -d30s http://localhost:9001/api/status
```

### Audio Performance

**Test audio processing latency:**

```bash
# Run with profiling
go test -cpuprofile=cpu.prof -memprofile=mem.prof ./internal/audio

# Analyze profile
go tool pprof cpu.prof
```

## Troubleshooting Tests

### Common Issues

**1. Tests fail with "port already in use"**

```bash
# Find and kill process using port
lsof -ti:9001 | xargs kill -9

# Or use a different port
./build/airmixer server --listener :9002
```

**2. Database locked errors**

```bash
# Clean test database
rm -f test.db

# Use in-memory database for tests
go test -tags=inmemory ./...
```

**3. Race condition detection**

```bash
# Always run with race detector
go test -race ./...

# If race detected, check the output for details
```

**4. Missing dependencies**

```bash
# Download all dependencies
make deps

# Or manually
go mod download
go mod tidy
```

### Debug Mode

**Run tests with debug output:**

```bash
# Verbose test output
go test -v ./...

# Run specific test with debug
go test -v -run TestSpecific ./internal/router

# Use delve debugger
dlv test ./internal/router
```

### Test Coverage Goals

- **Unit tests**: Aim for >80% coverage
- **Integration tests**: Cover critical paths
- **E2E tests**: Cover main user workflows

**Check coverage:**

```bash
# Generate coverage report
make test-coverage

# Open in browser
open coverage.html  # macOS
xdg-open coverage.html  # Linux
```

## Continuous Integration

### GitHub Actions Example

```yaml
name: Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - uses: actions/setup-go@v2
        with:
          go-version: '1.21'
      - run: make deps
      - run: make test
      - run: make lint
```

## Best Practices

1. **Write tests first** (TDD) for new features
2. **Use table-driven tests** for multiple scenarios
3. **Mock external dependencies** (network, file system)
4. **Test error cases** as well as success cases
5. **Keep tests fast** - unit tests should run in <1s
6. **Use descriptive test names** - `TestAddRoute_WithInvalidSource_ReturnsError`
7. **Clean up after tests** - remove test databases, close connections
8. **Run tests before committing** - `make test` should pass

## Additional Resources

- [Go Testing Documentation](https://golang.org/pkg/testing/)
- [Table-Driven Tests](https://github.com/golang/go/wiki/TableDrivenTests)
- [Go Test Flags](https://golang.org/cmd/go/#hdr-Testing_flags)

