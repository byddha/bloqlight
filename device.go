// Device management and HID I/O for the ROBOBLOQ USB Light Bar.
// See protocol.go for packet format details.
package bloqlight

import (
	"errors"
	"fmt"
	"sync"

	"github.com/karalabe/hid"
)

var (
	ErrDeviceNotFound = errors.New("light bar device not found")
	ErrWriteFailed    = errors.New("failed to write to device")
)

// Device represents a connected ROBOBLOQ USB Light Bar.
// Create with Open, close with Close. All methods are thread-safe.
type Device struct {
	dev      *hid.Device
	seq      byte
	mu       sync.Mutex
	LEDCount int    // Number of LEDs (queried from device)
	Firmware string // Firmware version, e.g. "5.1.34"
	MAC      string // Bluetooth MAC address
}

// Open connects to the first available light bar and queries its info.
func Open() (*Device, error) {
	devices := hid.Enumerate(VendorID, ProductID)
	if len(devices) == 0 {
		return nil, ErrDeviceNotFound
	}

	dev, err := devices[0].Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceNotFound, err)
	}

	d := &Device{dev: dev}
	if err := d.queryDeviceInfo(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("failed to query device info: %w", err)
	}
	return d, nil
}

// ListDevices prints info about all connected light bars to stdout.
func ListDevices() {
	devices := hid.Enumerate(VendorID, ProductID)
	fmt.Printf("Found %d HID device(s):\n", len(devices))
	for i, info := range devices {
		fmt.Printf("  [%d] Path: %s\n", i, info.Path)
		fmt.Printf("      Interface: %d\n", info.Interface)
		fmt.Printf("      Product: %s\n", info.Product)
		fmt.Printf("      Manufacturer: %s\n", info.Manufacturer)
		fmt.Printf("      UsagePage: 0x%04X, Usage: 0x%04X\n", info.UsagePage, info.Usage)
	}
}

// OpenInterface connects to a specific USB interface number.
func OpenInterface(interfaceNum int) (*Device, error) {
	devices := hid.Enumerate(VendorID, ProductID)
	for _, info := range devices {
		if info.Interface == interfaceNum {
			dev, err := info.Open()
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrDeviceNotFound, err)
			}
			return &Device{dev: dev}, nil
		}
	}
	return nil, fmt.Errorf("%w: interface %d not found", ErrDeviceNotFound, interfaceNum)
}

// Close releases the HID connection.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.dev != nil {
		err := d.dev.Close()
		d.dev = nil
		return err
	}
	return nil
}

func (d *Device) nextSeq() byte {
	seq := d.seq
	d.seq++
	return seq
}

func (d *Device) write(pkt []byte) error {
	if d.dev == nil {
		return ErrDeviceNotFound
	}
	_, err := d.dev.Write(pkt)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWriteFailed, err)
	}
	return nil
}

func (d *Device) queryDeviceInfo() error {
	resp, err := d.ReadDeviceInfo()
	if err != nil {
		return err
	}
	d.Firmware, d.LEDCount, d.MAC, err = ParseDeviceInfoResponse(resp)
	return err
}

// ReadDeviceInfo queries and returns the raw device info response.
func (d *Device) ReadDeviceInfo() ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	pkt := BuildDeviceInfoPacket(d.nextSeq())
	if err := d.write(pkt[:]); err != nil {
		return nil, err
	}

	resp := make([]byte, ReportSize)
	n, err := d.dev.Read(resp)
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}

	return resp[:n], nil
}

// SetLEDs sets LED colors using the slow RB protocol (~7 FPS).
// Deprecated: use SetLEDsFast for better performance.
func (d *Device) SetLEDs(colors []RGB) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(colors) == 0 {
		return nil
	}

	for i := 0; i < len(colors); i += MaxLEDsPerPkt {
		end := min(i+MaxLEDsPerPkt, len(colors))

		chunk := colors[i:end]
		startIdx := i + 1 // 1-based index

		pkt := BuildPerLEDPacket(d.nextSeq(), chunk, startIdx)
		if err := d.write(pkt[:]); err != nil {
			return err
		}
	}

	return nil
}

// SetLEDsFast sets LED colors using the fast SC protocol (~100 FPS).
func (d *Device) SetLEDsFast(colors []RGB) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(colors) == 0 {
		return nil
	}

	pkt := BuildSyncScreenPacket(d.nextSeq(), colors[:min(len(colors), d.LEDCount)])
	if pkt == nil {
		return nil
	}

	const maxPayload = 64
	for i := 0; i < len(pkt); i += maxPayload {
		end := min(i+maxPayload, len(pkt))
		chunk := make([]byte, 1+end-i)
		chunk[0] = 0x00 // Report ID
		copy(chunk[1:], pkt[i:end])

		if err := d.write(chunk); err != nil {
			return err
		}
	}

	return nil
}

// TurnOff sets all LEDs to black.
func (d *Device) TurnOff() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	pkt := BuildTurnOffPacket(d.nextSeq(), d.LEDCount)
	return d.write(pkt[:])
}

// SetAllColor sets all LEDs to a single color.
func (d *Device) SetAllColor(color RGB) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	pkt := BuildRangeColorPacket(d.nextSeq(), color, 1, d.LEDCount)
	return d.write(pkt[:])
}
