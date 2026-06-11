# TC66C Toolkit (CSV Logging Fork)

A command-line toolkit for interacting with TC66/TC66C USB power meters. This fork has been heavily stripped down and optimized specifically for high-speed, automated CSV data logging.

## Disclaimer

**This fork was heavily overhauled for a specific research use-case.** It can and will contain bugs, and the UX is highly specialized. Use at your own risk.

I gutted the original firmware updater, web UI, and JSON outputs to build a zero-allocation, ultra-fast CSV pipeline. Since it no longer updates firmware, it shouldn't be able to brick your device during high-frequency polling, but again—it's offered as-is.

## Features

- **High-speed CSV logging**: Zero-allocation pipeline perfect for piping into Pandas, Grafana, or benchmark scripts
- **Continuous polling**: Monitor readings in real-time at configurable intervals
- **Auto-detection**: Automatically finds the meter on available serial ports
- **Interactive & Scriptable**: Prompts for a filename, or accepts piped input for fully headless bash scripting
- **Safe shutdown**: Catches `Ctrl+C` or `SIGTERM` to gracefully flush buffers and close the CSV
- **Cross-platform**: Works on Linux, macOS, and Windows

## Installation

### From Source

Requires Go 1.21 or later:

```bash
https://github.com/fabcj/tc66c-csv-logger.git
cd tc66c-toolkit
go build -o tc66c-toolkit .
```

## Usage

### Basic Commands
This fork compiles into a single, specialized command. There are no subcommands like get or poll anymore.

###Interactive Mode

Run the binary. It will auto-detect the meter and prompt you for a save location:

```bash
./tc66c-toolkit
```
### Automated Scripting Mode
You can pipe the filename directly into the program and specify polling flags. This completely bypasses the interactive prompt, making it perfect for automated benchmark scripts:
``` bash
# Poll every 100ms and save to benchmark.csv
echo "benchmark.csv" | ./tc66c-toolkit -interval 100ms

# Specify an exact port
echo "data.csv" | ./tc66c-toolkit -port /dev/ttyUSB0 -interval 500ms
```

### Flags
-port: Serial port device path (leave blank to auto-detect)

-interval: Sampling polling interval (default: 250ms)

### Output Formats
CSV Format
Data is written natively to CSV format for maximum performance:

```
timestamp,voltage_v,current_a,power_w,resistance_ohm,capacity_mah,energy_mwh,dplus_v,dminus_v,temperature_c
2026-06-11 16:05:35.123,5.1234,0.51234,2.6234,10.00,1234,5678,2.75,2.75,25.0
2026-06-11 16:05:35.223,5.1230,0.51240,2.6250,10.01,1234,5678,2.75,2.75,25.0
```
### Library Usage
The toolkit can also be used as a Go library:
```go
package main

import (
    "fmt"
    "[github.com/YOUR-USERNAME/tc66c-toolkit/internal/protocol](https://github.com/YOUR-USERNAME/tc66c-toolkit/internal/protocol)"
)

func main() {
    device, err := protocol.NewTC66C("/dev/ttyACM0")
    if err != nil {
        panic(err)
    }
    defer device.Close()

    reading, err := device.GetReading()
    if err != nil {
        panic(err)
    }

    fmt.Printf("Voltage: %.4f V\n", reading.Voltage)
    fmt.Printf("Current: %.5f A\n", reading.Current)
    fmt.Printf("Power: %.4f W\n", reading.Power)
}
```
## Troubleshooting
Permission Denied on Linux
Add your user to the dialout group:

```Bash
sudo usermod -a -G dialout $USER
```
Then log out and back in.

Alternatively, run with sudo (not recommended for regular use).

### Device Not Found
Check which port your device is on:

```Bash
# Linux
ls /dev/ttyACM* /dev/ttyUSB*

# macOS
ls /dev/cu.usbmodem*

# Windows
# Check Device Manager for COM ports
```
Then specify the port:
```Bash
./tc66c-toolkit -port /dev/ttyUSB0  # or COM3 on Windows
```
## Protocol
The communication protocol is based on the sigrok project's TC66C protocol documentation. The device uses:

Baud rate: 115200

Data bits: 8

Parity: None

Stop bits: 1

All measurement data is AES-ECB encrypted with a static key.

## License
Licensed under The "Better Ask The LLM" License (BATL) - Software offered "as is, maybe" with no warranties or guarantees. Use at your own risk, and when in doubt, better ask the LLM!

See LICENSE.md for details.
