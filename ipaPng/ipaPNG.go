package ipaPng

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"image"
	"image/color"
	"image/png"
	"io"
)

// 89 50 4E 47 0D 0A 1A 0A
var pngHeader = "\x89\x50\x4E\x47\x0D\x0A\x1A\x0A"
var iHDRLength uint32 = 13

const (
	dsStart    = ""
	dsSeenCgBI = "CgBI"
	dsSeenIHDR = "IHDR"
	dsSeenIDAT = "IDAT"
	dsSeenIEND = "IEND"
)

// Color type, as per the PNG spec.
const (
	ctGrayscale      = 0
	ctTrueColor      = 2
	ctPaletted       = 3
	ctGrayscaleAlpha = 4
	ctTrueColorAlpha = 6
)

// A cb is a combination of color type and bit depth.
const (
	cbInvalid = false
	cbValid   = true
)

// Filter type, as per the PNG spec.
const (
	ftNone    = 0
	ftSub     = 1
	ftUp      = 2
	ftAverage = 3
	ftPaeth   = 4
	nFilter   = 5
)

// Interlace type.
const (
	itNone  = 0
	itAdam7 = 1
)

// interlaceScan defines the placement and size of a pass for Adam7 interlacing.
type interlaceScan struct {
	xFactor, yFactor, xOffset, yOffset int
}

// interlacing defines Adam7 interlacing, with 7 passes of reduced images.
// See https://www.w3.org/TR/PNG/#8Interlace
var interlacing = []interlaceScan{
	{8, 8, 0, 0},
	{8, 8, 4, 0},
	{4, 8, 0, 4},
	{4, 4, 2, 0},
	{2, 4, 0, 2},
	{2, 2, 1, 0},
	{1, 2, 0, 1},
}

var chunkOrderError = errors.New("chunk out of order")

type IpaPNG struct {
	Img               image.Image
	r                 io.ReadSeeker
	crc               hash.Hash32
	IsCgBI            bool
	width             int
	height            int
	depth             int
	bitsPerPixel      int
	interlace         uint32
	colorType         int
	CompressionMethod uint32
	FilterMethod      uint32
	chunks            []*Chunk // Not exported == won't appear in JSON string.
	IDAT              []byte
	idatLength        int
	stage             int
	buf               [8]byte
}

// PrintChunks will return a string containign chunk number, name and the first 20
// bytes of each chunk.
func (cgbi IpaPNG) PrintChunks() string {
	var output string
	for i, c := range cgbi.chunks {
		output += fmt.Sprintf("-----------\n")
		output += fmt.Sprintf("Chunk # %d\n", i)
		output += fmt.Sprintf("Chunk length: %d\n", c.Length)
		output += fmt.Sprintf("Chunk type: %v\n", c.CType)

		limit := 20
		if len(c.Data) < 20 {
			limit = len(c.Data)
		}
		output += fmt.Sprintf("Chunk data (20 bytes): % x\n", c.Data[:limit])
	}
	return output
}

// Parse IHDR chunk.
// https://golang.org/src/image/png/reader.go?#L142 is your friend.
func (cgbi *IpaPNG) parseIHDR(iHDR *Chunk) error {
	if iHDR.Length != iHDRLength {
		errString := fmt.Sprintf("invalid IHDR length: got %cgbi - expected %cgbi",
			iHDR.Length, iHDRLength)
		return errors.New(errString)
	}

	// IHDR: http://www.libpng.org/pub/png/spec/1.2/PNG-Chunks.html#C.IHDR

	// width:              4 bytes
	// height:             4 bytes
	// Bit depth:          1 byte
	// Color type:         1 byte
	// Compression method: 1 byte
	// Filter method:      1 byte
	// Interlace method:   1 byte

	tmp := iHDR.Data

	cgbi.width = int(binary.BigEndian.Uint32(tmp[0:4]))
	if cgbi.width <= 0 {
		errString := fmt.Sprintf("invalid width in iHDR - got %x", tmp[0:4])
		return errors.New(errString)
	}

	cgbi.height = int(binary.BigEndian.Uint32(tmp[4:8]))
	if cgbi.height <= 0 {
		errString := fmt.Sprintf("invalid height in iHDR - got %x", tmp[4:8])
		return errors.New(errString)
	}

	cgbi.depth = int(tmp[8])
	cgbi.colorType = int(tmp[9])
	cb := cbInvalid
	switch cgbi.colorType {
	case ctGrayscale:
		if cgbi.depth == 1 || cgbi.depth == 2 || cgbi.depth == 4 || cgbi.depth == 8 || cgbi.depth == 16 {
			cb = cbValid
		}
		cgbi.bitsPerPixel = cgbi.depth
	case 2:
		if cgbi.depth == 8 || cgbi.depth == 16 {
			cb = cbValid
		}
		cgbi.bitsPerPixel = cgbi.depth * 3
	case 3:
		if cgbi.depth == 1 || cgbi.depth == 2 || cgbi.depth == 4 || cgbi.depth == 8 {
			cb = cbValid
		}
		cgbi.bitsPerPixel = cgbi.depth
	case 4:
		if cgbi.depth == 8 || cgbi.depth == 16 {
			cb = cbValid
		}
		cgbi.bitsPerPixel = cgbi.depth * 2
	case 6:
		if cgbi.depth == 8 || cgbi.depth == 16 {
			cb = cbValid
		}
		cgbi.bitsPerPixel = cgbi.depth * 4
	}
	if cb == cbInvalid {
		return errors.New(fmt.Sprintf("bit depth %cgbi, color type %cgbi", cgbi.depth, cgbi.colorType))
	}

	// Only compression method 0 is supported
	if uint32(tmp[10]) != 0 {
		errString := fmt.Sprintf("invalid compression method - expected 0 - got %x",
			tmp[10])
		return errors.New(errString)
	}
	cgbi.CompressionMethod = uint32(tmp[10])

	// Only filter method 0 is supported
	if uint32(tmp[11]) != 0 {
		errString := fmt.Sprintf("invalid filter method - expected 0 - got %x",
			tmp[11])
		return errors.New(errString)
	}
	cgbi.FilterMethod = uint32(tmp[11])

	// Only interlace methods 0 and 1 are supported
	if uint32(tmp[12]) != 0 && uint32(tmp[12]) != 1 {
		errString := fmt.Sprintf("invalid interlace method - expected 0 or 1 - got %x",
			tmp[12])
		return errors.New(errString)
	}
	cgbi.interlace = uint32(tmp[12])

	return nil
}

func (cgbi *IpaPNG) parseIDAT(IDAT *Chunk) (err error) {
	cgbi.IDAT = append(cgbi.IDAT, IDAT.Data...)
	return
}

func (cgbi *IpaPNG) checkHeader() error {
	_, err := io.ReadFull(cgbi.r, cgbi.buf[:len(pngHeader)])
	if err != nil {
		return err
	}
	if string(cgbi.buf[:len(pngHeader)]) != pngHeader {
		return errors.New("not a PNG file")
	}
	return nil
}

func (cgbi *IpaPNG) parseChunk() error {
	if len(cgbi.chunks) == 0 {
		return errors.New("not got any chunk")
	}

	if cgbi.chunks[0].CType != dsSeenCgBI {
		cgbi.IsCgBI = false
		cgbi.chunks = []*Chunk{}
		cgbi.r.Seek(0, io.SeekStart)
		var err error
		cgbi.Img, err = png.Decode(cgbi.r)
		return err
	}

	stage := dsStart
	for idx := 1; idx < len(cgbi.chunks); idx++ {
		var err error
		chunk := cgbi.chunks[idx]
		// Read the chunk data.
		switch chunk.CType {
		case dsSeenIHDR:
			if stage != dsStart {
				return chunkOrderError
			}
			stage = dsSeenIHDR
			err = cgbi.parseIHDR(chunk)
		case dsSeenIDAT:
			if stage != dsSeenIHDR && stage != dsSeenIDAT {
				return chunkOrderError
			}
			stage = dsSeenIDAT
			err = cgbi.parseIDAT(chunk)
		case dsSeenIEND:
			if stage != dsSeenIDAT {
				return chunkOrderError
			}
			stage = dsSeenIEND
			cgbi.Img, err = cgbi.decode()
		default: // not parse
		}
		if err != nil {
			return err
		}
	}
	if stage != dsSeenIEND {
		return errors.New("the file can not found IEND chunk")
	}
	return nil
}

// decode decodes the IDAT data into an image.
func (cgbi *IpaPNG) decode() (image.Image, error) {
	b := bytes.NewReader(cgbi.IDAT)
	r, err := zlib.NewReader(b)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var img image.Image
	//fmt.Printf("do decode,interlace:%v\n", cgbi.interlace)
	if cgbi.interlace == itNone {
		img, err = cgbi.readImagePass(r, 0, false)
		if err != nil {
			return nil, err
		}
	} else if cgbi.interlace == itAdam7 {
		// Allocate a blank image of the full size.
		img, err = cgbi.readImagePass(nil, 0, true)
		if err != nil {
			return nil, err
		}
		for pass := 0; pass < 7; pass++ {
			imagePass, err := cgbi.readImagePass(r, pass, false)
			if err != nil {
				return nil, err
			}
			if imagePass != nil {
				cgbi.mergePassInto(img, imagePass, pass)
			}
		}
	}

	// Check for EOF, to verify the zlib checksum.
	//n := 0
	//for i := 0; n == 0 && err == nil; i++ {
	//	if i == 100 {
	//		return nil, io.ErrNoProgress
	//	}
	//	n, err = r.Read(cgbi.buf[:1])
	//}
	//if err != nil && err != io.EOF {
	//	return nil, err
	//}
	//if n != 0 {
	//	return nil, errors.New("too much pixel data")
	//}

	return img, nil
}

// readImagePass reads a single image pass, sized according to the pass number.
func (cgbi *IpaPNG) readImagePass(r io.Reader, pass int, allocateOnly bool) (image.Image, error) {
	pixOffset := 0
	var (
		nRgba   *image.NRGBA
		nRgba64 *image.NRGBA64
		img     image.Image
	)
	width, height := cgbi.width, cgbi.height
	if cgbi.interlace == itAdam7 && !allocateOnly {
		p := interlacing[pass]
		// Add the multiplication factor and subtract one, effectively rounding up.
		width = (width - p.xOffset + p.xFactor - 1) / p.xFactor
		height = (height - p.yOffset + p.yFactor - 1) / p.yFactor
		// A PNG image can't have zero width or height, but for an interlaced
		// image, an individual pass might have zero width or height. If so, we
		// shouldn't even read a per-row filter type byte, so return early.
		if width == 0 || height == 0 {
			return nil, nil
		}
	}
	//fmt.Printf("readImagePass width:%v, height:%v, colorType:%v, depth:%v\n", width, height, cgbi.colorType, cgbi.depth)
	if cgbi.depth == 16 {
		nRgba64 = image.NewNRGBA64(image.Rect(0, 0, width, height))
		img = nRgba64
	} else {
		nRgba = image.NewNRGBA(image.Rect(0, 0, width, height))
		img = nRgba
	}

	if allocateOnly {
		return img, nil
	}
	bytesPerPixel := (cgbi.bitsPerPixel + 7) / 8

	// The +1 is for the per-row filter type, which is at cr[0].
	rowSize := 1 + (cgbi.bitsPerPixel*width+7)/8
	// cr and pr are the bytes for the current and previous row.
	cr := make([]uint8, rowSize)
	pr := make([]uint8, rowSize)

	for y := 0; y < height; y++ {
		// Read the decompressed bytes.
		_, err := io.ReadFull(r, cr)
		if err != nil {
			fmt.Printf("readImagePass read error:%v", err)
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil, errors.New("not enough pixel data")
			}
			return nil, err
		}

		// Apply the filter.
		cDat := cr[1:]
		pDat := pr[1:]
		switch cr[0] {
		case ftNone:
			// No-op.
		case ftSub:
			for i := bytesPerPixel; i < len(cDat); i++ {
				cDat[i] += cDat[i-bytesPerPixel]
			}
		case ftUp:
			for i, p := range pDat {
				cDat[i] += p
			}
		case ftAverage:
			// The first column has no column to the left of it, so it is a
			// special case. We know that the first column exists because we
			// check above that width != 0, and so len(cDat) != 0.
			for i := 0; i < bytesPerPixel; i++ {
				cDat[i] += pDat[i] / 2
			}
			for i := bytesPerPixel; i < len(cDat); i++ {
				cDat[i] += uint8((int(cDat[i-bytesPerPixel]) + int(pDat[i])) / 2)
			}
		case ftPaeth:
			filterPaeth(cDat, pDat, bytesPerPixel)
		default:
			return nil, errors.New("bad filter type")
		}

		// Convert from bytes to colors.
		switch cgbi.depth {
		case 1:
			for x := 0; x < width; x += 8 {
				b := cDat[x/8]
				for x2 := 0; x2 < 8 && x+x2 < width; x2++ {
					yCol := (b >> 7) * 0xff
					aCol := uint8(0xff)
					nRgba.SetNRGBA(x+x2, y, color.NRGBA{yCol, yCol, yCol, aCol})
					b <<= 1
				}
			}
		case 2:
			for x := 0; x < width; x += 4 {
				b := cDat[x/4]
				for x2 := 0; x2 < 4 && x+x2 < width; x2++ {
					ycol := (b >> 6) * 0x55
					acol := uint8(0xff)
					nRgba.SetNRGBA(x+x2, y, color.NRGBA{ycol, ycol, ycol, acol})
					b <<= 2
				}
			}
		case 4:
			for x := 0; x < width; x += 2 {
				b := cDat[x/2]
				for x2 := 0; x2 < 2 && x+x2 < width; x2++ {
					ycol := (b >> 4) * 0x11
					acol := uint8(0xff)
					nRgba.SetNRGBA(x+x2, y, color.NRGBA{ycol, ycol, ycol, acol})
					b <<= 4
				}
			}
		case 8:
			//for x := 0; x < width; x++ {
			//	ycol := cDat[2*x+0]
			//	nRgba.SetNRGBA(x, y, color.NRGBA{ycol, ycol, ycol, cDat[2*x+1]})
			//}
			for x := 0; x < width*4; x += 4 {
				cDat[x], cDat[x+2] = cDat[x+2], cDat[x]
			}
			copy(nRgba.Pix[pixOffset:], cDat)
			pixOffset += nRgba.Stride
		case 16:
			for x := 0; x < width; x++ {
				bCol := uint16(cDat[8*x+0])<<8 | uint16(cDat[8*x+1])
				gCol := uint16(cDat[8*x+2])<<8 | uint16(cDat[8*x+3])
				rCol := uint16(cDat[8*x+4])<<8 | uint16(cDat[8*x+5])
				aCol := uint16(cDat[8*x+6])<<8 | uint16(cDat[8*x+7])
				nRgba64.SetNRGBA64(x, y, color.NRGBA64{rCol, gCol, bCol, aCol})
			}
		}

		// The current row for y is the previous row for y+1.
		pr, cr = cr, pr
	}

	return img, nil
}

// mergePassInto merges a single pass into a full sized image.
func (cgbi *IpaPNG) mergePassInto(dst image.Image, src image.Image, pass int) {
	p := interlacing[pass]
	var (
		srcPix        []uint8
		dstPix        []uint8
		stride        int
		rect          image.Rectangle
		bytesPerPixel int
	)
	switch target := dst.(type) {
	case *image.Alpha:
		srcPix = src.(*image.Alpha).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 1
	case *image.Alpha16:
		srcPix = src.(*image.Alpha16).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 2
	case *image.Gray:
		srcPix = src.(*image.Gray).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 1
	case *image.Gray16:
		srcPix = src.(*image.Gray16).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 2
	case *image.NRGBA:
		srcPix = src.(*image.NRGBA).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 4
	case *image.NRGBA64:
		srcPix = src.(*image.NRGBA64).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 8
	case *image.Paletted:
		srcPix = src.(*image.Paletted).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 1
	case *image.RGBA:
		srcPix = src.(*image.RGBA).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 4
	case *image.RGBA64:
		srcPix = src.(*image.RGBA64).Pix
		dstPix, stride, rect = target.Pix, target.Stride, target.Rect
		bytesPerPixel = 8
	}
	s, bounds := 0, src.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		dBase := (y*p.yFactor+p.yOffset-rect.Min.Y)*stride + (p.xOffset-rect.Min.X)*bytesPerPixel
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			d := dBase + x*p.xFactor*bytesPerPixel
			copy(dstPix[d:], srcPix[s:s+bytesPerPixel])
			s += bytesPerPixel
		}
	}
}
