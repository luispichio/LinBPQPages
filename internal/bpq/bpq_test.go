package bpq

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(source), "..", "..", "design", "fixtures")
}

func TestCurrentLayoutAndSyntheticDirmes(t *testing.T) {
	layout := CurrentLayout()
	if layout.RecordSize != 308 || layout.Number.Offset != 2 || layout.UTF8.Offset != 246 || layout.DateReceived.Offset != 247 || layout.Spare.Offset+layout.Spare.Size != layout.RecordSize {
		t.Fatalf("unexpected packed layout: %+v", layout)
	}
	data := testDirmes([]testMessage{
		{number: 4, kind: 'B', status: 'N', size: 56, date: 1_800_000_000, from: "N0CALL", title: []byte("Título €"), utf8: true},
		{number: 2, kind: 'P', status: 'K', from: "MEMBER", to: "OTHER", title: []byte("M\x80xico"), utf8: false},
		{number: 3, kind: 'T', status: '$', title: []byte("Queued"), utf8: true},
		{number: 1, kind: 0, status: 'N', title: []byte("ignored"), utf8: true},
	})
	latest, messages, err := parseDirmes(data, layout)
	if err != nil {
		t.Fatal(err)
	}
	if latest != 4 || len(messages) != 3 {
		t.Fatalf("latest=%d messages=%d, want latest=4 and 3 valid records", latest, len(messages))
	}
	if messages[0].Number != 4 || messages[0].Date != 1_800_000_000 || messages[0].Title != "Título €" || !messages[0].UTF8 {
		t.Fatalf("unexpected newest record: %+v", messages[0])
	}
	if messages[1].Number != 3 || messages[1].Status != "$" {
		t.Fatalf("unexpected queued record: %+v", messages[1])
	}
	if messages[2].Title != "M€xico" {
		t.Fatalf("CP1252 title decoded as %q", messages[2].Title)
	}
}

func TestDirmesRejectsCorruptControlAndPartialRecord(t *testing.T) {
	layout := CurrentLayout()
	if _, _, err := parseDirmes([]byte{0, 1}, layout); err == nil {
		t.Fatal("partial DIRMES record accepted")
	}
	data := testDirmes(nil)
	data[1] = 0
	if _, _, err := parseDirmes(data, layout); err == nil {
		t.Fatal("invalid control record accepted")
	}
}

func TestMessagesServeLastGoodSnapshotDuringRewrite(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Mail", "Import"), 0750); err != nil {
		t.Fatal(err)
	}
	copyFixtureConfig(t, root)
	path := filepath.Join(root, "DIRMES.SYS")
	if err := os.WriteFile(path, testDirmes([]testMessage{{number: 1, kind: 'B', status: 'N', title: []byte("stable"), utf8: true}}), 0640); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Messages()
	if err != nil || len(first) != 1 || first[0].Title != "stable" {
		t.Fatalf("initial messages=%+v err=%v", first, err)
	}
	if err := os.WriteFile(path, []byte{1, 2, 3}, 0640); err != nil {
		t.Fatal(err)
	}
	second, err := store.Messages()
	if err != nil || len(second) != 1 || second[0].Title != "stable" {
		t.Fatalf("stale messages=%+v err=%v", second, err)
	}
}

func TestLinmailUsersAuthenticationAndImportHealth(t *testing.T) {
	root := fixtureRoot(t)
	store, err := NewStore(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	info, err := store.Instance()
	if err != nil {
		t.Fatal(err)
	}
	if info.BBSName != "TSTBBS" || info.SYSOPCall != "TSTBBS" || !info.Import.Configured || info.Import.IntervalSeconds != 600 {
		t.Fatalf("unexpected instance config: %+v", info)
	}
	user, ok, err := store.Authenticate("member", "member-pass")
	if err != nil || !ok || user.Callsign != "MEMBER" || user.Sysop {
		t.Fatalf("regular login user=%+v ok=%v err=%v", user, ok, err)
	}
	user, ok, err = store.Authenticate("tstbbs", "sysop-pass")
	if err != nil || !ok || !user.Sysop {
		t.Fatalf("sysop login user=%+v ok=%v err=%v", user, ok, err)
	}
	for _, attempt := range []struct{ call, password string }{
		{"MEMBER", "MEMBER-PASS"},
		{"EXCLUDED", "excluded-pass"},
		{"NOPASS", ""},
	} {
		if _, ok, err := store.Authenticate(attempt.call, attempt.password); err != nil || ok {
			t.Fatalf("unexpected login for %s: ok=%v err=%v", attempt.call, ok, err)
		}
	}
}

func TestLibconfigSubsetSupportsCommentsAndEscapes(t *testing.T) {
	config, err := parseLibconfig([]byte(`
# comentario de línea
main : { BBSName = "N0CALL"; Text = "línea\nsegunda"; Values = [ 1, 2, "tres" ]; };
/* comentario de bloque */
Users : { N0CALL = "Name^Pass"; }; // comentario final
`))
	if err != nil {
		t.Fatal(err)
	}
	main := configObject(config["main"])
	if configString(main, "BBSName") != "N0CALL" || configString(main, "Text") != "línea\nsegunda" {
		t.Fatalf("unexpected parsed main group: %#v", main)
	}
	values, ok := main["Values"].([]any)
	if !ok || len(values) != 3 {
		t.Fatalf("unexpected parsed array: %#v", main["Values"])
	}
}

func TestMailBodyFBBRFC822AndB2Attachments(t *testing.T) {
	store := &Store{root: fixtureRoot(t)}
	plain, err := store.ReadMessageBody(1)
	if err != nil || len(plain.Routing) != 1 || len(plain.Path) != 1 || plain.Path[0] != "TSTBBS" || !strings.Contains(plain.Body, "Segunda línea") {
		t.Fatalf("plain body=%+v err=%v", plain, err)
	}
	mail, err := store.ReadMessageBody(3)
	if err != nil || !strings.Contains(mail.Body, "pasarela") || len(mail.Routing) != 1 || len(mail.Path) != 1 || mail.Path[0] != "UPLINK" {
		t.Fatalf("RFC822 body=%+v err=%v", mail, err)
	}
	b2, err := store.ReadMessageBody(2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b2.Body, "Texto de prueba: ágil") || len(b2.Attachments) != 1 {
		t.Fatalf("unexpected B2 body: %+v", b2)
	}
	attachment := b2.Attachments[0]
	if attachment.Name != "nota.bin" || attachment.ContentType != "application/octet-stream" || string(attachment.Data) != "\x01\x02\x03\x04" {
		t.Fatalf("unexpected attachment: %+v data=%v", attachment, attachment.Data)
	}
	if _, err := store.ReadMessageBody(99); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing message error=%v, want os.ErrNotExist", err)
	}
	legacyText, err := parseMessageBody([]byte("Caf\xe9\r\n"))
	if err != nil || legacyText.Body != "Café" {
		t.Fatalf("Windows-1252 body=%q err=%v", legacyText.Body, err)
	}
}

func TestAppendMailAndValidation(t *testing.T) {
	root := t.TempDir()
	queue := filepath.Join(root, "Mail", "Import")
	if err := os.MkdirAll(queue, 0750); err != nil {
		t.Fatal(err)
	}
	store := &Store{root: root}
	request := ComposeRequest{Type: "P", To: "MEMBER", Via: "UPLINK.#AREA.LOCAL", Title: "Hola", Body: "Línea uno\r\nLínea dos"}
	if err := store.AppendMail("TSTBBS", request); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMail("TSTBBS", ComposeRequest{Type: "B", To: "ALL", Title: "Boletín", Body: "Contenido"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(queue, "mail.in"))
	if err != nil {
		t.Fatal(err)
	}
	want := "SP MEMBER @ UPLINK.#AREA.LOCAL < TSTBBS\nHola\nLínea uno\nLínea dos\n/EX\nSB ALL < TSTBBS\nBoletín\nContenido\n/EX\n"
	if string(data) != want {
		t.Fatalf("mail.in=%q, want %q", string(data), want)
	}
	entries, err := os.ReadDir(queue)
	if err != nil || len(entries) != 1 || entries[0].Name() != "mail.in" {
		t.Fatalf("unexpected queue files: %v err=%v", entries, err)
	}
	invalid := []ComposeRequest{
		{Type: "P", To: "MEMBER", Title: "bad\nSP OTHER", Body: "x"},
		{Type: "P", To: "MEMBER", Title: strings.Repeat("á", 31), Body: "x"},
		{Type: "P", To: "MEMBER", Title: "bad", Body: "x\n/EX"},
	}
	for _, item := range invalid {
		if err := item.Validate(); err == nil {
			t.Fatalf("invalid request accepted: %+v", item)
		}
	}
}

func TestAppendMailSerializesConcurrentMessages(t *testing.T) {
	root := t.TempDir()
	queue := filepath.Join(root, "Mail", "Import")
	if err := os.MkdirAll(queue, 0750); err != nil {
		t.Fatal(err)
	}
	store := &Store{root: root}
	const count = 16
	errorsFound := make(chan error, count)
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			err := store.AppendMail("TSTBBS", ComposeRequest{
				Type: "P", To: "MEMBER", Title: "Concurrent", Body: "payload-" + strconv.Itoa(index),
			})
			errorsFound <- err
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(queue, "mail.in"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "/EX\n"); got != count {
		t.Fatalf("found %d complete queue records, want %d: %q", got, count, data)
	}
}

func TestDecodedFilesDoesNotFollowSymlinks(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "mapa.txt"), []byte("mapa"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "list.csv"), []byte("private index"), 0640); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	store := &Store{filesPath: directory}
	files, err := store.DecodedFiles()
	if err != nil || len(files) != 1 || files[0].Name != "mapa.txt" {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	if _, _, err := store.ReadDecodedFile("leak.txt"); err == nil {
		t.Fatal("symlink was served")
	}
	for _, name := range []string{"../secret.txt", `..\secret.txt`, ""} {
		if _, _, err := store.ReadDecodedFile(name); err == nil {
			t.Fatalf("unsafe name %q was accepted", name)
		}
	}
}

func TestFixtureSevenPlusDirectory(t *testing.T) {
	store := &Store{filesPath: filepath.Join(fixtureRoot(t), "HTML", "7plus")}
	files, err := store.DecodedFiles()
	if err != nil || len(files) != 1 || files[0].Name != "demo.txt" || files[0].Primary == nil || !strings.HasPrefix(files[0].Primary.MIME, "text/plain") {
		t.Fatalf("fixture 7+ files=%+v err=%v", files, err)
	}
}

type testMessage struct {
	number int32
	kind   byte
	status byte
	size   int32
	date   int64
	from   string
	to     string
	title  []byte
	utf8   bool
}

func testDirmes(messages []testMessage) []byte {
	layout := CurrentLayout()
	data := make([]byte, layout.RecordSize*(len(messages)+1))
	control := data[:layout.RecordSize]
	control[layout.Status.Offset] = 2
	binary.LittleEndian.PutUint32(control[layout.Number.Offset:], uint32(len(messages)))
	latest := int32(0)
	for _, message := range messages {
		if message.number > latest {
			latest = message.number
		}
	}
	binary.LittleEndian.PutUint32(control[layout.Length.Offset:], uint32(latest))
	for index, message := range messages {
		record := data[(index+1)*layout.RecordSize : (index+2)*layout.RecordSize]
		record[layout.Type.Offset] = message.kind
		record[layout.Status.Offset] = message.status
		binary.LittleEndian.PutUint32(record[layout.Number.Offset:], uint32(message.number))
		binary.LittleEndian.PutUint32(record[layout.Length.Offset:], uint32(message.size))
		binary.LittleEndian.PutUint64(record[layout.DateReceived.Offset:], uint64(message.date))
		copy(record[layout.From.Offset:layout.From.Offset+layout.From.Size], message.from)
		copy(record[layout.To.Offset:layout.To.Offset+layout.To.Size], message.to)
		copy(record[layout.Title.Offset:layout.Title.Offset+layout.Title.Size], message.title)
		if message.utf8 {
			record[layout.UTF8.Offset] = 1
		}
	}
	return data
}

func copyFixtureConfig(t *testing.T, destination string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureRoot(t), "linmail.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "linmail.cfg"), data, 0640); err != nil {
		t.Fatal(err)
	}
}
