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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// App signs in as a GitHub App installed on one repository and hands out its
// installation token, renewing it before it expires. Use it as Client.Auth.
type App struct {
	ID         string
	Key        *rsa.PrivateKey
	Repository string // owner/name the App is installed on
	BaseURL    string
	HTTP       *http.Client
	Now        func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// ParseKey reads the private key GitHub generates for an App (PKCS#1 PEM), or
// a PKCS#8 one.
func ParseKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("the GitHub App key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App key is not an RSA key")
	}
	return rk, nil
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// jwt is the App's own short-lived credential, used only to get installation tokens.
func (a *App) jwt() (string, error) {
	enc := base64.RawURLEncoding
	now := a.now()
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": a.ID})
	unsigned := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + enc.EncodeToString(sig), nil
}

// Token returns an installation token for the repository.
func (a *App) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && a.now().Before(a.expires.Add(-5*time.Minute)) {
		return a.token, nil
	}
	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(a.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	as := &Client{BaseURL: base, Token: jwt, HTTP: hc}
	var inst struct {
		ID int64 `json:"id"`
	}
	if err := as.Global(ctx, "GET", "/repos/"+a.Repository+"/installation", nil, &inst); err != nil {
		return "", fmt.Errorf("finding the GitHub App's installation on %s: %w", a.Repository, err)
	}
	var tok struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := as.Global(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", inst.ID), map[string]any{}, &tok); err != nil {
		return "", fmt.Errorf("getting the GitHub App's token: %w", err)
	}
	a.token, a.expires = tok.Token, tok.ExpiresAt
	return a.token, nil
}
