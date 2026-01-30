package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

func runClient(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	cmd := strings.Join(args, " ")

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", controlPort), 2*time.Second)
	if err != nil {
		return fmt.Errorf("cannot connect to server (is it running?): %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	_, err = conn.Write([]byte(cmd + "\n"))
	if err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}

	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	response = strings.TrimSpace(response)

	if strings.HasPrefix(response, "error:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "error: "))
	}

	if args[0] == "status" {
		printStatus(response)
		return nil
	}

	fmt.Println(response)
	return nil
}

func printStatus(response string) {
	// Parse: mode:hyper udp:5568 ctrl:5569 leds:30 brightness:100 color:0,0,0 firmware:5.1.34
	parts := strings.Fields(response)
	for _, part := range parts {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "mode":
			fmt.Printf("Mode: %s\n", kv[1])
		case "udp":
			fmt.Printf("UDP port: %s (HyperHDR)\n", kv[1])
		case "ctrl":
			fmt.Printf("Control port: %s\n", kv[1])
		case "leds":
			fmt.Printf("LED count: %s\n", kv[1])
		case "brightness":
			fmt.Printf("Brightness: %s%%\n", kv[1])
		case "color":
			fmt.Printf("Manual color: RGB(%s)\n", kv[1])
		case "firmware":
			fmt.Printf("Firmware: %s\n", kv[1])
		}
	}
}

func printUsage() {
	fmt.Println(`Usage: bloqlight [command]

Server mode (no arguments):
  bloqlight                    Start server (UDP + TCP control)

Client commands:
  bloqlight set R,G,B          Set color (e.g., 255,128,0)
  bloqlight set off            Turn off LEDs
  bloqlight mode hyper         Listen to HyperHDR
  bloqlight mode manual        Ignore HyperHDR, keep current color
  bloqlight brightness [0-100] Get or set brightness (affects all output)
  bloqlight status             Show current mode and connection info`)
}
