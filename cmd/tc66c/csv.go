package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/skgsergio/tc66-toolkit/internal/protocol"
)

func startCSVLogging(port string, filename string, interval time.Duration) {
	fmt.Printf("Connecting to meter...\n")
	tc, err := protocol.NewTC66C(port)
	if err != nil {
		fmt.Printf("Connection failed: %v\n", err)
		os.Exit(1)
	}
	defer tc.Close()

	cw, err := NewCSVWriter(filename)
	if err != nil {
		fmt.Printf("Failed to create CSV: %v\n", err)
		os.Exit(1)
	}
	defer cw.Close()

	fmt.Printf("\n▶ RECORDING STARTED\n")
	fmt.Printf("Saving data to '%s' every %v.\n", filename, interval)
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

			if err := cw.WriteLog(reading); err != nil {
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

	if _, err := writer.WriteString(protocol.CSVHeader()); err != nil {
		file.Close()
		return nil, err
	}
	if err := writer.WriteByte('\n'); err != nil {
		file.Close()
		return nil, err
	}

	return &CSVWriter{
		file:    file,
		writer:  writer,
		scratch: make([]byte, 0, 128),
	}, nil
}

func (cw *CSVWriter) WriteLog(r *protocol.Reading) error {
	now := time.Now()
	cw.scratch = cw.scratch[:0]

	// Timestamp
	cw.scratch = now.AppendFormat(cw.scratch, "2006-01-02 15:04:05.000")
	cw.scratch = append(cw.scratch, ',')

	// Voltage (V)
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Voltage, 'f', 4, 64)
	cw.scratch = append(cw.scratch, ',')

	// Current (A)
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Current, 'f', 5, 64)
	cw.scratch = append(cw.scratch, ',')

	// Power (W)
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Power, 'f', 4, 64)
	cw.scratch = append(cw.scratch, ',')

	// Resistance (Ohms)
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Resistance, 'f', 2, 64)
	cw.scratch = append(cw.scratch, ',')

	// Capacity (mAh)
	cw.scratch = strconv.AppendUint(cw.scratch, uint64(r.CapacitymAh), 10)
	cw.scratch = append(cw.scratch, ',')

	// Energy (mWh)
	cw.scratch = strconv.AppendUint(cw.scratch, uint64(r.EnergymWh), 10)
	cw.scratch = append(cw.scratch, ',')

	// D+ Line Voltage
	cw.scratch = strconv.AppendFloat(cw.scratch, r.DPlus, 'f', 2, 64)
	cw.scratch = append(cw.scratch, ',')

	// D- Line Voltage
	cw.scratch = strconv.AppendFloat(cw.scratch, r.DMinus, 'f', 2, 64)
	cw.scratch = append(cw.scratch, ',')

	// Temperature (C)
	cw.scratch = strconv.AppendFloat(cw.scratch, r.Temperature, 'f', 1, 64)
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
