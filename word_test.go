package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func tinyPNGBase64(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestWordRenderSavePages(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "demo.docx")
	if err := os.WriteFile(source, []byte("docx"), 0o644); err != nil {
		t.Fatal(err)
	}

	encoded := tinyPNGBase64(t)
	svc := &WordRender{source: source}
	out, err := svc.SavePages([]string{encoded, "data:image/png;base64," + encoded})
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(dir, "demo_images") {
		t.Fatalf("out = %q", out)
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("files = %d, want 2", len(entries))
	}
	for i, want := range []string{"demo_001.png", "demo_002.png"} {
		if entries[i].Name() != want {
			t.Fatalf("file[%d] = %q, want %q", i, entries[i].Name(), want)
		}
		data, err := os.ReadFile(filepath.Join(out, want))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s is not a valid png: %v", want, err)
		}
		if img.Bounds().Dx() != 2 {
			t.Fatalf("%s width = %d", want, img.Bounds().Dx())
		}
	}
}

func TestWordRenderSavePagesErrors(t *testing.T) {
	svc := &WordRender{}
	if _, err := svc.SavePages(nil); err == nil {
		t.Fatal("expected error for empty pages")
	}
	if _, err := svc.SavePages([]string{tinyPNGBase64(t)}); err == nil {
		t.Fatal("expected error without a picked docx")
	}

	svc = &WordRender{source: filepath.Join(t.TempDir(), "a.docx")}
	if _, err := svc.SavePages([]string{"not base64!!"}); err == nil {
		t.Fatal("expected decode error")
	}
}
