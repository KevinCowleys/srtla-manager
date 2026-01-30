package dji

import (
	"log"
	"os"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
)

// DBusNotificationHandler handles BLE notifications directly via D-Bus
type DBusNotificationHandler struct {
	mu          sync.RWMutex
	conn        *dbus.Conn
	charPaths   map[string]dbus.ObjectPath // characteristic UUID -> D-Bus path
	callbacks   map[string]func([]byte)    // characteristic UUID -> callback
	signalChan  chan *dbus.Signal
	stopChan    chan struct{}
	running     bool
	notifyFDs   map[string]int // characteristic UUID -> file descriptor from AcquireNotify
}

// NewDBusNotificationHandler creates a new D-Bus notification handler
func NewDBusNotificationHandler() (*DBusNotificationHandler, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, err
	}

	return &DBusNotificationHandler{
		conn:       conn,
		charPaths:  make(map[string]dbus.ObjectPath),
		callbacks:  make(map[string]func([]byte)),
		signalChan: make(chan *dbus.Signal, 100),
		stopChan:   make(chan struct{}),
		notifyFDs:  make(map[string]int),
	}, nil
}

// FindCharacteristicPath finds the D-Bus path for a characteristic by UUID
func (h *DBusNotificationHandler) FindCharacteristicPath(deviceAddr string, charUUID string) (dbus.ObjectPath, error) {
	// Convert device address to D-Bus path format (XX:XX:XX:XX:XX:XX -> XX_XX_XX_XX_XX_XX)
	devicePathPart := strings.ReplaceAll(strings.ToUpper(deviceAddr), ":", "_")
	charUUIDLower := strings.ToLower(charUUID)

	// List all objects under org.bluez
	var managedObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	obj := h.conn.Object("org.bluez", "/")
	err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&managedObjects)
	if err != nil {
		return "", err
	}

	// Find the characteristic path
	for path, interfaces := range managedObjects {
		pathStr := string(path)

		// Check if this is a characteristic under our device
		if !strings.Contains(pathStr, devicePathPart) {
			continue
		}

		charIface, hasChar := interfaces["org.bluez.GattCharacteristic1"]
		if !hasChar {
			continue
		}

		// Check UUID
		if uuidVar, ok := charIface["UUID"]; ok {
			uuid := uuidVar.Value().(string)
			if strings.Contains(strings.ToLower(uuid), charUUIDLower) {
				log.Printf("[DJI-DBus] Found characteristic %s at path: %s\n", charUUID, path)
				return path, nil
			}
		}
	}

	return "", nil
}

// RegisterCharacteristic registers a characteristic for notification monitoring
func (h *DBusNotificationHandler) RegisterCharacteristic(charUUID string, path dbus.ObjectPath, callback func([]byte)) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.charPaths[charUUID] = path
	h.callbacks[charUUID] = callback
	log.Printf("[DJI-DBus] Registered characteristic %s at %s\n", charUUID, path)
}

// EnableNotifications enables D-Bus notifications for registered characteristics
// Uses AcquireNotify for exclusive file descriptor access to notification data
func (h *DBusNotificationHandler) EnableNotifications() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Subscribe to ALL signals first (for debugging/fallback)
	matchRuleAll := "type='signal',sender='org.bluez'"
	call := h.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRuleAll)
	if call.Err != nil {
		log.Printf("[DJI-DBus] Failed to add general match rule: %v\n", call.Err)
	} else {
		log.Printf("[DJI-DBus] Added general match rule for org.bluez signals\n")
	}

	// Try AcquireNotify first (gives exclusive access to notification data via file descriptor)
	// This bypasses the Value property entirely and reads directly from the BLE stack
	for uuid, path := range h.charPaths {
		obj := h.conn.Object("org.bluez", path)

		// AcquireNotify returns (fd, mtu) - the fd is for reading notification data
		var fd dbus.UnixFD
		var mtu uint16
		call := obj.Call("org.bluez.GattCharacteristic1.AcquireNotify", 0, map[string]dbus.Variant{})
		if call.Err != nil {
			log.Printf("[DJI-DBus] AcquireNotify failed for %s: %v (will try StartNotify)\n", uuid, call.Err)

			// Fall back to StartNotify
			call = obj.Call("org.bluez.GattCharacteristic1.StartNotify", 0)
			if call.Err != nil {
				if !strings.Contains(call.Err.Error(), "Already notifying") {
					log.Printf("[DJI-DBus] StartNotify also failed for %s: %v\n", uuid, call.Err)
				} else {
					log.Printf("[DJI-DBus] Notifications already enabled on %s\n", uuid)
				}
			} else {
				log.Printf("[DJI-DBus] Started notifications via StartNotify on %s\n", uuid)
			}
		} else {
			err := call.Store(&fd, &mtu)
			if err != nil {
				log.Printf("[DJI-DBus] Failed to store AcquireNotify result for %s: %v\n", uuid, err)
				continue
			}
			h.notifyFDs[uuid] = int(fd)
			log.Printf("[DJI-DBus] AcquireNotify SUCCESS for %s: fd=%d, mtu=%d\n", uuid, fd, mtu)
		}
	}

	h.conn.Signal(h.signalChan)
	return nil
}

// Start begins processing D-Bus signals and FD readers
func (h *DBusNotificationHandler) Start() {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true

	// Start FD readers for each AcquireNotify file descriptor
	for uuid, fd := range h.notifyFDs {
		go h.readFromFD(uuid, fd)
	}
	h.mu.Unlock()

	go h.processSignals()
}

// readFromFD reads notification data directly from a file descriptor
func (h *DBusNotificationHandler) readFromFD(uuid string, fd int) {
	log.Printf("[DJI-DBus] Starting FD reader for %s (fd=%d)\n", uuid, fd)

	// Create an os.File from the file descriptor
	file := os.NewFile(uintptr(fd), "ble-notify-"+uuid)
	if file == nil {
		log.Printf("[DJI-DBus] Failed to create file from fd %d for %s\n", fd, uuid)
		return
	}
	defer file.Close()

	buf := make([]byte, 512) // BLE MTU is typically < 512

	for {
		h.mu.RLock()
		running := h.running
		h.mu.RUnlock()

		if !running {
			log.Printf("[DJI-DBus] FD reader stopping for %s\n", uuid)
			return
		}

		n, err := file.Read(buf)
		if err != nil {
			log.Printf("[DJI-DBus] FD read error for %s: %v\n", uuid, err)
			return
		}

		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			log.Printf("[DJI-DBus] FD read %d bytes for %s: %x\n", n, uuid, data)

			// Invoke callback
			h.mu.RLock()
			callback, ok := h.callbacks[uuid]
			h.mu.RUnlock()

			if ok && callback != nil {
				callback(data)
			}
		}
	}
}

// Stop stops the notification handler
func (h *DBusNotificationHandler) Stop() {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	h.mu.Unlock()

	close(h.stopChan)

	// Close file descriptors from AcquireNotify
	h.mu.Lock()
	for uuid, fd := range h.notifyFDs {
		log.Printf("[DJI-DBus] Closing FD %d for %s\n", fd, uuid)
		// The FD reader goroutine will see an error and exit
	}
	h.notifyFDs = make(map[string]int)
	h.mu.Unlock()

	// Stop notifications on each characteristic (for StartNotify-based ones)
	for uuid, path := range h.charPaths {
		obj := h.conn.Object("org.bluez", path)
		call := obj.Call("org.bluez.GattCharacteristic1.StopNotify", 0)
		if call.Err != nil {
			log.Printf("[DJI-DBus] Failed to stop notifications on %s: %v\n", uuid, call.Err)
		}
	}
}

// processSignals handles incoming D-Bus signals
func (h *DBusNotificationHandler) processSignals() {
	log.Printf("[DJI-DBus] Signal processor started\n")

	for {
		select {
		case <-h.stopChan:
			log.Printf("[DJI-DBus] Signal processor stopped\n")
			return
		case signal := <-h.signalChan:
			h.handleSignal(signal)
		}
	}
}

// handleSignal processes a single D-Bus signal
func (h *DBusNotificationHandler) handleSignal(signal *dbus.Signal) {
	// Log ALL signals for debugging
	pathStr := string(signal.Path)

	// Only log signals from our device or bluez
	if strings.Contains(pathStr, "8C_58_23_58_67_8C") || strings.Contains(signal.Name, "bluez") {
		log.Printf("[DJI-DBus] Signal: name=%s path=%s\n", signal.Name, pathStr)
	}

	if signal.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" {
		return
	}

	// Check if this is a GattCharacteristic1 property change
	if len(signal.Body) < 2 {
		log.Printf("[DJI-DBus] PropertiesChanged signal with insufficient body: %d items\n", len(signal.Body))
		return
	}

	iface, ok := signal.Body[0].(string)
	if !ok {
		log.Printf("[DJI-DBus] PropertiesChanged: interface is not string: %T\n", signal.Body[0])
		return
	}

	// Log all property changes from our device
	if strings.Contains(pathStr, "8C_58_23_58_67_8C") {
		log.Printf("[DJI-DBus] PropertiesChanged: interface=%s path=%s\n", iface, pathStr)
	}

	if iface != "org.bluez.GattCharacteristic1" {
		return
	}

	changedProps, ok := signal.Body[1].(map[string]dbus.Variant)
	if !ok {
		log.Printf("[DJI-DBus] PropertiesChanged: changed props is not map: %T\n", signal.Body[1])
		return
	}

	// Log what properties changed
	for propName := range changedProps {
		log.Printf("[DJI-DBus] Property changed: %s\n", propName)
	}

	// Check for Value property change
	valueVar, hasValue := changedProps["Value"]
	if !hasValue {
		log.Printf("[DJI-DBus] PropertiesChanged: no Value property in changes\n")
		return
	}

	// Log the raw variant for debugging
	log.Printf("[DJI-DBus] Value variant: type=%T, signature=%s\n", valueVar.Value(), valueVar.Signature())

	// Extract the value - D-Bus can send it in various forms
	var data []byte
	switch v := valueVar.Value().(type) {
	case []byte:
		data = v
		log.Printf("[DJI-DBus] Extracted as []byte: len=%d\n", len(data))
	case []interface{}:
		// D-Bus sometimes sends arrays as []interface{}
		log.Printf("[DJI-DBus] Got []interface{} with %d elements\n", len(v))
		data = make([]byte, len(v))
		for i, elem := range v {
			if b, ok := elem.(byte); ok {
				data[i] = b
			} else if b, ok := elem.(uint8); ok {
				data[i] = b
			} else {
				log.Printf("[DJI-DBus] Element %d is not byte: %T\n", i, elem)
			}
		}
	default:
		log.Printf("[DJI-DBus] Unknown Value type: %T, raw value: %v\n", valueVar.Value(), valueVar.Value())
		return
	}

	log.Printf("[DJI-DBus] Final data length from signal: %d\n", len(data))

	// Find which characteristic this is for
	signalPath := signal.Path

	// If data is empty, try reading the Value property directly
	// BlueZ sometimes signals PropertiesChanged but doesn't include the value in the signal
	if len(data) == 0 {
		log.Printf("[DJI-DBus] Signal payload empty, reading Value property directly from %s\n", signalPath)
		data = h.readValueFromPath(signalPath)
		if len(data) == 0 {
			log.Printf("[DJI-DBus] Direct read also returned empty data, skipping\n")
			return
		}
		log.Printf("[DJI-DBus] Got data from direct read: %d bytes\n", len(data))
	}

	log.Printf("[DJI-DBus] Data (%d bytes): %x\n", len(data), data)

	h.mu.RLock()
	for uuid, path := range h.charPaths {
		if path == signalPath {
			if callback, ok := h.callbacks[uuid]; ok {
				h.mu.RUnlock()
				log.Printf("[DJI-DBus] Invoking callback for %s\n", uuid)
				callback(data)
				return
			}
		}
	}
	h.mu.RUnlock()
	log.Printf("[DJI-DBus] No callback registered for path %s\n", signalPath)
}

// ReadValue reads the current value of a characteristic directly via D-Bus
func (h *DBusNotificationHandler) ReadValue(charUUID string) ([]byte, error) {
	h.mu.RLock()
	path, ok := h.charPaths[charUUID]
	h.mu.RUnlock()

	if !ok {
		return nil, nil
	}

	return h.readValueFromPath(path), nil
}

// readValueFromPath reads the Value property directly from a D-Bus path
func (h *DBusNotificationHandler) readValueFromPath(path dbus.ObjectPath) []byte {
	obj := h.conn.Object("org.bluez", path)

	// Try reading the Value property directly first (fastest for notifications)
	variant, err := obj.GetProperty("org.bluez.GattCharacteristic1.Value")
	if err == nil {
		data := h.extractBytes(variant)
		if len(data) > 0 {
			return data
		}
	} else {
		log.Printf("[DJI-DBus] GetProperty failed: %v\n", err)
	}

	// Fall back to ReadValue method
	var result []byte
	call := obj.Call("org.bluez.GattCharacteristic1.ReadValue", 0, map[string]dbus.Variant{})
	if call.Err == nil {
		call.Store(&result)
		if len(result) > 0 {
			return result
		}
	} else {
		log.Printf("[DJI-DBus] ReadValue call failed: %v\n", call.Err)
	}

	return nil
}

// extractBytes extracts a byte slice from a dbus.Variant
func (h *DBusNotificationHandler) extractBytes(variant dbus.Variant) []byte {
	switch v := variant.Value().(type) {
	case []byte:
		return v
	case []interface{}:
		data := make([]byte, len(v))
		for i, elem := range v {
			if b, ok := elem.(byte); ok {
				data[i] = b
			} else if b, ok := elem.(uint8); ok {
				data[i] = b
			}
		}
		return data
	}
	return nil
}
