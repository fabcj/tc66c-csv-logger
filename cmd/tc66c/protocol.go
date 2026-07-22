package main

import (
	"encoding/binary"
	"errors"
	"time"
)

// Static errors to guarantee zero heap allocation during high-frequency polling failures.
var (
	ErrReadTimeout     = errors.New("read timeout")
	ErrNotFirmwareMode = errors.New("device not in firmware mode")
	ErrChecksumFailed  = errors.New("checksum failed")
)

// DeviceMode tracks the current hardware state of the TC66C.
type DeviceMode int

const (
	MODE_FIRMWARE DeviceMode = iota
	MODE_BOOTLOADER
	MODE_UNKNOWN
)

const (
	BLOCK_SIZE  = 64
	NUM_BLOCKS  = 3
	PACKET_SIZE = BLOCK_SIZE * NUM_BLOCKS

	CMD_QUERY  = "query\r\n"
	CMD_GET_VA = "getva\r\n"
)

var cmdGetVA = []byte(CMD_GET_VA)

// TC66C represents a connection to the hardware USB meter.
type TC66C struct {
	port *serialPort
	Mode DeviceMode

	// Pre-allocated buffers prevent memory leaks during continuous polling
	encBuf  [PACKET_SIZE]byte
	decBuf  [PACKET_SIZE]byte
	reading powerReading
}

// powerReading holds the parsed telemetry data.
type powerReading struct {
	Voltage     float64
	Current     float64
	Power       float64
	Resistance  float64
	CapacitymAh uint32
	EnergymWh   uint32
	DPlus       float64
	DMinus      float64
	Temperature float64
}

// NewTC66C initializes the connection to the USB meter and determines its mode.
func NewTC66C(portName string) (*TC66C, error) {
	// Reduced timeout to 300ms to recover rapidly if reads fail
	port, err := openSerialPort(portName, 115200, 300*time.Millisecond)
	if err != nil {
		// Return the error directly to avoid fmt.Errorf allocations
		return nil, err
	}

	tc := &TC66C{
		port: port,
		Mode: MODE_UNKNOWN,
	}

	if err := tc.WriteCommand([]byte(CMD_QUERY)); err == nil {
		time.Sleep(10 * time.Millisecond)
		var res [4]byte
		if err := tc.ReadFull(res[:]); err == nil {
			switch string(res[:]) {
			case "firm":
				tc.Mode = MODE_FIRMWARE
			case "boot":
				tc.Mode = MODE_BOOTLOADER
			default:
				tc.Mode = MODE_UNKNOWN
			}
		}
	}

	return tc, nil
}

// TCClose securely shuts down the serial port connection.
func (tc *TC66C) TCClose() error {
	return tc.port.Close()
}

// WriteCommand sends a raw byte command to the hardware.
func (tc *TC66C) WriteCommand(cmd []byte) error {
	_, err := tc.port.Write(cmd)
	return err
}

// ReadFull blocks until the provided buffer is entirely filled or a timeout occurs.
func (tc *TC66C) ReadFull(buff []byte) error {
	total := 0
	for total < len(buff) {
		n, err := tc.port.Read(buff[total:])
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrReadTimeout
		}
		total += n
	}

	return nil
}

// GetReading requests, decrypts, and parses the latest telemetry snapshot.
func (tc *TC66C) GetReading() (*powerReading, error) {
	if tc.Mode != MODE_FIRMWARE {
		return nil, ErrNotFirmwareMode
	}

	if err := tc.WriteCommand(cmdGetVA); err != nil {
		return nil, err
	}

	// Essential 10ms hardware settling time for TC66C MCU
	time.Sleep(10 * time.Millisecond)

	encrypted := tc.encBuf[:]
	if err := tc.ReadFull(encrypted); err != nil {
		return nil, err
	}

	decrypted := tc.decBuf[:]
	if err := DecryptPacket(encrypted, decrypted); err != nil {
		return nil, err
	}

	if err := ParseZeroAlloc(decrypted, &tc.reading); err != nil {
		return nil, err
	}

	return &tc.reading, nil
}

// ParseZeroAlloc translates the raw decrypted binary directly into the powerReading struct.
func ParseZeroAlloc(data []byte, r *powerReading) error {
	pac1 := data[0:64]
	pac2 := data[64:128]

	if !VerifyCheckSum(pac1[0:60], binary.LittleEndian.Uint16(pac1[60:62])) ||
		!VerifyCheckSum(pac2[0:60], binary.LittleEndian.Uint16(pac2[60:62])) {
		return ErrChecksumFailed
	}

	r.Voltage = float64(binary.LittleEndian.Uint32(pac1[48:52])) * 1e-4
	r.Current = float64(binary.LittleEndian.Uint32(pac1[52:56])) * 1e-5
	r.Power = float64(binary.LittleEndian.Uint32(pac1[56:60])) * 1e-4

	r.Resistance = float64(binary.LittleEndian.Uint32(pac2[4:8])) * 1e-1

	r.CapacitymAh = binary.LittleEndian.Uint32(pac2[8:12])
	r.EnergymWh = binary.LittleEndian.Uint32(pac2[12:16])

	r.DPlus = float64(binary.LittleEndian.Uint32(pac2[32:36])) * 1e-2
	r.DMinus = float64(binary.LittleEndian.Uint32(pac2[36:40])) * 1e-2

	tempRaw := binary.LittleEndian.Uint32(pac2[28:32])
	if binary.LittleEndian.Uint32(pac2[24:28]) != 0 {
		r.Temperature = -float64(tempRaw)
	} else {
		r.Temperature = float64(tempRaw)
	}

	return nil
}
