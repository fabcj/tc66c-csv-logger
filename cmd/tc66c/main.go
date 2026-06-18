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
	portFlag        := flag.String("port", "", "Serial port device path")
	intervalFlag    := flag.Duration("interval", 250*time.Millisecond, "Sampling polling interval")
	targetPIDFlag   := flag.Int("pid", -1, "Target Process ID (PID) to attribute power consumption to")
	serviceFlag     := flag.String("service", "", "Systemd service name to attribute power consumption to (e.g. apache2)")
	flag.Parse()

	if *targetPIDFlag > 0 && *serviceFlag != "" {
		fmt.Println("Error: -pid and -service are mutually exclusive.")
		os.Exit(1)
	}

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

	fmt.Print("Enter output CSV file name (Press Enter for default: 'tc66c_output.csv'): ")
	reader := bufio.NewReader(os.Stdin)
	filename, _ := reader.ReadString('\n')
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "tc66c_output.csv"
	}

	startCSVLogging(port, filename, *intervalFlag, *targetPIDFlag, *serviceFlag)
}