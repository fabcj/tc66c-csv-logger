package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/skgsergio/tc66-toolkit/internal/cpu"
	"github.com/skgsergio/tc66-toolkit/internal/protocol"
)

func startCSVLogging(port string, filename string, interval time.Duration, targetPID int, serviceName string) {
	fmt.Printf("[PORT Monitor]: Connecting to monitor\n")
	tc, err := protocol.NewTC66C(port)
	if err != nil {
		fmt.Printf("Connection failed: %v\n", err)
		os.Exit(1)
	}
	defer tc.Close()

	cpuMon := cpu.NewMonitor(targetPID, serviceName)
	cpuEnabled := true
	if err := cpuMon.Prime(); err != nil {
		fmt.Printf("[CPU Monitor] PMU initialization skipped: %v\n", err)
		cpuEnabled = false
	}
	defer cpuMon.Close()

	cw, err := NewCSVWriter(filename)
	if err != nil {
		fmt.Printf("Failed to create CSV: %v\n", err)
		os.Exit(1)
	}
	defer cw.Close()

	fmt.Printf("\n▶ RECORDING STARTED\n")
	fmt.Printf("Saving data to '%s' every %v.\n", filename, interval)
	if cpuEnabled {
		fmt.Printf("CPU telemetry (temp/usage/freq) is being logged alongside each sample.\n")
	}
	fmt.Printf("Press Ctrl+C to safely stop and save the file.\n\n")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var sampleCount uint64

	for {
		select {
		case <-sigChan:
			fmt.Printf("\n[Signal] Intercepted shutdown. Flushed %d samples to disk safely. Goodbye!\n", sampleCount)
			return

		case <-ticker.C:
			reading, err := tc.GetReading()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[Device Error] Failed reading packet: %v\n", err)
				continue
			}

			var cpuReading *cpu.Reading
			if cpuEnabled {
				cpuReading, err = cpuMon.Sample()
				if err != nil {
					fmt.Fprintf(os.Stderr, "[CPU Monitor] Failed reading stats: %v\n", err)
					cpuReading = nil
				}
			}

			if err := cw.WriteLog(reading, cpuReading); err != nil {
				fmt.Fprintf(os.Stderr, "[Write Error] Failed writing to file: %v\n", err)
				return
			}

			sampleCount++
			if sampleCount%10 == 0 {
				fmt.Printf("\rCapturing data... %d samples recorded.", sampleCount)
			}
		}
	}
}

type CSVWriter struct {
	file    *os.File
	writer  *bufio.Writer
	scratch []byte
}

func NewCSVWriter(filename string) (*CSVWriter, error) {
	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSV file: %w", err)
	}

	writer := bufio.NewWriterSize(file, 64*1024)

	header := protocol.CSVHeader() + ",cpu_temp_c,cpu_usage_pct,cpu_freq_mhz,cpu_total_cycles,cpu_target_cycles,attributed_watts\n"
	if _, err := writer.WriteString(header); err != nil {
		file.Close()
		return nil, err
	}

	return &CSVWriter{
		file:    file,
		writer:  writer,
		scratch: make([]byte, 0, 160),
	}, nil
}

func (cw *CSVWriter) WriteLog(r *protocol.Reading, cpuR *cpu.Reading) error {
	now := time.Now()
	cw.scratch = cw.scratch[:0]

	cw.scratch = now.AppendFormat(cw.scratch, "2006-01-02 15:04:05.000")
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Voltage, 'f', 4, 64)
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Current, 'f', 5, 64)
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Power, 'f', 4, 64)
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Resistance, 'f', 2, 64)
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendUint(cw.scratch, uint64(r.CapacitymAh), 10)
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendUint(cw.scratch, uint64(r.EnergymWh), 10)
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendFloat(cw.scratch, r.DPlus, 'f', 2, 64)
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendFloat(cw.scratch, r.DMinus, 'f', 2, 64)
	cw.scratch = append(cw.scratch, ',')
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Temperature, 'f', 1, 64)
	cw.scratch = append(cw.scratch, ',')

	if cpuR != nil {
		const pi4IdleBaseline = 2.50
		attributedWatts := 0.0
		if r.Power > pi4IdleBaseline && cpuR.TotalCycles > 0 {
			attributedWatts = (r.Power - pi4IdleBaseline) * (float64(cpuR.TargetCycles) / float64(cpuR.TotalCycles))
		}

		cw.scratch = strconv.AppendFloat(cw.scratch, cpuR.TemperatureC, 'f', 1, 64)
		cw.scratch = append(cw.scratch, ',')
		cw.scratch = strconv.AppendFloat(cw.scratch, cpuR.UsagePercent, 'f', 1, 64)
		cw.scratch = append(cw.scratch, ',')
		cw.scratch = strconv.AppendFloat(cw.scratch, cpuR.FreqMHz, 'f', 0, 64)
		cw.scratch = append(cw.scratch, ',')
		cw.scratch = strconv.AppendUint(cw.scratch, cpuR.TotalCycles, 10)
		cw.scratch = append(cw.scratch, ',')
		cw.scratch = strconv.AppendUint(cw.scratch, cpuR.TargetCycles, 10)
		cw.scratch = append(cw.scratch, ',')
		cw.scratch = strconv.AppendFloat(cw.scratch, attributedWatts, 'f', 4, 64)
	} else {
		cw.scratch = append(cw.scratch, ",,,,,0"...)
	}

	cw.scratch = append(cw.scratch, '\n')
	_, err := cw.writer.Write(cw.scratch)
	return err
}

func (cw *CSVWriter) Close() error {
	if err := cw.writer.Flush(); err != nil {
		cw.file.Close()
		return fmt.Errorf("failed to flush CSV buffer: %w", err)
	}
	return cw.file.Close()
}