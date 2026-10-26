# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME}" \
    -o /out/airmixer ./cmd/airmixer

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 airmixer \
    && mkdir -p /data && chown airmixer:airmixer /data
COPY --from=build /out/airmixer /usr/local/bin/airmixer
USER airmixer
ENV AIRMIXER_SERVER_DATA_DIR=/data
VOLUME /data
# 9001: web UI and API. 49152: default AirPlay receiver (RTSP); audio uses
# dynamic UDP ports, so run with host networking.
EXPOSE 9001 49152
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:9001/health || exit 1
ENTRYPOINT ["airmixer"]
CMD ["server"]
