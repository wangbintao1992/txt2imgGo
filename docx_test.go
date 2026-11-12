package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image/png"
	"io"
	"strings"
	"testing"
)

func TestWriteDOCX(t *testing.T) {
	var buf bytes.Buffer
	text := "日记·第1天　天气：多云　温度：31℃\n\n第一段 & <测试>\n第二段"
	if err := WriteDOCX(&buf, text, true, "测试文档", RenderOptions{}); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = string(data)
	}
	for _, name := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"word/document.xml",
		"word/styles.xml",
		"word/_rels/document.xml.rels",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("missing %s", name)
		}
	}

	doc := files["word/document.xml"]
	if strings.Contains(doc, "<测试>") {
		t.Fatal("XML not escaped")
	}
	if !strings.Contains(doc, "&amp;") || !strings.Contains(doc, "&lt;测试&gt;") {
		t.Fatalf("expected escaped entities in %q", doc)
	}
	if got := strings.Count(doc, "<w:p>"); got != 3 {
		t.Fatalf("paragraphs = %d, want 3", got)
	}
	// 根元素必须能通过 XML 解析，否则 Word 会报文件损坏。
	var probe struct{}
	if err := xml.Unmarshal([]byte(doc), &probe); err != nil {
		t.Fatalf("document.xml invalid: %v", err)
	}
}

func TestWriteDOCXMerged(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDOCX(&buf, "第一行\n第二行\n\n第三行", false, "", RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var doc string
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		doc = string(data)
	}
	if got := strings.Count(doc, "<w:p>"); got != 1 {
		t.Fatalf("merged paragraphs = %d, want 1", got)
	}
	if !strings.Contains(doc, "第一行第二行第三行") {
		t.Fatalf("merged text missing: %q", doc)
	}
}

func TestWriteDOCXEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDOCX(&buf, " \n\n ", true, "", RenderOptions{}); err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestWriteDOCXWatermark(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	var buf bytes.Buffer
	err := WriteDOCX(&buf, "带水印的正文", true, "水印测试", RenderOptions{
		Font:             ref,
		Watermark:        "内部资料",
		WatermarkSize:    46,
		WatermarkOpacity: 0.18,
		WatermarkAngle:   -30,
	})
	if err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
	}

	for _, name := range []string{
		"word/header1.xml",
		"word/_rels/header1.xml.rels",
		"word/media/watermark.png",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("missing %s", name)
		}
	}
	if !strings.Contains(string(files["word/document.xml"]), `w:headerReference`) {
		t.Fatal("document.xml missing header reference")
	}
	if !strings.Contains(string(files["word/_rels/document.xml.rels"]), "header1.xml") {
		t.Fatal("document rels missing header1.xml")
	}
	if _, err := png.Decode(bytes.NewReader(files["word/media/watermark.png"])); err != nil {
		t.Fatalf("watermark png invalid: %v", err)
	}
	var probe struct{}
	if err := xml.Unmarshal(files["word/header1.xml"], &probe); err != nil {
		t.Fatalf("header1.xml invalid: %v", err)
	}
}

func TestWriteDOCXWithoutWatermarkHasNoHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDOCX(&buf, "无水印", true, "", RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if strings.Contains(f.Name, "header") {
			t.Fatalf("unexpected header part %s", f.Name)
		}
	}
}
