package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"
)

func TestBuildDocumentSplitsParagraphs(t *testing.T) {
	doc := BuildDocument("第一段\n\n第二段", true, 10, pageWidth, pageHeight)
	if doc.Pages() != 1 {
		t.Fatalf("pages = %d, want 1", doc.Pages())
	}
	if len(doc.Lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(doc.Lines))
	}
	if doc.Lines[0].Text != "第一段" || !doc.Lines[1].Blank || doc.Lines[2].Text != "第二段" {
		t.Fatalf("unexpected lines: %#v", doc.Lines)
	}
}

func TestBuildDocumentMergesLines(t *testing.T) {
	doc := BuildDocument("第一段\n第二段", false, 10, pageWidth, pageHeight)
	if doc.Pages() != 1 {
		t.Fatalf("pages = %d, want 1", doc.Pages())
	}
	if len(doc.Lines) != 1 || doc.Lines[0].Text != "第一段第二段" {
		t.Fatalf("unexpected lines: %#v", doc.Lines)
	}
}

func TestBuildDocumentPages(t *testing.T) {
	doc := BuildDocument(strings.Repeat("字", 1000), true, 10, pageWidth, pageHeight)
	want := (len(doc.Lines) + doc.LinesPerPage - 1) / doc.LinesPerPage
	if doc.Pages() != want {
		t.Fatalf("pages = %d, want %d", doc.Pages(), want)
	}
	for i := 0; i < doc.Pages(); i++ {
		if len(doc.PageLines(i)) == 0 {
			t.Fatalf("page %d has no lines", i)
		}
	}
}

func TestRenderPage(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument("测试文本\nWatermark preview", true, 24, pageWidth, pageHeight)
	img, err := RenderPage(doc, 0, RenderOptions{Font: ref, Watermark: "内部资料"})
	if err != nil {
		t.Fatal(err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != doc.PageWidth || bounds.Dy() != doc.PageHeight {
		t.Fatalf("image is %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), doc.PageWidth, doc.PageHeight)
	}
	nonWhite := 0
	for y := 0; y < pageHeight; y += 3 {
		for x := 0; x < pageWidth; x += 3 {
			if img.RGBAAt(x, y) != (color.RGBA{255, 255, 255, 255}) {
				nonWhite++
			}
		}
	}
	if nonWhite < 50 {
		t.Fatalf("rendered page has only %d non-white samples", nonWhite)
	}
}

func TestWatermarkVisible(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	face, err := newFontFace(ref, 46)
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()

	img := image.NewRGBA(image.Rect(0, 0, pageWidth, pageHeight))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	drawDiagonalWatermark(img, "内部资料 INTERNAL", face, -30, 0.14)

	nonWhite := 0
	for y := 0; y < pageHeight; y += 2 {
		for x := 0; x < pageWidth; x += 2 {
			if img.RGBAAt(x, y) != (color.RGBA{255, 255, 255, 255}) {
				nonWhite++
			}
		}
	}
	if nonWhite < 100 {
		t.Fatalf("watermark has only %d non-white samples", nonWhite)
	}
}

func TestBuildDocumentCustomSize(t *testing.T) {
	doc := BuildDocument(strings.Repeat("字", 500), true, 20, 800, 1000)
	if doc.PageWidth != 800 || doc.PageHeight != 1000 {
		t.Fatalf("page is %dx%d, want 800x1000", doc.PageWidth, doc.PageHeight)
	}
	if doc.MarginX <= 0 || doc.MarginTop <= 0 || doc.MarginBottom <= 0 {
		t.Fatalf("invalid margins: %#v", doc)
	}
	if doc.Pages() == 0 || doc.LinesPerPage < 1 {
		t.Fatalf("invalid pagination: pages=%d linesPerPage=%d", doc.Pages(), doc.LinesPerPage)
	}
}

func TestRenderPageCustomSize(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument(strings.Repeat("尺寸自适应测试", 200), true, 24, 800, 1000)
	img, err := RenderPage(doc, 0, RenderOptions{Font: ref, Watermark: "水印"})
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds(); got.Dx() != 800 || got.Dy() != 1000 {
		t.Fatalf("image is %dx%d, want 800x1000", got.Dx(), got.Dy())
	}
}

func antiOCRTestOptions(ref FontRef) RenderOptions {
	return RenderOptions{
		Font:    ref,
		AntiOCR: AntiOCR{Warp: 55, Crowd: 45, Stroke: 55, Noise: 45},
	}
}

func TestAntiOCRDeterministic(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument(strings.Repeat("反 OCR 确定性测试 text 123\n", 60), true, 20, 600, 800)
	opt := antiOCRTestOptions(ref)

	first, err := RenderPage(doc, 0, opt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderPage(doc, 0, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("same page rendered twice is not deterministic")
	}

	if doc.Pages() > 1 {
		other, err := RenderPage(doc, 1, opt)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(first.Pix, other.Pix) {
			t.Fatal("different pages produced identical pixels")
		}
	}
}

func TestAntiOCRZeroMatchesPlain(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument("零强度应与普通渲染一致\nplain render", true, 20, 600, 800)

	plain, err := RenderPage(doc, 0, RenderOptions{Font: ref})
	if err != nil {
		t.Fatal(err)
	}
	zero, err := RenderPage(doc, 0, RenderOptions{Font: ref, AntiOCR: AntiOCR{}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain.Pix, zero.Pix) {
		t.Fatal("zero anti-OCR settings changed the output")
	}
}

func TestAntiOCRKeepsTextVisible(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument(strings.Repeat("反 OCR 可读性测试", 80), true, 20, 600, 800)
	img, err := RenderPage(doc, 0, antiOCRTestOptions(ref))
	if err != nil {
		t.Fatal(err)
	}
	nonWhite := 0
	for y := 0; y < img.Bounds().Dy(); y += 3 {
		for x := 0; x < img.Bounds().Dx(); x += 3 {
			if img.RGBAAt(x, y) != (color.RGBA{255, 255, 255, 255}) {
				nonWhite++
			}
		}
	}
	if nonWhite < 200 {
		t.Fatalf("anti-OCR page has only %d non-white samples", nonWhite)
	}
}

func TestAntiOCRStrengthChangesOutput(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument("强度差异测试", true, 20, 600, 800)

	weak := antiOCRTestOptions(ref)
	weak.AntiOCR = AntiOCR{Noise: 5}
	strong := antiOCRTestOptions(ref)
	strong.AntiOCR = AntiOCR{Noise: 80}

	a, err := RenderPage(doc, 0, weak)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RenderPage(doc, 0, strong)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("different noise strengths produced identical pixels")
	}
}
