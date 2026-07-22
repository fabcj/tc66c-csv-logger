//go:build darwin

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
//
// Unlike Linux, BSD/Darwin termios doesn't encode the baud rate as bit flags
// packed into Cflag - Ispeed/Ospeed just hold the literal rate (that's why
// syscall.B115200 on darwin is simply the integer 115200), so there's no
// CBAUD-style masking needed here.
type serialPort struct {
	fd int
}

// openSerialPort initializes and configures a raw serial connection.
func openSerialPort(path string, baud uint32, readTimeout time.Duration) (*serialPort, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, errors.New("open " + path + ": " + err.Error())
	}

	var raw syscall.Termios
	if err := termiosIoctl(fd, syscall.TIOCGETA, &raw); err != nil {
		syscall.Close(fd)
		return nil, errors.New("TIOCGETA on " + path + ": " + err.Error())
	}

	speed, err := darwinBaudSpeed(baud)
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
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8 | syscall.CLOCAL | syscall.CREAD
	raw.Ispeed = speed
	raw.Ospeed = speed

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

	if err := termiosIoctl(fd, syscall.TIOCSETA, &raw); err != nil {
		syscall.Close(fd)
		return nil, errors.New("TIOCSETA on " + path + ": " + err.Error())
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

// darwinBaudSpeed maps the requested baud rate to the appropriate Darwin constant.
func darwinBaudSpeed(baud uint32) (uint64, error) {
	switch baud {
	case 9600, 19200, 38400, 57600, 115200:
		return uint64(baud), nil
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
