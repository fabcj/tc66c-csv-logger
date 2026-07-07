//go:build darwin

package main

import (
	"log"
)

// CPUHeader returns the CSV header. Keeping this identical ensures 
// your data logger pipelines don't break on Mac.
func CPUHeader() string {
	return ",cpu_temp_c,cpu_usage_pct,cpu_freq_mhz,cpu_total_cycles,cpu_target_cycles,attributed_watts\n"
}

// cpuReading mimics the structure exactly so your data pipelines compile.
type cpuReading struct {
	TemperatureC float64
	UsagePercent float64
	FreqMHz      float64
	TotalCycles  uint64
	TargetCycles uint64
}

// Monitor is a stub structure for macOS. We don't need the Linux file descriptors 
// or cgroup paths here.
type Monitor struct {
	targetPID int
}

// NewCPUMonitor returns a dummy monitor when running on macOS.
func NewCPUMonitor(targetPID int, serviceName string) *Monitor {
	log.Println("[Warning] CPU hardware performance monitoring is not supported on macOS.")
	return &Monitor{
		targetPID: targetPID,
	}
}

// Prime is a no-op fallback for macOS.
func (m *Monitor) Prime() error {
	return nil
}

// Sample returns safe, zeroed-out metric values since Linux kernel subsystems 
// like /proc/stat and perf_event_open do not exist on macOS.
func (m *Monitor) Sample() (*cpuReading, error) {
	return &cpuReading{
		TemperatureC: 0.0,
		UsagePercent: 0.0,
		FreqMHz:      0.0,
		TotalCycles:  0,
		TargetCycles: 0,
	}, nil
}

// CPUClose is a no-op cleanup fallback for macOS.
func (m *Monitor) CPUClose() {
	// No open file descriptors or Linux perf events to close on macOS
}