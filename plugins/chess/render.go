package chess

// render.go — مولّد صورة الرقعة. الخلفية (مربعات + إحداثيات + حدود) وصور القطع
// مُولَّدة مسبقاً ومضمّنة في الملف التنفيذي (embed)؛ كل طلب = نسخ الخلفية +
// تلوين آخر نقلة + لصق القطع + ترميز PNG (≈ 5ms، والمكرر من الكاش أقل من 0.1ms).
//
// الأبعاد ثابتة: 480×480 (مربع = 55px) — نفس مخرجات النسخة السابقة.

import (
	"bytes"
	"compress/zlib"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"sync"
)

//go:embed assets/*.png
var assetsFS embed.FS

const (
	boardPx   = 480
	squarePx  = 55
	viewUnits = 394.0 // مجموع وحدات chess.svg (8*45 + هوامش + حدود)
	sqUnits   = 45.0
	offUnits  = 17.0 // board_offset
)

var (
	lastLight = color.RGBA{0xCD, 0xD2, 0x6A, 0xFF}
	lastDark  = color.RGBA{0xAA, 0xBA, 0x44, 0xFF}
)

type renderer struct {
	bg     [2]*image.RGBA       // [0] منظور الأبيض، [1] منظور الأسود
	pieces map[int8]*image.RGBA // مسبقة الضرب (premultiplied): تفعّل المسار السريع في draw.Over
	check  *image.RGBA
	cache  *lruCache
	pool   sync.Pool
}

func decodeAsset(name string) (image.Image, error) {
	b, err := assetsFS.ReadFile("assets/" + name)
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(b))
}

func newRenderer() (*renderer, error) {
	r := &renderer{pieces: map[int8]*image.RGBA{}, cache: newLRU(512)}
	for i, name := range []string{"bg_white.png", "bg_black.png"} {
		img, err := decodeAsset(name)
		if err != nil {
			return nil, err
		}
		rgba := image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), img, image.Point{}, draw.Src)
		if rgba.Bounds().Dx() != boardPx || rgba.Bounds().Dy() != boardPx {
			return nil, fmt.Errorf("حجم الخلفية %v ≠ %d", rgba.Bounds().Size(), boardPx)
		}
		r.bg[i] = rgba
	}
	names := map[int8]string{pawn: "p", knight: "n", bishop: "b", rook: "r", queen: "q", king: "k"}
	for t, n := range names {
		for _, side := range []struct {
			sign int8
			pfx  string
		}{{1, "w"}, {-1, "b"}} {
			img, err := decodeAsset(side.pfx + n + ".png")
			if err != nil {
				return nil, err
			}
			rgba := image.NewRGBA(img.Bounds())
			draw.Draw(rgba, rgba.Bounds(), img, image.Point{}, draw.Src)
			r.pieces[side.sign*t] = rgba
		}
	}
	glow := checkGlow(squarePx)
	r.check = image.NewRGBA(glow.Bounds())
	draw.Draw(r.check, r.check.Bounds(), glow, image.Point{}, draw.Src)
	r.pool.New = func() any { return new(bytes.Buffer) }
	return r, nil
}

// checkGlow يرسم التدرج الشعاعي الأحمر لمربع الكش — نفس تدرج chess.svg:
// 0%→#ff0000، 50%→#e70000 (شفافية 1)، 100%→#9e0000 (شفافية 0)، نصف قطر = نصف المربع.
func checkGlow(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	c := float64(n) / 2
	lerp := func(a, b, t float64) float64 { return a + (b-a)*t }
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			d := math.Hypot(float64(x)+0.5-c, float64(y)+0.5-c) / c // 0 عند المركز، 1 عند نصف القطر
			var rr, a float64
			switch {
			case d >= 1:
				continue
			case d <= 0.5:
				t := d / 0.5
				rr, a = lerp(0xff, 0xe7, t), 1
			default:
				t := (d - 0.5) / 0.5
				rr, a = lerp(0xe7, 0x9e, t), lerp(1, 0, t)
			}
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(rr + 0.5), A: uint8(a*255 + 0.5)})
		}
	}
	return img
}

// squareXY: الزاوية العليا اليسرى للمربع بالبكسل.
func squareXY(sq int, flipped bool) (int, int) {
	f, rk := sq%8, sq/8
	col, row := f, 7-rk
	if flipped {
		col, row = 7-f, rk
	}
	scale := boardPx / viewUnits
	return int(math.Round((offUnits + float64(col)*sqUnits) * scale)),
		int(math.Round((offUnits + float64(row)*sqUnits) * scale))
}

// Render يرسم الموضع ويعيد PNG base64. lastFrom/lastTo = -1 إن لم توجد نقلة أخيرة.
func (r *renderer) Render(p *Position, flipped bool, lastFrom, lastTo int) string {
	checkSq := -1
	if p.InCheck() {
		checkSq = p.KingSquare()
	}
	key := p.BoardFEN() + "|" + strconv.FormatBool(flipped) + "|" + strconv.Itoa(lastFrom) + "|" + strconv.Itoa(lastTo) + "|" + strconv.Itoa(checkSq)
	if v, ok := r.cache.get(key); ok {
		return v
	}

	bgIdx := 0
	if flipped {
		bgIdx = 1
	}
	src := r.bg[bgIdx]
	img := image.NewRGBA(src.Bounds())
	copy(img.Pix, src.Pix)

	for _, sq := range [2]int{lastFrom, lastTo} {
		if sq < 0 {
			continue
		}
		x, y := squareXY(sq, flipped)
		c := lastDark
		if (sq%8+sq/8)%2 == 1 {
			c = lastLight
		}
		draw.Draw(img, image.Rect(x, y, x+squarePx, y+squarePx), image.NewUniform(c), image.Point{}, draw.Src)
	}
	if checkSq >= 0 {
		x, y := squareXY(checkSq, flipped)
		draw.Draw(img, image.Rect(x, y, x+squarePx, y+squarePx), r.check, image.Point{}, draw.Over)
	}
	for sq := 0; sq < 64; sq++ {
		t := p.sq[sq]
		if t == 0 {
			continue
		}
		x, y := squareXY(sq, flipped)
		draw.Draw(img, image.Rect(x, y, x+squarePx, y+squarePx), r.pieces[t], image.Point{}, draw.Over)
	}

	buf := r.pool.Get().(*bytes.Buffer)
	buf.Reset()
	encodeRGBPNG(buf, img)
	out := base64.StdEncoding.EncodeToString(buf.Bytes())
	r.pool.Put(buf)
	r.cache.put(key, out)
	return out
}

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
