package chess

// png.go — مشفّر PNG سريع للرقعة.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
)

// encodeRGBPNG يكتب صورة معتمة كـ PNG (RGB 8-bit) بترميز سريع: بلا فلاتر
// (filter=None) وضغط flate بمستوى 1. مشفّر image/png القياسي يجرّب 5 فلاتر
// لكل صف وهذا يستهلك ≈ 80% من زمن الرسم؛ والرقعة ألوان مسطّحة تُضغط جيداً بدونها.
func encodeRGBPNG(w *bytes.Buffer, img *image.RGBA) {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()

	raw := make([]byte, 0, height*(1+width*3))
	for y := 0; y < height; y++ {
		raw = append(raw, 0) // filter: None
		row := img.Pix[y*img.Stride : y*img.Stride+width*4]
		for x := 0; x < width*4; x += 4 {
			raw = append(raw, row[x], row[x+1], row[x+2])
		}
	}

	var z bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
	_, _ = zw.Write(raw)
	_ = zw.Close()

	w.WriteString("\x89PNG\r\n\x1a\n")
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
	ihdr[8], ihdr[9] = 8, 2 // عمق 8 بت، لون حقيقي RGB
	writeChunk(w, "IHDR", ihdr[:])
	writeChunk(w, "IDAT", z.Bytes())
	writeChunk(w, "IEND", nil)
}

func writeChunk(w *bytes.Buffer, typ string, data []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data)))
	w.Write(n[:])
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	w.WriteString(typ)
	w.Write(data)
	binary.BigEndian.PutUint32(n[:], crc.Sum32())
	w.Write(n[:])
}
