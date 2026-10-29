package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Bank is Bailey Bank's app API. The casino keeps no balances of its own:
// every bet is settled by the bank as one event between the player's bucket
// and the house bucket.
type Bank struct {
	URL   string // e.g. http://bank:8080
	Token string
	HTTP  *http.Client
}

type Bucket struct {
	Name    string `json:"name"`
	Balance int64  `json:"balance"`
}

type Account struct {
	ID      int64    `json:"id"`
	Login   string   `json:"login"`
	Balance int64    `json:"balance"`
	Buckets []Bucket `json:"buckets"`
}

type House struct {
	Login   string `json:"login"`
	Bucket  string `json:"bucket"`
	Balance int64  `json:"balance"`
}

type Tx struct {
	Seq    uint64 `json:"seq"`
	Stake  int64  `json:"stake"`
	Payout int64  `json:"payout"`
	Memo   string `json:"memo"`
}

// BankError is a refusal the player should see, such as insufficient funds.
type BankError struct {
	Status  int
	Code    string
	Message string
}

func (e *BankError) Error() string { return e.Message }

type playerReply struct {
	Account Account `json:"account"`
	House   House   `json:"house"`
}

type WagerReply struct {
	Transaction Tx      `json:"transaction"`
	Duplicate   bool    `json:"duplicate"`
	Account     Account `json:"account"`
	House       House   `json:"house"`
}

type Wager struct {
	Player    int64  `json:"player"`
	Bucket    string `json:"bucket"`
	Stake     int64  `json:"stake"`
	Payout    int64  `json:"payout"`
	MaxPayout int64  `json:"maxPayout"`
	Memo      string `json:"memo"`
	Key       string `json:"key"`
}

func (b *Bank) client() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (b *Bank) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(b.URL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := b.client().Do(req)
	if err != nil {
		return fmt.Errorf("bank unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" && res.StatusCode < 500 && e.Error.Code != "unauthenticated" {
			return &BankError{Status: res.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
		}
		return fmt.Errorf("bank %s %s: %s", method, path, res.Status)
	}
	return json.Unmarshal(raw, out)
}

// Exchange swaps a one-time sign-in code from the bank for the player's identity.
func (b *Bank) Exchange(ctx context.Context, code string) (Identity, error) {
	var r struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := b.do(ctx, http.MethodPost, "/api/app/auth", map[string]string{"code": code}, &r); err != nil {
		return Identity{}, err
	}
	return Identity{ID: r.ID, Login: r.Login}, nil
}

func (b *Bank) Player(ctx context.Context, id int64) (Account, House, error) {
	var r playerReply
	err := b.do(ctx, http.MethodGet, "/api/app/players/"+strconv.FormatInt(id, 10), nil, &r)
	return r.Account, r.House, err
}

func (b *Bank) Wager(ctx context.Context, w Wager) (WagerReply, error) {
	var r WagerReply
	err := b.do(ctx, http.MethodPost, "/api/app/wagers", w, &r)
	return r, err
}

// Play is one bet the bank settled against the house bucket.
type Play struct {
	Seq    uint64    `json:"seq"`
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	Player int64     `json:"player"`
	Login  string    `json:"login"`
	Stake  int64     `json:"stake"`
	Payout int64     `json:"payout"`
	Memo   string    `json:"memo"`
}

// Plays lists settled bets after the bank event seq, oldest first, a page at a time.
func (b *Bank) Plays(ctx context.Context, after uint64) ([]Play, error) {
	var r struct {
		Plays []Play `json:"plays"`
	}
	err := b.do(ctx, http.MethodGet, "/api/app/plays?after="+strconv.FormatUint(after, 10), nil, &r)
	return r.Plays, err
}
