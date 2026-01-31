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
CONFIG_DIR="/home/srtla/srtla-manager-config"
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
    
    # Add user to necessary groups for hardware access
    log_info "Setting up user groups for hardware access..."
    usermod -a -G video "$SERVICE_USER" || log_warn "Failed to add $SERVICE_USER to video group"
    usermod -a -G dialout "$SERVICE_USER" || log_warn "Failed to add $SERVICE_USER to dialout group"
    
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
    chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_DIR" || log_error "Failed to chown $CONFIG_DIR"
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
    log_info "chmod +x completed"


    # Skip version check to avoid hanging if binary does not support it
    log_info "Skipping binary version check (may hang if not supported)"

    # Stop service if it's running before replacing binary
    log_info "Checking if $SERVICE_NAME is running before replacing binary..."
    if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
        log_info "Stopping existing service before replacing binary..."
        systemctl stop "$SERVICE_NAME" || log_warn "Failed to stop service"
    fi

    # Kill any remaining processes using the binary
    log_info "Checking for running $BINARY_NAME processes..."
    if pgrep -x "$BINARY_NAME" > /dev/null; then
        log_info "Waiting for processes to terminate..."
        pkill -TERM "$BINARY_NAME"
        sleep 3

        # Force kill if still running
        if pgrep -x "$BINARY_NAME" > /dev/null; then
            log_warn "Force killing remaining processes..."
            pkill -KILL "$BINARY_NAME"
            sleep 1
        fi
    fi

    log_info "Copying new binary to $INSTALL_DIR/$BINARY_NAME..."
    cp "$TEMP_BINARY" "$INSTALL_DIR/$BINARY_NAME" || {
        log_error "Failed to copy binary to install directory"
        log_error "The binary may still be in use. Try: systemctl stop $SERVICE_NAME"
        exit 1
    }
    log_info "Setting ownership and permissions on $INSTALL_DIR/$BINARY_NAME..."
    chown "$SERVICE_USER:$SERVICE_GROUP" "$INSTALL_DIR/$BINARY_NAME"
    chmod 755 "$INSTALL_DIR/$BINARY_NAME"

    log_info "Binary installed to $INSTALL_DIR/$BINARY_NAME"
}

create_default_config() {
    CONFIG_FILE="$CONFIG_DIR/config.yaml"
    
    if [ -f "$CONFIG_FILE" ]; then
        log_warn "Config file already exists at $CONFIG_FILE, ensuring correct ownership and permissions"
        chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_FILE" || log_error "Failed to chown $CONFIG_FILE"
        chmod 666 "$CONFIG_FILE" || log_error "Failed to chmod $CONFIG_FILE"
        return
    fi
    
    log_info "Creating default configuration..."
    
    cat > "$CONFIG_FILE" << 'EOF'
rtmp:
        listen_port: 1935
        stream_key: live
srt:
        local_port: 6000
srtla:
        enabled: true
        binary_path: srtla_send
        remote_host: localhost
        remote_port: 5000
        bind_ips: []
        bind_ips_file: ""
        classic: false
        no_quality: false
        exploration: false
web:
        port: 8080
logging:
        debug: false
        file_path: logs/srtla-manager.log
        max_size_mb: 10
        max_backups: 3
cameras: {}
usb_cameras: {}
EOF
    
    chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_FILE" || log_error "Failed to chown $CONFIG_FILE"
    chmod 666 "$CONFIG_FILE" || log_error "Failed to chmod $CONFIG_FILE"
    
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
# Kill any process using port 8080 or 1935 before starting
ExecStartPre=/usr/bin/bash -c 'fuser -k 8080/tcp 2>/dev/null || true'
ExecStartPre=/usr/bin/bash -c 'fuser -k 1935/tcp 2>/dev/null || true'
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
ReadWritePaths=$DATA_DIR $LOG_DIR $CONFIG_DIR

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

install_srtla_installer() {
        # Always remove the socket before starting installer
        SOCKET_PATH="/run/srtla-installer.sock"
        if [ -S "$SOCKET_PATH" ]; then
            log_info "Cleaning up old srtla-installer socket at $SOCKET_PATH before start..."
            # Check if a process is listening on the socket
            if fuser "$SOCKET_PATH" 2>/dev/null | grep -q '[0-9]'; then
                log_warn "A process is using $SOCKET_PATH. Attempting to stop srtla-installer service and kill any stale processes."
                systemctl stop srtla-installer 2>/dev/null || true
                sleep 2
                # Try to kill any process still using the socket
                fuser -k "$SOCKET_PATH" 2>/dev/null || true
                sleep 1
            fi
            rm -f "$SOCKET_PATH" || {
                log_error "Failed to remove socket $SOCKET_PATH. Please remove it manually with: sudo rm $SOCKET_PATH"
                return 1
            }
        fi

        # Ensure we are running as root for correct permissions
        if [[ $EUID -ne 0 ]]; then
            log_error "srtla-installer must be started as root to own the socket and set permissions. Aborting."
            return 1
        fi
    log_info "\nSRTLA Installer Daemon Installation (srtla-installer)"
    INSTALLER_REPO="KevinCowleys/srtla-manager"
    INSTALLER_NAME="srtla-installer"
    INSTALLER_SERVICE="srtla-installer"
    INSTALLER_DIR="/usr/local/bin"
    # Use same arch/OS detection as above
    PATTERN="installer-${OS_TYPE}-${ARCH_TYPE}"
    log_info "Fetching latest release information for srtla-installer..."
    INSTALLER_RELEASE_INFO=$(curl -sL "https://api.github.com/repos/${INSTALLER_REPO}/releases/latest") || {
        log_error "Failed to fetch latest release from GitHub for srtla-installer"
        return
    }
    INSTALLER_URL=$(echo "$INSTALLER_RELEASE_INFO" | grep -o '"browser_download_url": "[^"]*' | cut -d'"' -f4 | grep "$PATTERN" | head -1)
    if [ -z "$INSTALLER_URL" ]; then
        log_error "No compatible srtla-installer binary found for $PATTERN"
        log_warn "Available assets:"
        echo "$INSTALLER_RELEASE_INFO" | grep -o '"name": "[^"]*' | cut -d'"' -f4 | grep installer
        return
    fi
    log_info "Download URL for srtla-installer: $INSTALLER_URL"
    TMP_INSTALLER="/tmp/$INSTALLER_NAME"
    curl -L -o "$TMP_INSTALLER" "$INSTALLER_URL" || {
        log_error "Failed to download srtla-installer binary"
        return
    }
    chmod +x "$TMP_INSTALLER"
    mv "$TMP_INSTALLER" "$INSTALLER_DIR/$INSTALLER_NAME" || {
        log_error "Failed to move srtla-installer to $INSTALLER_DIR"
        return
    }
    chown root:root "$INSTALLER_DIR/$INSTALLER_NAME"
    chmod 755 "$INSTALLER_DIR/$INSTALLER_NAME"
    log_info "srtla-installer installed to $INSTALLER_DIR/$INSTALLER_NAME"
    # Create systemd service
    INSTALLER_SERVICE_FILE="/etc/systemd/system/${INSTALLER_SERVICE}.service"
    cat > "$INSTALLER_SERVICE_FILE" << EOF
[Unit]
Description=SRTLA Privileged Installer Daemon
After=network.target

[Service]
Type=simple
User=root
Group=root
# Kill any process using the socket and remove it before starting
ExecStartPre=/usr/bin/bash -c 'fuser -k /run/srtla-installer.sock 2>/dev/null || true'
ExecStartPre=/usr/bin/bash -c 'rm -f /run/srtla-installer.sock 2>/dev/null || true'
ExecStart=$INSTALLER_DIR/$INSTALLER_NAME
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=srtla-installer
NoNewPrivileges=true
ReadWritePaths=/run /tmp

[Install]
WantedBy=multi-user.target
EOF
    chmod 644 "$INSTALLER_SERVICE_FILE"
    log_info "Systemd service file created at $INSTALLER_SERVICE_FILE"
    systemctl daemon-reload
    systemctl enable "$INSTALLER_SERVICE"
    systemctl restart "$INSTALLER_SERVICE"
    sleep 2
    if systemctl is-active --quiet "$INSTALLER_SERVICE"; then
        log_info "srtla-installer service started successfully"
    else
        log_error "srtla-installer service failed to start"
        systemctl status "$INSTALLER_SERVICE" || true
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

    # Install and start srtla-installer first
    install_srtla_installer

    # Wait for srtla-installer socket to be available (max 15s)
    SOCKET_PATH="/run/srtla-installer.sock"
    log_info "Waiting for srtla-installer socket at $SOCKET_PATH..."
    for i in {1..15}; do
        if [ -S "$SOCKET_PATH" ]; then
            # Try to connect to the socket
            timeout 1 bash -c "> /dev/tcp/localhost/0" 2>/dev/null || true
            log_info "srtla-installer socket is available."
            break
        fi
        sleep 1
        if [ $i -eq 15 ]; then
            log_error "Timeout waiting for srtla-installer socket at $SOCKET_PATH."
            exit 1
        fi
    done

    # Now enable and start srtla-manager service
    enable_service
    print_summary

    # --- SRTLA Send Install ---
    log_info "\nSRTLA Send Installation (irlserver/srtla_send)"
    SEND_REPO="irlserver/srtla_send"
    log_info "Fetching latest release information for srtla_send..."
    SEND_RELEASE_INFO=$(curl -sL "https://api.github.com/repos/${SEND_REPO}/releases/latest") || {
        log_error "Failed to fetch latest release from GitHub for srtla_send"
        exit 1
    }
    SEND_LATEST_VERSION=$(echo "$SEND_RELEASE_INFO" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4)
    if [ -z "$SEND_LATEST_VERSION" ]; then
        log_error "Failed to fetch latest srtla_send release: API may have rate limited the request"
        exit 1
    fi
    log_info "Latest srtla_send version: $SEND_LATEST_VERSION"
    # Find .deb asset
    SEND_DEB_URL=$(echo "$SEND_RELEASE_INFO" | grep -o '"browser_download_url": "[^"]*\.deb"' | cut -d'"' -f4 | grep "amd64" | head -1)
    if [ -z "$SEND_DEB_URL" ]; then
        log_error "No .deb package found for srtla_send (amd64)"
        log_warn "Available assets:"
        echo "$SEND_RELEASE_INFO" | grep -o '"name": "[^"]*' | cut -d'"' -f4
        exit 1
    fi
    log_info "Download URL for srtla_send .deb: $SEND_DEB_URL"
    SEND_DEB_FILE="/tmp/srtla_send_${SEND_LATEST_VERSION}_amd64.deb"
    curl -L -o "$SEND_DEB_FILE" "$SEND_DEB_URL" || {
        log_error "Failed to download srtla_send .deb package"
        exit 1
    }
    log_info "Downloaded srtla_send .deb to $SEND_DEB_FILE"
    # Install .deb
    if command -v dpkg &> /dev/null; then
        log_info "Installing srtla_send .deb with dpkg..."
        dpkg -i "$SEND_DEB_FILE" || {
            log_warn "dpkg install failed, trying apt-get -f install..."
            apt-get -f install -y || {
                log_error "Failed to fix dependencies for srtla_send .deb"
                exit 1
            }
        }
    else
        log_error "dpkg not found, cannot install .deb package for srtla_send"
        exit 1
    fi
    log_info "srtla_send installed successfully."
    # Clean up
    rm -f "$SEND_DEB_FILE"
    log_info "srtla_send .deb install complete."
}

main
