//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// Define package-level static errors to avoid heap allocations during parsing and reading.
var (
	ErrNoIntDigits  = errors.New("no integer digits found")
	ErrThermalFile  = errors.New("thermal file unavailable")
	ErrCPUFreqFile  = errors.New("cpufreq file unavailable")
	ErrCgroupFile   = errors.New("cgroup file unavailable")
	ErrUsageUsec    = errors.New("usage_usec not found")
	ErrProcStatFile = errors.New("procStat file unavailable")
)

const (
	thermalZonePath  = "/sys/class/thermal/thermal_zone0/temp"
	cpuFreqPath      = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq"
	procStatPath     = "/proc/stat"
	cgroupV2BasePath = "/sys/fs/cgroup"
)

const (
	perfTypeHardware          = 0
	perfCountHWCPUCycles      = 0
	perfFlagExcludeKernel     = 1 << 2
	perfFlagExcludeHypervisor = 1 << 4
)

// cpuReading holds the parsed hardware telemetry.
type cpuReading struct {
	TemperatureC float64
	UsagePercent float64
	FreqMHz      float64
	TotalCycles  uint64
	TargetCycles uint64
}

// Monitor manages file descriptors and state for zero-allocation CPU tracking.
type Monitor struct {
	prevIdle            uint64
	prevTotal           uint64
	primed              bool
	targetPID           int
	targetFd            int
	systemFds           []int
	prevSysCyc          uint64
	prevTgtCyc          uint64
	cgroupStatPath      string
	prevCgroupUsec      uint64
	cgroupTicksPerCycle float64

	// Open File Handles held open for persistent zero-alloc reads
	thermalFile  *os.File
	cpuFreqFile  *os.File
	procStatFile *os.File
	cgroupFile   *os.File

	// Reusable byte buffer for sysfs/procfs reads (zero heap allocations)
	readBuf [1024]byte
	reading cpuReading
}

// perfEventAttr maps to the Linux kernel perf_event_attr struct.
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

// CPUHeader returns the standard column names for the CPU data.
func CPUHeader() string {
	return ",cpu_temp_c,cpu_usage_pct,cpu_freq_mhz,cpu_total_cycles,cpu_target_cycles\n"
}

// openCycleCounter uses raw syscalls to open hardware perf event counters.
func openCycleCounter(pid, cpu int) (int, error) {
	attr := perfEventAttr{
		Type:   perfTypeHardware,
		Size:   uint32(unsafe.Sizeof(perfEventAttr{})),
		Config: perfCountHWCPUCycles,
		Bits:   perfFlagExcludeKernel | perfFlagExcludeHypervisor,
	}
	fd, _, errno := syscall.Syscall6(
		syscall.SYS_PERF_EVENT_OPEN,
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

// resolveCgroupPath dynamically locates the systemd cgroup path for the target service.
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
	// This error is only generated once at startup, so standard string concatenation is fine.
	return "", errors.New("cgroup cpu.stat not found for service: " + serviceName)
}

// NewCPUMonitor constructs and initializes a new hardware monitor.
func NewCPUMonitor(targetPID int, serviceName string) *Monitor {
	m := &Monitor{
		targetPID: targetPID,
		targetFd:  -1,
	}

	// Open sysfs / procfs files ONCE during initialization
	m.thermalFile, _ = os.Open(thermalZonePath)
	m.cpuFreqFile, _ = os.Open(cpuFreqPath)
	m.procStatFile, _ = os.Open(procStatPath)

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
			m.cgroupFile, _ = os.Open(path)
		}
	case targetPID > 0:
		fd, err := openCycleCounter(targetPID, -1)
		if err == nil {
			m.targetFd = fd
		}
	}

	return m
}

// Prime reads initial values to establish the baseline for delta calculations.
func (m *Monitor) Prime() error {
	idle, total, err := m.readStat()
	if err != nil {
		return err
	}
	m.prevIdle = idle
	m.prevTotal = total
	m.primed = true

	if m.cgroupFile != nil {
		usec, err := m.readCgroupUsec()
		if err == nil {
			m.prevCgroupUsec = usec
		}
		freqHz, err := m.readCPUFreqHz()
		if err == nil && freqHz > 0 {
			m.cgroupTicksPerCycle = 1e6 / freqHz
		}
	}

	return nil
}

// parseUintBytes is a fast inline byte-slice parser avoiding string allocations.
func parseUintBytes(b []byte) (uint64, error) {
	var n uint64
	var found bool
	for _, c := range b {
		if c >= '0' && c <= '9' {
			n = n*10 + uint64(c-'0')
			found = true
		} else if found {
			break
		}
	}
	if !found {
		return 0, ErrNoIntDigits
	}
	return n, nil
}

// readTemperature reads and parses the hardware thermal zone sensor.
func (m *Monitor) readTemperature() (float64, error) {
	if m.thermalFile == nil {
		return 0, ErrThermalFile
	}
	n, err := m.thermalFile.ReadAt(m.readBuf[:], 0)
	if err != nil && n == 0 {
		return 0, err
	}
	milliC, err := parseUintBytes(m.readBuf[:n])
	if err != nil {
		return 0, err
	}
	return float64(milliC) / 1000.0, nil
}

// readFrequency reads the current CPU scaling frequency.
func (m *Monitor) readFrequency() (float64, error) {
	if m.cpuFreqFile == nil {
		return 0, ErrCPUFreqFile
	}
	n, err := m.cpuFreqFile.ReadAt(m.readBuf[:], 0)
	if err != nil && n == 0 {
		return 0, err
	}
	khz, err := parseUintBytes(m.readBuf[:n])
	if err != nil {
		return 0, err
	}
	return float64(khz) / 1000.0, nil
}

// readCPUFreqHz converts the hardware frequency into raw Hertz.
func (m *Monitor) readCPUFreqHz() (float64, error) {
	mhz, err := m.readFrequency()
	if err != nil {
		return 0, err
	}
	return mhz * 1e6, nil
}

// readCgroupUsec reads the raw microsecond CPU usage for the designated cgroup.
func (m *Monitor) readCgroupUsec() (uint64, error) {
	if m.cgroupFile == nil {
		return 0, ErrCgroupFile
	}
	n, err := m.cgroupFile.ReadAt(m.readBuf[:], 0)
	if err != nil && n == 0 {
		return 0, err
	}
	data := m.readBuf[:n]
	key := []byte("usage_usec")
	idx := bytes.Index(data, key)
	if idx == -1 {
		return 0, ErrUsageUsec
	}
	return parseUintBytes(data[idx+len(key):])
}

// readStat processes /proc/stat to determine overall system CPU idle and total times.
func (m *Monitor) readStat() (idle uint64, total uint64, err error) {
	if m.procStatFile == nil {
		return 0, 0, ErrProcStatFile
	}
	n, err := m.procStatFile.ReadAt(m.readBuf[:], 0)
	if err != nil && n == 0 {
		return 0, 0, err
	}
	data := m.readBuf[:n]
	idx := bytes.IndexByte(data, '\n')
	if idx == -1 {
		idx = len(data)
	}
	line := data[:idx]

	i := 0
	for i < len(line) && (line[i] == 'c' || line[i] == 'p' || line[i] == 'u' || line[i] == ' ') {
		i++
	}

	fieldIdx := 0
	for i < len(line) {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}
		var val uint64
		var found bool
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			val = val*10 + uint64(line[i]-'0')
			i++
			found = true
		}
		if found {
			total += val
			if fieldIdx == 3 || fieldIdx == 4 { // idle or iowait
				idle += val
			}
			fieldIdx++
		}
	}
	return idle, total, nil
}

// readFdCounter reads the binary value directly out of a kernel perf event file descriptor.
func readFdCounter(fd int) (uint64, error) {
	var buf [8]byte
	n, err := syscall.Read(fd, buf[:])
	if err != nil || n != 8 {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf[:]), nil
}

// Sample collects a complete set of CPU telemetry metrics.
func (m *Monitor) Sample() (*cpuReading, error) {
	temp, _ := m.readTemperature()
	freq, _ := m.readFrequency()
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
	case m.cgroupFile != nil:
		usec, err := m.readCgroupUsec()
		if err == nil {
			deltaUsec := usec - m.prevCgroupUsec
			m.prevCgroupUsec = usec
			freqHz, err := m.readCPUFreqHz()
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

	m.reading = cpuReading{
		TemperatureC: temp,
		UsagePercent: usage,
		FreqMHz:      freq,
		TotalCycles:  deltaSys,
		TargetCycles: deltaTgt,
	}
	return &m.reading, nil
}

// readUsage calculates CPU load percent based on total vs idle deltas.
func (m *Monitor) readUsage() (float64, error) {
	idle, total, err := m.readStat()
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

// CPUClose safely disposes of all open file descriptors.
func (m *Monitor) CPUClose() {
	if m.thermalFile != nil {
		m.thermalFile.Close()
	}
	if m.cpuFreqFile != nil {
		m.cpuFreqFile.Close()
	}
	if m.procStatFile != nil {
		m.procStatFile.Close()
	}
	if m.cgroupFile != nil {
		m.cgroupFile.Close()
	}
	if m.targetFd > 0 {
		syscall.Close(m.targetFd)
	}
	for _, fd := range m.systemFds {
		syscall.Close(fd)
	}
}
