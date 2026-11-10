package main

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeUTF8(t *testing.T) {
	got, err := decodeText([]byte("你好，世界"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "你好，世界" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeUTF8BOM(t *testing.T) {
	got, err := decodeText(append([]byte{0xef, 0xbb, 0xbf}, []byte("hello")...))
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeGB18030(t *testing.T) {
	data, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文编码"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeText(data)
	if err != nil {
		t.Fatal(err)
	}
	if got != "中文编码" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeUTF16BOM(t *testing.T) {
	units := utf16.Encode([]rune("你好"))
	data := []byte{0xff, 0xfe}
	for _, unit := range units {
		var pair [2]byte
		binary.LittleEndian.PutUint16(pair[:], unit)
		data = append(data, pair[:]...)
	}
	got, err := decodeText(data)
	if err != nil {
		t.Fatal(err)
	}
	if got != "你好" {
		t.Fatalf("got %q", got)
	}
}
