.PHONY: build clean run test fmt vet install build-web

# Binary name
BINARY_NAME=srtla-manager
BIN_DIR=bin

# Version information
VERSION ?= v0.0.0-dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BRANCH ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%d_%H:%M:%S' 2>/dev/null || echo "unknown")
BUILDER ?= $(shell whoami 2>/dev/null)@$(shell hostname 2>/dev/null || echo "unknown")

# Build flags for version injection
LDFLAGS=-ldflags "\
	-X 'srtla-manager/internal/version.Version=$(VERSION)' \
	-X 'srtla-manager/internal/version.Commit=$(COMMIT)' \
	-X 'srtla-manager/internal/version.Branch=$(BRANCH)' \
	-X 'srtla-manager/internal/version.BuildTime=$(BUILD_TIME)' \
	-X 'srtla-manager/internal/version.Builder=$(BUILDER)'"

# Build flags for installer version injection
INSTALLER_LDFLAGS=-ldflags "\
	-X 'main.Version=$(VERSION)' \
	-X 'main.Commit=$(COMMIT)' \
	-X 'main.Branch=$(BRANCH)' \
	-X 'main.BuildTime=$(BUILD_TIME)' \
	-X 'main.Builder=$(BUILDER)'"

# Build web assets (optional - can use modular files directly)
build-web:
	@echo "Building web assets..."
	@cd pkg/web && ./build.sh

# Build the application
build:
	@echo "Building $(BINARY_NAME) (version: $(VERSION))..."
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/srtla-manager
	@echo "Building srtla-installer (version: $(VERSION))..."
	go build $(INSTALLER_LDFLAGS) -o $(BIN_DIR)/srtla-installer ./cmd/srtla-installer

# Build with bundled/minified web assets
build-prod: build-web build

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BIN_DIR)
	@go clean

# Run the application
run: build
	@echo "Running $(BINARY_NAME)..."
	@./$(BIN_DIR)/$(BINARY_NAME) -config /home/srtla/srtla-manager-config/config.yaml

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Run go vet
vet:
	@echo "Running go vet..."
	go vet ./...

# Install to system
install: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	@sudo cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/

# Show version information
version:
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Branch: $(BRANCH)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Builder: $(BUILDER)"

# Build with specific version (usage: make build-version VERSION=v1.0.0)
build-version: clean
	@echo "Building version $(VERSION)..."
	make build VERSION=$(VERSION)
	@sudo cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/

# Development checks
check: fmt vet test

# Build for multiple platforms
build-all:
	@echo "Building for multiple platforms..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/srtla-manager
	GOOS=linux GOARCH=arm64 go build -o $(BIN_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/srtla-manager
	GOOS=darwin GOARCH=amd64 go build -o $(BIN_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/srtla-manager
	GOOS=darwin GOARCH=arm64 go build -o $(BIN_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/srtla-manager
	GOOS=windows GOARCH=amd64 go build -o $(BIN_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/srtla-manager
