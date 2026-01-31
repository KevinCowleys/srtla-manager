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
)

// Version/build info (set via -ldflags)
var (
	Version   = "v0.0.0-dev"
	Commit    = "unknown"
	Branch    = "unknown"
	BuildTime = "unknown"
	Builder   = "unknown"
)

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

func handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		writeResponse(conn, false, "Failed to read request: "+err.Error())
		return
	}
	line = strings.TrimSpace(line)
	var req InstallRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		writeResponse(conn, false, "Invalid JSON: "+err.Error())
		return
	}
	if req.Token != authToken {
		writeResponse(conn, false, "Unauthorized: invalid token")
		return
	}
	if !strings.HasSuffix(req.DebPath, ".deb") {
		writeResponse(conn, false, "Invalid .deb file path")
		return
	}
	if _, err := os.Stat(req.DebPath); err != nil {
		writeResponse(conn, false, "File not found: "+err.Error())
		return
	}
	cmd := exec.Command("dpkg", "-i", req.DebPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		writeResponse(conn, false, fmt.Sprintf("dpkg failed: %v\n%s", err, output))
		return
	}
	writeResponse(conn, true, string(output))
}

func writeResponse(w io.Writer, success bool, msg string) {
	resp := InstallResponse{Success: success, Message: msg}
	data, _ := json.Marshal(resp)
	w.Write(append(data, '\n'))
}
