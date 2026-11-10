package main

import (
	"encoding/binary"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func readTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return decodeText(data)
}

func decodeText(data []byte) (string, error) {
	switch {
	case len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf:
		data = data[3:]
	case len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe:
		return decodeUTF16(data[2:], binary.LittleEndian), nil
	case len(data) >= 2 && data[0] == 0xfe && data[1] == 0xff:
		return decodeUTF16(data[2:], binary.BigEndian), nil
	}

	if utf8.Valid(data) {
		return strings.TrimPrefix(string(data), "\ufeff"), nil
	}

	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(decoded), "\ufeff"), nil
}

func decodeUTF16(data []byte, order binary.ByteOrder) string {
	data = data[:len(data)/2*2]
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = order.Uint16(data[i*2:])
	}
	return strings.TrimPrefix(string(utf16.Decode(units)), "\ufeff")
}
