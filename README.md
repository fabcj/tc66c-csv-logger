# TC66C Toolkit (CSV Logging Fork)

A command-line toolkit for interacting with TC66/TC66C USB power meters. This fork has been heavily stripped down and optimized specifically for high-speed, automated CSV data logging and extreme power efficiency. 

## Disclaimer

**This fork was heavily overhauled for a specific scientific research use-case.** It can and will contain bugs, and the UX is highly specialized. Use at your own risk.

I gutted the original firmware updater, web UI, and JSON outputs to build a zero-allocation, ultra-fast CSV pipeline. It essentially functions as bare-metal C-level systems code written in Go. Since it no longer updates firmware, it shouldn't be able to brick your device during high-frequency polling, but again—it's offered as-is.

## Features

- **Extreme Power Efficiency**: Zero heap-allocation data parsing and persistent file descriptors to aggressively avoid garbage collection and CPU wake-ups.
- **High-Speed CSV Logging**: Direct byte-slice manipulation perfect for piping into Pandas, Grafana, or automated benchmark scripts.
- **Hardware-Level CPU Tracking (Linux)**: Natively reads `/sys`, `/proc`, and utilizes `perf_event_open` syscalls to log CPU temperature, frequency, and cycles perfectly synced with USB power metrics. (Gracefully disables itself on macOS/Windows).
- **Remote HTTP Server Mode**: Run completely headless with a lightweight HTTP API to start and stop logging remotely (ideal for Raspberry Pi deployments).
- **Auto-Detection**: Automatically finds the meter on available serial ports across operating systems.
- **Safe Shutdown**: Intercepts `Ctrl+C` or `SIGTERM` to safely flush buffers and close the CSV without data corruption.

## Installation

### From Source

Requires Go 1.21 or later:

```bash
git clone [https://github.com/fabcj/tc66c-csv-logger.git](https://github.com/fabcj/tc66c-csv-logger.git)
cd tc66c-toolkit
go build -o tc66c-toolkit .

```

## Usage

### Direct CLI Mode

This fork compiles into a single, specialized command with zero background network overhead. By default, it will auto-detect the meter, generate a timestamped filename, and start logging immediately.

```bash
# Basic run (auto-generates filename like tc66c_data_20260722_140340.csv)
./tc66c-toolkit

# Specify a custom filename and poll every 100ms
./tc66c-toolkit -F benchmark.csv -I 100ms

# Specify an exact port and track a specific Process ID (PID)
./tc66c-toolkit -P /dev/ttyUSB0 -I 500ms -D 1234

```

### Remote HTTP Server Mode

You can start the toolkit as a headless server to control logging remotely via HTTP endpoints. This is highly recommended for remote data collection nodes.

```bash
# Start the listener on port 9000
./tc66c-toolkit -listen :9000

```

**API Endpoints:**

* **Start Logging:** `http://localhost:9000/start?file=out.csv&nocpu=1`
* **Stop Logging:** `http://localhost:9000/stop`

### Flags

| Flag | Alias | Description | Default |
| --- | --- | --- | --- |
| `-file` | `-F` | Output CSV filename | `tc66c_data_<timestamp>.csv` |
| `-interval` | `-I` | Sampling poll interval (e.g., `100ms`, `1s`) | `500ms` |
| `-port` | `-P` | Serial port device path (leave blank to auto-detect) | Auto |
| `-pid` | `-D` | Target Process ID (PID) to monitor for CPU cycles | `-1` (None) |
| `-service` | `-S` | Systemd Service name to monitor (Linux cgroups) | `""` |
| `-listen` |  | Address to listen on for HTTP remote control (e.g., `:9000`) | `""` |
| `-nocpu` |  | Disable CPU (temp/usage/freq/cycles) logging entirely | `false` |

## Output Format

Data is written natively to CSV format. If CPU monitoring is enabled, system telemetry is perfectly aligned with the power readings.

```csv
timestamp,voltage_v,current_a,power_w,resistance_ohm,capacity_mah,energy_mwh,dplus_v,dminus_v,temperature_c,cpu_temp_c,cpu_usage_pct,cpu_freq_mhz,cpu_total_cycles,cpu_target_cycles
2026-07-22 14:05:35.123,5.1234,0.51234,2.6234,10.00,1234,5678,2.75,2.75,25.0,45.2,12.5,3200,1543200,84500
2026-07-22 14:05:35.623,5.1230,0.51240,2.6250,10.01,1234,5678,2.75,2.75,25.0,46.1,14.2,3200,1654300,91200

```

*(Note: If `-nocpu` is passed, or if running on macOS/Windows, the CPU columns are cleanly padded with zeros to ensure strict structural integrity for downstream data pipelines).*

## Troubleshooting

### Permission Denied on Linux

Add your user to the dialout group:

```bash
sudo usermod -a -G dialout $USER

```

Then log out and back in. Alternatively, run with `sudo` (not recommended for regular use).

### Device Not Found

Check which port your device is on:

```bash
# Linux
ls /dev/ttyACM* /dev/ttyUSB*

# macOS
ls /dev/cu.usbmodem* /dev/cu.usbserial*

# Windows
# Check Device Manager for COM ports

```

Then specify the port manually:

```bash
./tc66c-toolkit -P /dev/ttyUSB0

```

## Protocol

The communication protocol is based on the sigrok project's TC66C protocol documentation. The device uses:

* **Baud rate:** 115200
* **Data bits:** 8
* **Parity:** None
* **Stop bits:** 1

All measurement data is AES-ECB encrypted with a static key.

## License

Licensed under The "Better Ask The LLM" License (BATL) - Software offered "as is, maybe" with no warranties or guarantees. Use at your own risk, and when in doubt, better ask the LLM!

See `LICENSE.md` for details.