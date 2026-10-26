APP        := airmixer
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X main.Version=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME)
PLATFORMS  := linux/amd64 linux/arm64 linux/arm darwin/amd64 darwin/arm64 windows/amd64

export CGO_ENABLED := 0

.DEFAULT_GOAL := help
.PHONY: help build build-all run dev test test-coverage fuzz fmt vet lint check tidy clean docker-build docker-run

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build build/airmixer
	go build -trimpath -ldflags "$(LDFLAGS)" -o build/$(APP) ./cmd/airmixer

build-all: ## Cross-compile release binaries into dist/
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(APP)-$$os-$$arch$$ext ./cmd/airmixer || exit 1; \
	done

run: build ## Build and run the server
	./build/$(APP) server

dev: ## Run with hot reload (needs github.com/air-verse/air)
	air

test: ## Run tests with the race detector
	CGO_ENABLED=1 go test -race ./...

test-coverage: ## Write coverage.html
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

fuzz: ## Fuzz the ALAC decoder for 30 seconds
	go test ./internal/audio/alac -run '^$$' -fuzz FuzzDecode -fuzztime 30s

fmt: ## Format code
	gofmt -w .

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run

check: vet lint test ## Run everything CI runs

tidy: ## Tidy go.mod
	go mod tidy

clean: ## Remove build output (never touches data/)
	rm -rf build dist tmp coverage.out coverage.html

docker-build: ## Build the Docker image
	docker build --build-arg VERSION=$(VERSION) --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t $(APP):$(VERSION) -t $(APP):latest .

docker-run: docker-build ## Run the Docker image with host networking
	docker run --rm -it --network host -v $(CURDIR)/data:/data $(APP):latest
