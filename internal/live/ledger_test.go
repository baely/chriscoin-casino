package live

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"chriscoin-casino/internal/github"
	"chriscoin-casino/internal/web"
)

type fakeBank struct{ plays []web.Play }

func (b *fakeBank) Plays(_ context.Context, after uint64) ([]web.Play, error) {
	out := []web.Play{}
	for _, p := range b.plays {
		if p.Seq > after && len(out) < 2 { // pages of two
			out = append(out, p)
		}
	}
	return out, nil
}

type fakeRepo struct {
	file    []byte
	commits []string
	moved   int // main moves under the next n commits
}

func (r *fakeRepo) Main(context.Context) (string, error) { return "head", nil }
func (r *fakeRepo) File(context.Context, string, string) ([]byte, error) {
	if r.file == nil {
		return nil, &github.APIError{Status: 404}
	}
	return r.file, nil
}
func (r *fakeRepo) CommitFile(_ context.Context, _, _ string, content []byte, msg string) (string, error) {
	if r.moved > 0 {
		r.moved--
		return "", &github.APIError{Status: 422}
	}
	r.file, r.commits = append([]byte(nil), content...), append(r.commits, msg)
	return "sha", nil
}

func play(seq uint64, stake, payout int64) web.Play {
	return web.Play{Seq: seq, ID: "wager:x", At: time.Unix(int64(seq), 0), Player: 7, Login: "alice", Stake: stake, Payout: payout, Memo: "Roulette · red"}
}

func TestPublisherBatchesNewPlaysIntoOneCommit(t *testing.T) {
	bank, repo := &fakeBank{}, &fakeRepo{}
	p := &Publisher{Bank: bank, Repo: repo, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()

	if err := p.Step(ctx); err != nil || len(repo.commits) != 0 {
		t.Fatalf("nothing to publish: %v %v", err, repo.commits)
	}
	bank.plays = []web.Play{play(3, 10, 20), play(5, 10, 0), play(9, 100, 131)}
	repo.moved = 1 // the old worker merged a PR meanwhile
	if err := p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(repo.commits) != 1 || repo.commits[0] != "Live ledger: 3 plays (#1–#3)" {
		t.Fatalf("commits = %v", repo.commits)
	}
	l, err := Parse(repo.file)
	if err != nil || l.Head().N != 3 || l.BankSeq() != 9 {
		t.Fatalf("published ledger = %+v, %v", l, err)
	}

	// A restarted publisher picks up from what is on main.
	bank.plays = append(bank.plays, play(12, 5, 10))
	again := &Publisher{Bank: bank, Repo: repo, Log: p.Log}
	if err := again.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(repo.commits) != 2 || repo.commits[1] != "Live ledger: play #4" {
		t.Fatalf("commits = %v", repo.commits)
	}
	if err := again.Step(ctx); err != nil || len(repo.commits) != 2 {
		t.Fatalf("republished with nothing new: %v %v", err, repo.commits)
	}
}

func TestParseRejectsAnEditedLine(t *testing.T) {
	l, _ := Parse(nil)
	l.Append([]web.Play{play(1, 10, 20), play(2, 10, 0)})
	if _, err := Parse(l.Bytes()); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(l.Bytes()), `"payout":0`, `"payout":9`, 1)
	if _, err := Parse([]byte(edited)); err == nil {
		t.Fatal("an edited payout went unnoticed")
	}
}
