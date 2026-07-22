//go:build windows

package main

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

// serialPort provides a minimal raw Windows handle-based serial connection,
// implemented using low-level Win32 syscalls (kernel32.dll).
type serialPort struct {
	handle syscall.Handle
}

// dcb mirrors the Win32 DCB structure for configuring serial communications.
type dcb struct {
	DCBlength, BaudRate                            uint32
	Flags                                          uint32
	wReserved, XonLim, XoffLim                     uint16
	ByteSize, Parity, StopBits                     uint8
	XonChar, XoffChar, ErrorChar, EOFChar, EvtChar uint8
	wReserved1                                     uint16
}

// commTimeouts mirrors the Win32 COMMTIMEOUTS structure.
type commTimeouts struct {
	ReadIntervalTimeout         uint32
	ReadTotalTimeoutMultiplier  uint32
	ReadTotalTimeoutConstant    uint32
	WriteTotalTimeoutMultiplier uint32
	WriteTotalTimeoutConstant   uint32
}

var (
	modkernel32         = syscall.NewLazyDLL("kernel32.dll")
	procSetCommState    = modkernel32.NewProc("SetCommState")
	procSetCommTimeouts = modkernel32.NewProc("SetCommTimeouts")
)

// openSerialPort initializes and configures a raw serial connection on Windows.
func openSerialPort(path string, baud uint32, readTimeout time.Duration) (*serialPort, error) {
	pathPtr, err := syscall.UTF16PtrFromString(`\\.\` + path)
	if err != nil {
		return nil, err
	}

	h, err := syscall.CreateFile(
		pathPtr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, errors.New("open " + path + ": " + err.Error())
	}

	var dcbStruct dcb
	dcbStruct.DCBlength = uint32(unsafe.Sizeof(dcbStruct))
	dcbStruct.BaudRate = baud
	dcbStruct.ByteSize = 8
	dcbStruct.Parity = 0     // NOPARITY
	dcbStruct.StopBits = 0   // ONESTOPBIT
	dcbStruct.Flags = 0x0001 // fBinary

	r1, _, errLast := procSetCommState.Call(uintptr(h), uintptr(unsafe.Pointer(&dcbStruct)))
	if r1 == 0 {
		syscall.CloseHandle(h)
		return nil, errors.New("SetCommState failed: " + errLast.Error())
	}

	ms := uint32(readTimeout / time.Millisecond)
	timeouts := commTimeouts{
		ReadIntervalTimeout:         ms,
		ReadTotalTimeoutConstant:    ms,
		ReadTotalTimeoutMultiplier:  0,
		WriteTotalTimeoutConstant:   100,
		WriteTotalTimeoutMultiplier: 0,
	}

	r1, _, errLast = procSetCommTimeouts.Call(uintptr(h), uintptr(unsafe.Pointer(&timeouts)))
	if r1 == 0 {
		syscall.CloseHandle(h)
		return nil, errors.New("SetCommTimeouts failed: " + errLast.Error())
	}

	return &serialPort{handle: h}, nil
}

// Read wraps the low-level Windows ReadFile syscall.
func (p *serialPort) Read(b []byte) (int, error) {
	var done uint32
	err := syscall.ReadFile(p.handle, b, &done, nil)
	if err != nil {
		return 0, err
	}
	return int(done), nil
}

// Write wraps the low-level Windows WriteFile syscall.
func (p *serialPort) Write(b []byte) (int, error) {
	var done uint32
	err := syscall.WriteFile(p.handle, b, &done, nil)
	if err != nil {
		return 0, err
	}
	return int(done), nil
}

// Close releases the open Windows file handle safely.
func (p *serialPort) Close() error {
	return syscall.CloseHandle(p.handle)
}
