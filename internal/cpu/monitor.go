package cpu

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// CPU locations
	thermalZonePath = "/sys/class/thermal/thermal_zone0/temp"
	cpuFreqPath = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq"
	procStatPath = "/proc/stat"
)

// Reading is a single CPU telemetry snapshot.
type Reading struct {
	TemperatureC float64
	UsagePercent float64
	FreqMHz      float64
}

// Monitor reads CPU
type Monitor struct {
	prevIdle  uint64
	prevTotal uint64
	primed    bool
}

// Creates Monitor
func NewMonitor() *Monitor {
	return &Monitor{}
}

// Takes Baseline Reading
func (m *Monitor) Prime() error {
	idle, total, err := readStat()
	if err != nil {
		return err
	}
	m.prevIdle = idle
	m.prevTotal = total
	m.primed = true
	return nil
}

// Sampel Reads all info in Reading Struct
func (m *Monitor) Sample() (*Reading, error) {
	temp, err := readTemperature()
	if err != nil {
		return nil, fmt.Errorf("[CPU-Monitor]: temperature read failed: %w", err)
	}

	freq, err := readFrequency()
	if err != nil {
		return nil, fmt.Errorf("[CPU-Monitor]: frequency read failed: %w", err)
	}

	usage, err := m.readUsage()
	if err != nil {
		return nil, fmt.Errorf("[CPU-Monitor]: usage read failed: %w", err)
	}

	return &Reading{
		TemperatureC: temp,
		UsagePercent: usage,
		FreqMHz:      freq,
	}, nil
}

func readTemperature() (float64, error) {
	data, err := os.ReadFile(thermalZonePath)
	if err != nil {
		return 0, err
	}

	milliC, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected value in %s: %w", thermalZonePath, err)
	}

	return milliC / 1000.0, nil
}

func readFrequency() (float64, error) {
	data, err := os.ReadFile(cpuFreqPath)
	if err != nil {
		return 0, err
	}

	khz, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected value in %s: %w", cpuFreqPath, err)
	}

	return khz / 1000.0, nil
}

func readStat() (idle uint64, total uint64, err error) {
	f, err := os.Open(procStatPath)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, 0, fmt.Errorf("empty %s", procStatPath)
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected format in %s", procStatPath)
	}

	for i, raw := range fields[1:] {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("unexpected value in %s: %w", procStatPath, err)
		}
		total += v
		// Idle for nonbusy time
		if i == 3 || i == 4 {
			idle += v
		}
	}

	return idle, total, nil
}

func (m *Monitor) readUsage() (float64, error) {
	idle, total, err := readStat()
	if err != nil {
		return 0, err
	}

	if !m.primed {
		m.prevIdle = idle
		m.prevTotal = total
		m.primed = true
		return 0, nil
	}

	deltaTotal := total - m.prevTotal
	deltaIdle := idle - m.prevIdle

	m.prevIdle = idle
	m.prevTotal = total

	if deltaTotal == 0 {
		return 0, nil
	}

	usage := (1.0 - float64(deltaIdle)/float64(deltaTotal)) * 100.0

	// Clamp for safety.
	if usage < 0 {
		usage = 0
	} else if usage > 100 {
		usage = 100
	}

	return usage, nil
}
