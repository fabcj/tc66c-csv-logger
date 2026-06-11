package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func autoDetectPort() (string, error) {
	patterns := []string{"/dev/ttyACM*", "/dev/ttyUSB*"}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			return matches[0], nil
		}
	}

	return "", fmt.Errorf("TC66C not found. Is it plugged in?")
}

func main() {
	portFlag := flag.String("port", "", "Serial port device path (leave blank for auto-detect)")
	intervalFlag := flag.Duration("interval", 250*time.Millisecond, "Sampling polling interval")
	flag.Parse()

	// Autodetect tc66c
	port := *portFlag
	if port == "" {
		var err error
		port, err = autoDetectPort()
		if err != nil {
			fmt.Printf("Hardware Error: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("✓ Found TC66C on %s\n", port)

	// Ask file location
	fmt.Print("Enter output CSV file name (Press Enter for default: 'tc66c_output.csv'): ")
	reader := bufio.NewReader(os.Stdin)
	filename, _ := reader.ReadString('\n')
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "tc66c_output.csv"
	}

	// Start recording .csv
	startCSVLogging(port, filename, *intervalFlag)
}
