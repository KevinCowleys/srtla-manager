#!/bin/bash
# SRTLA Manager Installation Script
# This script downloads and installs srtla-manager from GitHub and sets up systemd service

set -e

# Configuration
REPO="KevinCowleys/srtla-manager"
INSTALL_DIR="/opt/srtla-manager"
BINARY_NAME="srtla-manager"
SERVICE_NAME="srtla-manager"
SERVICE_USER="srtla"
SERVICE_GROUP="srtla"
CONFIG_DIR="/etc/srtla-manager"
DATA_DIR="/var/lib/srtla-manager"
LOG_DIR="/var/log/srtla-manager"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

detect_os() {
    OS=$(uname -s)
    ARCH=$(uname -m)
    
    if [[ "$OS" == "Linux" ]]; then
        OS_TYPE="linux"
        if [[ "$ARCH" == "x86_64" ]]; then
            ARCH_TYPE="amd64"
        elif [[ "$ARCH" == "aarch64" ]]; then
            ARCH_TYPE="arm64"
        elif [[ "$ARCH" == "armv7l" ]]; then
            ARCH_TYPE="armv7"
        else
            ARCH_TYPE="$ARCH"
        fi
    elif [[ "$OS" == "Darwin" ]]; then
        OS_TYPE="darwin"
        if [[ "$ARCH" == "arm64" ]]; then
            ARCH_TYPE="arm64"
        else
            ARCH_TYPE="amd64"
        fi
    else
        log_error "Unsupported OS: $OS"
        exit 1
    fi
    
    log_info "Detected OS: $OS_TYPE, Architecture: $ARCH_TYPE"
}

get_latest_version() {
    log_info "Fetching latest release information from GitHub..."
    
    RELEASE_INFO=$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest") || {
        log_error "Failed to fetch latest release from GitHub"
        exit 1
    }
    
    LATEST_VERSION=$(echo "$RELEASE_INFO" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4)
    
    if [ -z "$LATEST_VERSION" ]; then
        log_error "Failed to fetch latest release: API may have rate limited the request"
        exit 1
    fi
    
    log_info "Latest version: $LATEST_VERSION"
}

find_download_url() {
    PATTERN="${OS_TYPE}-${ARCH_TYPE}"
    
    # Get all asset URLs
    DOWNLOAD_URL=$(echo "$RELEASE_INFO" | grep -o '"browser_download_url": "[^"]*' | cut -d'"' -f4 | grep "$PATTERN" | head -1)
    
    if [ -z "$DOWNLOAD_URL" ]; then
        log_error "No compatible binary found for $PATTERN"
        log_warn "Available assets:"
        echo "$RELEASE_INFO" | grep -o '"name": "[^"]*' | cut -d'"' -f4 | grep -v ".sha256"
        exit 1
    fi
    
    log_info "Download URL: $DOWNLOAD_URL"
}

check_requirements() {
    log_info "Checking system requirements..."
    
    # Check if running as root
    if [[ $EUID -ne 0 ]]; then
        log_error "This script must be run as root"
        exit 1
    fi
    
    # Check for curl
    if ! command -v curl &> /dev/null; then
        log_error "curl is required but not installed"
        exit 1
    fi
    
    # Check for systemctl
    if ! command -v systemctl &> /dev/null; then
        log_error "systemctl is required but not installed"
        exit 1
    fi
}

install_dependencies() {
    log_info "Installing dependencies..."
    
    # Detect package manager
    if command -v apt-get &> /dev/null; then
        PKG_MANAGER="apt"
        log_info "Detected package manager: apt"
        apt-get update || log_warn "Failed to update package lists"
        apt-get install -y ffmpeg v4l-utils network-manager android-tools-adb || {
            log_warn "Some packages may have failed to install, continuing..."
        }
    elif command -v dnf &> /dev/null; then
        PKG_MANAGER="dnf"
        log_info "Detected package manager: dnf"
        dnf install -y ffmpeg v4l-utils NetworkManager android-tools || {
            log_warn "Some packages may have failed to install, continuing..."
        }
    elif command -v yum &> /dev/null; then
        PKG_MANAGER="yum"
        log_info "Detected package manager: yum"
        yum install -y ffmpeg v4l-utils NetworkManager android-tools || {
            log_warn "Some packages may have failed to install, continuing..."
        }
    elif command -v pacman &> /dev/null; then
        PKG_MANAGER="pacman"
        log_info "Detected package manager: pacman"
        pacman -Sy --noconfirm ffmpeg v4l-utils networkmanager android-tools || {
            log_warn "Some packages may have failed to install, continuing..."
        }
    elif command -v zypper &> /dev/null; then
        PKG_MANAGER="zypper"
        log_info "Detected package manager: zypper"
        zypper install -y ffmpeg v4l-utils NetworkManager android-tools || {
            log_warn "Some packages may have failed to install, continuing..."
        }
    else
        log_warn "Could not detect package manager. Please install dependencies manually:"
        log_warn "  - ffmpeg"
        log_warn "  - v4l-utils (v4l2-ctl)"
        log_warn "  - NetworkManager (nmcli)"
        log_warn "  - android-tools (adb)"
        return
    fi
    
    # Verify critical dependencies
    log_info "Verifying installed dependencies..."
    if ! command -v ffmpeg &> /dev/null; then
        log_warn "ffmpeg not found in PATH after installation"
    else
        log_info "✓ ffmpeg: $(ffmpeg -version 2>&1 | head -1 | cut -d' ' -f3)"
    fi
    
    if ! command -v v4l2-ctl &> /dev/null; then
        log_warn "v4l2-ctl not found (USB camera support may be limited)"
    else
        log_info "✓ v4l2-ctl: installed"
    fi
    
    if ! command -v nmcli &> /dev/null; then
        log_warn "nmcli not found (WiFi management will not work)"
    else
        log_info "✓ nmcli: $(nmcli --version 2>&1 | head -1)"
    fi
    
    if ! command -v adb &> /dev/null; then
        log_warn "adb not found (modem/Android device support will not work)"
    else
        log_info "✓ adb: $(adb version 2>&1 | head -1)"
    fi
}

create_user_and_dirs() {
    log_info "Setting up user and directories..."
    
    # Create service user if it doesn't exist
    if ! id "$SERVICE_USER" &>/dev/null 2>&1; then
        log_info "Creating system user $SERVICE_USER..."
        useradd -r -s /bin/false -d "$INSTALL_DIR" "$SERVICE_USER" || true
    fi
    
    # Create directories
    mkdir -p "$INSTALL_DIR"
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$DATA_DIR"
    mkdir -p "$LOG_DIR"
    
    # Set permissions
    chown -R "$SERVICE_USER:$SERVICE_GROUP" "$INSTALL_DIR"
    chown -R "$SERVICE_USER:$SERVICE_GROUP" "$DATA_DIR"
    chown -R "$SERVICE_USER:$SERVICE_GROUP" "$LOG_DIR"
    
    chmod 755 "$CONFIG_DIR"
    chmod 755 "$DATA_DIR"
    chmod 755 "$LOG_DIR"
}

download_binary() {
    log_info "Downloading $BINARY_NAME version $LATEST_VERSION..."
    
    TEMP_DIR=$(mktemp -d) || {
        log_error "Failed to create temporary directory"
        exit 1
    }
    trap "rm -rf '$TEMP_DIR'" EXIT
    
    TEMP_BINARY="$TEMP_DIR/$BINARY_NAME"
    TEMP_CHECKSUM="$TEMP_DIR/$BINARY_NAME.sha256"
    
    curl -sL -o "$TEMP_BINARY" "$DOWNLOAD_URL" || {
        log_error "Download of binary failed"
        exit 1
    }
    
    curl -sL -o "$TEMP_CHECKSUM" "${DOWNLOAD_URL}.sha256" || {
        log_warn "Could not download checksum file, skipping verification"
        TEMP_CHECKSUM=""
    }
    
    if [ ! -f "$TEMP_BINARY" ]; then
        log_error "Download failed: binary file not found"
        exit 1
    fi
    
    # Verify checksum if available
    if [ -n "$TEMP_CHECKSUM" ] && [ -f "$TEMP_CHECKSUM" ]; then
        log_info "Verifying checksum..."
        # Extract hash from file (format: "hash filename" or "hash  filename")
        EXPECTED_HASH=$(cut -d' ' -f1 "$TEMP_CHECKSUM")
        ACTUAL_HASH=$(sha256sum "$TEMP_BINARY" | cut -d' ' -f1)
        
        if [ "$EXPECTED_HASH" != "$ACTUAL_HASH" ]; then
            log_error "Checksum verification failed - binary may be corrupted"
            log_error "Expected: $EXPECTED_HASH"
            log_error "Got:      $ACTUAL_HASH"
            exit 1
        fi
        log_info "Checksum verified successfully"
    fi
    
    chmod +x "$TEMP_BINARY"
    
    # Verify the binary works
    if ! "$TEMP_BINARY" -version &>/dev/null 2>&1; then
        # Fallback - not all binaries support -version
        log_warn "Could not verify binary version flag"
    fi
    
    # Stop service if it's running before replacing binary
    if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
        log_info "Stopping existing service before replacing binary..."
        systemctl stop "$SERVICE_NAME" || log_warn "Failed to stop service"
        sleep 1
    fi
    
    # Move to install directory
    cp "$TEMP_BINARY" "$INSTALL_DIR/$BINARY_NAME" || {
        log_error "Failed to copy binary to install directory"
        exit 1
    }
    chown "$SERVICE_USER:$SERVICE_GROUP" "$INSTALL_DIR/$BINARY_NAME"
    chmod 755 "$INSTALL_DIR/$BINARY_NAME"
    
    log_info "Binary installed to $INSTALL_DIR/$BINARY_NAME"
}

create_default_config() {
    CONFIG_FILE="$CONFIG_DIR/config.yaml"
    
    if [ -f "$CONFIG_FILE" ]; then
        log_warn "Config file already exists at $CONFIG_FILE, skipping creation"
        return
    fi
    
    log_info "Creating default configuration..."
    
    cat > "$CONFIG_FILE" << 'EOF'
web:
  port: 8080

rtmp:
  listen_port: 1935
  stream_key: live

srtla:
  enabled: true
  remote_host: localhost
  remote_port: 5000
  bind_ips: []

ffmpeg:
  path: ffmpeg
  input_options: ""
  output_options: ""

process:
  logs_retention: 1000
EOF
    
    chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_FILE"
    chmod 640 "$CONFIG_FILE"
    
    log_info "Default configuration created at $CONFIG_FILE"
}

create_systemd_service() {
    log_info "Creating systemd service..."
    
    SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
    
    cat > "$SERVICE_FILE" << EOF
[Unit]
Description=SRTLA Manager - Stream Management System
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_GROUP
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$BINARY_NAME -config $CONFIG_DIR/config.yaml
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=srtla-manager

# Process management
KillMode=process
TimeoutStopSec=30

# Security
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$DATA_DIR $LOG_DIR

[Install]
WantedBy=multi-user.target
EOF
    
    chmod 644 "$SERVICE_FILE"
    
    log_info "Systemd service file created at $SERVICE_FILE"
}

enable_service() {
    log_info "Enabling and starting systemd service..."
    
    systemctl daemon-reload
    systemctl enable "$SERVICE_NAME"
    systemctl start "$SERVICE_NAME"
    
    # Wait a moment for service to start
    sleep 2
    
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log_info "Service started successfully"
    else
        log_error "Service failed to start"
        systemctl status "$SERVICE_NAME" || true
        exit 1
    fi
}

print_summary() {
    cat << EOF

${GREEN}Installation Complete!${NC}

Service Name: $SERVICE_NAME
Binary Location: $INSTALL_DIR/$BINARY_NAME
Config Directory: $CONFIG_DIR
Data Directory: $DATA_DIR
Log Directory: $LOG_DIR

Useful Commands:
  Start:    systemctl start $SERVICE_NAME
  Stop:     systemctl stop $SERVICE_NAME
  Status:   systemctl status $SERVICE_NAME
  Logs:     journalctl -u $SERVICE_NAME -f
  Restart:  systemctl restart $SERVICE_NAME

Access the web interface at: http://localhost:8080

EOF
}

# Main execution
main() {
    log_info "SRTLA Manager Installation Script"
    log_info "Repository: $REPO"
    
    check_requirements
    install_dependencies
    detect_os
    get_latest_version
    find_download_url
    create_user_and_dirs
    download_binary
    create_default_config
    create_systemd_service
    enable_service
    print_summary
}

main
