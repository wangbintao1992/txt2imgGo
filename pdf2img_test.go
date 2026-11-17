package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePDFFixture 生成一个多页 PDF，返回实际页数。
func writePDFFixture(t *testing.T, path string, pages int) int {
	t.Helper()
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument(strings.Repeat("PDF 转图片测试段落，用来撑出多页内容。\n", 40*pages), true, 20, 600, 800)
	var buf bytes.Buffer
	if err := WritePDF(&buf, doc, RenderOptions{Font: ref}, PDFSecurity{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return doc.Pages()
}

func TestPreparePDFDocument(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "demo.pdf")
	total := writePDFFixture(t, source, 2)

	doc, err := preparePDFDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if doc.PageCount != total {
		t.Fatalf("PageCount = %d, want %d", doc.PageCount, total)
	}
	if doc.Stem != "demo" {
		t.Fatalf("Stem = %q", doc.Stem)
	}
}

func TestPDFRenderPage(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "demo.pdf")
	writePDFFixture(t, source, 1)

	doc, err := preparePDFDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()

	img, err := doc.RenderPage(1, 150)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 500 || img.Bounds().Dy() < 500 {
		t.Fatalf("page too small: %v", img.Bounds())
	}
	if _, err := doc.RenderPage(doc.PageCount+1, 150); err == nil {
		t.Fatal("expected an error for a page past the end")
	}

	big, err := doc.RenderPage(1, 300)
	if err != nil {
		t.Fatal(err)
	}
	if big.Bounds().Dx() <= img.Bounds().Dx() {
		t.Fatalf("300 dpi (%v) not larger than 150 dpi (%v)", big.Bounds(), img.Bounds())
	}
}

func TestExportPDFRangeToPNGs(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "demo.pdf")
	writePDFFixture(t, source, 2)

	doc, err := preparePDFDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()

	out, files, err := doc.ExportPNGs(pdfConvertOptions{StartPage: 1, EndPage: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(dir, "demo_images") {
		t.Fatalf("out = %q", out)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if got := filepath.Base(files[0]); got != "demo_001.png" {
		t.Fatalf("file = %q, want demo_001.png", got)
	}
	img := decodePNGFile(t, files[0])
	if img.Bounds().Dx() < 500 {
		t.Fatalf("image too small: %v", img.Bounds())
	}
}

func TestConvertPDFToPNGs(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "demo.pdf")
	total := writePDFFixture(t, source, 2)

	var statuses []string
	out, files, err := convertPDFToPNGs(source, pdfConvertOptions{
		DPI: 300,
		Progress: func(status string, fraction float64) {
			statuses = append(statuses, status)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(dir, "demo_images") {
		t.Fatalf("out = %q", out)
	}
	if len(files) != total {
		t.Fatalf("got %d files, want %d", len(files), total)
	}
	img := decodePNGFile(t, files[0])
	if img.Bounds().Dx() < 1000 {
		t.Fatalf("300 dpi image too small: %v", img.Bounds())
	}
	if len(statuses) < 2 {
		t.Fatalf("expected progress updates, got %v", statuses)
	}
}

func TestPDFToPNGsErrors(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := convertPDFToPNGs(filepath.Join(dir, "a.txt"), pdfConvertOptions{}); err == nil {
		t.Fatal("expected an error for an unsupported extension")
	}
	if _, _, err := convertPDFToPNGs(filepath.Join(dir, "missing.pdf"), pdfConvertOptions{}); err == nil {
		t.Fatal("expected an error for a missing file")
	}
	broken := filepath.Join(dir, "broken.pdf")
	if err := os.WriteFile(broken, []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := convertPDFToPNGs(broken, pdfConvertOptions{}); err == nil {
		t.Fatal("expected an error for an invalid PDF")
	}
}

func decodePNGFile(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("%s is not a valid png: %v", path, err)
	}
	return img
}
