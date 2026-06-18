package protocol

import (
	"encoding/binary"
	"fmt"
	"time"

	"go.bug.st/serial"
)

const (
	BlockSize  = 64
	NumBlocks  = 3
	PacketSize = BlockSize * NumBlocks

	CmdQuery = "query\r\n"
	CmdGetVA = "getva\r\n"
)

type DeviceMode int

const (
	ModeFirmware DeviceMode = iota
	ModeBootloader
	ModeUnknown
)

type TC66C struct {
	port serial.Port
	Mode DeviceMode
}

func NewTC66C(portName string) (*TC66C, error) {
	mode := &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("failed to open port: %w", err)
	}

	if err := port.SetReadTimeout(2 * time.Second); err != nil {
		port.Close()
		return nil, fmt.Errorf("failed to set timeout: %w", err)
	}

	tc := &TC66C{port: port, Mode: ModeUnknown}

	if err := tc.WriteCommand([]byte(CmdQuery)); err == nil {
		response := make([]byte, 4)
		if err := tc.ReadFull(response); err == nil {
			switch string(response) {
			case "firm":
				tc.Mode = ModeFirmware
			case "boot":
				tc.Mode = ModeBootloader
			}
		}
	}

	return tc, nil
}

func (tc *TC66C) Close() error {
	return tc.port.Close()
}

func (tc *TC66C) WriteCommand(cmd []byte) error {
	_, err := tc.port.Write(cmd)
	time.Sleep(50 * time.Millisecond)
	return err
}

func (tc *TC66C) ReadFull(buf []byte) error {
	total := 0
	for total < len(buf) {
		n, err := tc.port.Read(buf[total:])
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("read timeout")
		}
		total += n
	}
	return nil
}

func (tc *TC66C) GetReading() (*Reading, error) {
	if tc.Mode != ModeFirmware {
		return nil, fmt.Errorf("device not in firmware mode")
	}

	if err := tc.WriteCommand([]byte(CmdGetVA)); err != nil {
		return nil, err
	}

	encrypted := make([]byte, PacketSize)
	if err := tc.ReadFull(encrypted); err != nil {
		return nil, err
	}

	decrypted := make([]byte, PacketSize)
	if err := DecryptPacketInPlace(encrypted, decrypted); err != nil {
		return nil, err
	}

	reading := &Reading{}
	if err := ParseZeroAlloc(decrypted, reading); err != nil {
		return nil, err
	}
	return reading, nil
}

type Reading struct {
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

func CSVHeader() string {
	return "timestamp,voltage_v,current_a,power_w,resistance_ohm,capacity_mah,energy_mwh,dplus_v,dminus_v,temperature_c"
}

func ParseZeroAlloc(data []byte, r *Reading) error {
	pac1 := data[0:64]
	pac2 := data[64:128]

	if !VerifyChecksum(pac1[0:60], binary.LittleEndian.Uint16(pac1[60:62])) ||
		!VerifyChecksum(pac2[0:60], binary.LittleEndian.Uint16(pac2[60:62])) {
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