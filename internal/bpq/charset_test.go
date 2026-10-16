package bpq

import (
	"strings"
	"testing"
)

func TestCharsetTablesCoverHighBytes(t *testing.T) {
	if len(cp437High) != 128 || len(cp1251High) != 128 {
		t.Fatalf("tablas de 128 runas esperadas: cp437=%d cp1251=%d", len(cp437High), len(cp1251High))
	}
	for index, character := range cp437High {
		if character == 0 {
			t.Fatalf("cp437High[%d] sin mapeo", index)
		}
	}
	for index, character := range cp1251High {
		if character == 0 {
			t.Fatalf("cp1251High[%d] sin mapeo", index)
		}
	}
	// Muestras verificadas contra utf8Routines.c de LinBPQ.
	cp437Samples := map[byte]rune{0x80: 0x00c7, 0xB3: 0x2502, 0xC4: 0x2500, 0xBF: 0x2510, 0xEA: 0x03a9, 0xF8: 0x00b0}
	for value, want := range cp437Samples {
		if got := cp437High[value-0x80]; got != want {
			t.Fatalf("cp437High[%#x]=%#x, want %#x", value, got, want)
		}
	}
	if got := cp1251High[0xC0-0x80]; got != 0x0410 {
		t.Fatalf("cp1251High[0xC0]=%#x, want %#x", got, rune(0x0410))
	}
}

func TestDecodeCP437AndCP1251(t *testing.T) {
	if got := decodeCP437([]byte{0xC4, 0xC4, 0xBF, 0xB3, 0xC0, 0xC3, 0xB4}); got != "──┐│└├┤" {
		t.Fatalf("box drawing CP437=%q", got)
	}
	if got := decodeCP437([]byte("50\xEA")); got != "50Ω" {
		t.Fatalf("ohm CP437=%q", got)
	}
	if got := decodeCP437([]byte{0xB0, 0xB1, 0xB2, 0xDB}); got != "░▒▓█" {
		t.Fatalf("shades CP437=%q", got)
	}
	if got := decodeCP437([]byte{0xA4, 0x82, 0xA0}); got != "ñéá" {
		t.Fatalf("accents CP437=%q", got)
	}
	if got := decodeCP1251([]byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}); got != "Привет" {
		t.Fatalf("cyrillic CP1251=%q", got)
	}
}

func TestGuessLegacyCharset(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want int
	}{
		{"dibujo de cajas", []byte("\xC4\xC4\xC4\xBF\xB3\xB3"), 437},
		{"español CP1252", []byte("Caf\xe9"), 1252},
		{"cirílico CP1251", []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}, 1251},
		{"ascii", []byte("hello"), 1252},
	}
	for _, item := range cases {
		if got := guessLegacyCharset(item.in); got != item.want {
			t.Fatalf("%s: guess=%d, want %d", item.name, got, item.want)
		}
	}
}

func TestDecodeAutoPreservesUTF8AndDetectsLegacy(t *testing.T) {
	utf8Text := "LinBPQPages es una aplicación web.\n"
	if got := decodeAuto([]byte(utf8Text)); got != utf8Text {
		t.Fatalf("UTF-8 válido=%q", got)
	}
	if got := decodeAuto([]byte("M\xe9xico, aplicaci\xf3n web")); got != "México, aplicación web" {
		t.Fatalf("CP1252=%q", got)
	}
	if got := decodeAuto([]byte{0xC4, 0xC4, 0xC4, 0xC4, 0xC4, 0xBF, 0xB3, 0xB3, 0xC3, 0xB4}); got != "─────┐││├┤" {
		t.Fatalf("CP437=%q", got)
	}
}

// TestMessageBodyDetectsUTF8WithoutDirmesFlag reproduce el caso de un cuerpo
// UTF-8 en un registro cuyo indicador UTF8 de DIRMES.SYS nunca fue marcado.
func TestMessageBodyDetectsUTF8WithoutDirmesFlag(t *testing.T) {
	text := "LinBPQPages es una aplicación web para leer y redactar mensajes en un BBS LinBPQ.\n"
	body, err := parseMessageBody([]byte(text))
	if err != nil || body.Body != strings.TrimSuffix(text, "\n") {
		t.Fatalf("cuerpo UTF-8=%q err=%v", body.Body, err)
	}
	if strings.Contains(body.Body, "Ã") {
		t.Fatalf("mojibake CP1252 en cuerpo UTF-8: %q", body.Body)
	}
}

// TestMessageBodyDetectsCP437Art reproduce el arte de cajas de origen DOS.
func TestMessageBodyDetectsCP437Art(t *testing.T) {
	raw := "Balun transformer notes\n\n" +
		"   Coax o)\xC4\xC4\xC4\xBF\n" +
		"    50\xEA   \xB3  )||\n" +
		"\xB3   \xC3\xC4\xC4\xC4\xC4\xC4\xC4\xC4\n" +
		"      ?\xB3   )||    50\xEA\n" +
		"       \\o\xC4\xB4||    Balanced\n" +
		"                 \xC0\xC4\xC4\xC4\xC4\xC4\xC4\xC4\n" +
		"The transformer losses/SWR were bad & the Marconi T mode was not used.\n"
	body, err := parseMessageBody([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Body, "o)───┐") || !strings.Contains(body.Body, "50Ω") || !strings.Contains(body.Body, "└───────") {
		t.Fatalf("arte CP437 mal decodificado:\n%s", body.Body)
	}
}

func TestMIMEDeclaredCharsetWins(t *testing.T) {
	declared, err := parseMessageBody([]byte("From: sender@example.invalid\nContent-Type: text/plain; charset=cp437\n\n50\xEA\n"))
	if err != nil || declared.Body != "50Ω" {
		t.Fatalf("charset declarado cp437=%q err=%v", declared.Body, err)
	}
	undeclared, err := parseMessageBody([]byte("From: sender@example.invalid\nContent-Type: text/plain\n\naplicación segura\n"))
	if err != nil || undeclared.Body != "aplicación segura" {
		t.Fatalf("parte MIME sin charset=%q err=%v", undeclared.Body, err)
	}
	if got := decodeTextBytes([]byte("Caf\xe9"), "windows-1252"); got != "Café" {
		t.Fatalf("windows-1252 declarado=%q", got)
	}
	if got := decodeTextBytes([]byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}, "windows-1251"); got != "Привет" {
		t.Fatalf("windows-1251 declarado=%q", got)
	}
	// Un charset desconocido cae en la detección por contenido.
	if got := decodeTextBytes([]byte("Caf\xe9"), "koi8-r"); got != "Café" {
		t.Fatalf("charset desconocido=%q", got)
	}
}

// TestDirmesAutoDetectsTitleCharset verifica que el indicador UTF8 del índice
// no controla la decodificación: el contenido decide.
func TestDirmesAutoDetectsTitleCharset(t *testing.T) {
	data := testDirmes([]testMessage{
		{number: 1, kind: 'B', status: 'N', title: []byte("Título UTF-8"), utf8: false},
		{number: 2, kind: 'B', status: 'N', title: []byte("M\xe9xico"), utf8: true},
		{number: 3, kind: 'B', status: 'N', title: []byte("Plan \xC4\xC4\xBF"), utf8: false},
	})
	_, messages, err := parseDirmes(data, CurrentLayout())
	if err != nil || len(messages) != 3 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if messages[2].Title != "Título UTF-8" || messages[2].UTF8 {
		t.Fatalf("título UTF-8 con indicador en 0: %+v", messages[2])
	}
	if messages[1].Title != "México" || !messages[1].UTF8 {
		t.Fatalf("título CP1252 con indicador en 1: %+v", messages[1])
	}
	if messages[0].Title != "Plan ──┐" {
		t.Fatalf("título CP437: %q", messages[0].Title)
	}
}
