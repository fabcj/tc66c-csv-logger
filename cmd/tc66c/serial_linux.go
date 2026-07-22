//go:build linux

package main

import (
	"errors"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

// serialPort is a minimal raw termios-based serial port, implemented with
// only the standard library (no go.bug.st/serial, no golang.org/x/sys).
type serialPort struct {
	fd int
}

// CBAUD isn't exported by the standard library's syscall package (it's a
// historical omission - present in the kernel/glibc headers as 0010017 in
// asm-generic/termbits.h), so it's defined locally.
const linuxCBAUD = 0010017

// openSerialPort initializes and configures a raw serial connection.
func openSerialPort(path string, baud uint32, readTimeout time.Duration) (*serialPort, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, errors.New("open " + path + ": " + err.Error())
	}

	var raw syscall.Termios
	if err := termiosIoctl(fd, syscall.TCGETS, &raw); err != nil {
		syscall.Close(fd)
		return nil, errors.New("TCGETS on " + path + ": " + err.Error())
	}

	baudBits, err := linuxBaudBits(baud)
	if err != nil {
		syscall.Close(fd)
		return nil, err
	}

	// Equivalent to cfmakeraw(): 8N1, no software/hardware flow control, no
	// canonical/line-editing mode, no signal-generating characters, and no
	// output post-processing - just a clean byte pipe to the device.
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB | uint32(linuxCBAUD)
	raw.Cflag |= syscall.CS8 | syscall.CLOCAL | syscall.CREAD | baudBits
	raw.Ispeed = baudBits
	raw.Ospeed = baudBits

	// VMIN=0, VTIME=N: each read() blocks for up to N deciseconds and
	// returns with whatever showed up (0 bytes on timeout). This gives
	// ReadFull's retry loop the same per-attempt timeout behavior the
	// previous go.bug.st/serial-based SetReadTimeout provided.
	raw.Cc[syscall.VMIN] = 0
	tenths := readTimeout / (100 * time.Millisecond)
	if tenths > 255 {
		tenths = 255
	}
	raw.Cc[syscall.VTIME] = uint8(tenths)

	if err := termiosIoctl(fd, syscall.TCSETS, &raw); err != nil {
		syscall.Close(fd)
		return nil, errors.New("TCSETS on " + path + ": " + err.Error())
	}

	return &serialPort{fd: fd}, nil
}

// termiosIoctl applies terminal IO control commands securely.
func termiosIoctl(fd int, req uintptr, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}

// linuxBaudBits maps the requested baud rate to the appropriate Linux syscall constant.
func linuxBaudBits(baud uint32) (uint32, error) {
	switch baud {
	case 9600:
		return syscall.B9600, nil
	case 19200:
		return syscall.B19200, nil
	case 38400:
		return syscall.B38400, nil
	case 57600:
		return syscall.B57600, nil
	case 115200:
		return syscall.B115200, nil
	default:
		return 0, errors.New("unsupported baud rate: " + strconv.FormatUint(uint64(baud), 10))
	}
}

// Read wraps the underlying syscall Read logic.
func (p *serialPort) Read(b []byte) (int, error) {
	return syscall.Read(p.fd, b)
}

// Write wraps the underlying syscall Write logic.
func (p *serialPort) Write(b []byte) (int, error) {
	return syscall.Write(p.fd, b)
}

// Close releases the file descriptor safely.
func (p *serialPort) Close() error {
	return syscall.Close(p.fd)
}
