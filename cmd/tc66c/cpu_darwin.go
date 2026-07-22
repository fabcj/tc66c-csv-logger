//go:build darwin

package main

import (
	"log"
)

func CPUHeader() string {
	return ",cpu_temp_c,cpu_usage_pct,cpu_freq_mhz,cpu_total_cycles,cpu_target_cycles\n"
}

type cpuReading struct {
	TemperatureC float64
	UsagePercent float64
	FreqMHz      float64
	TotalCycles  uint64
	TargetCycles uint64
}

type Monitor struct {
	targetPID int
	reading   cpuReading
}

func NewCPUMonitor(targetPID int, serviceName string) *Monitor {
	log.Println("[Note] CPU hardware performance monitoring is disabled on macOS.")
	return &Monitor{
		targetPID: targetPID,
	}
}

func (m *Monitor) Prime() error {
	return nil
}

func (m *Monitor) Sample() (*cpuReading, error) {
	return &m.reading, nil
}

func (m *Monitor) CPUClose() {
}
