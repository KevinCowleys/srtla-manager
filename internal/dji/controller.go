package dji

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// Timeouts matching Moblin
const (
	StartStreamingTimeout = 60 * time.Second // Global timeout for entire streaming setup
	StopStreamingTimeout  = 10 * time.Second // Timeout for stop streaming
	ResponseTimeout       = 5 * time.Second  // Timeout waiting for individual responses
)

// ConnectionState represents the state of a DJI device connection
type ConnectionState string

const (
	StateIdle            ConnectionState = "idle"
	StateDiscovering     ConnectionState = "discovering"
	StateConnecting      ConnectionState = "connecting"
	StatePairing         ConnectionState = "pairing"
	StateSettingUpWiFi   ConnectionState = "settingup_wifi"
	StateWiFiSetupFailed ConnectionState = "wifi_setup_failed"
	StatePreparingStream ConnectionState = "preparing_stream"
	StateConfiguring     ConnectionState = "configuring"
	StateStartingStream  ConnectionState = "starting_stream"
	StateStreaming       ConnectionState = "streaming"
	StateStopping        ConnectionState = "stopping"
	StateError           ConnectionState = "error"
)

// DJI BLE characteristic UUIDs
var (
	DJI_SERVICE_UUID = bluetooth.NewUUID([16]byte{0xfb, 0x34, 0x9b, 0x5f, 0x80, 0x00, 0x00, 0x80, 0x00, 0x10, 0x00, 0x00, 0xf0, 0xff, 0x00, 0x00})
	DJI_FFF4_UUID    = bluetooth.NewUUID([16]byte{0xfb, 0x34, 0x9b, 0x5f, 0x80, 0x00, 0x00, 0x80, 0x00, 0x10, 0x00, 0x00, 0xf4, 0xff, 0x00, 0x00})
	DJI_FFF5_UUID    = bluetooth.NewUUID([16]byte{0xfb, 0x34, 0x9b, 0x5f, 0x80, 0x00, 0x00, 0x80, 0x00, 0x10, 0x00, 0x00, 0xf5, 0xff, 0x00, 0x00})
)

// DeviceState represents the complete state of a connected DJI device
type DeviceState struct {
	Device            *DiscoveredDevice
	ConnectionState   ConnectionState
	LastError         string
	LastUpdate        time.Time
	StreamConfig      *StreamConfig
	BatteryPercentage int                            // Battery level from camera (-1 if unknown)
	responseChan      chan *DjiMessage               // Channel for receiving parsed responses
	stopNotify        chan struct{}                  // Signal to stop notification listener
	bleDevice         bluetooth.Device               // The connected BLE device
	fff3Char          bluetooth.DeviceCharacteristic // FFF3 characteristic (possible response channel)
	fff4Char          bluetooth.DeviceCharacteristic // FFF4 characteristic for notifications
	fff5Char          bluetooth.DeviceCharacteristic // FFF5 characteristic for writing
	dbusHandler       *DBusNotificationHandler       // D-Bus notification handler for this device
}

// Controller manages DJI device connections and configuration
type Controller struct {
	mu           sync.RWMutex
	scanner      *Scanner
	deviceStates map[string]*DeviceState
	updateChan   chan *DeviceState
}

// NewController creates a new DJI device controller
func NewController(scanner *Scanner) *Controller {
	return &Controller{
		scanner:      scanner,
		deviceStates: make(map[string]*DeviceState),
		updateChan:   make(chan *DeviceState, 10),
	}
}

// ConnectDevice initiates connection to a discovered device
func (c *Controller) ConnectDevice(deviceID string) error {
	discovered := c.scanner.GetDevice(deviceID)
	if discovered == nil {
		return fmt.Errorf("device not found: %s", deviceID)
	}

	c.mu.Lock()
	state, exists := c.deviceStates[deviceID]
	if !exists {
		state = &DeviceState{
			Device:            discovered,
			ConnectionState:   StateIdle,
			BatteryPercentage: -1,
			responseChan:      make(chan *DjiMessage, 10),
			stopNotify:        make(chan struct{}),
		}
		c.deviceStates[deviceID] = state
	} else {
		// Reset for reconnection
		state.responseChan = make(chan *DjiMessage, 10)
		state.stopNotify = make(chan struct{})
		state.BatteryPercentage = -1
	}
	c.mu.Unlock()

	log.Printf("[DJI] Connecting to device: %s (%s)\n", discovered.Name, discovered.Model)
	c.updateState(deviceID, StateConnecting, "")

	go c.connectAndSetup(deviceID, discovered)
	return nil
}

// connectAndSetup performs the BLE connection and DJI protocol setup
func (c *Controller) connectAndSetup(deviceID string, discovered *DiscoveredDevice) {
	c.mu.RLock()
	state := c.deviceStates[deviceID]
	c.mu.RUnlock()

	if state == nil {
		return
	}

	adapter := c.scanner.GetAdapter()

	// Step 1: Connect to device
	log.Printf("[DJI] Step 1: Establishing BLE connection to %s\n", discovered.Address.String())

	device, err := adapter.Connect(discovered.Address, bluetooth.ConnectionParams{})
	if err != nil {
		c.updateState(deviceID, StateError, fmt.Sprintf("BLE connect failed: %v", err))
		return
	}
	log.Printf("[DJI] BLE connection established\n")

	c.mu.Lock()
	state.bleDevice = device
	c.mu.Unlock()

	// Step 2: Discover services
	log.Printf("[DJI] Step 2: Discovering services...\n")

	services, err := device.DiscoverServices(nil)
	if err != nil {
		c.updateState(deviceID, StateError, fmt.Sprintf("Service discovery failed: %v", err))
		return
	}

	log.Printf("[DJI] Found %d services\n", len(services))

	// Step 3: Find and setup characteristics
	log.Printf("[DJI] Step 3: Finding DJI characteristics...\n")

	var fff3Found, fff4Found, fff5Found bool
	for _, service := range services {
		log.Printf("[DJI] Service: %s\n", service.UUID().String())

		chars, err := service.DiscoverCharacteristics(nil)
		if err != nil {
			log.Printf("[DJI] Failed to discover characteristics: %v\n", err)
			continue
		}

		for _, char := range chars {
			uuid := char.UUID().String()
			log.Printf("[DJI]   Characteristic: %s\n", uuid)

			// Check for FFF3, FFF4 (notifications) and FFF5 (write)
			if strings.Contains(strings.ToLower(uuid), "fff3") {
				c.mu.Lock()
				state.fff3Char = char
				c.mu.Unlock()
				fff3Found = true
				log.Printf("[DJI] Found FFF3 characteristic\n")
			} else if strings.Contains(strings.ToLower(uuid), "fff4") {
				c.mu.Lock()
				state.fff4Char = char
				c.mu.Unlock()
				fff4Found = true
				log.Printf("[DJI] Found FFF4 notification characteristic\n")
			} else if strings.Contains(strings.ToLower(uuid), "fff5") {
				c.mu.Lock()
				state.fff5Char = char
				c.mu.Unlock()
				fff5Found = true
				log.Printf("[DJI] Found FFF5 write characteristic\n")
			}
		}
	}

	if !fff5Found {
		c.updateState(deviceID, StateError, "FFF5 write characteristic not found")
		return
	}

	// Step 4: Set up D-Bus notification handler (bypasses broken tinygo-org/bluetooth notifications)
	log.Printf("[DJI] Step 4: Setting up D-Bus notification handler\n")

	dbusHandler, err := NewDBusNotificationHandler()
	if err != nil {
		log.Printf("[DJI] Warning: Failed to create D-Bus handler: %v\n", err)
		log.Printf("[DJI] Falling back to tinygo-org/bluetooth notifications\n")
	} else {
		c.mu.Lock()
		state.dbusHandler = dbusHandler
		c.mu.Unlock()

		// Find D-Bus paths for characteristics
		if fff3Found {
			path, err := dbusHandler.FindCharacteristicPath(deviceID, "fff3")
			if err != nil {
				log.Printf("[DJI] Warning: Failed to find FFF3 D-Bus path: %v\n", err)
			} else if path != "" {
				dbusHandler.RegisterCharacteristic("fff3", path, func(data []byte) {
					log.Printf("[DJI] D-Bus FFF3 notification (%d bytes): %x\n", len(data), data)
					c.handleNotification(deviceID, state, data)
				})
			}
		}

		if fff4Found {
			path, err := dbusHandler.FindCharacteristicPath(deviceID, "fff4")
			if err != nil {
				log.Printf("[DJI] Warning: Failed to find FFF4 D-Bus path: %v\n", err)
			} else if path != "" {
				dbusHandler.RegisterCharacteristic("fff4", path, func(data []byte) {
					log.Printf("[DJI] D-Bus FFF4 notification (%d bytes): %x\n", len(data), data)
					c.handleNotification(deviceID, state, data)
				})
			}
		}

		// Enable notifications via D-Bus
		if err := dbusHandler.EnableNotifications(); err != nil {
			log.Printf("[DJI] Warning: Failed to enable D-Bus notifications: %v\n", err)
		}

		// Start the D-Bus signal processor
		dbusHandler.Start()
		log.Printf("[DJI] D-Bus notification handler started\n")
	}

	// NOTE: Disabled tinygo-org/bluetooth notifications - they use AcquireNotify internally
	// which takes exclusive ownership of notification data, interfering with our D-Bus handler.
	// Our D-Bus handler now uses AcquireNotify directly for better control.
	// if fff4Found {
	// 	log.Printf("[DJI] Also enabling tinygo-org/bluetooth notifications as backup\n")
	// 	... disabled ...
	// }

	// Give time for notification handlers to initialize
	log.Printf("[DJI] Waiting for notification handlers to initialize...\n")
	time.Sleep(500 * time.Millisecond)

	// Step 5: Send DJI protocol pair message
	log.Printf("[DJI] Step 5: Sending DJI protocol pair message\n")
	c.updateState(deviceID, StatePairing, "")

	pairMsg := CreatePairMessage()
	response, err := c.sendAndWaitForResponse(deviceID, pairMsg, PairTransactionID, ResponseTimeout)
	if err != nil {
		log.Printf("[DJI] Pair message failed: %v\n", err)
	} else if response != nil {
		if response.IsPairAlreadyPaired() {
			log.Printf("[DJI] Device already paired\n")
		} else {
			log.Printf("[DJI] Pair response: %x\n", response.Payload)
		}
	}

	log.Printf("[DJI] DJI pairing complete, device ready for streaming\n")
	c.updateState(deviceID, StateIdle, "")
}

// pollFFF4 continuously reads FFF4 characteristic as a fallback for notifications
func (c *Controller) pollFFF4(deviceID string, state *DeviceState) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var lastData []byte
	pollCount := 0
	timeoutCount := 0
	errorCount := 0
	successCount := 0

	log.Printf("[DJI] FFF4 polling goroutine started - will poll every 200ms\n")

	for {
		select {
		case <-state.stopNotify:
			log.Printf("[DJI] FFF4 polling stopped (polls=%d, timeouts=%d, errors=%d, success=%d)\n",
				pollCount, timeoutCount, errorCount, successCount)
			return
		case <-ticker.C:
			pollCount++

			// Log first 5 polls, then every 50th
			verbose := pollCount <= 5 || pollCount%50 == 0

			if verbose {
				log.Printf("[DJI] FFF4 poll #%d starting...\n", pollCount)
			}

			c.mu.RLock()
			fff4 := state.fff4Char
			c.mu.RUnlock()

			// Read with timeout using a goroutine
			resultChan := make(chan struct {
				data []byte
				err  error
			}, 1)

			go func() {
				buf := make([]byte, 512)
				n, err := fff4.Read(buf)
				if err != nil {
					resultChan <- struct {
						data []byte
						err  error
					}{nil, err}
				} else {
					resultChan <- struct {
						data []byte
						err  error
					}{buf[:n], nil}
				}
			}()

			// Wait for read with timeout
			select {
			case result := <-resultChan:
				if result.err != nil {
					errorCount++
					if verbose {
						log.Printf("[DJI] FFF4 poll #%d read error: %v\n", pollCount, result.err)
					}
					continue
				}
				successCount++
				if verbose {
					log.Printf("[DJI] FFF4 poll #%d read success: %d bytes\n", pollCount, len(result.data))
				}
				if len(result.data) > 0 && !bytesEqual(result.data, lastData) {
					log.Printf("[DJI] FFF4 polled NEW data (%d bytes): %x\n", len(result.data), result.data)
					lastData = make([]byte, len(result.data))
					copy(lastData, result.data)
					c.handleNotification(deviceID, state, result.data)
				} else if len(result.data) > 0 && verbose {
					log.Printf("[DJI] FFF4 poll #%d: same data as before (%d bytes)\n", pollCount, len(result.data))
				} else if len(result.data) == 0 && verbose {
					log.Printf("[DJI] FFF4 poll #%d: empty read (0 bytes)\n", pollCount)
				}
			case <-time.After(150 * time.Millisecond):
				timeoutCount++
				if verbose {
					log.Printf("[DJI] FFF4 poll #%d: read TIMEOUT (blocking) - total timeouts: %d\n", pollCount, timeoutCount)
				}
			}
		}
	}
}

// bytesEqual compares two byte slices
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// handleNotification processes an incoming BLE notification
func (c *Controller) handleNotification(deviceID string, state *DeviceState, data []byte) {
	log.Printf("[DJI] Received notification (%d bytes): %x\n", len(data), data)

	// Try to parse as DJI message
	msg, err := DecodeMessage(data)
	if err != nil {
		log.Printf("[DJI] Failed to decode message: %v\n", err)
		return
	}

	log.Printf("[DJI] Decoded message - Target: 0x%04X, ID: 0x%04X, Type: 0x%06X, Payload: %x\n",
		msg.Target, msg.TransactionID, msg.MessageType, msg.Payload)

	// Check for battery status updates
	if battery := msg.GetBatteryPercentage(); battery >= 0 {
		c.mu.Lock()
		state.BatteryPercentage = battery
		c.mu.Unlock()
		log.Printf("[DJI] Battery: %d%%\n", battery)
	}

	// Send to response channel for waiters
	select {
	case state.responseChan <- msg:
	default:
		// Channel full, discard old messages
		select {
		case <-state.responseChan:
		default:
		}
		state.responseChan <- msg
	}
}

// sendAndWaitForResponse sends a message and waits for a response with matching transaction ID
func (c *Controller) sendAndWaitForResponse(deviceID string, msg *DjiMessage, expectedTxID uint16, timeout time.Duration) (*DjiMessage, error) {
	c.mu.RLock()
	state := c.deviceStates[deviceID]
	c.mu.RUnlock()

	if state == nil {
		return nil, fmt.Errorf("device state not found")
	}

	// Send the message
	if err := c.sendBLEMessage(deviceID, msg); err != nil {
		return nil, err
	}

	// Wait for response with matching transaction ID
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for response (txID: 0x%04X)", expectedTxID)
		case response := <-state.responseChan:
			if response.TransactionID == expectedTxID {
				return response, nil
			}
			log.Printf("[DJI] Received response with different txID: 0x%04X (expected 0x%04X)\n",
				response.TransactionID, expectedTxID)
		}
	}
}

// sendBLEMessage sends a DJI protocol message
func (c *Controller) sendBLEMessage(deviceID string, msg *DjiMessage) error {
	c.mu.RLock()
	state := c.deviceStates[deviceID]
	c.mu.RUnlock()

	if state == nil {
		return fmt.Errorf("device state not found")
	}

	messageData := msg.Encode()

	log.Printf("[DJI] Sending message - Target: 0x%04X, ID: 0x%04X, Type: 0x%06X\n",
		msg.Target, msg.TransactionID, msg.MessageType)
	log.Printf("[DJI] Full encoded message (%d bytes): %x\n", len(messageData), messageData)

	c.mu.RLock()
	fff5 := state.fff5Char
	fff4 := state.fff4Char
	c.mu.RUnlock()

	_, err := fff5.WriteWithoutResponse(messageData)
	if err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	log.Printf("[DJI] Message sent successfully\n")

	// Try to read response immediately after writing
	// Some BLE implementations need this to trigger notification delivery
	go func() {
		time.Sleep(50 * time.Millisecond)
		for i := 0; i < 10; i++ {
			readDone := make(chan struct {
				n   int
				err error
			}, 1)

			buf := make([]byte, 512)
			go func() {
				n, err := fff4.Read(buf)
				readDone <- struct {
					n   int
					err error
				}{n, err}
			}()

			select {
			case result := <-readDone:
				if result.err == nil && result.n > 0 {
					data := buf[:result.n]
					log.Printf("[DJI] FFF4 read-after-write #%d (%d bytes): %x\n", i+1, result.n, data)
					c.handleNotification(deviceID, state, data)
					return
				} else if result.err != nil {
					log.Printf("[DJI] FFF4 read-after-write #%d error: %v\n", i+1, result.err)
				}
			case <-time.After(100 * time.Millisecond):
				log.Printf("[DJI] FFF4 read-after-write #%d: timeout (read blocking)\n", i+1)
			}
			time.Sleep(50 * time.Millisecond)
		}
		log.Printf("[DJI] FFF4 read-after-write: gave up after 10 attempts\n")
	}()

	return nil
}

// ConfigureStreaming configures streaming on a connected device
func (c *Controller) ConfigureStreaming(deviceID string, config *StreamConfig) error {
	c.mu.RLock()
	state, exists := c.deviceStates[deviceID]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("device state not found: %s", deviceID)
	}

	if state.Device == nil {
		return fmt.Errorf("device not found: %s", deviceID)
	}

	if config == nil {
		return fmt.Errorf("stream config cannot be nil")
	}

	log.Printf("[DJI] Configuring streaming for %s: RTMP=%s, WiFi=%s\n",
		state.Device.Name, config.RTMPURL, config.WiFiSSID)

	// Determine if OA5+ model for message encoding
	isOA5Plus := state.Device.Model == ModelOsmoAction5Pro || state.Device.Model == ModelOsmoAction6
	config.IsOA5Plus = isOA5Plus

	go c.runStreamingStateMachine(deviceID, state, config, isOA5Plus)
	return nil
}

// runStreamingStateMachine executes the streaming setup sequence
func (c *Controller) runStreamingStateMachine(deviceID string, state *DeviceState, config *StreamConfig, isOA5Plus bool) {
	ctx, cancel := context.WithTimeout(context.Background(), StartStreamingTimeout)
	defer cancel()

	checkTimeout := func() bool {
		select {
		case <-ctx.Done():
			log.Printf("[DJI] Global timeout reached, aborting streaming setup\n")
			c.updateState(deviceID, StateError, "Streaming setup timeout (60s)")
			return true
		default:
			return false
		}
	}

	// Step 1: Send stop streaming (cleanup)
	log.Printf("[DJI] Step 1: Sending stop streaming (cleanup)\n")
	c.updateState(deviceID, StateStopping, "")
	stopMsg := CreateStopStreamingMessage()
	c.sendAndWaitForResponse(deviceID, stopMsg, StopStreamingTransactionID, 2*time.Second)

	if checkTimeout() {
		return
	}

	// Step 2: Send preparing to livestream
	log.Printf("[DJI] Step 2: Sending preparing to livestream\n")
	c.updateState(deviceID, StatePreparingStream, "")
	prepareMsg := CreatePreparingToLivestreamMessage()
	_, err := c.sendAndWaitForResponse(deviceID, prepareMsg, PreparingToLivestreamTransactionID, ResponseTimeout)
	if err != nil {
		log.Printf("[DJI] Prepare response: %v (continuing anyway)\n", err)
	}

	if checkTimeout() {
		return
	}

	// Step 3: Send WiFi config
	log.Printf("[DJI] Step 3: Sending WiFi config (SSID: %s)\n", config.WiFiSSID)
	c.updateState(deviceID, StateSettingUpWiFi, "")
	wifiMsg, err := CreateWiFiConfigMessage(config.WiFiSSID, config.WiFiPassword)
	if err != nil {
		c.updateState(deviceID, StateError, err.Error())
		return
	}

	wifiResponse, err := c.sendAndWaitForResponse(deviceID, wifiMsg, SetupWiFiTransactionID, ResponseTimeout)
	if err != nil {
		log.Printf("[DJI] WiFi config response error: %v\n", err)
	} else if wifiResponse != nil {
		if wifiResponse.IsWiFiSetupSuccess() {
			log.Printf("[DJI] WiFi setup successful\n")
		} else {
			log.Printf("[DJI] WiFi setup response: %x (may indicate failure)\n", wifiResponse.Payload)
			c.updateState(deviceID, StateWiFiSetupFailed, "WiFi setup failed")
			return
		}
	}

	if checkTimeout() {
		return
	}

	// Step 4: Send Configure message for OA4+
	if state.Device.Model == ModelOsmoAction4 || isOA5Plus {
		log.Printf("[DJI] Step 4: Sending configure message (stabilization)\n")
		c.updateState(deviceID, StateConfiguring, "")
		configMsg, err := CreateConfigureMessage(config.Stabilization, isOA5Plus)
		if err != nil {
			log.Printf("[DJI] Failed to create configure message: %v\n", err)
			c.updateState(deviceID, StateError, fmt.Sprintf("Configure message failed: %v", err))
			return
		}

		_, err = c.sendAndWaitForResponse(deviceID, configMsg, ConfigureTransactionID, ResponseTimeout)
		if err != nil {
			log.Printf("[DJI] Configure response: %v (continuing anyway)\n", err)
		}

		if checkTimeout() {
			return
		}
	}

	// Step 5: Send start streaming
	log.Printf("[DJI] Step 5: Sending start streaming (RTMP: %s)\n", config.RTMPURL)
	c.updateState(deviceID, StateStartingStream, "")
	streamMsg, err := CreateStartStreamMessage(*config)
	if err != nil {
		c.updateState(deviceID, StateError, err.Error())
		return
	}

	streamResponse, err := c.sendAndWaitForResponse(deviceID, streamMsg, StartStreamingTransactionID, ResponseTimeout)
	if err != nil {
		log.Printf("[DJI] Start streaming response: %v (continuing anyway)\n", err)
	} else if streamResponse != nil {
		log.Printf("[DJI] Start streaming response: %x\n", streamResponse.Payload)
	}

	if checkTimeout() {
		return
	}

	// Step 6: For OA5P/OA6, send confirm start streaming
	if isOA5Plus {
		log.Printf("[DJI] Step 6: Sending confirm start streaming (OA5P/OA6)\n")
		confirmMsg := CreateConfirmStartStreamingMessage()
		if err := c.sendBLEMessage(deviceID, confirmMsg); err != nil {
			log.Printf("[DJI] Failed to send confirm: %v\n", err)
		}
	}

	c.mu.Lock()
	state.StreamConfig = config
	c.mu.Unlock()

	log.Printf("[DJI] Streaming configuration complete!\n")
	c.updateState(deviceID, StateStreaming, "")
}

// StopStreaming stops streaming on a device
func (c *Controller) StopStreaming(deviceID string) error {
	c.mu.RLock()
	state, exists := c.deviceStates[deviceID]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("device state not found: %s", deviceID)
	}

	log.Printf("[DJI] Stopping stream on device: %s\n", state.Device.Name)
	c.updateState(deviceID, StateStopping, "")

	go func() {
		stopMsg := CreateStopStreamingMessage()
		response, err := c.sendAndWaitForResponse(deviceID, stopMsg, StopStreamingTransactionID, StopStreamingTimeout)
		if err != nil {
			log.Printf("[DJI] Stop streaming response: %v\n", err)
		} else if response != nil {
			log.Printf("[DJI] Stop streaming confirmed: %x\n", response.Payload)
		}
		c.updateState(deviceID, StateIdle, "")
	}()

	return nil
}

// GetBatteryPercentage returns the current battery percentage for a device
func (c *Controller) GetBatteryPercentage(deviceID string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state, exists := c.deviceStates[deviceID]
	if !exists {
		return -1
	}
	return state.BatteryPercentage
}

// DisconnectDevice closes connection to a device
func (c *Controller) DisconnectDevice(deviceID string) error {
	c.mu.Lock()
	state, exists := c.deviceStates[deviceID]
	if exists {
		// Stop D-Bus notification handler
		if state.dbusHandler != nil {
			state.dbusHandler.Stop()
		}
		// Close notification stop channel
		select {
		case <-state.stopNotify:
			// Already closed
		default:
			close(state.stopNotify)
		}
		// Disconnect BLE device
		if state.bleDevice != (bluetooth.Device{}) {
			state.bleDevice.Disconnect()
		}
	}
	delete(c.deviceStates, deviceID)
	c.mu.Unlock()

	log.Printf("[DJI] Disconnected from device: %s\n", deviceID)
	return nil
}

// RemoveDevice removes any cached state for a device
func (c *Controller) RemoveDevice(deviceID string) {
	c.mu.Lock()
	delete(c.deviceStates, deviceID)
	c.mu.Unlock()
}

// GetDeviceState returns the current state of a device
func (c *Controller) GetDeviceState(deviceID string) *DeviceState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.deviceStates[deviceID]
}

// GetAllDeviceStates returns all device states
func (c *Controller) GetAllDeviceStates() []*DeviceState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	states := make([]*DeviceState, 0, len(c.deviceStates))
	for _, state := range c.deviceStates {
		states = append(states, state)
	}
	return states
}

// updateState updates the internal state of a device
func (c *Controller) updateState(deviceID string, state ConnectionState, errMsg string) {
	c.mu.Lock()
	deviceState, exists := c.deviceStates[deviceID]
	if !exists {
		c.mu.Unlock()
		return
	}

	deviceState.ConnectionState = state
	deviceState.LastError = errMsg
	deviceState.LastUpdate = time.Now()
	c.mu.Unlock()

	log.Printf("[DJI] %s state -> %s\n", deviceID, state)

	select {
	case c.updateChan <- deviceState:
	default:
	}
}

// SubscribeUpdates returns a channel for device state updates
func (c *Controller) SubscribeUpdates() <-chan *DeviceState {
	return c.updateChan
}
