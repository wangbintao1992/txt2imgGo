package main

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestPDFPermissions(t *testing.T) {
	all := PDFSecurity{AllowPrint: true, AllowCopy: true, AllowExtract: true}
	if got, want := pdfPermissions(all), int32(-1324); got != want {
		t.Fatalf("all allowed = %d, want %d", got, want)
	}

	none := PDFSecurity{}
	if got, want := pdfPermissions(none), int32(-3904); got != want {
		t.Fatalf("nothing allowed = %d, want %d", got, want)
	}

	printOnly := PDFSecurity{AllowPrint: true}
	if got, want := pdfPermissions(printOnly), int32(-1852); got != want {
		t.Fatalf("print only = %d, want %d", got, want)
	}

	// 禁止复制或禁止提取任一项，bit 5 都会被清除。
	copyOnly := PDFSecurity{AllowCopy: true}
	if got, want := pdfPermissions(copyOnly), int32(-3904); got != want {
		t.Fatalf("copy only = %d, want %d", got, want)
	}
}

func TestWritePDFEncrypted(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument("加密导出测试", true, 20, 600, 800)
	var buf bytes.Buffer
	sec := PDFSecurity{
		OpenPassword:  "open-123",
		OwnerPassword: "owner-456",
		AllowPrint:    false,
		AllowCopy:     false,
		AllowExtract:  false,
	}
	if err := WritePDF(&buf, doc, RenderOptions{Font: ref}, sec, nil); err != nil {
		t.Fatal(err)
	}
	data := buf.String()
	if !strings.Contains(data, "/Encrypt ") {
		t.Fatal("missing /Encrypt in trailer")
	}
	if !strings.Contains(data, "/Filter /Standard /V 2 /R 3 /Length 128") {
		t.Fatal("missing standard security handler dictionary")
	}
	if !strings.Contains(data, "/ID [<") {
		t.Fatal("missing /ID")
	}
	if !strings.Contains(data, "/P -3904") {
		t.Fatalf("unexpected /P: %s", firstLineWith(data, "/P "))
	}

	encDict := firstLineWith(data, "/Filter /Standard")
	if !strings.Contains(encDict, "/O <") || !strings.Contains(encDict, "/U <") {
		t.Fatalf("missing /O or /U: %s", encDict)
	}
}

func TestPDFEncryptorObjectRoundTrip(t *testing.T) {
	sec := PDFSecurity{OpenPassword: "user", OwnerPassword: "owner"}
	enc, err := newPDFEncryptor(sec)
	if err != nil {
		t.Fatal(err)
	}
	if enc == nil {
		t.Fatal("expected encryptor")
	}
	if len(enc.o) != 32 || len(enc.u) != 32 || len(enc.key) != 16 {
		t.Fatalf("bad lengths: O=%d U=%d key=%d", len(enc.o), len(enc.u), len(enc.key))
	}
	if len(enc.objectKey(3, 0)) != 16 {
		t.Fatalf("object key length = %d", len(enc.objectKey(3, 0)))
	}

	plain := []byte("stream payload")
	cipher := enc.Encrypt(7, 0, plain)
	if bytes.Equal(plain, cipher) {
		t.Fatal("ciphertext equals plaintext")
	}
	back := make([]byte, len(cipher))
	pdfRC4(enc.objectKey(7, 0), cipher, back)
	if !bytes.Equal(plain, back) {
		t.Fatalf("round trip mismatch: %q", back)
	}
}

// 固定 ID 与密码时的期望值由独立的 PDF 规范实现（Python）计算，
// 用于防止密钥派生逻辑被无意改动。
func TestPDFEncryptorKnownValues(t *testing.T) {
	sec := PDFSecurity{
		OpenPassword:  "user",
		OwnerPassword: "owner",
		AllowPrint:    true,
		AllowCopy:     false,
		AllowExtract:  false,
	}
	id, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	enc, err := newPDFEncryptorWithID(sec, id)
	if err != nil {
		t.Fatal(err)
	}
	const (
		wantP = int32(-1852)
		wantO = "0ba3835f88f90388e74e54584125ce142be0de24c6b0d37746e075b891756671"
		wantU = "c9a62844b26932ea7c98a8b0f41b95ac00000000000000000000000000000000"
	)
	if enc.p != wantP {
		t.Fatalf("P = %d, want %d", enc.p, wantP)
	}
	if got := hex.EncodeToString(enc.o); got != wantO {
		t.Fatalf("O = %s, want %s", got, wantO)
	}
	if got := hex.EncodeToString(enc.u); got != wantU {
		t.Fatalf("U = %s, want %s", got, wantU)
	}
}

func TestWritePDFWithoutPasswordSkipsEncryption(t *testing.T) {
	ref, ok := detectSystemFont()
	if !ok {
		t.Skip("no system font found")
	}
	doc := BuildDocument("无密码", true, 20, 600, 800)
	var buf bytes.Buffer
	if err := WritePDF(&buf, doc, RenderOptions{Font: ref}, PDFSecurity{AllowPrint: true}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "/Encrypt") {
		t.Fatal("unexpected /Encrypt without password")
	}
}

func firstLineWith(s, sub string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}
