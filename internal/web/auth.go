package web

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Identity is a GitHub account; the numeric ID is the bank account.
type Identity struct {
	ID    int64
	Login string
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Players sign in through Bailey Bank: the casino sends them to the bank's
// /auth/app, which signs them in (with GitHub, if needed) and returns them to
// /auth/callback with a one-time code. The casino swaps that code for their
// identity server to server, using its app token, so it never talks to GitHub.
const stateCookie = "casino_signin_state"

type session struct {
	Identity
	csrf    string
	expires time.Time
}

type sessions struct {
	mu  sync.Mutex
	m   map[string]*session
	ttl time.Duration
}

func newSessions(ttl time.Duration) *sessions {
	return &sessions{m: map[string]*session{}, ttl: ttl}
}

func (s *sessions) create(id Identity) string {
	tok := randomToken(32)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.m {
		if time.Now().After(v.expires) {
			delete(s.m, k)
		}
	}
	s.m[tok] = &session{Identity: id, csrf: randomToken(24), expires: time.Now().Add(s.ttl)}
	return tok
}

func (s *sessions) get(tok string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.m[tok]
	if sess == nil || time.Now().After(sess.expires) {
		delete(s.m, tok)
		return nil
	}
	return sess
}

func (s *sessions) delete(tok string) {
	s.mu.Lock()
	delete(s.m, tok)
	s.mu.Unlock()
}
