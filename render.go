package main

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"

	xdraw "golang.org/x/image/draw"
)

const (
	pageWidth      = 1240
	pageHeight     = 1754
	minPageWidth   = 320
	maxPageWidth   = 4000
	minPageHeight  = 320
	maxPageHeight  = 6000
	pageMarginX    = 96
	pageMarginTop  = 110
	pageMarginBot  = 120
	maxFontSize    = 36.0
	lineHeightRate = 1.7
	footerFontSize = 17.0
)

var (
	textColor   = color.RGBA{R: 0x24, G: 0x27, B: 0x2c, A: 0xff}
	footerColor = color.RGBA{R: 0x9a, G: 0x9f, B: 0xa7, A: 0xff}
)

type Line struct {
	Text  string
	Blank bool
}

type Document struct {
	Lines          []Line
	PageStarts     []int
	LinesPerPage   int
	FontSize       float64
	LineHeight     int
	PageWidth      int
	PageHeight     int
	MarginX        int
	MarginTop      int
	MarginBottom   int
	FooterFontSize float64
	Fingerprint    uint64
}

func (d *Document) Pages() int {
	return len(d.PageStarts)
}

func (d *Document) PageLines(page int) []Line {
	if page < 0 || page >= len(d.PageStarts) {
		return nil
	}
	start := d.PageStarts[page]
	end := start + d.LinesPerPage
	if end > len(d.Lines) {
		end = len(d.Lines)
	}
	return d.Lines[start:end]
}

func BuildDocument(text string, splitParagraphs bool, charsPerLine, width, height int) *Document {
	if charsPerLine < 1 {
		charsPerLine = 1
	}
	if charsPerLine > 100 {
		charsPerLine = 100
	}
	width = clampInt(width, minPageWidth, maxPageWidth)
	height = clampInt(height, minPageHeight, maxPageHeight)

	marginX := scaledDimension(width, pageMarginX, pageWidth, 8, width/4)
	marginTop := scaledDimension(height, pageMarginTop, pageHeight, 8, height/5)
	marginBottom := scaledDimension(height, pageMarginBot, pageHeight, 8, height/5)
	contentWidth := width - 2*marginX
	contentHeight := height - marginTop - marginBottom
	if contentWidth < 1 {
		contentWidth = 1
	}
	if contentHeight < 1 {
		contentHeight = 1
	}

	maxSizeByWidth := maxFontSize * float64(width) / float64(pageWidth)
	size := math.Min(maxSizeByWidth, float64(contentWidth)/float64(charsPerLine))
	maxSizeByHeight := float64(contentHeight) / lineHeightRate
	if size > maxSizeByHeight {
		size = maxSizeByHeight
	}
	if size < 1 {
		size = 1
	}
	lineHeight := int(math.Round(size * lineHeightRate))
	if lineHeight < 1 {
		lineHeight = 1
	}

	lines := buildLines(text, splitParagraphs, charsPerLine)
	linesPerPage := contentHeight / lineHeight
	if linesPerPage < 1 {
		linesPerPage = 1
	}

	starts := make([]int, 0, len(lines)/linesPerPage+1)
	for i := 0; i < len(lines); {
		for i < len(lines) && lines[i].Blank {
			i++
		}
		if i >= len(lines) {
			break
		}
		starts = append(starts, i)
		i += linesPerPage
	}

	footerSize := footerFontSize * float64(width) / float64(pageWidth)
	if footerSize < 8 {
		footerSize = 8
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	fmt.Fprintf(h, "|%t|%d|%d|%d", splitParagraphs, charsPerLine, width, height)
	return &Document{
		Lines:          lines,
		PageStarts:     starts,
		LinesPerPage:   linesPerPage,
		FontSize:       size,
		LineHeight:     lineHeight,
		PageWidth:      width,
		PageHeight:     height,
		MarginX:        marginX,
		MarginTop:      marginTop,
		MarginBottom:   marginBottom,
		FooterFontSize: footerSize,
		Fingerprint:    h.Sum64(),
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func scaledDimension(value, base, baseTotal, minimum, maximum int) int {
	out := int(math.Round(float64(value) * float64(base) / float64(baseTotal)))
	if out < minimum {
		out = minimum
	}
	if maximum > 0 && out > maximum {
		out = maximum
	}
	return out
}

func buildLines(text string, splitParagraphs bool, charsPerLine int) []Line {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	if !splitParagraphs {
		merged := mergeParagraphs(text)
		if merged == "" {
			return nil
		}
		wrapped := wrapText(merged, charsPerLine)
		lines := make([]Line, len(wrapped))
		for i, text := range wrapped {
			lines[i] = Line{Text: text}
		}
		return lines
	}

	var out []Line
	first := true
	gap := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\u3000")
		if strings.TrimSpace(line) == "" {
			if !first {
				gap = true
			}
			continue
		}
		if !first && gap {
			out = append(out, Line{Blank: true})
		}
		for _, wrapped := range wrapText(line, charsPerLine) {
			out = append(out, Line{Text: wrapped})
		}
		first = false
		gap = true
	}
	for len(out) > 0 && out[len(out)-1].Blank {
		out = out[:len(out)-1]
	}
	return out
}

func mergeParagraphs(text string) string {
	var b strings.Builder
	var last rune
	hasLast := false
	space := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && hasLast && !(isCJK(last) && isCJK(r)) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		last = r
		hasLast = true
		space = false
	}
	return strings.TrimSpace(b.String())
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) ||
		(r >= 0x3000 && r <= 0x303f) ||
		(r >= 0xff00 && r <= 0xffef)
}

func wrapText(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	runes := []rune(s)
	var out []string
	for len(runes) > n {
		out = append(out, strings.TrimRight(string(runes[:n]), " \t\u3000"))
		runes = []rune(strings.TrimLeft(string(runes[n:]), " \t\u3000"))
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

type RenderOptions struct {
	Font             FontRef
	Watermark        string
	WatermarkSize    float64
	WatermarkOpacity float64
	WatermarkAngle   float64
	AntiOCR          AntiOCR
}

type AntiOCR struct {
	Warp   float64
	Crowd  float64
	Stroke float64
	Noise  float64
}

func (a AntiOCR) active() bool {
	return a.Warp > 0 || a.Crowd > 0 || a.Stroke > 0 || a.Noise > 0
}

func (a AntiOCR) distortsText() bool {
	return a.Warp > 0 || a.Crowd > 0 || a.Stroke > 0
}

func RenderPage(doc *Document, page int, opt RenderOptions) (*image.RGBA, error) {
	lines := doc.PageLines(page)
	if lines == nil {
		return nil, fmt.Errorf("页码超出范围")
	}

	face, err := newFontFace(opt.Font, doc.FontSize)
	if err != nil {
		return nil, err
	}
	defer face.Close()

	img := image.NewRGBA(image.Rect(0, 0, doc.PageWidth, doc.PageHeight))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	if strings.TrimSpace(opt.Watermark) != "" {
		size := opt.WatermarkSize
		if size <= 0 {
			size = 46
		}
		if size < 12 {
			size = 12
		}
		if size > 160 {
			size = 160
		}
		opacity := opt.WatermarkOpacity
		if opacity <= 0 {
			opacity = 0.14
		}
		if opacity > 1 {
			opacity = 1
		}
		wmFace, err := newFontFace(opt.Font, size)
		if err == nil {
			drawDiagonalWatermark(img, opt.Watermark, wmFace, opt.WatermarkAngle, opacity)
			wmFace.Close()
		}
	}

	var pageRNG *rand.Rand
	if opt.AntiOCR.active() {
		pageRNG = newPageRand(doc, page, opt.AntiOCR)
	}

	if opt.AntiOCR.distortsText() {
		drawPerturbedText(img, doc, lines, face, opt.AntiOCR, pageRNG)
	} else {
		metrics := face.Metrics()
		ascent := metrics.Ascent
		descent := metrics.Descent

		d := &font.Drawer{
			Dst:  img,
			Src:  image.NewUniform(textColor),
			Face: face,
		}
		for i, line := range lines {
			if line.Blank || line.Text == "" {
				continue
			}
			top := doc.MarginTop + i*doc.LineHeight
			baseline := top + ascent.Ceil() + (doc.LineHeight-ascent.Ceil()-descent.Ceil())/2
			d.Dot = fixed.P(doc.MarginX, baseline)
			d.DrawString(line.Text)
		}
	}

	drawFooter(img, doc, page, opt.Font)
	if pageRNG != nil {
		if opt.AntiOCR.Warp > 0 {
			img = elasticWarp(img, opt.AntiOCR.Warp, doc.FontSize, pageRNG)
		}
		if opt.AntiOCR.Noise > 0 {
			drawInterference(img, opt.AntiOCR.Noise, pageRNG)
			drawTextureNoise(img, opt.AntiOCR.Noise, pageRNG)
		}
	}
	return img, nil
}

func newPageRand(doc *Document, page int, anti AntiOCR) *rand.Rand {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%d|%d|%d|%d|%d", doc.Fingerprint, page,
		int(math.Round(anti.Warp)),
		int(math.Round(anti.Crowd)),
		int(math.Round(anti.Stroke)),
		int(math.Round(anti.Noise)))
	seed := h.Sum64()
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

func drawPerturbedText(dst *image.RGBA, doc *Document, lines []Line, face font.Face, anti AntiOCR, rng *rand.Rand) {
	metrics := face.Metrics()
	ascent := metrics.Ascent.Ceil()
	descent := metrics.Descent.Ceil()
	fontSize := doc.FontSize

	warp := anti.Warp / 100
	crowd := anti.Crowd / 100
	contentLeft := float64(doc.MarginX)
	contentTop := float64(doc.MarginTop)
	contentBottom := float64(doc.PageHeight - doc.MarginBottom)

	for i, line := range lines {
		if line.Blank || line.Text == "" {
			continue
		}
		top := float64(doc.MarginTop + i*doc.LineHeight)
		baseline := top + float64(ascent) + float64(doc.LineHeight-ascent-descent)/2
		cursor := contentLeft

		for _, r := range line.Text {
			adv, ok := face.GlyphAdvance(r)
			advPx := float64(adv) / 64
			if !ok {
				advPx = 0
			}

			dx, dy := 0.0, 0.0
			if warp > 0 {
				ampX := 0.10 * advPx * warp
				if ampX < 0.6*warp {
					ampX = 0.6 * warp
				}
				ampY := 0.16 * fontSize * warp
				dx = (rng.Float64()*2 - 1) * ampX
				dy = (rng.Float64()*2 - 1) * ampY
			}

			drawY := baseline + dy
			minY := contentTop + float64(ascent)
			maxY := contentBottom - float64(descent)
			if drawY < minY {
				drawY = minY
			}
			if drawY > maxY {
				drawY = maxY
			}

			if advPx > 0 {
				col := jitteredTextColor(rng, anti.Warp)
				drawPerturbedRune(dst, face, r, cursor+dx, drawY, anti, col, rng, ascent, descent, advPx)
			}

			advance := advPx
			if crowd > 0 && advPx > 0 {
				advance *= 1 - crowd*(0.05+0.30*rng.Float64())
			}
			cursor += advance
		}
	}
}

func drawPerturbedRune(dst *image.RGBA, face font.Face, r rune, x, baseline float64, anti AntiOCR, col color.NRGBA, rng *rand.Rand, ascent, descent int, advPx float64) {
	warp := anti.Warp / 100
	stroke := anti.Stroke / 100
	if warp <= 0 && stroke <= 0 {
		d := &font.Drawer{
			Dst:  dst,
			Src:  image.NewUniform(col),
			Face: face,
			Dot:  fixed.Point26_6{X: floatFixed(x), Y: floatFixed(baseline)},
		}
		d.DrawString(string(r))
		return
	}

	angle := 0.0
	scaleX, scaleY, shear := 1.0, 1.0, 0.0
	if warp > 0 {
		angle = (rng.Float64()*2 - 1) * 9 * warp
		scaleX = 1 + (rng.Float64()*2-1)*0.14*warp
		scaleY = 1 + (rng.Float64()*2-1)*0.14*warp
		shear = (rng.Float64()*2 - 1) * 0.16 * warp
	}

	const pad = 4
	tileW := int(math.Ceil(advPx)) + 2 + 2*pad
	tileH := ascent + descent + 2*pad
	if tileW < 1 || tileH < 1 {
		return
	}
	tile := image.NewRGBA(image.Rect(0, 0, tileW, tileH))
	d := &font.Drawer{
		Dst:  tile,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.P(pad, pad+ascent),
	}
	d.DrawString(string(r))

	radians := angle * math.Pi / 180
	cosine, sine := math.Cos(radians), math.Sin(radians)
	m00 := cosine * scaleX
	m01 := cosine*shear - sine*scaleY
	m10 := sine * scaleX
	m11 := sine*shear + cosine*scaleY

	transformed := transformRGBA(tile, m00, m01, m10, m11)
	rw := float64(transformed.Bounds().Dx())
	rh := float64(transformed.Bounds().Dy())
	scx := float64(tileW) / 2
	scy := float64(tileH) / 2
	vx := float64(pad) - scx
	vy := float64(pad+ascent) - scy
	originX := x - rw/2 - (m00*vx + m01*vy)
	originY := baseline - rh/2 - (m10*vx + m11*vy)
	originX = math.Round(originX)
	originY = math.Round(originY)

	if stroke > 0 {
		ghosts := 1 + int(stroke*2.99)
		for g := 0; g < ghosts; g++ {
			off := (0.8 + 2.2*rng.Float64()) * stroke
			dir := rng.Float64() * 2 * math.Pi
			gx := math.Cos(dir) * off
			gy := math.Sin(dir) * off
			alpha := uint8(math.Min(210, (70+90*rng.Float64())*stroke))
			drawMaskedRGBA(dst, int(originX+gx), int(originY+gy), transformed, alpha)
		}
	}

	rect := image.Rect(int(originX), int(originY), int(originX)+int(rw), int(originY)+int(rh))
	draw.Draw(dst, rect, transformed, image.Point{}, draw.Over)
}

func drawMaskedRGBA(dst *image.RGBA, x, y int, src *image.RGBA, alpha uint8) {
	if alpha == 0 {
		return
	}
	rect := image.Rect(x, y, x+src.Bounds().Dx(), y+src.Bounds().Dy())
	draw.DrawMask(dst, rect, src, image.Point{}, image.NewUniform(color.Alpha{A: alpha}), image.Point{}, draw.Over)
}

func transformRGBA(src *image.RGBA, a, b, c, d float64) *image.RGBA {
	sw := float64(src.Bounds().Dx())
	sh := float64(src.Bounds().Dy())
	scx, scy := sw/2, sh/2

	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, cx := range []float64{-scx, scx} {
		for _, cy := range []float64{-scy, scy} {
			x := a*cx + b*cy
			y := c*cx + d*cy
			minX = math.Min(minX, x)
			maxX = math.Max(maxX, x)
			minY = math.Min(minY, y)
			maxY = math.Max(maxY, y)
		}
	}
	dw := int(math.Ceil(maxX - minX))
	dh := int(math.Ceil(maxY - minY))
	if dw < 1 || dh < 1 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	matrix := f64.Aff3{
		a, b, -a*scx - b*scy - minX,
		c, d, -c*scx - d*scy - minY,
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	xdraw.BiLinear.Transform(dst, matrix, src, src.Bounds(), xdraw.Over, nil)
	return dst
}

func elasticWarp(src *image.RGBA, strength, fontSize float64, rng *rand.Rand) *image.RGBA {
	strength /= 100
	if strength <= 0 {
		return src
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	amp := 0.35 * fontSize * strength
	if amp < 0.6 {
		amp = 0.6
	}

	ph1 := rng.Float64() * 2 * math.Pi
	ph2 := rng.Float64() * 2 * math.Pi
	ph3 := rng.Float64() * 2 * math.Pi
	ph4 := rng.Float64() * 2 * math.Pi
	ly1 := fontSize * (6 + 6*rng.Float64())
	lx1 := fontSize * (6 + 6*rng.Float64())
	if ly1 < 1 {
		ly1 = 1
	}
	if lx1 < 1 {
		lx1 = 1
	}
	ly2 := ly1 * 0.45
	lx2 := lx1 * 0.55

	shiftX := make([]float64, h)
	for y := 0; y < h; y++ {
		v := float64(y)
		shiftX[y] = amp * (0.7*math.Sin(2*math.Pi*v/ly1+ph1) + 0.3*math.Sin(2*math.Pi*v/ly2+ph2))
	}
	shiftY := make([]float64, w)
	for x := 0; x < w; x++ {
		v := float64(x)
		shiftY[x] = amp * (0.7*math.Sin(2*math.Pi*v/lx1+ph3) + 0.3*math.Sin(2*math.Pi*v/lx2+ph4))
	}

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		dy := shiftX[y]
		row := y * dst.Stride
		for x := 0; x < w; x++ {
			sample := sampleBilinear(src, float64(x)+shiftY[x], float64(y)+dy)
			i := row + x*4
			dst.Pix[i] = sample.R
			dst.Pix[i+1] = sample.G
			dst.Pix[i+2] = sample.B
			dst.Pix[i+3] = sample.A
		}
	}
	return dst
}

func sampleBilinear(img *image.RGBA, x, y float64) color.RGBA {
	bounds := img.Bounds()
	if x < float64(bounds.Min.X) {
		x = float64(bounds.Min.X)
	}
	if y < float64(bounds.Min.Y) {
		y = float64(bounds.Min.Y)
	}
	if x > float64(bounds.Max.X-1) {
		x = float64(bounds.Max.X - 1)
	}
	if y > float64(bounds.Max.Y-1) {
		y = float64(bounds.Max.Y - 1)
	}
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	x1 := x0 + 1
	if x1 >= bounds.Max.X {
		x1 = bounds.Max.X - 1
	}
	y1 := y0 + 1
	if y1 >= bounds.Max.Y {
		y1 = bounds.Max.Y - 1
	}
	fx := x - float64(x0)
	fy := y - float64(y0)

	c00 := img.RGBAAt(x0, y0)
	c10 := img.RGBAAt(x1, y0)
	c01 := img.RGBAAt(x0, y1)
	c11 := img.RGBAAt(x1, y1)
	blend := func(v00, v10, v01, v11 uint8) uint8 {
		top := float64(v00)*(1-fx) + float64(v10)*fx
		bottom := float64(v01)*(1-fx) + float64(v11)*fx
		return uint8(math.Round(top*(1-fy) + bottom*fy))
	}
	return color.RGBA{
		R: blend(c00.R, c10.R, c01.R, c11.R),
		G: blend(c00.G, c10.G, c01.G, c11.G),
		B: blend(c00.B, c10.B, c01.B, c11.B),
		A: 0xff,
	}
}

func drawInterference(dst *image.RGBA, strength float64, rng *rand.Rand) {
	strength /= 100
	if strength <= 0 {
		return
	}
	bounds := dst.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	lines := int(6 + 30*strength)
	for i := 0; i < lines; i++ {
		alpha := uint8((30 + 45*rng.Float64()) * strength)
		if alpha == 0 {
			continue
		}
		gray := uint8(80 + rng.IntN(80))
		col := color.NRGBA{R: gray, G: gray, B: uint8(math.Min(255, float64(gray)+25)), A: alpha}
		if rng.Float64() < 0.55 {
			y0 := bounds.Min.Y + rng.IntN(h)
			y1 := y0 + int((rng.Float64()*2-1)*float64(h)*0.05)
			drawLine(dst, bounds.Min.X, y0, bounds.Max.X-1, y1, col)
		} else {
			x0 := bounds.Min.X + rng.IntN(w)
			y0 := bounds.Min.Y + rng.IntN(h)
			length := 40 + rng.IntN(max(1, w/3))
			angle := rng.Float64() * math.Pi
			x1 := x0 + int(math.Cos(angle)*float64(length))
			y1 := y0 + int(math.Sin(angle)*float64(length))
			drawLine(dst, x0, y0, x1, y1, col)
		}
	}
	dots := int(140 * strength)
	for i := 0; i < dots; i++ {
		x := bounds.Min.X + rng.IntN(w)
		y := bounds.Min.Y + rng.IntN(h)
		alpha := uint8((20 + 45*rng.Float64()) * strength)
		gray := uint8(50 + rng.IntN(150))
		blendPixel(dst, x, y, color.NRGBA{R: gray, G: gray, B: gray, A: alpha})
	}
}

func drawLine(dst *image.RGBA, x0, y0, x1, y1 int, col color.NRGBA) {
	dx := absInt(x1 - x0)
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	dy := -absInt(y1 - y0)
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		blendPixel(dst, x0, y0, col)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func blendPixel(dst *image.RGBA, x, y int, col color.NRGBA) {
	if !image.Pt(x, y).In(dst.Bounds()) {
		return
	}
	i := dst.PixOffset(x, y)
	a := uint32(col.A)
	inv := 255 - a
	dst.Pix[i] = uint8((uint32(col.R)*a + uint32(dst.Pix[i])*inv) / 255)
	dst.Pix[i+1] = uint8((uint32(col.G)*a + uint32(dst.Pix[i+1])*inv) / 255)
	dst.Pix[i+2] = uint8((uint32(col.B)*a + uint32(dst.Pix[i+2])*inv) / 255)
	dst.Pix[i+3] = 255
}

func jitteredTextColor(rng *rand.Rand, strength float64) color.NRGBA {
	if strength <= 0 {
		return color.NRGBA{R: textColor.R, G: textColor.G, B: textColor.B, A: 0xff}
	}
	delta := 24 * strength / 100
	shift := func(base uint8) uint8 {
		v := int(base) + int(math.Round((rng.Float64()*2-1)*delta))
		switch {
		case v < 0:
			return 0
		case v > 255:
			return 255
		default:
			return uint8(v)
		}
	}
	return color.NRGBA{R: shift(textColor.R), G: shift(textColor.G), B: shift(textColor.B), A: 0xff}
}

func floatFixed(v float64) fixed.Int26_6 {
	return fixed.Int26_6(math.Round(v * 64))
}

func drawTextureNoise(dst *image.RGBA, strength float64, rng *rand.Rand) {
	if strength <= 0 {
		return
	}
	const bandHeight = 64
	bounds := dst.Bounds()
	maxAlpha := 0.08 * strength / 100
	band := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bandHeight))
	for y := bounds.Min.Y; y < bounds.Max.Y; y += bandHeight {
		for i := 0; i < len(band.Pix); i += 4 {
			v := uint8(rng.IntN(256))
			band.Pix[i] = v
			band.Pix[i+1] = v
			band.Pix[i+2] = v
			band.Pix[i+3] = uint8(rng.Float64() * maxAlpha * 255)
		}
		h := bandHeight
		if remain := bounds.Max.Y - y; remain < h {
			h = remain
		}
		draw.Draw(dst, image.Rect(bounds.Min.X, y, bounds.Max.X, y+h), band, image.Point{}, draw.Over)
	}
}

func drawFooter(dst *image.RGBA, doc *Document, page int, ref FontRef) {
	face, err := newFontFace(ref, doc.FooterFontSize)
	if err != nil {
		return
	}
	defer face.Close()

	label := fmt.Sprintf("%d / %d", page+1, doc.Pages())
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(footerColor),
		Face: face,
	}
	width := d.MeasureString(label).Ceil()
	footerBottom := int(math.Round(float64(doc.PageHeight) * 56 / pageHeight))
	if footerBottom < 12 {
		footerBottom = 12
	}
	d.Dot = fixed.P(doc.PageWidth-doc.MarginX-width, doc.PageHeight-footerBottom)
	d.DrawString(label)
}

func drawDiagonalWatermark(dst *image.RGBA, text string, face font.Face, angle, opacity float64) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if opacity <= 0 {
		opacity = 0.14
	}
	if opacity > 1 {
		opacity = 1
	}
	alpha := uint8(math.Round(opacity * 255))
	if alpha == 0 {
		return
	}
	runes := []rune(text)
	if len(runes) > 80 {
		text = string(runes[:80])
	}

	metrics := face.Metrics()
	ascent := metrics.Ascent.Ceil()
	descent := metrics.Descent.Ceil()
	pad := 24
	w := font.MeasureString(face, text).Ceil()
	tile := image.NewRGBA(image.Rect(0, 0, w+2*pad, ascent+descent+2*pad))
	d := &font.Drawer{
		Dst:  tile,
		Src:  image.NewUniform(color.NRGBA{R: 0x30, G: 0x38, B: 0x45, A: alpha}),
		Face: face,
		Dot:  fixed.P(pad, pad+ascent),
	}
	d.DrawString(text)

	rotated := rotateRGBA(tile, angle)
	tw, th := rotated.Bounds().Dx(), rotated.Bounds().Dy()
	stepX := int(float64(tw)*1.15) + 48
	stepY := int(float64(th)*1.55) + 70
	if stepX < 1 || stepY < 1 {
		return
	}

	pageW, pageH := dst.Bounds().Dx(), dst.Bounds().Dy()
	row := 0
	for y := -th; y < pageH+th; y += stepY {
		offset := 0
		if row%2 == 1 {
			offset = stepX / 2
		}
		for x := -tw - offset; x < pageW+tw; x += stepX {
			r := image.Rect(x+offset, y, x+offset+tw, y+th)
			draw.Draw(dst, r, rotated, image.Point{}, draw.Over)
		}
		row++
	}
}

func rotateRGBA(src *image.RGBA, degrees float64) *image.RGBA {
	radians := degrees * math.Pi / 180
	c, s := math.Cos(radians), math.Sin(radians)
	sw, sh := float64(src.Bounds().Dx()), float64(src.Bounds().Dy())
	dw := int(math.Ceil(math.Abs(sw*c) + math.Abs(sh*s)))
	dh := int(math.Ceil(math.Abs(sw*s) + math.Abs(sh*c)))
	if dw < 1 || dh < 1 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}

	scx, scy := sw/2, sh/2
	dcx, dcy := float64(dw)/2, float64(dh)/2
	matrix := f64.Aff3{
		c, -s, dcx - c*scx + s*scy,
		s, c, dcy - s*scx - c*scy,
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	xdraw.BiLinear.Transform(dst, matrix, src, src.Bounds(), xdraw.Over, nil)
	return dst
}

func scalePreview(src image.Image, maxWidth, maxHeight int) *image.RGBA {
	b := src.Bounds()
	scale := math.Min(float64(maxWidth)/float64(b.Dx()), float64(maxHeight)/float64(b.Dy()))
	if scale > 1 {
		scale = 1
	}
	w := max(1, int(math.Round(float64(b.Dx())*scale)))
	h := max(1, int(math.Round(float64(b.Dy())*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

type FontRef struct {
	Family string
	Path   string
	Index  int
}

type fontKey struct {
	path  string
	index int
}

var fontCache = struct {
	sync.Mutex
	fonts map[fontKey]*sfnt.Font
}{fonts: make(map[fontKey]*sfnt.Font)}

func loadSFNT(ref FontRef) (*sfnt.Font, error) {
	key := fontKey{path: ref.Path, index: ref.Index}
	fontCache.Lock()
	defer fontCache.Unlock()

	if f, ok := fontCache.fonts[key]; ok {
		return f, nil
	}
	data, err := os.ReadFile(ref.Path)
	if err != nil {
		return nil, fmt.Errorf("读取字体失败: %w", err)
	}
	collection, err := sfnt.ParseCollection(data)
	if err != nil {
		return nil, fmt.Errorf("解析字体失败: %w", err)
	}
	if ref.Index < 0 || ref.Index >= collection.NumFonts() {
		return nil, fmt.Errorf("字体索引无效")
	}
	f, err := collection.Font(ref.Index)
	if err != nil {
		return nil, fmt.Errorf("读取字体字形失败: %w", err)
	}
	fontCache.fonts[key] = f
	return f, nil
}

func newFontFace(ref FontRef, size float64) (font.Face, error) {
	f, err := loadSFNT(ref)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("创建字体失败: %w", err)
	}
	return face, nil
}
