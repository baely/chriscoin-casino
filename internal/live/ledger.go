// Package live keeps the public copy of Bailey Bank's ledger in this
// repository (ledger/bank.jsonl) and the casino ledger derived from it
// (ledger/live.jsonl). The bank settles every bet; the live casino's
// Publisher copies its new events here and commits them as a GitHub App.
package live

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"chriscoin-casino/internal/github"
)

const (
	BankPath = "ledger/bank.jsonl"
	Path     = "ledger/live.jsonl"
)

// Event is the part of a bank event the casino ledger needs.
type Event struct {
	Seq      uint64    `json:"seq"`
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	At       time.Time `json:"at"`
	From     int64     `json:"from"`
	To       int64     `json:"to"`
	Login    string    `json:"login"`
	Amount   int64     `json:"amount"`
	Payout   int64     `json:"payout"`
	Memo     string    `json:"memo"`
	ToBucket string    `json:"toBucket"`
	Prev     string    `json:"prev"`
	Hash     string    `json:"hash"`
}

var hashField = regexp.MustCompile(`"hash":"[0-9a-f]{64}"}$`)

// Bank is the bank's ledger exactly as the bank stored it.
type Bank struct {
	data   []byte
	events []Event
}

// ParseBank checks every line's number, link and hash. A bank event's hash is
// the SHA-256 of its JSON with the hash left empty.
func ParseBank(data []byte) (*Bank, error) {
	b := &Bank{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if err := b.add(sc.Bytes()); err != nil {
			return nil, err
		}
	}
	return b, sc.Err()
}

func (b *Bank) add(line []byte) error {
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return fmt.Errorf("bank event %d: %w", len(b.events)+1, err)
	}
	prev := ""
	if n := len(b.events); n > 0 {
		prev = b.events[n-1].Hash
	}
	if e.Seq != uint64(len(b.events))+1 || e.Prev != prev || !hashField.Match(line) {
		return fmt.Errorf("bank event %d breaks the chain", len(b.events)+1)
	}
	sum := sha256.Sum256(hashField.ReplaceAll(line, []byte(`"hash":""}`)))
	if hex.EncodeToString(sum[:]) != e.Hash {
		return fmt.Errorf("bank event %d has the wrong hash", e.Seq)
	}
	b.events = append(b.events, e)
	b.data = append(append(b.data, line...), '\n')
	return nil
}

func (b *Bank) Bytes() []byte { return b.data }
func (b *Bank) Head() uint64  { return uint64(len(b.events)) }

// Fetch appends the bank's new events from its public ledger and returns how
// many arrived.
func (b *Bank) Fetch(ctx context.Context, bankURL string, hc *http.Client) (int, error) {
	start := b.Head()
	for {
		before := b.Head()
		url := strings.TrimRight(bankURL, "/") + "/api/ledger?after=" + strconv.FormatUint(before, 10)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		res, err := hc.Do(req)
		if err != nil {
			return 0, err
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		res.Body.Close()
		if err != nil {
			return 0, err
		}
		if res.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("GET %s: %s", url, res.Status)
		}
		for _, line := range bytes.Split(bytes.TrimRight(body, "\n"), []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			if err := b.add(line); err != nil {
				return 0, err
			}
		}
		if b.Head() == before {
			return int(b.Head() - start), nil
		}
	}
}

// Entry is one line of the casino ledger: a bet the bank settled against the
// house. Hash is the SHA-256 of the line encoded without its hash, so each
// line commits to every line before it.
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

// Casino derives the casino ledger: every bet settled against houseLogin's
// houseBucket, with each player's username at the time.
func (b *Bank) Casino(houseLogin, houseBucket string) ([]byte, int) {
	logins := map[int64]string{}
	var house int64
	var out bytes.Buffer
	last := Entry{}
	for _, e := range b.events {
		switch e.Type {
		case "open", "rename":
			logins[e.To] = e.Login
			if strings.EqualFold(e.Login, houseLogin) {
				house = e.To
			}
		case "wager":
			if house == 0 || e.To != house || !strings.EqualFold(e.ToBucket, houseBucket) {
				continue
			}
			en := Entry{N: last.N + 1, At: e.At.UTC(), BankSeq: e.Seq, ID: e.ID, Player: e.From, Login: logins[e.From], Game: e.Memo, Stake: e.Amount, Payout: e.Payout, Prev: last.Hash}
			en.Hash = en.digest()
			raw, _ := json.Marshal(en)
			out.Write(raw)
			out.WriteByte('\n')
			last = en
		}
	}
	return out.Bytes(), last.N
}

// Repo is what the publisher needs from GitHub.
type Repo interface {
	Main(ctx context.Context) (string, error)
	File(ctx context.Context, path, ref string) ([]byte, error)
	CommitFiles(ctx context.Context, base string, files map[string][]byte, message string) (string, error)
}

// Publisher copies the bank's new events into the repository as the GitHub
// App, at most one commit per Interval however many events arrived.
type Publisher struct {
	BankURL     string
	HTTP        *http.Client
	Repo        Repo
	HouseLogin  string
	HouseBucket string
	Interval    time.Duration
	Log         *slog.Logger

	bank      *Bank
	published uint64 // bank events already on main
}

// load reads the copy on main, or starts an empty one.
func (p *Publisher) load(ctx context.Context) error {
	head, err := p.Repo.Main(ctx)
	if err != nil {
		return err
	}
	data, err := p.Repo.File(ctx, BankPath, head)
	if github.IsStatus(err, 404) {
		data, err = nil, nil
	}
	if err != nil {
		return err
	}
	b, err := ParseBank(data)
	if err != nil {
		return fmt.Errorf("%s on main: %w", BankPath, err)
	}
	p.bank, p.published = b, b.Head()
	return nil
}

// Step fetches the bank's new events and commits them if there are any.
func (p *Publisher) Step(ctx context.Context) error {
	if p.bank == nil {
		if err := p.load(ctx); err != nil {
			return err
		}
	}
	if _, err := p.bank.Fetch(ctx, p.BankURL, p.HTTP); err != nil {
		return err
	}
	head := p.bank.Head()
	if head == p.published {
		return nil
	}
	casino, plays := p.bank.Casino(p.HouseLogin, p.HouseBucket)
	files := map[string][]byte{BankPath: p.bank.Bytes(), Path: casino}
	msg := fmt.Sprintf("Bailey Bank ledger: events %d–%d (%d casino plays in all)", p.published+1, head, plays)
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var base string
		if base, err = p.Repo.Main(ctx); err != nil {
			return err
		}
		if _, err = p.Repo.CommitFiles(ctx, base, files, msg); err == nil {
			p.Log.Info("published the bank ledger", "events", head-p.published, "head", head)
			p.published = head
			return nil
		}
		if !github.IsStatus(err, 422) {
			break // keep the events; the next tick retries
		}
	}
	return err
}

// Run publishes until ctx ends.
func (p *Publisher) Run(ctx context.Context) {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		if err := p.Step(ctx); err != nil && ctx.Err() == nil {
			p.Log.Warn("could not publish the bank ledger", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
