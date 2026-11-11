package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestWriteDOCX(t *testing.T) {
	var buf bytes.Buffer
	text := "日记·第1天　天气：多云　温度：31℃\n\n第一段 & <测试>\n第二段"
	if err := WriteDOCX(&buf, text, true, "测试文档"); err != nil {
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
	if err := WriteDOCX(&buf, "第一行\n第二行\n\n第三行", false, ""); err != nil {
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
	if err := WriteDOCX(&buf, " \n\n ", true, ""); err == nil {
		t.Fatal("expected error for empty text")
	}
}
