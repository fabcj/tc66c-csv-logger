//go:build linux

package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

func CPUHeader() string {
	return ",cpu_temp_c,cpu_usage_pct,cpu_freq_mhz,cpu_total_cycles,cpu_target_cycles,attributed_watts\n"
}

// Static Adress for Raspberry Pi 4 Model B
const (
	thermalZonePath  = "/sys/class/thermal/thermal_zone0/temp"
	cpuFreqPath      = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq"
	procStatPath     = "/proc/stat"
	cgroupV2BasePath = "/sys/fs/cgroup"
)

type cpuReading struct {
	TemperatureC float64
	UsagePercent float64
	FreqMHz      float64
	TotalCycles  uint64
	TargetCycles uint64
}

type Monitor struct {
	prevIdle      uint64
	prevTotal     uint64
	primed        bool
	targetPID     int
	targetFd      int
	systemFds     []int
	prevSysCyc    uint64
	prevTgtCyc    uint64
	cgroupStatPath string
	prevCgroupUsec uint64
	cgroupTicksPerCycle float64
}

type perfEventAttr struct {
	Type         uint32
	Size         uint32
	Config       uint64
	SamplePeriod uint64
	SampleType   uint64
	ReadFormat   uint64
	Bits         uint64
	WakeupEvents uint32
	BpType       uint32
	BpAddr       uint64
	BpLen        uint64
}

const (
	perfTypeHardware          = 0
	perfCountHWCPUCycles      = 0
	perfFlagExcludeKernel     = 1 << 2
	perfFlagExcludeHypervisor = 1 << 4
)

func openCycleCounter(pid, cpu int) (int, error) {
	attr := perfEventAttr{
		Type:   perfTypeHardware,
		Size:   uint32(unsafe.Sizeof(perfEventAttr{})),
		Config: perfCountHWCPUCycles,
		Bits:   perfFlagExcludeKernel | perfFlagExcludeHypervisor,
	}
	fd, _, errno := unix.Syscall6(
		unix.SYS_PERF_EVENT_OPEN,
		uintptr(unsafe.Pointer(&attr)),
		uintptr(pid),
		uintptr(cpu),
		^uintptr(0),
		0,
		0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func resolveCgroupPath(serviceName string) (string, error) {
	candidates := []string{
		filepath.Join(cgroupV2BasePath, "system.slice", serviceName+".service", "cpu.stat"),
		filepath.Join(cgroupV2BasePath, "system.slice", serviceName, "cpu.stat"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("cgroup cpu.stat not found for service %q; is it running under systemd with cgroup v2?", serviceName)
}

func readCgroupUsec(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if err := scanner.Err(); err != nil {
    	fmt.Printf("usage_usec not found in %s", err)
	}

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "usage_usec" {
			return strconv.ParseUint(fields[1], 10, 64)
		}
	}
	return 0, fmt.Errorf("usage_usec not found in %s", path)
}

func readCPUFreqHz() (float64, error) {
	data, err := os.ReadFile(cpuFreqPath)
	if err != nil {
		return 0, err
	}
	khz, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, err
	}
	return khz * 1000.0, nil
}

func NewCPUMonitor(targetPID int, serviceName string) *Monitor {
	m := &Monitor{
		targetPID: targetPID,
		targetFd:  -1,
	}

	numCPU := 0
	for {
		fd, err := openCycleCounter(-1, numCPU)
		if err != nil {
			break
		}
		m.systemFds = append(m.systemFds, fd)
		numCPU++
	}

	switch {
	case serviceName != "":
		path, err := resolveCgroupPath(serviceName)
		if err == nil {
			m.cgroupStatPath = path
		}

	case targetPID > 0:
		fd, err := openCycleCounter(targetPID, -1)
		if err == nil {
			m.targetFd = fd
		}
	}

	return m
}

func (m *Monitor) Prime() error {
	idle, total, err := readStat()
	if err != nil {
		return err
	}
	m.prevIdle = idle
	m.prevTotal = total
	m.primed = true

	if m.cgroupStatPath != "" {
		usec, err := readCgroupUsec(m.cgroupStatPath)
		if err == nil {
			m.prevCgroupUsec = usec
		}
		freqHz, err := readCPUFreqHz()
		if err == nil && freqHz > 0 {
			m.cgroupTicksPerCycle = 1e6 / freqHz
		}
	}

	return nil
}

func readFdCounter(fd int) (uint64, error) {
	var buf [8]byte
	n, err := unix.Read(fd, buf[:])
	if err != nil || n != 8 {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf[:]), nil
}

func (m *Monitor) Sample() (*cpuReading, error) {
	temp, _ := readTemperature()
	freq, _ := readFrequency()
	usage, _ := m.readUsage()

	var totalSysCycles uint64
	for _, fd := range m.systemFds {
		val, err := readFdCounter(fd)
		if err == nil {
			totalSysCycles += val
		}
	}

	var targetCycles uint64

	switch {
	case m.cgroupStatPath != "":
		usec, err := readCgroupUsec(m.cgroupStatPath)
		if err == nil {
			deltaUsec := usec - m.prevCgroupUsec
			m.prevCgroupUsec = usec
			freqHz, err := readCPUFreqHz()
			if err == nil && freqHz > 0 {
				targetCycles = uint64(float64(deltaUsec) * freqHz / 1e6)
			} else if m.cgroupTicksPerCycle > 0 {
				targetCycles = uint64(float64(deltaUsec) / m.cgroupTicksPerCycle)
			}
		}

	case m.targetFd > 0:
		val, err := readFdCounter(m.targetFd)
		if err == nil {
			targetCycles = val
		}
	}

	deltaSys := totalSysCycles - m.prevSysCyc
	deltaTgt := targetCycles - m.prevTgtCyc

	m.prevSysCyc = totalSysCycles
	m.prevTgtCyc = targetCycles

	return &cpuReading{
		TemperatureC: temp,
		UsagePercent: usage,
		FreqMHz:      freq,
		TotalCycles:  deltaSys,
		TargetCycles: deltaTgt,
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

	if usage < 0 {
		usage = 0
	} else if usage > 100 {
		usage = 100
	}

	return usage, nil
}

func (m *Monitor) CPUClose() {
	if m.targetFd > 0 {
		unix.Close(m.targetFd)
	}
	for _, fd := range m.systemFds {
		unix.Close(fd)
	}
}