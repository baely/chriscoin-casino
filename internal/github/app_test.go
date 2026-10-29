package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAppSignsInAndCachesItsToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	parsed, err := ParseKey(pemKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	issued := 0
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch r.URL.Path {
		case "/repos/baely/casino/installation", "/app/installations/42/access_tokens":
			parts := strings.Split(auth, ".")
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
				t.Errorf("bad JWT signature")
			}
			var claims map[string]any
			raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
			json.Unmarshal(raw, &claims)
			if claims["iss"] != "123" {
				t.Errorf("claims = %v", claims)
			}
			if r.URL.Path == "/repos/baely/casino/installation" {
				w.Write([]byte(`{"id":42}`))
				return
			}
			issued++
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_installation", "expires_at": now.Add(time.Hour)})
		case "/repos/baely/casino/git/ref/heads/main":
			if auth != "ghs_installation" {
				t.Errorf("API call used %q", auth)
			}
			w.Write([]byte(`{"object":{"sha":"abc"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer gh.Close()

	app := &App{ID: "123", Key: parsed, Repository: "baely/casino", BaseURL: gh.URL, Now: func() time.Time { return now }}
	c := &Client{Repository: "baely/casino", BaseURL: gh.URL, HTTP: gh.Client(), Auth: app.Token}
	for i := 0; i < 2; i++ {
		if sha, err := c.Main(context.Background()); err != nil || sha != "abc" {
			t.Fatalf("main = %q, %v", sha, err)
		}
	}
	if issued != 1 {
		t.Fatalf("issued %d tokens, want 1 (cached)", issued)
	}
	now = now.Add(56 * time.Minute) // within five minutes of expiry: renew
	c.Main(context.Background())
	if issued != 2 {
		t.Fatalf("issued %d tokens, want 2", issued)
	}
	if _, err := ParseKey([]byte("nope")); err == nil {
		t.Fatal("a non-PEM key was accepted")
	}
}
