// bloqlight is a UDP-to-HID bridge for the ROBOBLOQ USB Light Bar.
//
// Usage:
//
//	bloqlight                     Start server (UDP + TCP control)
//	bloqlight set R,G,B           Set color (e.g., 255,128,0)
//	bloqlight set off             Turn off LEDs
//	bloqlight mode hyper          Listen to HyperHDR
//	bloqlight mode manual         Ignore HyperHDR
//	bloqlight brightness [0-100]  Get or set brightness
//	bloqlight status              Show status
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"bloqlight"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}

	if len(os.Args) > 1 && os.Args[1][0] != '-' {
		if err := runClient(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	port := flag.Int("port", 5568, "UDP port to listen on")
	debug := flag.Bool("debug", false, "enable debug logging")
	iface := flag.Int("interface", -1, "HID interface number (-1 = auto)")
	list := flag.Bool("list", false, "list HID devices and exit")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("bloqlight - ROBOBLOQ Light Bar Driver")

	if *list {
		bloqlight.ListDevices()
		return
	}

	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", controlPort), 100*time.Millisecond); err == nil {
		conn.Close()
		log.Fatal("Another instance is already running")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	var device *bloqlight.Device
	for {
		var err error
		if *iface >= 0 {
			device, err = bloqlight.OpenInterface(*iface)
		} else {
			device, err = bloqlight.Open()
		}
		if err == nil {
			break
		}
		log.Printf("Device not found (%v), retrying in 2s...", err)

		select {
		case <-sigCh:
			log.Println("Shutting down...")
			os.Exit(0)
		case <-time.After(2 * time.Second):
		}
	}

	log.Printf("Device connected (firmware %s, %d LEDs)", device.Firmware, device.LEDCount)

	srv := newServer(device, *port, *debug)

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			log.Println("Turning off LEDs...")
			srv.deviceMu.Lock()
			srv.device.TurnOff()
			srv.device.Close()
			srv.deviceMu.Unlock()
		})
	}
	defer cleanup()

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cleanup()
		os.Exit(0)
	}()

	if err := srv.run(); err != nil {
		log.Printf("Server error: %v", err)
	}
}
