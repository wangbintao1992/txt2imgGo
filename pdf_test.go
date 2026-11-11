package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWritePDF(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument(strings.Repeat("PDF 导出测试\n", 60), true, 20, 600, 800)
	var buf bytes.Buffer
	err := WritePDF(&buf, doc, RenderOptions{
		Font:             ref,
		Watermark:        "内部资料",
		WatermarkSize:    40,
		WatermarkOpacity: 0.14,
		WatermarkAngle:   -30,
	}, PDFSecurity{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := buf.String()
	if !strings.HasPrefix(data, "%PDF-1.4") {
		t.Fatal("missing PDF header")
	}
	if !strings.HasSuffix(data, "%%EOF\n") {
		t.Fatal("missing PDF trailer")
	}
	if got := strings.Count(data, "/Type /Page /Parent"); got != doc.Pages() {
		t.Fatalf("page objects = %d, want %d", got, doc.Pages())
	}
	if got := strings.Count(data, "/Subtype /Image"); got != doc.Pages() {
		t.Fatalf("image objects = %d, want %d", got, doc.Pages())
	}
}
