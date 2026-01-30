package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bloqlight"
)

const controlPort = 5569

type server struct {
	device *bloqlight.Device
	port   int
	debug  bool

	paused        atomic.Bool
	brightness    atomic.Uint32 // 0-100, default 100
	manualColor   bloqlight.RGB
	restoreColor  *bloqlight.RGB // Last solid color from HyperHDR (nil if not solid)
	manualColorMu sync.Mutex

	latestColors   []bloqlight.RGB
	latestSolid    bool // True if latestColors is all same color, used for optimization
	latestRecvTime time.Time
	colorsMu       sync.Mutex

	recvCount atomic.Uint64
	sendCount atomic.Uint64
	dropCount atomic.Uint64

	totalLatencyUs atomic.Uint64
	totalDeviceUs  atomic.Uint64
	totalQueueUs   atomic.Uint64
	latencyCount   atomic.Uint64
	maxLatencyUs   atomic.Uint64
	maxDeviceUs    atomic.Uint64
}

func newServer(device *bloqlight.Device, port int, debug bool) *server {
	s := &server{
		device: device,
		port:   port,
		debug:  debug,
	}
	s.brightness.Store(100)
	return s
}

// run starts the UDP server and TCP control listener. Blocks until an error occurs.
//
// HyperHDR Configuration:
//   - LED Hardware -> Type: udpraw
//   - IP: 127.0.0.1 (or host IP)
//   - Port: 5568 (default)
func (s *server) run() error {
	addr := fmt.Sprintf(":%d", s.port)
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP port %d: %w", s.port, err)
	}
	defer conn.Close()

	log.Printf("UDP server listening on port %d", s.port)
	log.Printf("TCP control listening on port %d", controlPort)
	log.Printf("Configure HyperHDR: LED Hardware -> udpraw -> Port: %d", s.port)

	go s.runControlListener()

	sendCh := make(chan struct{}, 1)
	go s.senderLoop(sendCh)

	if s.debug {
		go s.statsLoop()
	}

	buf := make([]byte, 1024)
	for {
		n, addr, err := conn.ReadFrom(buf)
		recvTime := time.Now()
		if err != nil {
			return fmt.Errorf("UDP read error: %w", err)
		}

		colors := parseUDPColors(buf[:n], s.device.LEDCount)
		if len(colors) == 0 {
			continue
		}

		solid := true
		first := colors[0]
		for _, c := range colors[1:] {
			if c != first {
				solid = false
				break
			}
		}

		s.manualColorMu.Lock()
		if solid {
			s.restoreColor = &first
		} else {
			s.restoreColor = nil
		}
		s.manualColorMu.Unlock()

		if s.paused.Load() {
			continue
		}

		s.recvCount.Add(1)

		if s.debug {
			log.Printf("[RECV] %d bytes from %v -> %d LEDs, first=RGB(%d,%d,%d), solid=%v",
				n, addr, len(colors), colors[0].R, colors[0].G, colors[0].B, solid)
		}

		s.colorsMu.Lock()
		s.latestColors = colors
		s.latestSolid = solid
		s.latestRecvTime = recvTime
		s.colorsMu.Unlock()

		select {
		case sendCh <- struct{}{}:
		default:
			s.dropCount.Add(1)
			if s.debug {
				log.Printf("[DROP] Sender busy, dropping frame")
			}
		}
	}
}

func (s *server) senderLoop(ch <-chan struct{}) {
	for range ch {
		sendStart := time.Now()

		s.colorsMu.Lock()
		colors := s.latestColors
		solid := s.latestSolid
		recvTime := s.latestRecvTime
		s.latestColors = nil
		s.colorsMu.Unlock()

		if colors == nil {
			continue
		}

		queueTime := sendStart.Sub(recvTime)

		deviceStart := time.Now()
		var err error
		if solid {
			err = s.device.SetAllColor(s.applyBrightness([]bloqlight.RGB{colors[0]})[0])
		} else {
			err = s.device.SetLEDsFast(s.applyBrightness(colors))
		}
		if err != nil {
			log.Printf("[ERROR] Device write failed: %v", err)
			continue
		}
		deviceTime := time.Since(deviceStart)
		totalTime := time.Since(recvTime)

		s.sendCount.Add(1)

		totalUs := uint64(totalTime.Microseconds())
		deviceUs := uint64(deviceTime.Microseconds())
		queueUs := uint64(queueTime.Microseconds())

		s.totalLatencyUs.Add(totalUs)
		s.totalDeviceUs.Add(deviceUs)
		s.totalQueueUs.Add(queueUs)
		s.latencyCount.Add(1)

		if totalUs > s.maxLatencyUs.Load() {
			s.maxLatencyUs.Store(totalUs)
		}
		if deviceUs > s.maxDeviceUs.Load() {
			s.maxDeviceUs.Store(deviceUs)
		}

		if s.debug {
			log.Printf("[SEND] %d LEDs | queue=%v device=%v total=%v",
				len(colors), queueTime.Round(time.Microsecond),
				deviceTime.Round(time.Microsecond), totalTime.Round(time.Microsecond))
		}
	}
}

// parseUDPColors parses raw RGB bytes from HyperHDR udpraw format.
// Format: [R, G, B, R, G, B, ...] - 3 bytes per LED, no header.
func parseUDPColors(data []byte, maxLEDs int) []bloqlight.RGB {
	if len(data) < 3 {
		return nil
	}

	numLEDs := min(len(data)/3, maxLEDs)

	colors := make([]bloqlight.RGB, numLEDs)
	for i := range numLEDs {
		offset := i * 3
		colors[i] = bloqlight.RGB{
			R: data[offset],
			G: data[offset+1],
			B: data[offset+2],
		}
	}

	return colors
}

func (s *server) statsLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		recv := s.recvCount.Swap(0)
		sent := s.sendCount.Swap(0)
		drop := s.dropCount.Swap(0)

		totalLatency := s.totalLatencyUs.Swap(0)
		totalDevice := s.totalDeviceUs.Swap(0)
		totalQueue := s.totalQueueUs.Swap(0)
		count := s.latencyCount.Swap(0)
		maxLatency := s.maxLatencyUs.Swap(0)
		maxDevice := s.maxDeviceUs.Swap(0)

		if recv > 0 || sent > 0 {
			var avgLatency, avgDevice, avgQueue uint64
			if count > 0 {
				avgLatency = totalLatency / count
				avgDevice = totalDevice / count
				avgQueue = totalQueue / count
			}

			log.Printf("[STATS] recv=%d sent=%d drop=%d | avg: queue=%dµs device=%dµs total=%dµs | max: device=%dµs total=%dµs",
				recv, sent, drop, avgQueue, avgDevice, avgLatency, maxDevice, maxLatency)

			if avgLatency > 20000 {
				log.Printf("[WARN] High latency detected! avg=%dms - check USB/device", avgLatency/1000)
			}
			if drop > 0 {
				log.Printf("[WARN] Dropped %d frames - device can't keep up with source rate", drop)
			}
		}
	}
}

func (s *server) runControlListener() {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", controlPort))
	if err != nil {
		log.Printf("[CTRL] Failed to start control listener: %v", err)
		return
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[CTRL] Accept error: %v", err)
			continue
		}
		go s.handleControlConn(conn)
	}
}

func (s *server) handleControlConn(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}

	line = strings.TrimSpace(line)
	response := s.handleCommand(line)
	conn.Write([]byte(response + "\n"))
}

func (s *server) handleCommand(line string) string {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return "error: empty command"
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "set":
		return s.cmdSet(args)
	case "mode":
		return s.cmdMode(args)
	case "brightness":
		return s.cmdBrightness(args)
	case "status":
		return s.cmdStatus()
	default:
		return fmt.Sprintf("error: unknown command %q", cmd)
	}
}

func (s *server) cmdSet(args []string) string {
	if len(args) < 1 {
		return "error: usage: set <R,G,B|off>"
	}

	arg := strings.ToLower(args[0])

	if arg == "off" {
		s.paused.Store(true)
		if err := s.device.TurnOff(); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		log.Printf("[CTRL] LEDs turned off")
		return "ok"
	}

	color, ok := parseColor(arg)
	if !ok {
		return "error: invalid color (use R,G,B format)"
	}

	s.paused.Store(true)
	s.manualColorMu.Lock()
	s.manualColor = color
	s.manualColorMu.Unlock()

	if err := s.device.SetAllColor(s.applyBrightness([]bloqlight.RGB{color})[0]); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	log.Printf("[CTRL] Set color RGB(%d,%d,%d)", color.R, color.G, color.B)
	return "ok"
}

func (s *server) cmdMode(args []string) string {
	if len(args) < 1 {
		return "error: usage: mode <hyper|manual>"
	}

	mode := strings.ToLower(args[0])
	switch mode {
	case "hyper":
		s.manualColorMu.Lock()
		color := s.restoreColor
		s.manualColorMu.Unlock()

		if color != nil {
			if err := s.device.SetAllColor(s.applyBrightness([]bloqlight.RGB{*color})[0]); err != nil {
				log.Printf("[CTRL] Failed to restore color: %v", err)
			} else {
				log.Printf("[CTRL] Restored last HyperHDR color RGB(%d,%d,%d)", color.R, color.G, color.B)
			}
		}

		s.paused.Store(false)
		log.Printf("[CTRL] Mode: hyper (listening to HyperHDR)")
		return "ok"
	case "manual":
		s.paused.Store(true)
		log.Printf("[CTRL] Mode: manual (ignoring HyperHDR)")
		return "ok"
	default:
		return fmt.Sprintf("error: unknown mode %q (use hyper or manual)", mode)
	}
}

func (s *server) cmdBrightness(args []string) string {
	if len(args) < 1 {
		return fmt.Sprintf("%d", s.brightness.Load())
	}

	val, err := strconv.Atoi(args[0])
	if err != nil || val < 0 || val > 100 {
		return "error: brightness must be 0-100"
	}

	s.brightness.Store(uint32(val))
	log.Printf("[CTRL] Brightness set to %d%%", val)

	s.manualColorMu.Lock()
	manualColor := s.manualColor
	hyperColor := s.restoreColor
	s.manualColorMu.Unlock()

	if s.paused.Load() {
		if manualColor.R > 0 || manualColor.G > 0 || manualColor.B > 0 {
			s.device.SetAllColor(s.applyBrightness([]bloqlight.RGB{manualColor})[0])
		}
	} else if hyperColor != nil {
		s.device.SetAllColor(s.applyBrightness([]bloqlight.RGB{*hyperColor})[0])
	}

	return "ok"
}

func (s *server) cmdStatus() string {
	mode := "hyper"
	if s.paused.Load() {
		mode = "manual"
	}

	s.manualColorMu.Lock()
	color := s.manualColor
	s.manualColorMu.Unlock()

	return fmt.Sprintf("mode:%s udp:%d ctrl:%d leds:%d brightness:%d color:%d,%d,%d firmware:%s",
		mode, s.port, controlPort, s.device.LEDCount, s.brightness.Load(), color.R, color.G, color.B, s.device.Firmware)
}

func (s *server) applyBrightness(colors []bloqlight.RGB) []bloqlight.RGB {
	b := s.brightness.Load()
	if b >= 100 {
		return colors
	}
	scaled := make([]bloqlight.RGB, len(colors))
	for i, c := range colors {
		scaled[i] = bloqlight.RGB{
			R: byte(uint32(c.R) * b / 100),
			G: byte(uint32(c.G) * b / 100),
			B: byte(uint32(c.B) * b / 100),
		}
	}
	return scaled
}

func parseColor(s string) (bloqlight.RGB, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return bloqlight.RGB{}, false
	}
	r, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	g, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	b, err3 := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err1 != nil || err2 != nil || err3 != nil {
		return bloqlight.RGB{}, false
	}
	if r < 0 || r > 255 || g < 0 || g > 255 || b < 0 || b > 255 {
		return bloqlight.RGB{}, false
	}
	return bloqlight.RGB{R: byte(r), G: byte(g), B: byte(b)}, true
}
