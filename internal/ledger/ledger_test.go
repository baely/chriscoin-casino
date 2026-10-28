package ledger

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"chriscoin-casino/internal/game"
)

var now = time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)

func add(t *testing.T, events []Event, e Event) []Event {
	t.Helper()
	e.Version = 1
	e.Sequence = len(events) + 1
	e.At = now
	events = append(append([]Event{}, events...), e)
	if _, err := Replay(events); err != nil {
		t.Fatal(err)
	}
	return events
}
func setup(t *testing.T, house, deposit int64) []Event {
	t.Helper()
	cfg := Config{UpstreamRepository: "test/coin", TreasuryID: 9, StartingCommit: strings.Repeat("a", 40)}
	e := add(t, nil, Event{ID: "init", Type: "init", Config: &cfg, Repository: "test/casino", UpstreamCommit: strings.Repeat("a", 40), UpstreamHash: Hash(nil), Treasury: house})
	e = add(t, e, Event{ID: "account-1", Type: "account", UserID: 1, Login: "alice", Issue: 1})
	return add(t, e, Event{ID: "sync-a", Type: "sync", UpstreamCommit: strings.Repeat("b", 40), UpstreamCount: 1, UpstreamHash: Hash([]byte("deposit")), Treasury: house + deposit, Credits: []Credit{{ID: "payment-1", UserID: 1, Login: "alice", Amount: deposit}}})
}
func command(id int64, kind string, amount int64) Event {
	return Event{ID: fmt.Sprintf("comment-%d", id), Type: kind, UserID: 1, Issue: 1, Comment: id, BodyHash: Hash([]byte("test")), Amount: amount, GameVersion: game.Version}
}
func mustFail(t *testing.T, events []Event, e Event) {
	t.Helper()
	e.Version = 1
	e.Sequence = len(events) + 1
	e.At = now
	if _, err := Replay(append(append([]Event{}, events...), e)); err == nil {
		t.Fatalf("accepted invalid event %+v", e)
	}
}
func TestAccountingAndFees(t *testing.T) {
	events := setup(t, 100, 40)
	events = add(t, events, command(10, "wager", 10))
	s, _ := Replay(events)
	if s.Accounts[1].Available != 29 || s.FreeHouse() != 91 || s.Liabilities() != 39 {
		t.Fatalf("acceptance accounting: %+v", s)
	}
	events = add(t, events, Event{ID: "settle-10", Type: "settle", Ref: "comment-10", GameVersion: 1, Draw: game.Draw{Gate: 50, Return: 17}, Amount: 17})
	events = add(t, events, command(11, "withdraw", 20))
	events = add(t, events, Event{ID: "attempt-11", Type: "attempt", Ref: "comment-11"})
	events = add(t, events, Event{ID: "submitted-11", Type: "submitted", Ref: "comment-11", Issue: 88})
	events = add(t, events, Event{ID: "sync-b", Type: "sync", UpstreamCommit: strings.Repeat("c", 40), UpstreamHash: Hash([]byte("withdrawal")), UpstreamCount: 2, Treasury: 121, Payments: []Payment{{ID: "payment-88", Withdrawal: "comment-11", Issue: 88, UserID: 1, Amount: 19}}})
	s, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if s.Accounts[1].Available != 26 || s.Fees != 2 || s.Liabilities() != 26 || s.FreeHouse() != 95 || !s.Withdrawals["comment-11"].Paid {
		t.Fatalf("settled accounting: %+v", s)
	}
}
func TestSafetyBoundaries(t *testing.T) {
	events := setup(t, 0, 100)
	mustFail(t, events, command(1, "wager", 10))
	e := command(2, "wager", 1)
	e.UserID = 2
	mustFail(t, events, e)
	e = command(3, "withdraw", 1)
	mustFail(t, events, e)
	e = command(4, "withdraw", 100)
	mustFail(t, events, e)
	e = command(5, "wager", 1)
	e.Issue = 999
	mustFail(t, events, e)
	e = command(6, "wager", 1)
	e.GameVersion = 2
	mustFail(t, events, e)
	events = add(t, events, command(7, "wager", 1))
	mustFail(t, events, command(8, "wager", 1))
	events = add(t, events, Event{ID: "settle-7", Type: "settle", Ref: "comment-7", GameVersion: 1, Draw: game.Draw{Gate: 99, Return: 2}, Amount: 2})
	mustFail(t, events, Event{ID: "settle-again", Type: "settle", Ref: "comment-7", GameVersion: 1, Draw: game.Draw{Gate: 99, Return: 2}, Amount: 2})
	mustFail(t, events, Event{ID: "duplicate-deposit", Type: "sync", UpstreamCommit: strings.Repeat("c", 40), UpstreamHash: Hash(nil), UpstreamCount: 2, Treasury: 200, Credits: []Credit{{ID: "payment-1", UserID: 1, Amount: 100}}})
}
func TestNoCasinoAllowance(t *testing.T) {
	cfg := Config{UpstreamRepository: "test/coin", TreasuryID: 9, StartingCommit: strings.Repeat("a", 40)}
	events := add(t, nil, Event{ID: "init", Type: "init", Config: &cfg, Repository: "test/casino", UpstreamCommit: strings.Repeat("a", 40), UpstreamHash: Hash(nil)})
	events = add(t, events, Event{ID: "account-1", Type: "account", UserID: 1, Login: "alice", Issue: 1})
	s, _ := Replay(events)
	if s.Accounts[1].Available != 0 {
		t.Fatal("casino issued an allowance")
	}
}
func TestStrictInput(t *testing.T) {
	for _, body := range []string{"/wager 0", "/wager -1", "/wager 1.5", "/withdraw 99999999999999999999", "/wager 10 /withdraw 10", "/wager +10", "/wager 01"} {
		if _, _, err := Command(body); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
	for _, body := range []string{"/wager 10", "/withdraw 2", "/balance"} {
		if _, _, err := Command(body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Parse([]byte("{\"unknown\":1}\n")); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestDepositIntentDoesNotCreateFunds(t *testing.T) {
	events := setup(t, 100, 40)
	events = add(t, events, command(10, "deposit", 20))
	s, _ := Replay(events)
	if s.Accounts[1].Available != 39 || s.Liabilities() != 39 || s.Fees != 1 {
		t.Fatal("deposit intent created money")
	}
	events = add(t, events, Event{ID: "deposit-submitted-comment-10", Type: "deposit-submitted", Ref: "comment-10", Issue: 100})
	events = add(t, events, command(11, "deposit", 20))
	mustFail(t, events, Event{ID: "deposit-submitted-comment-11", Type: "deposit-submitted", Ref: "comment-11", Issue: 100})
	bad := command(12, "deposit", 20)
	bad.UserID = 2
	mustFail(t, events, bad)
}

func TestLargeWagerLimitedByBalanceAndBacking(t *testing.T) {
	events := setup(t, 2_000_000_000, 2_000_000_001)
	events = add(t, events, command(20, "wager", 2_000_000_000))
	s, _ := Replay(events)
	if s.Accounts[1].Available != 0 || s.Exposure() != 2_000_000_000 {
		t.Fatal("funded large wager was not accepted")
	}
	events = add(t, events, Event{ID: "settle-20", Type: "settle", Ref: "comment-20", GameVersion: 1, Draw: game.Draw{Gate: 99, Return: 4_000_000_000}, Amount: 4_000_000_000})
	s, _ = Replay(events)
	if s.Accounts[1].Available != 4_000_000_000 {
		t.Fatal("large payout failed")
	}
	mustFail(t, events, command(21, "wager", 4_000_000_000))
}
func TestInitializationMustMatchConfiguredStartingCommit(t *testing.T) {
	cfg := Config{UpstreamRepository: "test/coin", TreasuryID: 9, StartingCommit: strings.Repeat("a", 40)}
	mustFail(t, nil, Event{ID: "init", Type: "init", Config: &cfg, Repository: "test/casino", UpstreamCommit: strings.Repeat("b", 40), UpstreamHash: Hash(nil)})
	cfg.StartingCommit = "main"
	if err := cfg.Validate(); err == nil {
		t.Fatal("mutable ref accepted as starting commit")
	}
}
