.PHONY: build clean test run dev docker-build docker-run help build-all test-coverage fmt lint deps install uninstall docker-compose-up docker-compose-down

# Application name
APP_NAME := airmixer
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Build flags
LDFLAGS := -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOMOD := $(GOCMD) mod
GOFMT := gofmt
GOLINT := golangci-lint

# Directories
CMD_DIR := cmd/airmixer
BUILD_DIR := build
DIST_DIR := dist

.DEFAULT_GOAL := help

help: ## Show this help message with examples
	@echo '╔══════════════════════════════════════════════════════════════════════════════╗'
	@echo '║                          Air-Mixer Makefile Help                            ║'
	@echo '╚══════════════════════════════════════════════════════════════════════════════╝'
	@echo ''
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ''
	@echo '╔══════════════════════════════════════════════════════════════════════════════╗'
	@echo '║                          Common Workflows                                    ║'
	@echo '╚══════════════════════════════════════════════════════════════════════════════╝'
	@echo ''
	@echo '  🚀 Quick Start:'
	@echo '     make deps          # Download dependencies'
	@echo '     make build         # Build the application'
	@echo '     make run           # Build and run the server'
	@echo '     make dev           # Run with Air hot reload (auto-restart on changes)'
	@echo ''
	@echo '  🧪 Development Workflow:'
	@echo '     make dev           # Run with hot reload (recommended for development)'
	@echo '     make clean         # Clean build artifacts'
	@echo '     make fmt           # Format code'
	@echo '     make lint          # Run linter'
	@echo '     make test          # Run tests'
	@echo '     make build         # Build after changes'
	@echo ''
	@echo '  📊 Quality Assurance:'
	@echo '     make fmt lint test build    # Full QA pipeline'
	@echo '     make test-coverage          # Tests with coverage report'
	@echo ''
	@echo '  🐳 Docker Workflow:'
	@echo '     make docker-build           # Build Docker image'
	@echo '     make docker-run             # Run in Docker container'
	@echo '     make docker-compose-up      # Start with docker-compose'
	@echo '     make docker-compose-down    # Stop docker-compose'
	@echo ''
	@echo '  📦 Release Build:'
	@echo '     make clean                  # Clean old builds'
	@echo '     make build-all              # Build for all platforms'
	@echo '     # Binaries will be in dist/ directory'
	@echo ''
	@echo '  🔧 Installation:'
	@echo '     make install                # Install to /usr/local/bin'
	@echo '     make uninstall              # Remove from /usr/local/bin'
	@echo ''
	@echo '╔══════════════════════════════════════════════════════════════════════════════╗'
	@echo '║                          Example Combinations                                ║'
	@echo '╚══════════════════════════════════════════════════════════════════════════════╝'
	@echo ''
	@echo '  Before committing code:'
	@echo '     make clean fmt lint test build'
	@echo ''
	@echo '  Full test suite with coverage:'
	@echo '     make clean test-coverage'
	@echo '     # Open coverage.html in browser to view results'
	@echo ''
	@echo '  Quick iteration during development:'
	@echo '     make dev           # Auto-restarts on code changes'
	@echo '     # OR'
	@echo '     make build && ./build/airmixer server'
	@echo ''
	@echo '  Test and run in one command:'
	@echo '     make test && make run'
	@echo ''
	@echo '  Clean rebuild everything:'
	@echo '     make clean deps build'
	@echo ''
	@echo '  Docker development workflow:'
	@echo '     make docker-build && make docker-run'
	@echo ''
	@echo '  Cross-platform release:'
	@echo '     make clean build-all'
	@echo '     # Creates binaries for Linux, macOS, and Windows'
	@echo ''
	@echo '╔══════════════════════════════════════════════════════════════════════════════╗'
	@echo '║                          Tips & Notes                                         ║'
	@echo '╚══════════════════════════════════════════════════════════════════════════════╝'
	@echo ''
	@echo '  • Most targets can be combined: make clean fmt test build'
	@echo '  • Use "make run" for quick testing (builds and runs in one step)'
	@echo '  • "make test-coverage" generates coverage.html for detailed analysis'
	@echo '  • "make build-all" requires Go cross-compilation support'
	@echo '  • Docker targets require Docker to be installed and running'
	@echo '  • See docs/TESTING.md for detailed testing instructions'
	@echo ''

build: ## Build the application (output: build/airmixer)
	@echo "Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME) ./$(CMD_DIR)
	@echo "Build complete: $(BUILD_DIR)/$(APP_NAME)"

build-all: ## Build for all platforms (Linux, macOS, Windows - output: dist/)
	@echo "Building for all platforms..."
	@mkdir -p $(DIST_DIR)
	@echo "  Building Linux AMD64..."
	@GOOS=linux GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP_NAME)-linux-amd64 ./$(CMD_DIR)
	@echo "  Building Linux ARM64..."
	@GOOS=linux GOARCH=arm64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP_NAME)-linux-arm64 ./$(CMD_DIR)
	@echo "  Building macOS AMD64..."
	@GOOS=darwin GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP_NAME)-darwin-amd64 ./$(CMD_DIR)
	@echo "  Building macOS ARM64 (Apple Silicon)..."
	@GOOS=darwin GOARCH=arm64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP_NAME)-darwin-arm64 ./$(CMD_DIR)
	@echo "  Building Windows AMD64..."
	@GOOS=windows GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP_NAME)-windows-amd64.exe ./$(CMD_DIR)
	@echo "Build complete: $(DIST_DIR)/"
	@ls -lh $(DIST_DIR)/

clean: ## Clean build artifacts and database
	@echo "Cleaning..."
	rm -f $(APP_NAME)
	rm -rf $(BUILD_DIR)
	rm -rf $(DIST_DIR)
	rm -rf webui/dist
	rm -f ./data/airmixer.db
	rm -f ./data/airmixer.db-shm
	rm -f ./data/airmixer.db-wal
	@echo "Clean complete"

test: ## Run all tests with race detection
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	@echo "Test complete"

test-coverage: test ## Run tests and generate HTML coverage report (output: coverage.html)
	@echo "Generating coverage report..."
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
	@echo "Open coverage.html in your browser to view detailed coverage"

fmt: ## Format code
	@echo "Formatting code..."
	$(GOFMT) -w .
	@echo "Format complete"

lint: ## Run linter
	@echo "Running linter..."
	@if command -v $(GOLINT) > /dev/null; then \
		$(GOLINT) run ./...; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "Dependencies updated"

run: build ## Build and run the application (starts server on port 9001)
	@echo "Running $(APP_NAME)..."
	@echo "Web UI: http://localhost:9001"
	@echo "API: http://localhost:9001/api"
	@echo "Press Ctrl+C to stop"
	@./$(BUILD_DIR)/$(APP_NAME) server

dev: ## Run with Air hot reload (auto-restarts on code changes)
	@echo "Starting $(APP_NAME) with Air hot reload..."
	@echo "Web UI: http://localhost:9001"
	@echo "API: http://localhost:9001/api"
	@echo "Press Ctrl+C to stop"
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "Air is not installed. Installing..."; \
		go install github.com/air-verse/air@latest; \
		air; \
	fi

docker-build: ## Build Docker image (tags: airmixer:VERSION and airmixer:latest)
	@echo "Building Docker image..."
	docker build -t $(APP_NAME):$(VERSION) .
	docker tag $(APP_NAME):$(VERSION) $(APP_NAME):latest
	@echo "Docker build complete"
	@echo "Images created: $(APP_NAME):$(VERSION) and $(APP_NAME):latest"

docker-run: docker-build ## Build and run Docker container (uses host network for mDNS)
	@echo "Running Docker container..."
	@echo "Web UI: http://localhost:9001"
	@echo "Press Ctrl+C to stop"
	docker run --rm -it \
		--network host \
		-v $(PWD)/data:/app/data \
		-p 9001:9001 \
		$(APP_NAME):latest

docker-compose-up: ## Start services with docker-compose (runs in background)
	@echo "Starting services with docker-compose..."
	docker-compose up -d
	@echo "Services started. Use 'make docker-compose-down' to stop."

docker-compose-down: ## Stop and remove docker-compose services
	@echo "Stopping docker-compose services..."
	docker-compose down
	@echo "Services stopped."

install: build ## Install the application to /usr/local/bin (requires sudo)
	@echo "Installing $(APP_NAME) to /usr/local/bin..."
	@if [ -w /usr/local/bin ]; then \
		cp $(BUILD_DIR)/$(APP_NAME) /usr/local/bin/; \
	else \
		sudo cp $(BUILD_DIR)/$(APP_NAME) /usr/local/bin/; \
	fi
	@echo "Installation complete. Run 'airmixer --help' to verify."

uninstall: ## Uninstall the application from /usr/local/bin (requires sudo)
	@echo "Uninstalling $(APP_NAME) from /usr/local/bin..."
	@if [ -w /usr/local/bin ]; then \
		rm -f /usr/local/bin/$(APP_NAME); \
	else \
		sudo rm -f /usr/local/bin/$(APP_NAME); \
	fi
	@echo "Uninstallation complete."

