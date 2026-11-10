package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
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
	pageMarginX    = 96
	pageMarginTop  = 110
	pageMarginBot  = 120
	maxFontSize    = 36.0
	minFontSize    = 6.0
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
	Lines        []Line
	PageStarts   []int
	LinesPerPage int
	FontSize     float64
	LineHeight   int
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

func BuildDocument(text string, splitParagraphs bool, charsPerLine int) *Document {
	if charsPerLine < 1 {
		charsPerLine = 1
	}
	if charsPerLine > 100 {
		charsPerLine = 100
	}

	contentWidth := pageWidth - 2*pageMarginX
	size := math.Min(maxFontSize, float64(contentWidth)/float64(charsPerLine))
	if size < minFontSize {
		size = minFontSize
	}
	lineHeight := int(math.Round(size * lineHeightRate))
	if lineHeight < 12 {
		lineHeight = 12
	}

	lines := buildLines(text, splitParagraphs, charsPerLine)
	linesPerPage := (pageHeight - pageMarginTop - pageMarginBot) / lineHeight
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

	return &Document{
		Lines:        lines,
		PageStarts:   starts,
		LinesPerPage: linesPerPage,
		FontSize:     size,
		LineHeight:   lineHeight,
	}
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
	Font      FontRef
	Watermark string
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

	img := image.NewRGBA(image.Rect(0, 0, pageWidth, pageHeight))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	if strings.TrimSpace(opt.Watermark) != "" {
		wmFace, err := newFontFace(opt.Font, 46)
		if err == nil {
			drawDiagonalWatermark(img, opt.Watermark, wmFace)
			wmFace.Close()
		}
	}

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
		top := pageMarginTop + i*doc.LineHeight
		baseline := top + ascent.Ceil() + (doc.LineHeight-ascent.Ceil()-descent.Ceil())/2
		d.Dot = fixed.P(pageMarginX, baseline)
		d.DrawString(line.Text)
	}

	drawFooter(img, doc, page, opt.Font)
	return img, nil
}

func drawFooter(dst *image.RGBA, doc *Document, page int, ref FontRef) {
	face, err := newFontFace(ref, footerFontSize)
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
	d.Dot = fixed.P(pageWidth-pageMarginX-width, pageHeight-56)
	d.DrawString(label)
}

func drawDiagonalWatermark(dst *image.RGBA, text string, face font.Face) {
	text = strings.TrimSpace(text)
	if text == "" {
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
		Src:  image.NewUniform(color.RGBA{R: 0x30, G: 0x38, B: 0x45, A: 34}),
		Face: face,
		Dot:  fixed.P(pad, pad+ascent),
	}
	d.DrawString(text)

	rotated := rotateRGBA(tile, -30)
	tw, th := rotated.Bounds().Dx(), rotated.Bounds().Dy()
	stepX := int(float64(tw)*1.15) + 48
	stepY := int(float64(th)*1.55) + 70
	if stepX < 1 || stepY < 1 {
		return
	}

	row := 0
	for y := -th; y < pageHeight+th; y += stepY {
		offset := 0
		if row%2 == 1 {
			offset = stepX / 2
		}
		for x := -tw - offset; x < pageWidth+tw; x += stepX {
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
