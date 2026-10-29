// Package live publishes the live casino's settled bets to the repository as
// a public, hash-chained ledger (ledger/live.jsonl). Bailey Bank settles every
// bet; this only copies what the bank recorded.
package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"chriscoin-casino/internal/github"
	"chriscoin-casino/internal/web"
)

const Path = "ledger/live.jsonl"

// Entry is one line of the live ledger. Hash is the SHA-256 of the line
// encoded without its hash, so each line commits to every line before it.
type Entry struct {
	N       int       `json:"n"`
	At      time.Time `json:"at"`
	BankSeq uint64    `json:"bank_seq"`
	ID      string    `json:"id"`
	Player  int64     `json:"player"`
	Login   string    `json:"login"`
	Game    string    `json:"game"`
	Stake   int64     `json:"stake"`
	Payout  int64     `json:"payout"`
	Prev    string    `json:"prev"`
	Hash    string    `json:"hash,omitempty"`
}

func (e Entry) digest() string {
	e.Hash = ""
	raw, _ := json.Marshal(e)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Ledger is the live ledger's content and chain head.
type Ledger struct {
	data []byte
	last Entry
}

// Parse checks the chain and returns the ledger.
func Parse(data []byte) (*Ledger, error) {
	l := &Ledger{data: data}
	for i, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if e.N != l.last.N+1 || e.Prev != l.last.Hash || e.Hash != e.digest() || e.BankSeq <= l.last.BankSeq {
			return nil, fmt.Errorf("line %d breaks the chain", i+1)
		}
		l.last = e
	}
	return l, nil
}

// Append adds plays and reports how many were new.
func (l *Ledger) Append(plays []web.Play) int {
	n := 0
	for _, p := range plays {
		if p.Seq <= l.last.BankSeq {
			continue
		}
		e := Entry{N: l.last.N + 1, At: p.At.UTC(), BankSeq: p.Seq, ID: p.ID, Player: p.Player, Login: p.Login, Game: p.Memo, Stake: p.Stake, Payout: p.Payout, Prev: l.last.Hash}
		e.Hash = e.digest()
		raw, _ := json.Marshal(e)
		l.data = append(append(l.data, raw...), '\n')
		l.last = e
		n++
	}
	return n
}

func (l *Ledger) Bytes() []byte   { return l.data }
func (l *Ledger) Head() Entry     { return l.last }
func (l *Ledger) BankSeq() uint64 { return l.last.BankSeq }

// Repo is what the publisher needs from GitHub.
type Repo interface {
	Main(ctx context.Context) (string, error)
	File(ctx context.Context, path, ref string) ([]byte, error)
	CommitFile(ctx context.Context, base, path string, content []byte, message string) (string, error)
}

// Plays is what the publisher needs from the bank.
type Plays interface {
	Plays(ctx context.Context, after uint64) ([]web.Play, error)
}

// Publisher copies new bets from the bank into the repository, at most one
// commit per Interval however many bets arrived.
type Publisher struct {
	Bank     Plays
	Repo     Repo
	Interval time.Duration
	Log      *slog.Logger

	ledger    *Ledger
	published int // ledger.Head().N already on main
}

// load reads the ledger from main, or starts an empty one.
func (p *Publisher) load(ctx context.Context) error {
	head, err := p.Repo.Main(ctx)
	if err != nil {
		return err
	}
	data, err := p.Repo.File(ctx, Path, head)
	if github.IsStatus(err, 404) {
		data, err = nil, nil
	}
	if err != nil {
		return err
	}
	l, err := Parse(data)
	if err != nil {
		return fmt.Errorf("%s on main: %w", Path, err)
	}
	p.ledger, p.published = l, l.Head().N
	return nil
}

// Step fetches new bets and commits them if there are any.
func (p *Publisher) Step(ctx context.Context) error {
	if p.ledger == nil {
		if err := p.load(ctx); err != nil {
			return err
		}
	}
	for {
		plays, err := p.Bank.Plays(ctx, p.ledger.BankSeq())
		if err != nil {
			return err
		}
		if p.ledger.Append(plays) == 0 {
			break
		}
	}
	head := p.ledger.Head()
	if head.N == p.published {
		return nil
	}
	msg := fmt.Sprintf("Live ledger: %d plays (#%d–#%d)", head.N-p.published, p.published+1, head.N)
	if head.N == p.published+1 {
		msg = fmt.Sprintf("Live ledger: play #%d", head.N)
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var base string
		if base, err = p.Repo.Main(ctx); err != nil {
			return err
		}
		if _, err = p.Repo.CommitFile(ctx, base, Path, p.ledger.Bytes(), msg); err == nil {
			p.Log.Info("published live ledger", "plays", head.N-p.published, "head", head.N)
			p.published = head.N
			return nil
		}
		if !github.IsStatus(err, 422) {
			break // keep the plays; the next tick retries
		}
	}
	return err
}

// Run publishes until ctx ends.
func (p *Publisher) Run(ctx context.Context) {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		if err := p.Step(ctx); err != nil && !errors.Is(err, context.Canceled) {
			p.Log.Warn("could not publish the live ledger", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
