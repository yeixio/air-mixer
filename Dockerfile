# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o airmixer \
    ./cmd/airmixer

# Final stage
FROM alpine:latest

# Install runtime dependencies
# Note: For mDNS/Bonjour, you may need avahi or similar
# For audio output, you may need additional packages
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    && update-ca-certificates

# Create app user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Create app directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/airmixer /app/airmixer

# Create data directory
RUN mkdir -p /app/data && \
    chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose ports
# 9001: Web UI
# 49152: AirPlay (default)
EXPOSE 9001 49152

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:9001/api/status || exit 1

# Run the application
ENTRYPOINT ["/app/airmixer"]
CMD ["start"]

