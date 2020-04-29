package ipaPng

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

// Each chunk starts with a uint32 length (big endian), then 4 byte name,
// then data and finally the CRC32 of the chunk data.
type Chunk struct {
	Length uint32 // chunk data length
	CType  string // chunk type
	Data   []byte // chunk data
	Crc32  uint32 // CRC32 of chunk data
	crc    hash.Hash32
}

// Populate will read bytes from the reader and populate a chunk.
func (c *Chunk) Populate(r io.Reader) error {

	// 4 byte
	buf := make([]byte, 4)
	// Read first four bytes == chunk length.
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	// Convert bytes to int.
	c.Length = binary.BigEndian.Uint32(buf)

	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	c.CType = string(buf)
	c.crc.Reset()
	c.crc.Write(buf)

	// Read chunk data.
	tmp := make([]byte, c.Length)
	if _, err := io.ReadFull(r, tmp); err != nil {
		return err
	}
	c.Data = tmp
	c.crc.Write(c.Data)
	// Read CRC32 hash
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	c.Crc32 = binary.BigEndian.Uint32(buf)
	sum32 := c.crc.Sum32()
	if c.Crc32 != sum32 {
		fmt.Printf("Crc32:%v, sum crc32:%v\n", c.Crc32, sum32)
		return errors.New(fmt.Sprintf("invalid checksum CType:%v", c.CType))
	}
	return nil
}
