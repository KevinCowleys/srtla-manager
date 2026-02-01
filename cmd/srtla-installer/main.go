package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Version/build info (set via -ldflags)
var (
	Version   = "v0.0.0-dev"
	Commit    = "unknown"
	Branch    = "unknown"
	BuildTime = "unknown"
	Builder   = "unknown"
)

// Track pending update operations
var pendingUpdates sync.WaitGroup

func DetailedInfo() string {
	return fmt.Sprintf(
		"srtla-installer %s\n  Commit: %s\n  Branch: %s\n  Built: %s\n  Builder: %s\n  Go: %s\n  OS: %s\n  Arch: %s",
		Version,
		Commit,
		Branch,
		BuildTime,
		Builder,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
}

const (
	socketPath = "/run/srtla-installer.sock"
	authToken  = "REPLACE_ME_WITH_A_SECURE_TOKEN"
)

type InstallRequest struct {
	Token   string `json:"token"`
	DebPath string `json:"deb_path"`
}

type InstallResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type UpdateBinaryRequest struct {
	Token       string `json:"token"`
	SourcePath  string `json:"source_path"`
	TargetPath  string `json:"target_path"`
	ServiceName string `json:"service_name"`
	BackupPath  string `json:"backup_path"`
}

type UpdateBinaryResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

type UpdateInstallerRequest struct {
	Token      string `json:"token"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
}

type UpdateInstallerResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

func main() {
	versionFlag := flag.Bool("version", false, "Show version and exit")
	versionShort := flag.Bool("v", false, "Show version and exit (shorthand)")
	flag.Parse()

	if *versionFlag || *versionShort {
		fmt.Println(DetailedInfo())
		os.Exit(0)
	}

	// If the socket file exists, check if it's stale (not in use)
	if _, err := os.Stat(socketPath); err == nil {
		// Try to connect to see if something is listening
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			// Something is listening, exit with error
			conn.Close()
			log.Fatalf("Socket %s already in use by another process", socketPath)
		}
		// If not in use, just try to bind; let net.Listen handle any errors
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", socketPath, err)
	}
	defer os.Remove(socketPath)
	defer listener.Close()
	os.Chmod(socketPath, 0660)
	// Set group ownership to the effective group (e.g., srtla)
	if grp, err := os.LookupEnv("SRTLA_INSTALLER_GROUP"); err == false || grp == "" {
		// Default: use current process group
		gid := os.Getegid()
		_ = os.Chown(socketPath, -1, gid)
	} else {
		// If env var is set, use that group
		if g, err := lookupGroupID(grp); err == nil {
			_ = os.Chown(socketPath, -1, g)
		}
	}
	log.Printf("srtla-installer daemon started on %s", socketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleConn(conn)
	}
}

// lookupGroupID returns the GID for a group name
func lookupGroupID(name string) (int, error) {
	// Only works on Unix
	f, err := os.Open("/etc/group")
	if err != nil {
		return -1, err
	}
	defer f.Close()
	var gid int
	var found bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, ":")
		if len(fields) >= 3 && fields[0] == name {
			fmt.Sscanf(fields[2], "%d", &gid)
			found = true
			break
		}
	}
	if !found {
		return -1, fmt.Errorf("group not found")
	}
	return gid, nil
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		writeResponse(conn, false, "Failed to read request: "+err.Error())
		return
	}
	line = strings.TrimSpace(line)

	// Try to detect request type by checking which fields are present
	// Check for self-update request first (has SourcePath but no TargetPath pointing to a service)
	var updateInstallerReq UpdateInstallerRequest
	if err := json.Unmarshal([]byte(line), &updateInstallerReq); err == nil &&
		updateInstallerReq.SourcePath != "" &&
		!strings.Contains(updateInstallerReq.TargetPath, "/srtla-manager") {
		// This is a self-update request
		handleInstallerUpdate(conn, updateInstallerReq)
		return
	}

	// Try binary update request (has ServiceName)
	var updateReq UpdateBinaryRequest
	if err := json.Unmarshal([]byte(line), &updateReq); err == nil && updateReq.SourcePath != "" {
		handleBinaryUpdate(conn, updateReq)
		return
	}

	// Fall back to InstallRequest
	var installReq InstallRequest
	if err := json.Unmarshal([]byte(line), &installReq); err != nil {
		writeResponse(conn, false, "Invalid JSON: "+err.Error())
		return
	}
	if !strings.HasSuffix(installReq.DebPath, ".deb") {
		writeResponse(conn, false, "Invalid .deb file path")
		return
	}
	if _, err := os.Stat(installReq.DebPath); err != nil {
		writeResponse(conn, false, "File not found: "+err.Error())
		return
	}
	cmd := exec.Command("dpkg", "-i", installReq.DebPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		writeResponse(conn, false, fmt.Sprintf("dpkg failed: %v\n%s", err, output))
		return
	}
	writeResponse(conn, true, string(output))
}

// handleInstallerUpdate handles self-update of the srtla-installer daemon
func handleInstallerUpdate(conn net.Conn, req UpdateInstallerRequest) {
	log.Printf("[UPDATE] Received installer update request: source=%s target=%s", req.SourcePath, req.TargetPath)

	// Validate paths
	if req.SourcePath == "" || req.TargetPath == "" {
		log.Printf("[UPDATE] FAILED: Missing paths")
		writeInstallerUpdateResponse(conn, false, "Source and target paths required")
		return
	}

	if _, err := os.Stat(req.SourcePath); err != nil {
		log.Printf("[UPDATE] FAILED: Source file not found: %v", err)
		writeInstallerUpdateResponse(conn, false, fmt.Sprintf("Source file not found: %v", err))
		return
	}

	log.Printf("[UPDATE] Source file validated, spawning update goroutine")

	// Fork a background goroutine to handle the update
	// Track it with WaitGroup so we can ensure it completes before exit
	pendingUpdates.Add(1)
	go func() {
		defer pendingUpdates.Done()
		log.Printf("[UPDATE] Update goroutine started")

		// Wait a brief moment for the parent to finish responding
		time.Sleep(500 * time.Millisecond)
		log.Printf("[UPDATE] Wait period complete, proceeding with binary replacement")

		// Replace the binary using rename (atomic on Linux)
		if err := os.Rename(req.SourcePath, req.TargetPath); err != nil {
			log.Printf("[UPDATE] FAILED: Could not replace binary: %v", err)
			return
		}
		log.Printf("[UPDATE] Binary replaced successfully")

		// Ensure proper permissions
		if err := os.Chmod(req.TargetPath, 0755); err != nil {
			log.Printf("[UPDATE] WARNING: Could not set permissions: %v", err)
		} else {
			log.Printf("[UPDATE] Permissions set to 0755")
		}

		// Restart the service
		log.Printf("[UPDATE] Restarting srtla-installer service...")
		cmd := exec.Command("systemctl", "restart", "srtla-installer")
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[UPDATE] WARNING: systemctl restart failed: %v, output: %s", err, output)
		} else {
			log.Printf("[UPDATE] Service restart command executed successfully")
		}
		log.Printf("[UPDATE] Update goroutine complete")
	}()

	log.Printf("[UPDATE] Sending success response to client")
	writeInstallerUpdateResponse(conn, true, "Update initiated, installer will restart shortly")
}

// handleBinaryUpdate handles privileged binary replacement
func handleBinaryUpdate(conn net.Conn, req UpdateBinaryRequest) {
	log.Printf("[BINARY_UPDATE] Received binary update request: source=%s target=%s service=%s backup=%s",
		req.SourcePath, req.TargetPath, req.ServiceName, req.BackupPath)

	// Validate paths
	if req.SourcePath == "" || req.TargetPath == "" {
		log.Printf("[BINARY_UPDATE] FAILED: Missing paths")
		writeBinaryUpdateResponse(conn, false, "Source and target paths required")
		return
	}

	if _, err := os.Stat(req.SourcePath); err != nil {
		log.Printf("[BINARY_UPDATE] FAILED: Source file not found: %v", err)
		writeBinaryUpdateResponse(conn, false, fmt.Sprintf("Source file not found: %v", err))
		return
	}

	// Create backup if specified
	if req.BackupPath != "" {
		log.Printf("[BINARY_UPDATE] Creating backup at %s", req.BackupPath)
		if err := copyFile(req.TargetPath, req.BackupPath); err != nil {
			log.Printf("[BINARY_UPDATE] FAILED: Could not create backup: %v", err)
			writeBinaryUpdateResponse(conn, false, fmt.Sprintf("Failed to create backup: %v", err))
			return
		}
		log.Printf("[BINARY_UPDATE] Backup created successfully")
	}

	// Stop the service if specified
	if req.ServiceName != "" {
		log.Printf("[BINARY_UPDATE] Stopping service %s", req.ServiceName)
		cmd := exec.Command("systemctl", "stop", req.ServiceName)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[BINARY_UPDATE] WARNING: Could not stop service: %v, output: %s", err, output)
		} else {
			log.Printf("[BINARY_UPDATE] Service stopped successfully")
		}
		// Give it a moment to fully stop
		time.Sleep(500 * time.Millisecond)
	}

	// Replace the binary using rename (atomic on Linux)
	log.Printf("[BINARY_UPDATE] Replacing binary: %s -> %s", req.SourcePath, req.TargetPath)
	if err := os.Rename(req.SourcePath, req.TargetPath); err != nil {
		log.Printf("[BINARY_UPDATE] FAILED: Could not replace binary: %v", err)
		writeBinaryUpdateResponse(conn, false, fmt.Sprintf("Failed to replace binary: %v", err))
		return
	}
	log.Printf("[BINARY_UPDATE] Binary replaced successfully")

	// Ensure proper permissions
	if err := os.Chmod(req.TargetPath, 0755); err != nil {
		log.Printf("[BINARY_UPDATE] WARNING: Could not set permissions: %v", err)
	} else {
		log.Printf("[BINARY_UPDATE] Permissions set to 0755")
	}

	// Restart the service if specified
	if req.ServiceName != "" {
		log.Printf("[BINARY_UPDATE] Starting service %s", req.ServiceName)
		cmd := exec.Command("systemctl", "start", req.ServiceName)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[BINARY_UPDATE] WARNING: Could not start service: %v, output: %s", err, output)
		} else {
			log.Printf("[BINARY_UPDATE] Service started successfully")
		}
	}

	log.Printf("[BINARY_UPDATE] Binary update complete, sending success response")
	writeBinaryUpdateResponse(conn, true, "Binary updated successfully")
}

// copyFile copies src to dst
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func writeResponse(w io.Writer, success bool, msg string) {
	resp := InstallResponse{Success: success, Message: msg}
	data, _ := json.Marshal(resp)
	w.Write(append(data, '\n'))
}

func writeBinaryUpdateResponse(w io.Writer, success bool, msg string) {
	resp := UpdateBinaryResponse{Success: success, Message: msg}
	data, _ := json.Marshal(resp)
	w.Write(append(data, '\n'))
}

func writeInstallerUpdateResponse(w io.Writer, success bool, msg string) {
	resp := UpdateInstallerResponse{Success: success, Message: msg}
	data, _ := json.Marshal(resp)
	w.Write(append(data, '\n'))
}
