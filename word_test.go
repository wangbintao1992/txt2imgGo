package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

func TestFindPDF(t *testing.T) {
	dir := t.TempDir()
	if _, err := findPDF(dir); err == nil {
		t.Fatal("expected an error for an empty directory")
	}
	want := filepath.Join(dir, "test.pdf")
	if err := os.WriteFile(want, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findPDF(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("findPDF = %q, want %q", got, want)
	}
}

func TestWordPageRange(t *testing.T) {
	cases := []struct {
		start, end, total  int
		wantStart, wantEnd int
	}{
		{0, 0, 5, 1, 5},
		{2, 4, 5, 2, 4},
		{4, 2, 5, 2, 4},
		{0, 99, 5, 1, 5},
		{-3, 2, 5, 1, 2},
		{8, 9, 5, 5, 5},
		{1, 0, 1, 1, 1},
	}
	for _, tc := range cases {
		start, end := pageRange(tc.start, tc.end, tc.total)
		if start != tc.wantStart || end != tc.wantEnd {
			t.Errorf("pageRange(%d, %d, %d) = (%d, %d), want (%d, %d)",
				tc.start, tc.end, tc.total, start, end, tc.wantStart, tc.wantEnd)
		}
	}
}

func writeWordFixture(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	text := "第一段 这是中文测试。\n第二段 LibreOffice 负责排版。\n第三段 水印叠加在页面上。"
	if err := WriteDOCX(file, text, true, "测试", RenderOptions{}); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// rewriteDOCX 重写 zip 条目，edit 返回新的内容。
func rewriteDOCX(t *testing.T, path string, edit func(name string, data []byte) []byte) {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		data = edit(f.Name, data)
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: f.Modified})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func addInlineFrameProps(t *testing.T, path string) {
	t.Helper()
	frame := `<w:framePr w:wrap="auto" w:vAnchor="margin" w:hAnchor="text" w:yAlign="inline"/>`
	rewriteDOCX(t, path, func(name string, data []byte) []byte {
		if name != "word/document.xml" {
			return data
		}
		return bytes.ReplaceAll(data, []byte("<w:pPr>"), []byte("<w:pPr>"+frame))
	})
}

func TestStripInlineFrameProps(t *testing.T) {
	inline := `<w:framePr w:wrap="auto" w:vAnchor="margin" w:hAnchor="text" w:yAlign="inline"/>`
	floating := `<w:framePr w:wrap="square" w:x="720" w:y="360"/>`
	input := []byte(`<w:pPr>` + inline + floating + `</w:pPr>`)
	got := string(stripInlineFrameProps(input))
	if strings.Contains(got, `w:yAlign="inline"`) {
		t.Fatalf("inline framePr not removed: %s", got)
	}
	if !strings.Contains(got, floating) {
		t.Fatalf("floating framePr removed: %s", got)
	}
	if plain := stripInlineFrameProps([]byte("<w:body></w:body>")); string(plain) != "<w:body></w:body>" {
		t.Fatalf("unexpected change: %q", plain)
	}
}

func TestSanitizeWordDOCX(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.docx")
	writeWordFixture(t, src)
	addInlineFrameProps(t, src)

	dst := filepath.Join(dir, "out.docx")
	if _, err := sanitizeWordDOCX(src, dst); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := make(map[string]bool)
	var document []byte
	for _, f := range zr.File {
		names[f.Name] = true
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		document, err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !names["[Content_Types].xml"] || !names["word/document.xml"] {
		t.Fatalf("entries missing: %v", names)
	}
	if bytes.Contains(document, []byte(`w:yAlign="inline"`)) {
		t.Fatal("inline framePr still present after sanitizing")
	}
}

func requireSoffice(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping LibreOffice integration test in short mode")
	}
	soffice := findSoffice()
	if soffice == "" {
		t.Skip("LibreOffice is not installed")
	}
	return soffice
}

func requireFont(t *testing.T) FontRef {
	t.Helper()
	ref, ok := detectSystemFont()
	if !ok || ref.Path == "" {
		t.Skip("no system font available")
	}
	return ref
}

func TestPrepareWordDocumentCleansInlineFrames(t *testing.T) {
	soffice := requireSoffice(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "frames.docx")
	writeWordFixture(t, source)
	addInlineFrameProps(t, source)

	doc, err := prepareWordDocument(soffice, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	// 未清理时 LibreOffice 会把每个带 framePr 的段落单独排一页。
	if doc.PageCount > 3 {
		t.Fatalf("inline framePr split the document into %d pages", doc.PageCount)
	}
}

func TestPrepareWordDocument(t *testing.T) {
	soffice := requireSoffice(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "demo.docx")
	writeWordFixture(t, source)

	doc, err := prepareWordDocument(soffice, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageCount < 1 {
		doc.Close()
		t.Fatalf("PageCount = %d", doc.PageCount)
	}
	if doc.Stem != "demo" {
		doc.Close()
		t.Fatalf("Stem = %q", doc.Stem)
	}
	if !fileExists(doc.PDFPath) {
		doc.Close()
		t.Fatalf("PDF missing at %q", doc.PDFPath)
	}

	img, err := doc.RenderPage(1, wordConvertOptions{})
	if err != nil {
		doc.Close()
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 1000 || img.Bounds().Dy() < 1000 {
		doc.Close()
		t.Fatalf("preview page too small: %v", img.Bounds())
	}
	if _, err := doc.RenderPage(doc.PageCount+1, wordConvertOptions{}); err == nil {
		doc.Close()
		t.Fatal("expected an error for a page past the end")
	}

	tmp := doc.TempDir
	doc.Close()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temporary directory %q not removed", tmp)
	}
}

func openPDFPages(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := reader.Open(data)
	if err != nil {
		t.Fatalf("%s is not a valid PDF: %v", path, err)
	}
	return doc.PageCount()
}

func TestExportWordRangePDF(t *testing.T) {
	soffice := requireSoffice(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "demo.docx")
	writeWordFixture(t, source)

	doc, err := prepareWordDocument(soffice, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()

	out, pages, err := doc.ExportPDF(wordConvertOptions{StartPage: 1, EndPage: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(dir, "demo.pdf") {
		t.Fatalf("out = %q", out)
	}
	if pages != 1 {
		t.Fatalf("pages = %d, want 1", pages)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatalf("%s is empty", out)
	}
	if got := openPDFPages(t, out); got != 1 {
		t.Fatalf("pdf has %d pages, want 1", got)
	}
}

func TestConvertWordToPDF(t *testing.T) {
	soffice := requireSoffice(t)
	ref := requireFont(t)

	dir := t.TempDir()
	source := filepath.Join(dir, "demo.docx")
	writeWordFixture(t, source)

	var statuses []string
	out, pages, err := convertWordToPDF(soffice, source, wordConvertOptions{
		Watermark:        "内部资料",
		WatermarkSize:    46,
		WatermarkOpacity: 0.14,
		WatermarkAngle:   -30,
		Font:             ref,
		Progress: func(status string, fraction float64) {
			statuses = append(statuses, status)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(dir, "demo.pdf") {
		t.Fatalf("out = %q", out)
	}
	if pages == 0 {
		t.Fatal("no pages exported")
	}
	if got := openPDFPages(t, out); got != pages {
		t.Fatalf("pdf has %d pages, want %d", got, pages)
	}
	if info, err := os.Stat(out); err != nil {
		t.Fatal(err)
	} else if info.Size() == 0 {
		t.Fatalf("%s is empty", out)
	}
	if len(statuses) < 2 {
		t.Fatalf("expected progress updates, got %v", statuses)
	}
}

func TestConvertWordToPDFErrors(t *testing.T) {
	if _, _, err := convertWordToPDF("", "a.docx", wordConvertOptions{}); err == nil {
		t.Fatal("expected an error without LibreOffice")
	}
	if _, _, err := convertWordToPDF("/bin/false", "a.txt", wordConvertOptions{}); err == nil {
		t.Fatal("expected an error for an unsupported extension")
	}
	if _, _, err := convertWordToPDF("/bin/false", filepath.Join(t.TempDir(), "missing.docx"), wordConvertOptions{}); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
