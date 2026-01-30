# SRTLA Manager

A Go-based manager for SRTLA (Secure Reliable Transport with Link Aggregation) streaming with support for DJI cameras, WiFi hotspot management, and USB network devices.

## Project Structure

This project follows the [Standard Go Project Layout](https://github.com/golang-standards/project-layout):

```
.
├── cmd/
│   └── srtla-manager/     # Main application entry point
├── internal/              # Private application code
│   ├── api/              # HTTP handlers and WebSocket
│   ├── config/           # Configuration management
│   ├── dji/              # DJI camera protocol and control
│   ├── modem/            # Modem management (ADB)
│   ├── process/          # Process management (FFmpeg, SRTLA)
│   ├── stats/            # Statistics collection
│   ├── system/           # System utilities
│   ├── usbnet/           # USB network device management
│   └── wifi/             # WiFi hotspot management
├── pkg/
│   └── web/              # Embedded web assets
├── docs/                 # Documentation
├── scripts/              # Build and setup scripts
├── bin/                  # Compiled binaries (generated)
├── config.yaml           # Default configuration
└── go.mod                # Go module definition
```

## Building

Build the project from the repository root:

```bash
make clean && make build && make run
```

## Running

```bash
./bin/srtla-manager -config config.yaml
```

## Package Organization

### `internal/`
Contains private application code that cannot be imported by other projects. This enforces encapsulation and prevents external dependencies on internal APIs.

### `pkg/`
Contains reusable packages that could potentially be imported by other projects. Currently contains the embedded web UI assets.

### `cmd/`
Contains the main application entry point(s). Each subdirectory represents an executable.

### `docs/`
All markdown documentation for features, APIs, and setup guides.

## Features

- SRTLA link aggregation for reliable video streaming
- DJI camera integration via BLE
- WiFi hotspot management
- USB network device detection and configuration
- FFmpeg streaming pipeline
- Real-time statistics and logging
- Web-based UI

For detailed feature documentation, see the [docs/](docs/) directory.
