package main

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

var AESKey = []byte{
	0x58, 0x21, 0xfa, 0x56, 0x01, 0xb2, 0xf0, 0x26,
	0x87, 0xff, 0x12, 0x04, 0x62, 0x2a, 0x4f, 0xb0,
	0x86, 0xf4, 0x02, 0x60, 0x81, 0x6f, 0x9a, 0x0b,
	0xa7, 0xf1, 0x06, 0x61, 0x9a, 0xb8, 0x72, 0x88,
}

var (
	cipherBlock cipher.Block
	crc16Table  [256]uint16
)

func init() {
	var err error
	cipherBlock, err = aes.NewCipher(AESKey)
	if err != nil {
		panic(fmt.Sprintf("[ERR] Failed to initialize AES cipher: %v", err))
	}

	for i := 0; i < 256; i++ {
		crc := uint16(i)
		for j := 0; j < 8; j++ {
			if crc&0x0001 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
		crc16Table[i] = crc
	}
}

func DecryptPacket(encrypted, decrypted []byte) error {
	if len(encrypted) < PACKET_SIZE || len(decrypted) < PACKET_SIZE {
		return fmt.Errorf("invalid packet size: got %d, want %d", len(encrypted), PACKET_SIZE)
	}

	blockSize := cipherBlock.BlockSize()
	for i := 0; i < PACKET_SIZE; i += blockSize {
		cipherBlock.Decrypt(decrypted[i:i+blockSize], encrypted[i:i+blockSize])
	}
	return nil
}

func VerifyCheckSum(data []byte, excpectedCRC uint16) bool {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = (crc >> 8) ^ crc16Table[byte(crc)^b]
	}
	return crc == excpectedCRC
}
