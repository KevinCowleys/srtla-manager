package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"srtla-manager/internal/api"
	"srtla-manager/internal/config"
	"srtla-manager/internal/logger"
	"srtla-manager/internal/modem"
	"srtla-manager/internal/process"
	"srtla-manager/internal/stats"
	"srtla-manager/internal/usbnet"
	"srtla-manager/internal/version"
	"srtla-manager/internal/wifi"
	"srtla-manager/pkg/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	cfgManager := config.NewManager(*configPath)
	if err := cfgManager.Load(); err != nil {
		log.Printf("[WARN] Failed to load config: %v\nAttempting to create a default config...", err)
		// Ensure the config directory exists before saving
		configDir := filepath.Dir(*configPath)
		if mkErr := os.MkdirAll(configDir, 0755); mkErr != nil {
			log.Fatalf("Failed to create config directory %s: %v", configDir, mkErr)
		}
		if saveErr := cfgManager.Save(); saveErr != nil {
			log.Fatalf("Failed to create default config: %v", saveErr)
		}
		log.Printf("[INFO] Default config created at %s", *configPath)
	}

	cfg := cfgManager.Get()

	// Initialize logger with config settings
	if err := logger.Init(cfg.Logging.FilePath, cfg.Logging.MaxSizeMB, cfg.Logging.MaxBackups, cfg.Logging.Debug); err != nil {
		log.Printf("[WARN] Failed to initialize file logging: %v (continuing with stdout only)", err)
		// Initialize with stdout-only logging as fallback
		if err := logger.Init("", 0, 0, cfg.Logging.Debug); err != nil {
			log.Fatalf("Failed to initialize logger: %v", err)
		}
	}
	defer logger.Get().Close()

	logger.Printf("Starting srtla-manager on port %d", cfg.Web.Port)

	statsCollector := stats.NewCollector()
	logBuffer := stats.NewLogBuffer(1000)

	ffmpegHandler := process.NewFFmpegHandler()
	srtlaHandler := process.NewSRTLAHandler()
	modemManager := modem.NewManager()
	usbnetSvc, err := usbnet.Start(context.Background(), usbnet.WithPersistPath("/var/lib/srtla-manager/device_mappings.json"))
	if err != nil {
		logger.Warn("Failed to start usbnet reconciler: %v", err)
	}

	wifiManager := wifi.NewManager(log.New(os.Stderr, "[WIFI] ", log.LstdFlags))
	wsHub := api.NewHub()
	go wsHub.Run()

	logCallback := func(log process.LogLine) {
		logBuffer.Add(log.Source, log.Line)
		wsHub.Broadcast("log", map[string]string{
			"source": log.Source,
			"line":   log.Line,
		})
		// Also write to the logger file
		logger.Printf("[%s] %s", log.Source, log.Line)
	}
	ffmpegHandler.SetLogCallback(logCallback)
	srtlaHandler.SetLogCallback(logCallback)

	handler := api.NewHandler(cfgManager, ffmpegHandler, srtlaHandler, modemManager, usbnetSvc, statsCollector, logBuffer, wsHub, wifiManager)
	handler.SetVersion(version.GetVersion())

	// Auto-start FFmpeg in receive-only mode so cameras can connect immediately
	if err := handler.StartReceiveMode(); err != nil {
		logger.Warn("Failed to auto-start FFmpeg in receive mode: %v", err)
	}

	// Watch for DJI device state changes
	// go func() {
	// 	log.Println("[DJI] State watcher started, monitoring for streaming state")
	// 	for deviceState := range handler.GetDJIController().SubscribeUpdates() {
	// 		log.Printf("[DJI] State update received: state=%s, hasConfig=%v\n",
	// 			deviceState.ConnectionState, deviceState.StreamConfig != nil)

	// 		if deviceState.ConnectionState == dji.StateStreaming {
	// 			log.Printf("[DJI] Device entered streaming state\n")

	// 			// FFmpeg should already be running in receive mode.
	// 			// If somehow idle, try to recover.
	// 			if handler.GetPipelineMode() == api.PipelineModeIdle {
	// 				log.Printf("[DJI] FFmpeg not running, recovering to receive mode...\n")
	// 				if err := handler.StartReceiveMode(); err != nil {
	// 					log.Printf("[DJI] ERROR: Failed to start receive mode: %v\n", err)
	// 				}
	// 			} else {
	// 				log.Printf("[DJI] FFmpeg already running in %s mode\n", handler.GetPipelineMode())
	// 			}
	// 		}
	// 	}
	// 	log.Println("[DJI] State watcher stopped")
	// }()

	go func() {
		ticker := time.NewTicker(time.Second)
		modemTicker := time.NewTicker(5 * time.Second)
		wifiTicker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		defer modemTicker.Stop()
		defer wifiTicker.Stop()

		for {
			select {
			case <-ticker.C:
				ffStats := ffmpegHandler.Stats()
				srtlaStats := srtlaHandler.Stats()
				ffStale := ffmpegHandler.IsStale(api.FFmpegStaleThreshold)
				srtlaStale := srtlaHandler.IsStale(api.SRTLAStaleThreshold)

				statsCollector.Record(ffStats.Bitrate, srtlaStats.TotalBitrate, ffStats.FPS)

				wsHub.Broadcast("stats", map[string]interface{}{
					"pipeline_mode": handler.GetPipelineMode(),
					"ffmpeg": map[string]interface{}{
						"state":   ffStats.State,
						"bitrate": ffStats.Bitrate,
						"fps":     ffStats.FPS,
						"stale":   ffStale,
					},
					"srtla": map[string]interface{}{
						"state":       srtlaStats.State,
						"bitrate":     srtlaStats.TotalBitrate,
						"connections": srtlaStats.Connections,
						"stale":       srtlaStale,
					},
				})

			case <-modemTicker.C:
				modemStatus := handler.GetModemStatus()
				wsHub.Broadcast("modems", modemStatus)

				usbStatus := handler.GetUSBNetStatus()
				wsHub.Broadcast("usbnet", usbStatus)

			case <-wifiTicker.C:
				wsHub.Broadcast("wifi", map[string]interface{}{
					"type": "wifi",
				})
			}
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/status", handler.HandleStatus)
	mux.HandleFunc("/api/stream/start", handler.HandleStreamStart)
	mux.HandleFunc("/api/stream/stop", handler.HandleStreamStop)
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.HandleConfigGet(w, r)
		case http.MethodPut:
			handler.HandleConfigUpdate(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/srtla/ips", handler.HandleSRTLAIPs)
	mux.HandleFunc("/api/srtla/ips/file", handler.HandleIPsFile)
	mux.HandleFunc("/api/srtla/ips/file/load", handler.HandleIPsFileLoad)
	mux.HandleFunc("/api/srtla/ips/file/save", handler.HandleIPsFileSave)
	mux.HandleFunc("/api/system/dependencies", handler.HandleDependencies)
	mux.HandleFunc("/api/system/install-deb", handler.HandleInstallDeb)
	mux.HandleFunc("/api/system/interfaces", handler.HandleInterfaces)
	mux.HandleFunc("/api/modems", handler.HandleModems)
	mux.HandleFunc("/api/modems/", handler.HandleModems)
	mux.HandleFunc("/api/usbnet", handler.HandleUSBNet)
	mux.HandleFunc("/api/wifi/networks", handler.HandleWiFi)
	mux.HandleFunc("/api/wifi/status", handler.HandleWiFi)
	mux.HandleFunc("/api/wifi/connect", handler.HandleWiFi)
	mux.HandleFunc("/api/wifi/disconnect", handler.HandleWiFi)
	mux.HandleFunc("/api/wifi/hotspot", handler.HandleWiFi)
	mux.HandleFunc("/api/wifi/hotspot/stop", handler.HandleWiFi)
	mux.HandleFunc("/api/wifi/forget", handler.HandleWiFi)
	mux.HandleFunc("/api/logs", handler.HandleLogs)
	mux.HandleFunc("/api/logs/download", handler.HandleLogsDownload)
	mux.HandleFunc("/api/debug", handler.HandleDebugMode)

	// Update endpoints
	mux.HandleFunc("/api/updates/check", handler.HandleCheckUpdates)
	mux.HandleFunc("/api/updates/releases", handler.HandleGetReleases)
	mux.HandleFunc("/api/updates/perform", handler.HandlePerformUpdate)
	mux.HandleFunc("/api/updates/backups", handler.HandleGetBackups)
	mux.HandleFunc("/api/updates/rollback", handler.HandleRollback)

	// SRTLA Send update endpoints
	mux.HandleFunc("/api/updates/srtla/check", handler.HandleCheckSRTLASendUpdates)
	mux.HandleFunc("/api/updates/srtla/releases", handler.HandleGetSRTLASendReleases)
	mux.HandleFunc("/api/updates/srtla/install", handler.HandleInstallSRTLASend)

	// HLS preview static files
	mux.Handle("/preview/", http.StripPrefix("/preview/", http.FileServer(http.Dir(handler.PreviewDir()))))
	mux.Handle("/preview-temp/", http.StripPrefix("/preview-temp/", http.FileServer(http.Dir("/tmp/srtla-preview-temp"))))

	// DJI Camera endpoints
	mux.HandleFunc("/api/cameras", handler.HandleCameraList)
	mux.HandleFunc("/api/cameras/scan", handler.HandleCameraScan)
	mux.HandleFunc("/api/cameras/scan/stop", handler.HandleCameraScanStop)
	mux.HandleFunc("/api/cameras/debug/add", handler.HandleDebugAddTestCamera) // Debug endpoint
	mux.HandleFunc("/api/cameras/", func(w http.ResponseWriter, r *http.Request) {
		// Route camera-specific endpoints
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/connect"):
			handler.HandleCameraConnect(w, r)
		case strings.HasSuffix(path, "/disconnect"):
			handler.HandleCameraDisconnect(w, r)
		case strings.HasSuffix(path, "/preview"):
			handler.HandleCameraPreview(w, r)
		case strings.HasSuffix(path, "/configure"):
			handler.HandleCameraConfigure(w, r)
		case strings.HasSuffix(path, "/stop"):
			handler.HandleCameraStop(w, r)
		case strings.HasSuffix(path, "/forget"):
			handler.HandleCameraForget(w, r)
		case strings.HasSuffix(path, "/refresh"):
			handler.HandleCameraRefresh(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// USB Camera endpoints
	mux.HandleFunc("GET /api/usbcams", handler.HandleUSBCameraList)
	mux.HandleFunc("POST /api/usbcams/scan", handler.HandleUSBCameraScan)
	mux.HandleFunc("GET /api/usbcams/{id}", handler.HandleUSBCameraGet)
	mux.HandleFunc("POST /api/usbcams/{id}/start", handler.HandleUSBCameraStart)
	mux.HandleFunc("POST /api/usbcams/{id}/stop", handler.HandleUSBCameraStop)
	mux.HandleFunc("POST /api/usbcams/{id}/preview", handler.HandleUSBCameraPreview)
	mux.HandleFunc("GET /api/usbcams/{id}/preview-stream", handler.HandleUSBCameraPreviewStream)
	mux.HandleFunc("POST /api/usbcams/{id}/preview/stop", handler.HandleUSBCameraPreviewStop)

	mux.HandleFunc("/ws", handler.HandleWebSocket)

	webContent, err := fs.Sub(web.FS, "assets")
	if err != nil {
		log.Fatalf("Failed to get web subdirectory: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(webContent)))

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Web.Port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server failed: %v", err)
		}
	}()

	logger.Printf("Server started at http://localhost:%d", cfg.Web.Port)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Println("Shutting down...")

	if usbnetSvc != nil {
		_ = usbnetSvc.Stop()
	}

	srtlaHandler.Stop()
	ffmpegHandler.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server shutdown error: %v", err)
	}

	logger.Println("Server stopped")
}
