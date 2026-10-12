package bpq

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

var windows1252High = [32]rune{
	0x20ac, 0x0081, 0x201a, 0x0192, 0x201e, 0x2026, 0x2020, 0x2021,
	0x02c6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008d, 0x017d, 0x008f,
	0x0090, 0x2018, 0x2019, 0x201c, 0x201d, 0x2022, 0x2013, 0x2014,
	0x02dc, 0x2122, 0x0161, 0x203a, 0x0153, 0x009d, 0x017e, 0x0178,
}

func bytesTrimRight(value []byte, cutset string) []byte {
	return bytes.TrimRight(value, cutset)
}

func decodeWindows1252(value []byte) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		if character >= 0x80 && character <= 0x9f {
			builder.WriteRune(windows1252High[character-0x80])
		} else {
			builder.WriteRune(rune(character))
		}
	}
	return builder.String()
}

func validUTF8(value []byte) string {
	if utf8.Valid(value) {
		return string(value)
	}
	return strings.ToValidUTF8(string(value), "�")
}
