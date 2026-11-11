package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	aeszip "github.com/yeka/zip"
)

func writeTestFiles(t *testing.T, names ...string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	files := make([]string, 0, len(names))
	for i, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("content-"+name), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
		_ = i
	}
	return dir, files
}

func TestWriteZIPArchiveEncrypted(t *testing.T) {
	_, files := writeTestFiles(t, "page_001.png", "page_002.png")
	out := filepath.Join(t.TempDir(), "out.zip")
	if err := writeZIPArchive(out, files, "secret", nil); err != nil {
		t.Fatal(err)
	}

	r, err := aeszip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.File) != 2 {
		t.Fatalf("entries = %d, want 2", len(r.File))
	}
	for _, f := range r.File {
		if !f.IsEncrypted() {
			t.Fatalf("%s is not encrypted", f.Name)
		}
		f.SetPassword("secret")
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if want := "content-" + filepath.Base(f.Name); string(data) != want {
			t.Fatalf("content = %q, want %q", data, want)
		}
	}

	// 错误密码必须读不出内容。
	bad, err := aeszip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	bad.File[0].SetPassword("wrong")
	rc, err := bad.File[0].Open()
	if err == nil {
		_, err = io.ReadAll(rc)
		rc.Close()
	}
	if err == nil {
		t.Fatal("wrong password read succeeded")
	}
}

func TestWriteZIPArchivePlain(t *testing.T) {
	_, files := writeTestFiles(t, "page_001.png")
	out := filepath.Join(t.TempDir(), "plain.zip")
	if err := writeZIPArchive(out, files, "", nil); err != nil {
		t.Fatal(err)
	}
	r, err := aeszip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.File) != 1 {
		t.Fatalf("entries = %d, want 1", len(r.File))
	}
	if r.File[0].IsEncrypted() {
		t.Fatal("entry should not be encrypted")
	}
}

func TestWrite7zArchive(t *testing.T) {
	bin := find7z()
	if bin == "" {
		t.Skip("7-Zip command not installed")
	}
	_, files := writeTestFiles(t, "page_001.png")
	out := filepath.Join(t.TempDir(), "out.7z")
	if err := write7zArchive(bin, out, files, "secret", nil); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		t.Fatalf("archive not written: %v", err)
	}
}
