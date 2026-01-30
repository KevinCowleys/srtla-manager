package dji

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// crc8 calculates CRC8 with polynomial 0x31, init 0xEE, RefIn=true, RefOut=true
// Using the reflected algorithm: process LSB first with reflected polynomial 0x8C
func crc8(data []byte) uint8 {
	crc := uint8(0xEE)
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if (crc & 1) != 0 {
				crc = (crc >> 1) ^ 0x8C // 0x8C is 0x31 bit-reversed
			} else {
				crc = crc >> 1
			}
		}
	}
	return crc
}

// crc16 calculates CRC16 with polynomial 0x1021, init 0x496C, RefIn=true, RefOut=true
func crc16(data []byte) uint16 {
	crc := uint16(0x496C)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if (crc & 1) != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc = crc >> 1
			}
		}
	}
	return crc
}

// StreamResolution represents video resolution setting
type StreamResolution string

const (
	Resolution480p  StreamResolution = "480p"
	Resolution720p  StreamResolution = "720p"
	Resolution1080p StreamResolution = "1080p"
)

// ImageStabilization represents video stabilization mode
type ImageStabilization string

const (
	StabilizationOff            ImageStabilization = "off"
	StabilizationRockSteady     ImageStabilization = "rocksteady"
	StabilizationHorizonSteady  ImageStabilization = "horizonsteady"
	StabilizationRockSteadyPlus ImageStabilization = "rocksteadyplus"
	StabilizationHorizonBalance ImageStabilization = "horizonbalance"
)

// StreamConfig contains camera streaming configuration
type StreamConfig struct {
	WiFiSSID      string
	WiFiPassword  string
	RTMPURL       string
	Resolution    StreamResolution
	FPS           int
	BitrateKbps   uint16
	Stabilization ImageStabilization
	IsOA5Plus     bool // true for OA5Pro and OA6
}

// resolutionToByte converts resolution to DJI protocol byte
func resolutionToByte(res StreamResolution) uint8 {
	switch res {
	case Resolution480p:
		return 0x47
	case Resolution720p:
		return 0x04
	case Resolution1080p:
		return 0x0A
	default:
		return 0x0A // Default to 1080p
	}
}

// fpsToByte converts FPS to DJI protocol byte
func fpsToByte(fps int) uint8 {
	switch fps {
	case 25:
		return 2
	case 30:
		return 3
	default:
		return 3 // Default to 30fps
	}
}

// stabilizationToByte converts stabilization setting to DJI protocol byte
// Matches Moblin's DjiConfigureMessagePayload values
func stabilizationToByte(stab ImageStabilization) uint8 {
	switch stab {
	case StabilizationOff:
		return 0
	case StabilizationRockSteady:
		return 1
	case StabilizationHorizonSteady:
		return 2
	case StabilizationRockSteadyPlus:
		return 3
	case StabilizationHorizonBalance:
		return 4
	default:
		return 0
	}
}

// EncodeWiFiConfig creates the BLE message payload for WiFi setup
// Matches Moblin's DjiSetupWifiMessagePayload: ssid_len + ssid + pwd_len + pwd
func EncodeWiFiConfig(ssid, password string) []byte {
	var buf bytes.Buffer

	// Trim whitespace from SSID and password
	ssid = strings.TrimSpace(ssid)
	password = strings.TrimSpace(password)

	// SSID length and value (max 32 chars)
	if len(ssid) > 32 {
		ssid = ssid[:32]
	}
	buf.WriteByte(byte(len(ssid)))
	buf.WriteString(ssid)

	// Password length and value (max 64 chars)
	if len(password) > 64 {
		password = password[:64]
	}
	buf.WriteByte(byte(len(password)))
	buf.WriteString(password)

	fmt.Printf("[DJI] WiFi payload: SSID=%q (%d bytes), Password=%q (%d bytes)\n",
		ssid, len(ssid), password, len(password))

	return buf.Bytes()
}

// EncodeStreamConfig creates the BLE message payload for RTMP streaming config
// Matches Moblin's DjiStartStreamingMessagePayload format exactly
func EncodeStreamConfig(config StreamConfig) []byte {
	var buf bytes.Buffer

	// payload1: 0x00
	buf.WriteByte(0x00)

	// byte1: model identifier (0x2A for OA5+, 0x2E for others)
	if config.IsOA5Plus {
		buf.WriteByte(0x2A)
	} else {
		buf.WriteByte(0x2E)
	}

	// payload2: 0x00
	buf.WriteByte(0x00)

	// Resolution byte
	buf.WriteByte(resolutionToByte(config.Resolution))

	// Bitrate (little-endian 16-bit)
	bitrateBuf := make([]byte, 2)
	binary.LittleEndian.PutUint16(bitrateBuf, config.BitrateKbps)
	buf.Write(bitrateBuf)

	// payload3: 0x02, 0x00
	buf.Write([]byte{0x02, 0x00})

	// FPS byte
	buf.WriteByte(fpsToByte(config.FPS))

	// payload4: 0x00, 0x00, 0x00
	buf.Write([]byte{0x00, 0x00, 0x00})

	// RTMP URL encoding (length, 0x00, url)
	rtmpBytes := encodeRTMPURL(config.RTMPURL)
	buf.Write(rtmpBytes)

	fmt.Printf("[DJI] Stream payload: model=0x%02X, res=0x%02X, bitrate=%d, fps=%d, url=%s\n",
		buf.Bytes()[1], resolutionToByte(config.Resolution), config.BitrateKbps, config.FPS, config.RTMPURL)

	return buf.Bytes()
}

// encodeRTMPURL encodes the RTMP URL according to DJI protocol
// Format: length (1 byte), 0x00, url data (matches djiPackUrl in Moblin)
func encodeRTMPURL(url string) []byte {
	if len(url) > 256 {
		url = url[:256]
	}

	var buf bytes.Buffer

	// Length prefix
	buf.WriteByte(byte(len(url)))

	// Extra 0x00 byte (required by DJI protocol)
	buf.WriteByte(0x00)

	// URL data
	buf.WriteString(url)

	return buf.Bytes()
}

// EncodeStabilizationConfig creates the BLE message payload for stabilization settings
// Matches Moblin's DjiConfigureMessagePayload format exactly
func EncodeStabilizationConfig(stab ImageStabilization, isOA5Plus bool) []byte {
	var buf bytes.Buffer

	// payload1: 0x01, 0x01
	buf.Write([]byte{0x01, 0x01})

	// byte1: 0x1A for OA5+, 0x08 for OA4/others
	if isOA5Plus {
		buf.WriteByte(0x1A)
	} else {
		buf.WriteByte(0x08)
	}

	// payload2: 0x00, 0x01
	buf.Write([]byte{0x00, 0x01})

	// Stabilization setting
	buf.WriteByte(stabilizationToByte(stab))

	fmt.Printf("[DJI] Configure payload: byte1=0x%02X, stabilization=%d\n",
		buf.Bytes()[2], stabilizationToByte(stab))

	return buf.Bytes()
}

// DjiMessage represents a complete DJI Bluetooth message
type DjiMessage struct {
	Target        uint16 // Moblin: 0x0702 for WiFi, 0x0102 for Configure, 0x0802 for Stream
	TransactionID uint16 // Moblin: 0x8C19 for WiFi, 0x8C2D for Configure, 0x8C2C for Stream
	MessageType   uint32 // Moblin: 0x470740 for WiFi, 0x8E0240 for Configure, 0x780840 for Stream
	Payload       []byte
}

// Encode creates the complete BLE message bytes using DJI protocol format
// Format: 0x55, length, 0x04, CRC8(header), target(2), id(2), type(3), payload, CRC16(all)
func (m *DjiMessage) Encode() []byte {
	var buf bytes.Buffer

	// Start byte
	buf.WriteByte(0x55)

	// Total message length = 13 + payload length
	// (0x55 + len + 0x04 + crc8 + target(2) + id(2) + type(3) + payload + crc16(2) = 13 + payload)
	totalLength := 13 + len(m.Payload)
	buf.WriteByte(byte(totalLength & 0xFF))

	// Version byte
	buf.WriteByte(0x04)

	// CRC8 of first 3 bytes (0x55, length, 0x04)
	headerCrc := crc8(buf.Bytes())
	buf.WriteByte(headerCrc)

	// Target (uint16, little-endian)
	buf.WriteByte(byte(m.Target & 0xFF))
	buf.WriteByte(byte((m.Target >> 8) & 0xFF))

	// TransactionID (uint16, little-endian)
	buf.WriteByte(byte(m.TransactionID & 0xFF))
	buf.WriteByte(byte((m.TransactionID >> 8) & 0xFF))

	// MessageType (uint24, little-endian)
	buf.WriteByte(byte(m.MessageType & 0xFF))
	buf.WriteByte(byte((m.MessageType >> 8) & 0xFF))
	buf.WriteByte(byte((m.MessageType >> 16) & 0xFF))

	// Payload
	buf.Write(m.Payload)

	// CRC16 of everything before CRC16 (all bytes so far)
	payloadCrc := crc16(buf.Bytes())
	buf.WriteByte(byte(payloadCrc & 0xFF))
	buf.WriteByte(byte((payloadCrc >> 8) & 0xFF))

	return buf.Bytes()
}

// CreateStartStreamMessage creates a complete DJI start streaming message
func CreateStartStreamMessage(config StreamConfig) (*DjiMessage, error) {
	if config.RTMPURL == "" {
		return nil, fmt.Errorf("RTMP URL cannot be empty")
	}
	if config.WiFiSSID == "" {
		return nil, fmt.Errorf("WiFi SSID cannot be empty")
	}

	payload := EncodeStreamConfig(config)

	return &DjiMessage{
		Target:        0x0802,   // Moblin startStreamingTarget
		TransactionID: 0x8C2C,   // Moblin startStreamingTransactionId
		MessageType:   0x780840, // Moblin startStreamingType
		Payload:       payload,
	}, nil
}

// CreateWiFiConfigMessage creates a complete DJI WiFi config message
func CreateWiFiConfigMessage(ssid, password string) (*DjiMessage, error) {
	if ssid == "" {
		return nil, fmt.Errorf("WiFi SSID cannot be empty")
	}

	payload := EncodeWiFiConfig(ssid, password)

	return &DjiMessage{
		Target:        0x0702,   // Moblin setupWifiTarget
		TransactionID: 0x8C19,   // Moblin setupWifiTransactionId
		MessageType:   0x470740, // Moblin setupWifiType
		Payload:       payload,
	}, nil
}

// CreateConfigureMessage creates the DJI OA4 configure message (required after WiFi setup)
func CreateConfigureMessage(stab ImageStabilization, isOA5Plus bool) (*DjiMessage, error) {
	payload := EncodeStabilizationConfig(stab, isOA5Plus)

	return &DjiMessage{
		Target:        0x0102,   // Moblin configureTarget
		TransactionID: 0x8C2D,   // Moblin configureTransactionId
		MessageType:   0x8E0240, // Moblin configureType
		Payload:       payload,
	}, nil
}

// Pair message constants from Moblin
var pairPayloadPrefix = []byte{
	0x20, 0x32, 0x38, 0x34, 0x61, 0x65, 0x35, 0x62,
	0x38, 0x64, 0x37, 0x36, 0x62, 0x33, 0x33, 0x37,
	0x35, 0x61, 0x30, 0x34, 0x61, 0x36, 0x34, 0x31,
	0x37, 0x61, 0x64, 0x37, 0x31, 0x62, 0x65, 0x61,
	0x33,
}

const pairPinCode = "mbln"

// CreatePairMessage creates the DJI pairing message
func CreatePairMessage() *DjiMessage {
	var buf bytes.Buffer
	buf.Write(pairPayloadPrefix)
	// Pack the pin code: length byte + string
	buf.WriteByte(byte(len(pairPinCode)))
	buf.WriteString(pairPinCode)

	return &DjiMessage{
		Target:        0x0702,   // Moblin pairTarget
		TransactionID: 0x8092,   // Moblin pairTransactionId
		MessageType:   0x450740, // Moblin pairType
		Payload:       buf.Bytes(),
	}
}

// CreateStopStreamingMessage creates the DJI stop streaming message
func CreateStopStreamingMessage() *DjiMessage {
	// Payload from Moblin: [0x01, 0x01, 0x1A, 0x00, 0x01, 0x02]
	payload := []byte{0x01, 0x01, 0x1A, 0x00, 0x01, 0x02}

	return &DjiMessage{
		Target:        0x0802,   // Moblin stopStreamingTarget
		TransactionID: 0xEAC8,   // Moblin stopStreamingTransactionId
		MessageType:   0x8E0240, // Moblin stopStreamingType
		Payload:       payload,
	}
}

// CreatePreparingToLivestreamMessage creates the DJI preparing to livestream message
func CreatePreparingToLivestreamMessage() *DjiMessage {
	// Payload from Moblin: [0x1A]
	payload := []byte{0x1A}

	return &DjiMessage{
		Target:        0x0802,   // Moblin preparingToLivestreamTarget
		TransactionID: 0x8C12,   // Moblin preparingToLivestreamTransactionId
		MessageType:   0xE10240, // Moblin preparingToLivestreamType
		Payload:       payload,
	}
}

// CreateConfirmStartStreamingMessage creates the confirm message for OA5P/OA6
// This is required after sending start streaming on these models
func CreateConfirmStartStreamingMessage() *DjiMessage {
	// Same as stop streaming but last byte is 0x01 instead of 0x02
	payload := []byte{0x01, 0x01, 0x1A, 0x00, 0x01, 0x01}

	return &DjiMessage{
		Target:        0x0802,   // Same as stopStreamingTarget
		TransactionID: 0xEAC8,   // Same as stopStreamingTransactionId
		MessageType:   0x8E0240, // Same as stopStreamingType
		Payload:       payload,
	}
}

// Message transaction IDs for response matching
const (
	PairTransactionID                  uint16 = 0x8092
	StopStreamingTransactionID         uint16 = 0xEAC8
	PreparingToLivestreamTransactionID uint16 = 0x8C12
	SetupWiFiTransactionID             uint16 = 0x8C19
	StartStreamingTransactionID        uint16 = 0x8C2C
	ConfigureTransactionID             uint16 = 0x8C2D
)

// Message types for response identification
const (
	BatteryStatusType uint32 = 0x020D00
)

// DecodeMessage parses a raw BLE response into a DjiMessage
// Format: 0x55, length, 0x04, CRC8, target(2), id(2), type(3), payload, CRC16(2)
func DecodeMessage(data []byte) (*DjiMessage, error) {
	if len(data) < 13 {
		return nil, fmt.Errorf("message too short: %d bytes", len(data))
	}

	// Check start byte
	if data[0] != 0x55 {
		return nil, fmt.Errorf("invalid start byte: 0x%02X (expected 0x55)", data[0])
	}

	// Check length
	length := int(data[1])
	if len(data) < length {
		return nil, fmt.Errorf("message truncated: got %d bytes, expected %d", len(data), length)
	}

	// Check version
	if data[2] != 0x04 {
		return nil, fmt.Errorf("invalid version byte: 0x%02X (expected 0x04)", data[2])
	}

	// Verify header CRC8 (bytes 0-2)
	headerCrc := crc8(data[0:3])
	if data[3] != headerCrc {
		return nil, fmt.Errorf("header CRC mismatch: got 0x%02X, calculated 0x%02X", data[3], headerCrc)
	}

	// Parse fields
	target := uint16(data[4]) | (uint16(data[5]) << 8)
	transactionID := uint16(data[6]) | (uint16(data[7]) << 8)
	messageType := uint32(data[8]) | (uint32(data[9]) << 8) | (uint32(data[10]) << 16)

	// Extract payload (everything between header and CRC16)
	payloadEnd := length - 2
	payload := make([]byte, payloadEnd-11)
	copy(payload, data[11:payloadEnd])

	// Verify CRC16 (all bytes before CRC16)
	calculatedCrc := crc16(data[0:payloadEnd])
	receivedCrc := uint16(data[payloadEnd]) | (uint16(data[payloadEnd+1]) << 8)
	if calculatedCrc != receivedCrc {
		return nil, fmt.Errorf("payload CRC mismatch: got 0x%04X, calculated 0x%04X", receivedCrc, calculatedCrc)
	}

	return &DjiMessage{
		Target:        target,
		TransactionID: transactionID,
		MessageType:   messageType,
		Payload:       payload,
	}, nil
}

// IsWiFiSetupSuccess checks if the response indicates WiFi setup was successful
// Moblin expects payload [0x00, 0x00] for success
func (m *DjiMessage) IsWiFiSetupSuccess() bool {
	return len(m.Payload) >= 2 && m.Payload[0] == 0x00 && m.Payload[1] == 0x00
}

// IsPairAlreadyPaired checks if the response indicates device is already paired
// Moblin expects payload [0x00, 0x01] for already paired
func (m *DjiMessage) IsPairAlreadyPaired() bool {
	return len(m.Payload) >= 2 && m.Payload[0] == 0x00 && m.Payload[1] == 0x01
}

// GetBatteryPercentage extracts battery percentage from a battery status message
// Returns -1 if not a battery message or payload too short
func (m *DjiMessage) GetBatteryPercentage() int {
	if m.MessageType != BatteryStatusType {
		return -1
	}
	if len(m.Payload) < 21 {
		return -1
	}
	return int(m.Payload[20])
}
