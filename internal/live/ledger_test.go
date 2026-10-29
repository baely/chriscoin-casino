package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"chriscoin-casino/internal/github"
)

// bankLines builds bank events the way Bailey Bank stores them.
func bankLines(t *testing.T, events ...map[string]any) []string {
	t.Helper()
	var lines []string
	prev := ""
	for i, e := range events {
		e["seq"], e["prev"], e["at"] = i+1, prev, time.Unix(int64(i), 0).UTC()
		e["hash"] = ""
		raw, _ := json.Marshal(e)
		// Bailey Bank puts hash last; json.Marshal sorts map keys, so move it.
		s := strings.Replace(string(raw), `,"hash":""`, "", 1)
		s = strings.TrimSuffix(s, "}") + `,"hash":""}`
		sum := sha256.Sum256([]byte(s))
		prev = hex.EncodeToString(sum[:])
		lines = append(lines, strings.TrimSuffix(s, `"hash":""}`)+`"hash":"`+prev+`"}`)
	}
	return lines
}

func sample(t *testing.T) []string {
	return bankLines(t,
		map[string]any{"id": "open-1", "type": "open", "to": 9, "login": "baely"},
		map[string]any{"id": "bucket-open-2", "type": "bucket-open", "to": 9, "bucket": "Casino"},
		map[string]any{"id": "open-3", "type": "open", "to": 7, "login": "alice"},
		map[string]any{"id": "w1", "type": "wager", "from": 7, "to": 9, "amount": 10, "payout": 20, "memo": "Roulette · red", "bucket": "Main", "toBucket": "Casino"},
		map[string]any{"id": "x1", "type": "transfer", "from": 7, "to": 9, "amount": 5, "bucket": "Main", "toBucket": "Main"},
		map[string]any{"id": "rename-6", "type": "rename", "to": 7, "login": "alicia"},
		map[string]any{"id": "w2", "type": "wager", "from": 7, "to": 9, "amount": 100, "memo": "Lucky Dip", "bucket": "Main", "toBucket": "casino"},
	)
}

func TestFetchCopiesTheBankLedgerAndDerivesTheCasinoLedger(t *testing.T) {
	lines := sample(t)
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		for i := after; i < len(lines) && i < after+3; i++ { // pages of three
			fmt.Fprintln(w, lines[i])
		}
	}))
	defer bank.Close()

	b, _ := ParseBank([]byte(lines[0] + "\n"))
	n, err := b.Fetch(context.Background(), bank.URL, bank.Client())
	if err != nil || n != 6 || b.Head() != 7 {
		t.Fatalf("fetched %d (head %d): %v", n, b.Head(), err)
	}
	if string(b.Bytes()) != strings.Join(lines, "\n")+"\n" {
		t.Fatal("the copy differs from what the bank stored")
	}
	if n, _ := b.Fetch(context.Background(), bank.URL, bank.Client()); n != 0 {
		t.Fatalf("refetched %d", n)
	}

	casino, plays := b.Casino("baely", "Casino")
	rows := strings.Split(strings.TrimSpace(string(casino)), "\n")
	if plays != 2 || len(rows) != 2 {
		t.Fatalf("casino ledger = %q", casino)
	}
	var first, second Entry
	json.Unmarshal([]byte(rows[0]), &first)
	json.Unmarshal([]byte(rows[1]), &second)
	if first.Login != "alice" || first.Stake != 10 || first.Payout != 20 || first.BankSeq != 4 || second.Login != "alicia" || second.Prev != first.Hash || second.Hash != second.digest() {
		t.Fatalf("entries = %+v %+v", first, second)
	}
}

func TestParseBankRejectsAnEditedEvent(t *testing.T) {
	lines := sample(t)
	if _, err := ParseBank([]byte(strings.Join(lines, "\n"))); err != nil {
		t.Fatal(err)
	}
	edited := append([]string{}, lines...)
	edited[3] = strings.Replace(edited[3], `"payout":20`, `"payout":90`, 1)
	if _, err := ParseBank([]byte(strings.Join(edited, "\n"))); err == nil {
		t.Fatal("an edited payout went unnoticed")
	}
	if _, err := ParseBank([]byte(strings.Join(append(lines[:2:2], lines[3:]...), "\n"))); err == nil {
		t.Fatal("a removed event went unnoticed")
	}
}

type fakeRepo struct {
	files   map[string][]byte
	commits []string
	moved   int // main moves under the next n commits
}

func (r *fakeRepo) Main(context.Context) (string, error) { return "head", nil }
func (r *fakeRepo) File(_ context.Context, path, _ string) ([]byte, error) {
	if f, ok := r.files[path]; ok {
		return f, nil
	}
	return nil, &github.APIError{Status: 404}
}
func (r *fakeRepo) CommitFiles(_ context.Context, _ string, files map[string][]byte, msg string) (string, error) {
	if r.moved > 0 {
		r.moved--
		return "", &github.APIError{Status: 422}
	}
	if r.files == nil {
		r.files = map[string][]byte{}
	}
	for p, f := range files {
		r.files[p] = append([]byte(nil), f...)
	}
	r.commits = append(r.commits, msg)
	return "sha", nil
}

func TestPublisherCommitsNewEventsOnce(t *testing.T) {
	lines := sample(t)
	served := 4
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		for i := after; i < served; i++ {
			fmt.Fprintln(w, lines[i])
		}
	}))
	defer bank.Close()
	repo := &fakeRepo{moved: 1} // the casino worker merged a PR meanwhile
	newPub := func() *Publisher {
		return &Publisher{BankURL: bank.URL, HTTP: bank.Client(), Repo: repo, HouseLogin: "baely", HouseBucket: "Casino", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	}
	p := newPub()
	ctx := context.Background()
	if err := p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(repo.commits) != 1 || repo.commits[0] != "Bailey Bank ledger: events 1–4 (1 casino plays in all)" {
		t.Fatalf("commits = %v", repo.commits)
	}
	if err := p.Step(ctx); err != nil || len(repo.commits) != 1 {
		t.Fatalf("committed with nothing new: %v %v", err, repo.commits)
	}

	// A restarted publisher carries on from what is on main.
	served = len(lines)
	if err := newPub().Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(repo.commits) != 2 || repo.commits[1] != "Bailey Bank ledger: events 5–7 (2 casino plays in all)" {
		t.Fatalf("commits = %v", repo.commits)
	}
	if string(repo.files[BankPath]) != strings.Join(lines, "\n")+"\n" || strings.Count(string(repo.files[Path]), "\n") != 2 {
		t.Fatalf("files = %q", repo.files)
	}
}
