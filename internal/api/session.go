package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"linbpqpages/internal/bpq"
)

const sessionCookieName = "linbpq_session"

type session struct {
	user    bpq.User
	expires time.Time
}

type sessions struct {
	mu  sync.Mutex
	all map[string]session
	ttl time.Duration
}

func newSessions(ttl time.Duration) *sessions {
	return &sessions{all: make(map[string]session), ttl: ttl}
}

func (s *sessions) create(user bpq.User) (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)
	now := time.Now()
	s.mu.Lock()
	for existing, value := range s.all {
		if !now.Before(value.expires) {
			delete(s.all, existing)
		}
	}
	s.all[token] = session{user: user, expires: now.Add(s.ttl)}
	s.mu.Unlock()
	return token, nil
}

func (s *sessions) get(token string) (bpq.User, bool) {
	if token == "" {
		return bpq.User{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.all[token]
	if !ok {
		return bpq.User{}, false
	}
	if !now.Before(value.expires) {
		delete(s.all, token)
		return bpq.User{}, false
	}
	return value.user, true
}

func (s *sessions) delete(token string) {
	s.mu.Lock()
	delete(s.all, token)
	s.mu.Unlock()
}

func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
