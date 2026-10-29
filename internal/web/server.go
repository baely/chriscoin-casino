// Package web is the live casino: instant games, with Bailey Bank as the only
// ledger and the sign-in. A bet is drawn here and settled by the bank in one
// event, so the result is final before the page starts animating it.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

const sessionCookie = "casino_session"

// Config configures the casino web service.
type Config struct {
	BankURL    string // the bank's public URL: sign-in and links
	Secure     bool
	Bank       *Bank // the bank's app API
	Log        *slog.Logger
	SessionTTL time.Duration
}

type Server struct {
	cfg      Config
	sessions *sessions
	mux      *http.ServeMux
}

func New(cfg Config) *Server {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	s := &Server{cfg: cfg, sessions: newSessions(cfg.SessionTTL), mux: http.NewServeMux()}
	static, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /{$}", s.page("index.html", true))
	s.mux.HandleFunc("GET /login", s.page("login.html", false))
	s.mux.HandleFunc("GET /auth/start", s.start)
	s.mux.HandleFunc("GET /auth/callback", s.callback)
	s.mux.HandleFunc("POST /logout", s.logout)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	s.mux.HandleFunc("GET /api/me", s.api(s.me))
	s.mux.HandleFunc("POST /api/play", s.api(s.play))
	return s
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if s.cfg.Secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		s.mux.ServeHTTP(w, r)
	})
}

// Serve runs until ctx is cancelled, then drains requests for up to grace.
func (s *Server) Serve(ctx context.Context, addr string, grace time.Duration) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		return srv.Shutdown(c)
	}
}

func (s *Server) current(r *http.Request) *session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return s.sessions.get(c.Value)
}

// page serves a static page; the game needs a session, the login page doesn't.
func (s *Server) page(name string, signedIn bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if (s.current(r) != nil) != signedIn {
			http.Redirect(w, r, map[bool]string{true: "/login", false: "/"}[signedIn], http.StatusSeeOther)
			return
		}
		b, _ := staticFS.ReadFile("static/" + name)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}
}

// start sends the player to the bank to sign in.
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	state := randomToken(24)
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: state, Path: "/auth", MaxAge: 600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.cfg.Secure})
	http.Redirect(w, r, strings.TrimRight(s.cfg.BankURL, "/")+"/auth/app?state="+url.QueryEscape(state), http.StatusFound)
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// callback receives the bank's one-time code and swaps it for the player.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string) { http.Redirect(w, r, "/login?error="+url.QueryEscape(msg), http.StatusSeeOther) }
	c, err := r.Cookie(stateCookie)
	state := r.URL.Query().Get("state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		fail("Sign-in expired. Please try again.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Path: "/auth", MaxAge: -1})
	id, err := s.cfg.Bank.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		s.cfg.Log.Warn("sign-in code refused", "error", err)
		fail("Bailey Bank didn't confirm the sign-in. Please try again.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.sessions.create(id), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.cfg.Secure, MaxAge: int(s.cfg.SessionTTL.Seconds())})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sess := s.current(r); sess != nil {
		if r.Header.Get("X-CSRF-Token") != sess.csrf {
			http.Error(w, "bad csrf token", http.StatusForbidden)
			return
		}
		c, _ := r.Cookie(sessionCookie)
		s.sessions.delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) api(h func(http.ResponseWriter, *http.Request, *session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.current(r)
		if sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Please sign in again.")
			return
		}
		if r.Method != http.MethodGet && (!sameOrigin(r) || r.Header.Get("X-CSRF-Token") != sess.csrf) {
			writeError(w, http.StatusForbidden, "csrf", "Your session expired. Reload the page.")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r, sess)
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, sess *session) {
	a, house, err := s.cfg.Bank.Player(r.Context(), sess.ID)
	if err != nil {
		s.bankFailed(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": sess.ID, "login": sess.Login, "csrf": sess.csrf,
		"bankUrl": s.cfg.BankURL, "account": a, "house": house, "games": catalogue()})
}

func (s *Server) bankFailed(w http.ResponseWriter, err error) {
	var be *BankError
	if errors.As(err, &be) {
		writeError(w, http.StatusUnprocessableEntity, be.Code, be.Message)
		return
	}
	s.cfg.Log.Error("bank call failed", "error", err)
	writeError(w, http.StatusBadGateway, "bank_unavailable", "Bailey Bank isn't answering. Nothing was bet; try again in a moment.")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
