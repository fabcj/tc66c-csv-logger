package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"time"
)

type CSVWriter struct {
	file *os.File
	writer *bufio.Writer
	scratch []byte
}

func CSVHeader() string {
	return "timestamp,voltage_v,current_a,power_w,resistance_ohm,capacity_mah,energy_mwh,dplus_v,dminus_v,temperature_c"
}

func startCSVLogging(port string, fileName string, interval time.Duration , serviceName string, pid int) {
	// Connect to port
	tc, err := NewTC66C(port)
	if err != nil {
		fmt.Printf("[ERR]Connection failed: %v\n", err)
		os.Exit(1)
	}

	// Enable CPU Monitor
	cpuMon := NewCPUMonitor(pid, serviceName)
	cpuEnabled := true
	if err := cpuMon.Prime(); err != nil {
		fmt.Printf("[LOG] PMU initialization skipped: %v\n", err)
		cpuEnabled = false
	}
	defer cpuMon.CPUClose()

	// Create CSV Writer
	cw, err := NewCSVWriter(fileName)
	if err != nil {
		fmt.Printf("[ERR] Failed to create CSV: %v\n", err)
		os.Exit(1)
	}
	defer cw.CSVClose()

	fmt.Printf("\n -- Power Meter Recording Started --\n")
	fmt.Printf("Saving data to '%s' every %v.\n", fileName, interval)

	if cpuEnabled {
		fmt.Printf("CPU (temp/usage/freq) is being logged alongside each sample.\n")
	}
	fmt.Printf("Press Ctrl+C to safely stop and save the file.\n\n")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var sampleCount uint64

	for {
		select {
		case <-sigChan:
			fmt.Printf("\n[LOG: Signal] Intercepted shutdown. Flushed %d samples to disk safely. Goodbye!\n", sampleCount)
			tc.TCClose()
			
			return

		case <-ticker.C:
			reading, err := tc.GetReading()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERR: Device] Failed reading packet: %v\n", err)
				continue
			}

			var cpuReading *cpuReading
			if cpuEnabled {
				cpuReading, err = cpuMon.Sample()
				if err != nil {
					fmt.Fprintf(os.Stderr, "[LOG: CPU Monitor] Failed reading stats: %v\n", err)
					cpuReading = nil
				}
			}

			if err := cw.WriteLog(reading, cpuReading); err != nil {
				fmt.Fprintf(os.Stderr, "[ERR: Write Error] Failed writing to file: %v\n", err)
				return
			}

			sampleCount++
			if sampleCount%10 == 0 {
				fmt.Printf("\rCapturing data... %d samples recorded.", sampleCount)
			}
		}
	}
}


// https://reintech.io/blog/reading-writing-files-go
// https://pkg.go.dev/encoding/csv
func NewCSVWriter (fileName string) (*CSVWriter, error)  {
	outFile, err := os.Create(fileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERR]Error creating file: %v\n", err)
		os.Exit(1)
	}

	// Used buffered Writer
	writer := bufio.NewWriter(outFile)
	
	//Iniitalize Header
	header := CSVHeader() + CPUHeader()
	if _, err := writer.WriteString(header); err != nil {
		outFile.Close()
		return nil, err
	}

	// Check ERror
	if err := writer.Flush(); err != nil {
        fmt.Fprintf(os.Stderr, "error flushing buffer: %v\n", err)
        os.Exit(1)
    }

	// Rturn reference to type
	return &CSVWriter{
		file:    outFile,
		writer:  writer,
		scratch: make([]byte, 0, 160),
	}, nil
}

func (cw *CSVWriter) WriteLog(r *powerReading, cpuR *cpuReading) error {
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

	} else {
		cw.scratch = append(cw.scratch, ",,,,,0"...)
	}

	cw.scratch = append(cw.scratch, '\n')
	_, err := cw.writer.Write(cw.scratch)
	return err
}

func (cw *CSVWriter) CSVClose() error {
	if err := cw.writer.Flush(); err != nil {
		cw.file.Close()
		return fmt.Errorf("failed to flush CSV buffer: %w", err)
    }
	return cw.file.Close()
}

