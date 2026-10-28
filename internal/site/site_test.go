package site

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chriscoin-casino/internal/game"
	"chriscoin-casino/internal/ledger"
)

// demo builds a ledger exercising every board tile, cash-out status, and
// player state. SITE_PREVIEW=dir also writes the rendered page there.
func demo(t *testing.T) []byte {
	t.Helper()
	at := time.Date(2026, 10, 28, 9, 30, 0, 0, time.UTC)
	cfg := ledger.Config{UpstreamRepository: "cdlewis/ChrisCoin", TreasuryID: 9, StartingCommit: strings.Repeat("a", 40)}
	events := []ledger.Event{}
	add := func(e ledger.Event) {
		e.Version, e.Sequence, e.At = 1, len(events)+1, at.Add(time.Duration(len(events))*time.Minute)
		events = append(events, e)
		if _, err := ledger.Replay(events); err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
	}
	hash := ledger.Hash([]byte("demo"))
	add(ledger.Event{ID: "init", Type: "init", Config: &cfg, Repository: "baely/chriscoin-casino", UpstreamCommit: cfg.StartingCommit, UpstreamHash: hash, Treasury: 5000})
	players := []struct {
		id    int64
		login string
	}{{1, "bailey-butler"}, {2, "cdlewis"}, {3, "night-owl"}}
	credits := []ledger.Credit{}
	for i, p := range players {
		add(ledger.Event{ID: fmt.Sprintf("account-%d", p.id), Type: "account", UserID: p.id, Login: p.login, Issue: 10 + i})
		credits = append(credits, ledger.Credit{ID: fmt.Sprintf("payment-%d", p.id), UserID: p.id, Login: p.login, Amount: 201})
	}
	add(ledger.Event{ID: "sync-b", Type: "sync", UpstreamCommit: strings.Repeat("b", 40), UpstreamCount: 3, UpstreamHash: hash, Treasury: 5603, Credits: credits})
	comment := int64(100)
	play := func(user int64, name, bet string, stake int64, d *game.Draw) {
		comment++
		e := ledger.Event{ID: fmt.Sprintf("comment-%d", comment), Type: "wager", UserID: user, Issue: 9 + int(user), Comment: comment, BodyHash: hash, Amount: stake, Game: name, Bet: bet, GameVersion: 1}
		add(e)
		if d == nil {
			return
		}
		payout, err := game.Settle(name, bet, stake, *d)
		if err != nil {
			t.Fatal(err)
		}
		add(ledger.Event{ID: "settle-" + e.ID, Type: "settle", Ref: e.ID, GameVersion: 1, Draw: *d, Amount: payout})
	}
	play(1, "", "", 10, &game.Draw{Gate: 50, Return: 17})
	play(2, game.Roulette, "red", 20, &game.Draw{Pocket: 14})
	play(3, game.Slots, "", 5, &game.Draw{Reels: []int64{0, 1, 2}})
	play(1, game.Roulette, "17", 5, &game.Draw{Pocket: 17})
	play(2, "", "", 25, &game.Draw{Gate: 2})
	play(3, game.Slots, "", 4, &game.Draw{Reels: []int64{9, 9, 9}})
	play(1, game.Roulette, "even", 30, &game.Draw{Pocket: 0})
	play(2, game.Slots, "", 10, &game.Draw{Reels: []int64{0, 3, 1}})
	play(3, "", "", 12, &game.Draw{Gate: 70, Return: 12})
	play(1, game.Roulette, "1-12", 15, &game.Draw{Pocket: 8})
	comment++
	add(ledger.Event{ID: fmt.Sprintf("comment-%d", comment), Type: "withdraw", UserID: 2, Issue: 11, Comment: comment, BodyHash: hash, Amount: 50})
	add(ledger.Event{ID: fmt.Sprintf("attempt-comment-%d", comment), Type: "attempt", Ref: fmt.Sprintf("comment-%d", comment)})
	add(ledger.Event{ID: fmt.Sprintf("submitted-comment-%d", comment), Type: "submitted", Ref: fmt.Sprintf("comment-%d", comment), Issue: 61})
	comment++
	add(ledger.Event{ID: fmt.Sprintf("comment-%d", comment), Type: "withdraw", UserID: 1, Issue: 10, Comment: comment, BodyHash: hash, Amount: 30})
	comment++
	add(ledger.Event{ID: fmt.Sprintf("comment-%d", comment), Type: "freeze", UserID: 9, Target: 3, Issue: 12, Comment: comment, BodyHash: hash, Reason: "spam"})
	comment++
	add(ledger.Event{ID: fmt.Sprintf("comment-%d", comment), Type: "credit", UserID: 9, Target: 1, Issue: 10, Comment: comment, BodyHash: hash, Amount: 5, Reason: "outage"})
	play(2, game.Roulette, "black", 8, nil)
	return ledger.Serialize(events)
}

func TestBuildRendersEveryState(t *testing.T) {
	dir := t.TempDir()
	if preview := os.Getenv("SITE_PREVIEW"); preview != "" {
		dir = preview
	}
	if err := Build(demo(t), dir, strings.Repeat("c", 40)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`<span class="pocket red">14</span>`, `<span class="pocket green">0</span>`, "7️⃣7️⃣7️⃣", "1.7×", "0×",
		"8 CC riding", "won 40 on 20", "lost 25", "got 12 back", `<span class="frozen">frozen</span>`,
		"Clearing in ChrisCoin", "Queued", "commit ccccccc", "/roulette 10 1-12",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page is missing %q", want)
		}
	}
	if strings.Contains(page, "in fees") {
		t.Fatal("page shows house fee details")
	}
	again := t.TempDir()
	if err := Build(demo(t), again, strings.Repeat("c", 40)); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(filepath.Join(again, "index.html")); string(second) != page {
		t.Fatal("identical ledgers rendered different pages")
	}
}

func TestCoins(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "−4,500"} {
		if got := coins(n); got != want {
			t.Fatalf("coins(%d) = %q", n, got)
		}
	}
}
