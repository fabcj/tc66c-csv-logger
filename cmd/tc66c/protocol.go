package main

import (
	"encoding/binary"
	"fmt"
	"time"

	"go.bug.st/serial"
)

type DeviceMode int

type TC66C struct {
	port 	serial.Port
	Mode 	DeviceMode
}

type powerReading struct {
	Voltage     	float64
	Current     	float64
	Power       	float64
	Resistance  	float64
	CapacitymAh 	uint32
	EnergymWh   	uint32
	DPlus       	float64
	DMinus      	float64
	Temperature 	float64
}

const (
	BLOCK_SIZE  = 64
	NUM_BLOCKS  = 3
	PACKET_SIZE = BLOCK_SIZE * NUM_BLOCKS

	CMD_QUERY  = "query\r\n"
	CMD_GET_VA = "getva\r\n"
)

const (
	MODE_FIRMWARE DeviceMode = iota
	MODE_BOOTLOADER
	MODE_UNKOWN
)


func NewTC66C(portName string) (*TC66C, error) {
	mode := &serial.Mode{
		BaudRate: 115200,
		Parity:   serial.NoParity,
		DataBits: 8,
		StopBits: serial.OneStopBit,
	}

	// Open port
	port, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("[ERR]: Failed to open port %w", err)
	}

	// Verify SetReadTimeout
	if err := port.SetReadTimeout(2 * time.Second); err != nil {
		port.Close()
		return nil, fmt.Errorf("[ERR]: Failed to set timeout: %w", err)
	}

	tc := &TC66C{
		port: 	port,
		Mode:	 MODE_UNKOWN,
	}

	if err := tc.WriteCommand([]byte(CMD_QUERY)); err == nil {
		var res = make([]byte, 4)
		if err := tc.ReadFull(res); err == nil {
			switch string(res) {
			case "firm":
				tc.Mode = MODE_FIRMWARE
			case "boot":
				tc.Mode = MODE_BOOTLOADER
			default:
				tc.Mode = MODE_UNKOWN
			}
		}
	}

	return tc, nil
}

func (tc *TC66C) TCClose() error {
	return tc.port.Close()
}

func (tc *TC66C) WriteCommand(cmd []byte) error {
	_, err := tc.port.Write(cmd)
	time.Sleep(50 * time.Millisecond)
	return err
}

func (tc *TC66C) ReadFull(buff []byte) error {
	total := 0
	for total < len(buff) {
		n, err := tc.port.Read(buff[total:])

		if err != nil {
			return err
		}

		if n == 0 {
			return fmt.Errorf("Read timeout")
		}
		total += n
	}
	return nil
}

func (tc *TC66C) GetReading() (*powerReading, error) {
	if tc.Mode != MODE_FIRMWARE {
		return nil, fmt.Errorf("device not in firmware mode")
	}

	if err := tc.WriteCommand([]byte(CMD_GET_VA)); err != nil {
		return nil, err
	}

	encrypted := make([]byte, PACKET_SIZE)
	if err := tc.ReadFull(encrypted); err != nil {
		return nil, err
	}

	decrypted := make([]byte, PACKET_SIZE)
	if err := DecryptPacket(encrypted, decrypted); err != nil {
		return nil, err
	}

	reading := &powerReading{}
	if err := ParseZeroAlloc(decrypted, reading); err != nil {
		return nil, err
	}
	return reading, nil
}

// Parses without allocation for optimized battery storage
func ParseZeroAlloc(data []byte, r *powerReading) error {
	pac1 := data[0:64]
	pac2 := data[64:128]

	if !VerifyCheckSum(pac1[0:60], binary.LittleEndian.Uint16(pac1[60:62])) ||
		!VerifyCheckSum(pac2[0:60], binary.LittleEndian.Uint16(pac2[60:62])) {
		return fmt.Errorf("checksum failed")
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