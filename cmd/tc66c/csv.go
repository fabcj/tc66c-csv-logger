package main

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"time"
)

// Define package-level static errors to avoid heap allocations
var (
	ErrCreateCSVFile  = errors.New("failed to create CSV file")
	ErrWriteCSVHeader = errors.New("failed to write CSV header")
	ErrFlushCSVHeader = errors.New("failed to flush CSV header")
	ErrFlushCSVBuffer = errors.New("failed to flush CSV buffer")
)

// CSVWriter handles buffered, zero-allocation writes to the output file.
type CSVWriter struct {
	file       *os.File
	writer     *bufio.Writer
	scratch    []byte
	cpuEnabled bool
}

// CSVHeader returns the standard column names for the power data.
func CSVHeader() string {
	return "timestamp,voltage_v,current_a,power_w,resistance_ohm,capacity_mah,energy_mwh,dplus_v,dminus_v,temperature_c"
}

// startCSVLogging initializes hardware, opens the CSV, and begins the zero-allocation polling loop.
func startCSVLogging(port string, fileName string, interval time.Duration, serviceName string, pid int, noCPU bool) {
	tc, err := NewTC66C(port)
	if err != nil {
		printMsg("[ERR] Hardware connection failed: " + err.Error())
		return
	}
	defer tc.TCClose()

	var cpuMon *Monitor
	cpuEnabled := false

	if !noCPU {
		cpuMon = NewCPUMonitor(pid, serviceName)
		if err := cpuMon.Prime(); err != nil {
			printMsg("[LOG] CPU Monitor init skipped: " + err.Error())
		} else {
			cpuEnabled = true
		}
		defer cpuMon.CPUClose()
	}

	cw, err := NewCSVWriter(fileName, cpuEnabled)
	if err != nil {
		printMsg("[ERR] Failed to create CSV: " + err.Error())
		return
	}
	defer cw.CSVClose()

	// Startup messages are one-time allocations and are perfectly safe.
	printMsg("\n-- TC66C Recording Started --")
	printMsg("Saving data to '" + fileName + "' every " + interval.String() + ".")

	if cpuEnabled {
		printMsg("CPU Monitoring: ENABLED")
	} else {
		printMsg("CPU Monitoring: DISABLED (-nocpu)")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var sampleCount uint64

	// --- ZERO ALLOCATION HOT LOOP BEGINS HERE ---
	for {
		select {
		case <-sigChan:
			printMsg("\n[LOG] Intercepted stop signal. Flushed " + strconv.FormatUint(sampleCount, 10) + " samples to disk safely.")
			return

		case <-ticker.C:
			reading, err := tc.GetReading()
			if err != nil {
				printMsg("[ERR: Meter] Read failed")
				continue
			}

			var cpuR *cpuReading
			if cpuEnabled {
				cpuR, err = cpuMon.Sample()
				if err != nil {
					printMsg("[ERR: CPU] Sample failed")
					cpuR = nil
				}
			}

			if err := cw.WriteLog(reading, cpuR); err != nil {
				printMsg("[ERR: Disk] Write failed")
				return
			}

			sampleCount++
			if sampleCount%10 == 0 {
				// Stack-allocated terminal update buffer.
				// We keep os.Stdout.Write here because printMsg forces a newline,
				// which would break the carriage return (\r) overwriting effect.
				var termBuf [64]byte
				scratch := termBuf[:0]
				scratch = append(scratch, "\rCapturing data... "...)
				scratch = strconv.AppendUint(scratch, sampleCount, 10)
				scratch = append(scratch, " samples recorded."...)
				os.Stdout.Write(scratch)
			}
		}
	}
	// --- ZERO ALLOCATION HOT LOOP ENDS HERE ---
}

// NewCSVWriter initializes a new CSV file and writes the header row.
func NewCSVWriter(fileName string, cpuEnabled bool) (*CSVWriter, error) {
	outFile, err := os.Create(fileName)
	if err != nil {
		return nil, ErrCreateCSVFile
	}

	writer := bufio.NewWriter(outFile)

	header := CSVHeader()
	if cpuEnabled {
		header += CPUHeader()
	} else {
		header += "\n"
	}

	if _, err := writer.WriteString(header); err != nil {
		outFile.Close()
		return nil, ErrWriteCSVHeader
	}

	if err := writer.Flush(); err != nil {
		outFile.Close()
		return nil, ErrFlushCSVHeader
	}

	return &CSVWriter{
		file:       outFile,
		writer:     writer,
		scratch:    make([]byte, 0, 160),
		cpuEnabled: cpuEnabled,
	}, nil
}

// WriteLog formats and writes a single row of telemetry data using a stack-allocated buffer.
func (cw *CSVWriter) WriteLog(r *powerReading, cpuR *cpuReading) error {
	now := time.Now()

	// Declare a fixed-size array on the stack
	var buf [256]byte
	// Create a slice backed by the stack array
	scratch := buf[:0]

	scratch = now.AppendFormat(scratch, "2006-01-02 15:04:05.000")
	scratch = append(scratch, ',')
	scratch = strconv.AppendFloat(scratch, r.Voltage, 'f', 4, 64)
	scratch = append(scratch, ',')
	scratch = strconv.AppendFloat(scratch, r.Current, 'f', 5, 64)
	scratch = append(scratch, ',')
	scratch = strconv.AppendFloat(scratch, r.Power, 'f', 4, 64)
	scratch = append(scratch, ',')
	scratch = strconv.AppendFloat(scratch, r.Resistance, 'f', 2, 64)
	scratch = append(scratch, ',')
	scratch = strconv.AppendUint(scratch, uint64(r.CapacitymAh), 10)
	scratch = append(scratch, ',')
	scratch = strconv.AppendUint(scratch, uint64(r.EnergymWh), 10)
	scratch = append(scratch, ',')
	scratch = strconv.AppendFloat(scratch, r.DPlus, 'f', 2, 64)
	scratch = append(scratch, ',')
	scratch = strconv.AppendFloat(scratch, r.DMinus, 'f', 2, 64)
	scratch = append(scratch, ',')
	scratch = strconv.AppendFloat(scratch, r.Temperature, 'f', 1, 64)

	if cw.cpuEnabled {
		scratch = append(scratch, ',')
		if cpuR != nil {
			scratch = strconv.AppendFloat(scratch, cpuR.TemperatureC, 'f', 1, 64)
			scratch = append(scratch, ',')
			scratch = strconv.AppendFloat(scratch, cpuR.UsagePercent, 'f', 1, 64)
			scratch = append(scratch, ',')
			scratch = strconv.AppendFloat(scratch, cpuR.FreqMHz, 'f', 0, 64)
			scratch = append(scratch, ',')
			scratch = strconv.AppendUint(scratch, cpuR.TotalCycles, 10)
			scratch = append(scratch, ',')
			scratch = strconv.AppendUint(scratch, cpuR.TargetCycles, 10)
		} else {
			// Exactly 5 CPU column fallbacks matching the header
			scratch = append(scratch, "0.0,0.0,0.0,0,0"...)
		}
	}

	scratch = append(scratch, '\n')
	_, err := cw.writer.Write(scratch)
	return err
}

// CSVClose flushes any remaining buffered data and securely closes the file handle.
func (cw *CSVWriter) CSVClose() error {
	if err := cw.writer.Flush(); err != nil {
		cw.file.Close()
		return ErrFlushCSVBuffer
	}
	return cw.file.Close()
}
