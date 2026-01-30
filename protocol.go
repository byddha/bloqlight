// Package bloqlight provides a driver for the ROBOBLOQ USB Light Bar (VID 0x1A86, PID 0xFE07).
//
// Use Open to connect to a device, then call methods like SetLEDsFast to control LEDs.
// This file contains protocol constants and packet building functions.
package bloqlight

import "fmt"

const (
	VendorID  = 0x1A86
	ProductID = 0xFE07

	ReportSize = 65 // HID report: 1 byte ID + 64 bytes payload

	HeaderR = 0x52 // 'R'
	HeaderB = 0x42 // 'B'

	CmdReadDeviceInfo = 0x82
	CmdSetSectionLED  = 0x86
	CmdSyncScreen     = 0x80

	MaxLEDsPerPkt  = 11
	BytesPerLED    = 5
	PacketOverhead = 6
)

type RGB struct {
	R, G, B byte
}

func checksum(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum += b
	}
	return sum
}

// BuildPerLEDPacket creates an RB header packet to set individual LED colors.
// Each LED uses 5 bytes: [idx, R, G, B, idx]. Max 11 LEDs per packet.
// Indices are 1-based. Deprecated: prefer BuildSyncScreenPacket for speed.
func BuildPerLEDPacket(seq byte, leds []RGB, startIdx int) [ReportSize]byte {
	var pkt [ReportSize]byte

	numLEDs := min(len(leds), MaxLEDsPerPkt)

	pkt[0] = 0x00                          // HID report ID
	pkt[1] = HeaderR                       // 'R'
	pkt[2] = HeaderB                       // 'B'
	pkt[3] = byte(6 + numLEDs*BytesPerLED) // Length
	pkt[4] = seq                           // Sequence number
	pkt[5] = CmdSetSectionLED              // Command

	offset := 6
	for i := range numLEDs {
		idx := byte(startIdx + i)
		pkt[offset] = idx         // Start index
		pkt[offset+1] = leds[i].R // Red
		pkt[offset+2] = leds[i].G // Green
		pkt[offset+3] = leds[i].B // Blue
		pkt[offset+4] = idx       // End index (same as start for single LED)
		offset += BytesPerLED
	}

	pkt[offset] = checksum(pkt[1:offset])
	return pkt
}

// BuildRangeColorPacket creates an RB header packet to set a contiguous range
// of LEDs to a single color. Indices are 1-based and inclusive.
func BuildRangeColorPacket(seq byte, color RGB, startIdx, endIdx int) [ReportSize]byte {
	var pkt [ReportSize]byte

	pkt[0] = 0x00             // HID report ID
	pkt[1] = HeaderR          // 'R'
	pkt[2] = HeaderB          // 'B'
	pkt[3] = 0x10             // Length = 16
	pkt[4] = seq              // Sequence number
	pkt[5] = CmdSetSectionLED // 0x86
	pkt[6] = byte(startIdx)   // startIdx
	pkt[7] = color.R          // R
	pkt[8] = color.G          // G
	pkt[9] = color.B          // B
	pkt[10] = byte(endIdx)    // endIdx
	pkt[11] = byte(endIdx + 1)
	pkt[12] = 0x00 // padding
	pkt[13] = 0x00 // padding
	pkt[14] = 0x00 // padding
	pkt[15] = 0xFE // terminator

	pkt[16] = checksum(pkt[1:16])
	return pkt
}

// BuildTurnOffPacket creates a packet that sets all LEDs to black.
func BuildTurnOffPacket(seq byte, ledCount int) [ReportSize]byte {
	return BuildRangeColorPacket(seq, RGB{0, 0, 0}, 1, ledCount)
}

// BuildDeviceInfoPacket creates a device info query packet (command `0x82`).
func BuildDeviceInfoPacket(seq byte) [ReportSize]byte {
	var pkt [ReportSize]byte
	pkt[0] = 0x00    // HID report ID
	pkt[1] = HeaderR // 'R'
	pkt[2] = HeaderB // 'B'
	pkt[3] = 0x06    // Length
	pkt[4] = seq     // Sequence number
	pkt[5] = 0x82    // Command: ReadDeviceInfo

	pkt[6] = checksum(pkt[1:6])
	return pkt
}

// BuildSyncScreenPacket creates an SC header packet for fast LED updates.
// Returns a variable-length packet (caller must chunk into 64-byte HID writes).
// Indices are 0-based. Returns nil if colors is empty.
func BuildSyncScreenPacket(seq byte, colors []RGB) []byte {
	numLEDs := len(colors)
	if numLEDs == 0 {
		return nil
	}

	dataLen := 7 + numLEDs*5
	pkt := make([]byte, dataLen)

	pkt[0] = 0x53                 // 'S'
	pkt[1] = 0x43                 // 'C'
	pkt[2] = byte(dataLen >> 8)   // Length high byte (big-endian)
	pkt[3] = byte(dataLen & 0xFF) // Length low byte
	pkt[4] = seq                  // Sequence
	pkt[5] = CmdSyncScreen        // 0x80

	offset := 6
	for i := range numLEDs {
		idx := byte(i)
		pkt[offset] = idx
		pkt[offset+1] = colors[i].R
		pkt[offset+2] = colors[i].G
		pkt[offset+3] = colors[i].B
		pkt[offset+4] = idx
		offset += 5
	}

	pkt[offset] = checksum(pkt[:offset])
	return pkt
}

// ParseDeviceInfoResponse extracts device info from a `0x82` response.
// Returns firmware version (e.g. "5.1.34"), LED count, and MAC address.
func ParseDeviceInfoResponse(resp []byte) (firmware string, ledCount int, mac string, err error) {
	if len(resp) < 18 {
		return "", 0, "", fmt.Errorf("invalid response length: %d", len(resp))
	}

	ledCount = int(resp[11])
	if ledCount == 0 {
		return "", 0, "", fmt.Errorf("device reported 0 LEDs")
	}

	firmware = fmt.Sprintf("%d.%d.%d", resp[6], resp[7], resp[8])
	mac = fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		resp[12], resp[13], resp[14], resp[15], resp[16], resp[17])

	return firmware, ledCount, mac, nil
}
