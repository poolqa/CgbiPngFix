package ipaPng

import (
	"bytes"
	"hash/crc32"
	"io"
)

// Decode reads a PNG image from r and returns it as an image.Image.
// The type of Image returned depends on the PNG contents.
func Decode(r *bytes.Reader) (*IpaPNG, error) {
	cgbi := &IpaPNG{
		r:   r,
		crc: crc32.NewIEEE(),
		IDAT: []byte{120, 156}, // default set zlib header
	}
	if err := cgbi.checkHeader(); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	stage := dsStart
	for stage != dsSeenIEND {
		c := Chunk{
			crc: crc32.NewIEEE(),
		}
		err := (&c).Populate(cgbi.r)
		if err != nil {
			return nil, err
		}
		// Drop the last empty chunk.
		if c.CType != "" {
			cgbi.chunks = append(cgbi.chunks, &c)
		}
		stage = c.CType
	}

	//do parse chunk
	err := cgbi.parseChunk()
	if err != nil {
		return nil, err
	}
	return cgbi, nil
}