package dji

import (
	"testing"
)

// Test CRC8 with known DJI protocol values
func TestCRC8(t *testing.T) {
	// Test case: header bytes 0x55, 0x1a (length 26), 0x04
	// This is a typical DJI message header
	header := []byte{0x55, 0x1a, 0x04}
	crc := crc8(header)
	t.Logf("CRC8 of %x = 0x%02x", header, crc)

	// The CRC should be deterministic
	crc2 := crc8(header)
	if crc != crc2 {
		t.Errorf("CRC8 not deterministic: %02x != %02x", crc, crc2)
	}
}

// Test CRC16 implementation
func TestCRC16(t *testing.T) {
	// Test with a simple data set
	data := []byte{0x55, 0x0d, 0x04, 0x33, 0x02, 0x07, 0x19, 0x8c, 0x40, 0x07, 0x47}
	crc := crc16(data)
	t.Logf("CRC16 of %x = 0x%04x", data, crc)
}

// Test WiFi config message encoding
func TestEncodeWiFiConfig(t *testing.T) {
	payload := EncodeWiFiConfig("TestSSID", "TestPass")
	t.Logf("WiFi payload (%d bytes): %x", len(payload), payload)

	// Expected format: ssid_len + ssid + pwd_len + pwd
	// 8 + "TestSSID" + 8 + "TestPass"
	expectedLen := 1 + 8 + 1 + 8
	if len(payload) != expectedLen {
		t.Errorf("Expected payload length %d, got %d", expectedLen, len(payload))
	}

	// First byte should be SSID length
	if payload[0] != 8 {
		t.Errorf("Expected SSID length 8, got %d", payload[0])
	}

	// After SSID, should be password length
	if payload[9] != 8 {
		t.Errorf("Expected password length 8, got %d", payload[9])
	}
}

// Test stream config message encoding
func TestEncodeStreamConfig(t *testing.T) {
	config := StreamConfig{
		WiFiSSID:      "TestSSID",
		WiFiPassword:  "TestPass",
		RTMPURL:       "rtmp://192.168.1.1/live/test",
		Resolution:    Resolution1080p,
		FPS:           30,
		BitrateKbps:   6000,
		Stabilization: StabilizationOff,
		IsOA5Plus:     false,
	}

	payload := EncodeStreamConfig(config)
	t.Logf("Stream payload (%d bytes): %x", len(payload), payload)

	// Check model byte at position 1
	if payload[1] != 0x2E {
		t.Errorf("Expected model byte 0x2E for non-OA5+, got 0x%02x", payload[1])
	}

	// Check resolution byte at position 3
	if payload[3] != 0x0A {
		t.Errorf("Expected resolution byte 0x0A for 1080p, got 0x%02x", payload[3])
	}
}

// Test complete message encoding
func TestMessageEncode(t *testing.T) {
	// Create a simple WiFi config message
	msg, err := CreateWiFiConfigMessage("TestSSID", "TestPass")
	if err != nil {
		t.Fatalf("Failed to create WiFi message: %v", err)
	}

	encoded := msg.Encode()
	t.Logf("WiFi message (%d bytes): %x", len(encoded), encoded)

	// Check start byte
	if encoded[0] != 0x55 {
		t.Errorf("Expected start byte 0x55, got 0x%02x", encoded[0])
	}

	// Check version byte
	if encoded[2] != 0x04 {
		t.Errorf("Expected version byte 0x04, got 0x%02x", encoded[2])
	}

	// Total length should be 13 + payload length
	payloadLen := len(msg.Payload)
	expectedLen := 13 + payloadLen
	if len(encoded) != expectedLen {
		t.Errorf("Expected encoded length %d, got %d", expectedLen, len(encoded))
	}

	// Length byte should match
	if encoded[1] != byte(expectedLen) {
		t.Errorf("Expected length byte %d, got %d", expectedLen, encoded[1])
	}
}

// Test all message creation functions
func TestCreateMessages(t *testing.T) {
	// Test pair message
	pairMsg := CreatePairMessage()
	t.Logf("Pair message: target=0x%04x, id=0x%04x, type=0x%06x, payload=%x",
		pairMsg.Target, pairMsg.TransactionID, pairMsg.MessageType, pairMsg.Payload)

	// Test stop streaming message
	stopMsg := CreateStopStreamingMessage()
	t.Logf("Stop message: target=0x%04x, id=0x%04x, type=0x%06x, payload=%x",
		stopMsg.Target, stopMsg.TransactionID, stopMsg.MessageType, stopMsg.Payload)

	// Test prepare message
	prepMsg := CreatePreparingToLivestreamMessage()
	t.Logf("Prepare message: target=0x%04x, id=0x%04x, type=0x%06x, payload=%x",
		prepMsg.Target, prepMsg.TransactionID, prepMsg.MessageType, prepMsg.Payload)

	// Test confirm message
	confirmMsg := CreateConfirmStartStreamingMessage()
	t.Logf("Confirm message: target=0x%04x, id=0x%04x, type=0x%06x, payload=%x",
		confirmMsg.Target, confirmMsg.TransactionID, confirmMsg.MessageType, confirmMsg.Payload)
}

// Test message decode (round-trip)
func TestMessageDecode(t *testing.T) {
	// Create a message, encode it, then decode it
	original, _ := CreateWiFiConfigMessage("TestSSID", "TestPass")
	encoded := original.Encode()

	t.Logf("Encoded message (%d bytes): %x", len(encoded), encoded)

	decoded, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatalf("Failed to decode message: %v", err)
	}

	// Verify fields match
	if decoded.Target != original.Target {
		t.Errorf("Target mismatch: got 0x%04x, expected 0x%04x", decoded.Target, original.Target)
	}
	if decoded.TransactionID != original.TransactionID {
		t.Errorf("TransactionID mismatch: got 0x%04x, expected 0x%04x", decoded.TransactionID, original.TransactionID)
	}
	if decoded.MessageType != original.MessageType {
		t.Errorf("MessageType mismatch: got 0x%06x, expected 0x%06x", decoded.MessageType, original.MessageType)
	}

	t.Logf("Decoded message - Target: 0x%04x, ID: 0x%04x, Type: 0x%06x, Payload: %x",
		decoded.Target, decoded.TransactionID, decoded.MessageType, decoded.Payload)
}

// Test WiFi success response detection
func TestWiFiSetupSuccess(t *testing.T) {
	// Create a mock WiFi success response
	msg := &DjiMessage{
		Target:        0x0702,
		TransactionID: SetupWiFiTransactionID,
		MessageType:   0x470740,
		Payload:       []byte{0x00, 0x00},
	}

	if !msg.IsWiFiSetupSuccess() {
		t.Error("Expected WiFi setup to be detected as successful")
	}

	// Test failure case
	msg.Payload = []byte{0x01, 0x00}
	if msg.IsWiFiSetupSuccess() {
		t.Error("Expected WiFi setup to be detected as failed")
	}
}

// Test pair already paired detection
func TestPairAlreadyPaired(t *testing.T) {
	msg := &DjiMessage{
		Target:        0x0702,
		TransactionID: PairTransactionID,
		MessageType:   0x450740,
		Payload:       []byte{0x00, 0x01},
	}

	if !msg.IsPairAlreadyPaired() {
		t.Error("Expected device to be detected as already paired")
	}
}

// Test battery percentage extraction
func TestBatteryPercentage(t *testing.T) {
	// Create a mock battery status message
	payload := make([]byte, 21)
	payload[20] = 75 // 75% battery

	msg := &DjiMessage{
		Target:        0x0000,
		TransactionID: 0x0000,
		MessageType:   BatteryStatusType,
		Payload:       payload,
	}

	battery := msg.GetBatteryPercentage()
	if battery != 75 {
		t.Errorf("Expected battery 75%%, got %d%%", battery)
	}

	// Test non-battery message
	msg.MessageType = 0x123456
	if msg.GetBatteryPercentage() != -1 {
		t.Error("Expected -1 for non-battery message")
	}
}
