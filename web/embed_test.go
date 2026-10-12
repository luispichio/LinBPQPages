package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedIndexAndTranslationsAreServed(t *testing.T) {
	for _, path := range []string{"/", "/app.js", "/i18n/es.js", "/i18n/en.js"} {
		request := httptest.NewRequest("GET", path, nil)
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("GET %s status=%d", path, response.Code)
		}
		body := response.Body.String()
		if path == "/" && !strings.Contains(body, "LinBPQ Pages") {
			t.Fatalf("embedded index at %s does not identify LinBPQPages", path)
		}
		if strings.TrimSpace(body) == "" {
			t.Fatalf("embedded asset %s is empty", path)
		}
	}
}
