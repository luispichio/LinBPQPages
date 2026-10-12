package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"linbpqpages/internal/bpq"
)

func TestMessageVisibilityMatchesLinBPQ(t *testing.T) {
	member := bpq.User{Callsign: "MEMBER"}
	sysop := bpq.User{Callsign: "TSTBBS", Sysop: true}
	cases := []struct {
		name    string
		message bpq.Message
		user    bpq.User
		want    bool
	}{
		{"bulletin visible", bpq.Message{Type: "B", Status: "N"}, member, true},
		{"NTS visible", bpq.Message{Type: "T", Status: "F"}, member, true},
		{"processing bulletin follows CheckUserMsg", bpq.Message{Type: "B", Status: "$"}, member, true},
		{"killed hidden", bpq.Message{Type: "B", Status: "K"}, member, false},
		{"held hidden", bpq.Message{Type: "B", Status: "H"}, member, false},
		{"private sender visible", bpq.Message{Type: "P", Status: "N", From: "member", To: "OTHER"}, member, true},
		{"private recipient visible", bpq.Message{Type: "P", Status: "N", From: "OTHER", To: "MEMBER"}, member, true},
		{"private other hidden", bpq.Message{Type: "P", Status: "N", From: "OTHER", To: "SOMEONE"}, member, false},
		{"private ssid is not stripped", bpq.Message{Type: "P", Status: "N", From: "MEMBER-1", To: "OTHER"}, member, false},
		{"unknown type hidden from user", bpq.Message{Type: "X", Status: "N"}, member, false},
		{"sysop sees killed", bpq.Message{Type: "P", Status: "K"}, sysop, true},
		{"sysop sees held and unknown", bpq.Message{Type: "X", Status: "H"}, sysop, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := messageVisibleTo(test.message, test.user); got != test.want {
				t.Fatalf("messageVisibleTo(%+v, %+v)=%v, want %v", test.message, test.user, got, test.want)
			}
		})
	}
}

func TestLoginAndPrivateSessionCookie(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"callsign":"member","password":"member-pass"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != sessionCookieName || !cookie[0].HttpOnly || cookie[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected session cookie: %+v", cookie)
	}
	var result struct {
		User bpq.User `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.User.Callsign != "MEMBER" || result.User.Sysop {
		t.Fatalf("unexpected logged-in user: %+v", result.User)
	}
	if strings.Contains(response.Body.String(), "member-pass") || strings.Contains(response.Body.String(), "password") {
		t.Fatalf("login response leaked credential fields: %s", response.Body.String())
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"callsign":"member","password":"MEMBER-PASS"}`))
	bad.Header.Set("Content-Type", "application/json")
	badResponse := httptest.NewRecorder()
	server.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusUnauthorized {
		t.Fatalf("case-mismatched password status=%d", badResponse.Code)
	}
}

func TestListVisibilityBeforePaginationAndStatus(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/messages?page=1&page_size=3", nil)
	request = request.WithContext(withUser(request.Context(), bpq.User{Callsign: "MEMBER"}))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Messages []bpq.Message `json:"messages"`
		Total    int           `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 4 || len(result.Messages) != 3 {
		t.Fatalf("regular list total=%d page=%d, want 4 and 3", result.Total, len(result.Messages))
	}
	for _, message := range result.Messages {
		if message.Number == 3 || message.Number == 5 || message.Number == 6 || message.Number == 8 {
			t.Fatalf("invisible message %d leaked into list", message.Number)
		}
	}
	largePage := httptest.NewRequest(http.MethodGet, "/api/messages?page=9223372036854775807&page_size=50", nil)
	largePage = largePage.WithContext(withUser(largePage.Context(), bpq.User{Callsign: "MEMBER"}))
	largePageResponse := httptest.NewRecorder()
	server.ServeHTTP(largePageResponse, largePage)
	if largePageResponse.Code != http.StatusOK {
		t.Fatalf("large page status=%d body=%s", largePageResponse.Code, largePageResponse.Body.String())
	}
	var emptyPage struct {
		Messages []bpq.Message `json:"messages"`
	}
	if err := json.Unmarshal(largePageResponse.Body.Bytes(), &emptyPage); err != nil || len(emptyPage.Messages) != 0 {
		t.Fatalf("large page messages=%v err=%v", emptyPage.Messages, err)
	}

	sysopRequest := httptest.NewRequest(http.MethodGet, "/api/messages?page_size=50", nil)
	sysopRequest = sysopRequest.WithContext(withUser(sysopRequest.Context(), bpq.User{Callsign: "TSTBBS", Sysop: true}))
	sysopResponse := httptest.NewRecorder()
	server.ServeHTTP(sysopResponse, sysopRequest)
	var sysopResult struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(sysopResponse.Body.Bytes(), &sysopResult); err != nil {
		t.Fatal(err)
	}
	if sysopResult.Total != 8 {
		t.Fatalf("sysop total=%d, want 8", sysopResult.Total)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusRequest = statusRequest.WithContext(withUser(statusRequest.Context(), bpq.User{Callsign: "MEMBER"}))
	statusResponse := httptest.NewRecorder()
	server.ServeHTTP(statusResponse, statusRequest)
	var status bpq.Status
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.LatestNumber != 8 || status.TotalMessages != 8 || !status.ImportConfigured || status.DelaySeconds != 600 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestMessageDetailAttachmentAndPrivate404(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/messages/2", nil)
	request = request.WithContext(withUser(request.Context(), bpq.User{Callsign: "MEMBER"}))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Body          bpq.MessageBody `json:"body"`
		BodyAvailable bool            `json:"body_available"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.BodyAvailable || len(result.Body.Attachments) != 1 || result.Body.Attachments[0].URL != "/api/messages/2/attachments/0" {
		t.Fatalf("unexpected detail body: %+v", result)
	}
	attachmentRequest := httptest.NewRequest(http.MethodGet, result.Body.Attachments[0].URL, nil)
	attachmentRequest = attachmentRequest.WithContext(withUser(attachmentRequest.Context(), bpq.User{Callsign: "MEMBER"}))
	attachmentResponse := httptest.NewRecorder()
	server.ServeHTTP(attachmentResponse, attachmentRequest)
	if attachmentResponse.Code != http.StatusOK || !bytes.Equal(attachmentResponse.Body.Bytes(), []byte{1, 2, 3, 4}) {
		t.Fatalf("attachment status=%d bytes=%v", attachmentResponse.Code, attachmentResponse.Body.Bytes())
	}
	if got := attachmentResponse.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("unsafe attachment disposition: %q", got)
	}

	private := httptest.NewRequest(http.MethodGet, "/api/messages/3", nil)
	private = private.WithContext(withUser(private.Context(), bpq.User{Callsign: "MEMBER"}))
	privateResponse := httptest.NewRecorder()
	server.ServeHTTP(privateResponse, private)
	if privateResponse.Code != http.StatusNotFound {
		t.Fatalf("private detail status=%d, want 404", privateResponse.Code)
	}
}

func TestComposeAppendsOnlyImportQueue(t *testing.T) {
	server, root := newTestServer(t)
	token, err := server.sessions.create(bpq.User{Callsign: "MEMBER"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/messages", strings.NewReader(`{"type":"P","to":"MEMBER","via":"UPLINK","title":"Respuesta","body":"Una línea"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("compose status=%d body=%s", response.Code, response.Body.String())
	}
	queued, err := os.ReadFile(filepath.Join(root, "Mail", "Import", "mail.in"))
	if err != nil {
		t.Fatal(err)
	}
	if string(queued) != "SP MEMBER @ UPLINK < MEMBER\nRespuesta\nUna línea\n/EX\n" {
		t.Fatalf("unexpected queue content: %q", queued)
	}
	if _, err := os.Stat(filepath.Join(root, "DIRMES.SYS")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "linmail.cfg")); err != nil {
		t.Fatal(err)
	}
}

func TestComposeRejectsCrossOriginPost(t *testing.T) {
	server, _ := newTestServer(t)
	token, err := server.sessions.create(bpq.User{Callsign: "MEMBER"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/messages", strings.NewReader(`{"type":"P","to":"MEMBER","title":"x","body":"x"}`))
	request.Host = "pages.example.invalid"
	request.Header.Set("Origin", "https://attacker.example.invalid")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin compose status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMissingMessageBodyIsGraceful(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/messages/5", nil)
	request = request.WithContext(withUser(request.Context(), bpq.User{Callsign: "TSTBBS", Sysop: true}))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("missing body detail status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		BodyAvailable bool `json:"body_available"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.BodyAvailable {
		t.Fatalf("body_available=%v err=%v", result.BodyAvailable, err)
	}
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"Mail/Import", "HTML/7plus"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0750); err != nil {
			t.Fatal(err)
		}
	}
	config, err := os.ReadFile(filepath.Join(apiFixtureRoot(t), "linmail.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "linmail.cfg"), config, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "DIRMES.SYS"), makeAPIDirmes(), 0640); err != nil {
		t.Fatal(err)
	}
	mailFixture, err := os.ReadFile(filepath.Join(apiFixtureRoot(t), "Mail", "m_000002.mes"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Mail", "m_000002.mes"), mailFixture, 0640); err != nil {
		t.Fatal(err)
	}
	store, err := bpq.NewStore(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(store, time.Hour, nil, nil), root
}

func apiFixtureRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(source), "..", "..", "design", "fixtures")
}

func makeAPIDirmes() []byte {
	layout := bpq.CurrentLayout()
	type record struct {
		number int32
		kind   byte
		status byte
		from   string
		to     string
		title  string
	}
	messages := []record{
		{1, 'B', 'N', "OTHER", "ALL", "Public bulletin"},
		{2, 'P', 'N', "MEMBER", "TSTBBS", "Private for member"},
		{3, 'P', 'N', "OTHER", "SOMEONE", "Private for other"},
		{4, 'T', 'F', "OTHER", "NTS", "NTS traffic"},
		{5, 'B', 'K', "OTHER", "ALL", "Killed bulletin"},
		{6, 'P', 'H', "MEMBER", "OTHER", "Held private"},
		{7, 'B', '$', "OTHER", "ALL", "Processing bulletin"},
		{8, 'X', 'N', "OTHER", "SYSOP", "Sysop-only type"},
		{9, 0, 'N', "", "", "Invalid"},
	}
	data := make([]byte, layout.RecordSize*(len(messages)+1))
	control := data[:layout.RecordSize]
	control[layout.Status.Offset] = 2
	binary.LittleEndian.PutUint32(control[layout.Number.Offset:], uint32(len(messages)))
	binary.LittleEndian.PutUint32(control[layout.Length.Offset:], 8)
	for index, item := range messages {
		recordData := data[(index+1)*layout.RecordSize : (index+2)*layout.RecordSize]
		recordData[layout.Type.Offset] = item.kind
		recordData[layout.Status.Offset] = item.status
		binary.LittleEndian.PutUint32(recordData[layout.Number.Offset:], uint32(item.number))
		binary.LittleEndian.PutUint32(recordData[layout.Length.Offset:], 100)
		binary.LittleEndian.PutUint64(recordData[layout.DateReceived.Offset:], uint64(1_800_000_000+item.number))
		copy(recordData[layout.From.Offset:], item.from)
		copy(recordData[layout.To.Offset:], item.to)
		copy(recordData[layout.Title.Offset:], item.title)
		recordData[layout.UTF8.Offset] = 1
	}
	return data
}
