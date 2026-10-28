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
	if s.Accounts[1].Available != 29 || s.HouseBalance() != 101 || s.Liabilities() != 39 {
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
	if s.Accounts[1].Available != 26 || s.Fees != 2 || s.Liabilities() != 26 || s.HouseBalance() != 95 || !s.Withdrawals["comment-11"].Paid {
		t.Fatalf("settled accounting: %+v", s)
	}
}
func TestSafetyBoundaries(t *testing.T) {
	events := setup(t, 0, 100)
	mustFail(t, events, command(1, "wager", 100))
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
	mustFail(t, events, command(8, "wager", 99))
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

func TestLargeWagerLimitedOnlyByBalance(t *testing.T) {
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
	mustFail(t, events, command(21, "wager", 4_000_000_001))
	add(t, events, command(21, "wager", 4_000_000_000))
}
func TestInitializationMustMatchConfiguredStartingCommit(t *testing.T) {
	cfg := Config{UpstreamRepository: "test/coin", TreasuryID: 9, StartingCommit: strings.Repeat("a", 40)}
	mustFail(t, nil, Event{ID: "init", Type: "init", Config: &cfg, Repository: "test/casino", UpstreamCommit: strings.Repeat("b", 40), UpstreamHash: Hash(nil)})
	cfg.StartingCommit = "main"
	if err := cfg.Validate(); err == nil {
		t.Fatal("mutable ref accepted as starting commit")
	}
}
func admin(id int64, kind string, amount int64, reason string) Event {
	return Event{ID: fmt.Sprintf("comment-%d", id), Type: kind, UserID: 9, Target: 1, Issue: 1, Comment: id, BodyHash: Hash([]byte("test")), Amount: amount, Reason: reason}
}
func TestAdminCreditAndFreeze(t *testing.T) {
	events := setup(t, 100, 40)
	mustFail(t, events, admin(10, "credit", 5, ""))
	forged := admin(10, "credit", 5, "self-service")
	forged.UserID = 1
	mustFail(t, events, forged)
	treasury := admin(10, "credit", 5, "self-dealing")
	treasury.Target = 9
	mustFail(t, events, treasury)
	wrongIssue := admin(10, "credit", 5, "wrong thread")
	wrongIssue.Issue = 2
	mustFail(t, events, wrongIssue)
	events = add(t, events, admin(10, "credit", 5, "sorry about the outage"))
	s, _ := Replay(events)
	if s.Accounts[1].Available != 44 || s.Granted != 5 || s.HouseBalance() != 96 {
		t.Fatalf("credit accounting: %+v", s)
	}

	mustFail(t, events, admin(11, "unfreeze", 0, ""))
	mustFail(t, events, admin(11, "freeze", 0, ""))
	events = add(t, events, admin(11, "freeze", 0, "spam"))
	mustFail(t, events, admin(12, "freeze", 0, "again"))
	mustFail(t, events, command(12, "wager", 5))
	mustFail(t, events, command(12, "deposit", 5))
	events = add(t, events, command(12, "withdraw", 5))
	events = add(t, events, command(13, "balance", 39))
	events = add(t, events, admin(14, "unfreeze", 0, ""))
	add(t, events, command(15, "wager", 5))
}
func TestAdminCommandParsing(t *testing.T) {
	for body, want := range map[string]string{
		"/credit 5 sorry about the outage": "credit 5 sorry about the outage",
		"/freeze  spam\nlinks":             "freeze 0 spam links",
		"/unfreeze":                        "unfreeze 0 ",
		"/wager 5":                         " 0 ",
	} {
		command, n, reason, err := AdminCommand(body)
		if err != nil || fmt.Sprintf("%s %d %s", command, n, reason) != want {
			t.Fatalf("%q parsed as %q %d %q %v", body, command, n, reason, err)
		}
	}
	for _, body := range []string{"/credit 5", "/credit five reasons", "/credit 0 nothing", "/freeze", "/credit 5 " + strings.Repeat("x", 201)} {
		if command, _, _, err := AdminCommand(body); command == "" || err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
}
func TestBetsAreHonouredBeyondTheHouseBalance(t *testing.T) {
	events := setup(t, 0, 40) // the house holds nothing beyond player funds
	straight := command(10, "wager", 39)
	straight.Game, straight.Bet = game.Roulette, "17"
	events = add(t, events, straight)
	s, _ := Replay(events)
	if s.Exposure() != 35*39 || s.Accounts[1].Available != 0 {
		t.Fatalf("roulette exposure %d", s.Exposure())
	}
	mustFail(t, events, Event{ID: "settle-10", Type: "settle", Ref: "comment-10", GameVersion: 1, Draw: game.Draw{Pocket: 17}, Amount: 70})
	events = add(t, events, Event{ID: "settle-10", Type: "settle", Ref: "comment-10", GameVersion: 1, Draw: game.Draw{Pocket: 17}, Amount: 36 * 39})
	s, _ = Replay(events)
	if s.Accounts[1].Available != 1404 || s.HouseBalance() != 40-1404 || s.Exposure() != 0 {
		t.Fatalf("roulette win: %d, house %d", s.Accounts[1].Available, s.HouseBalance())
	}
	spin := command(11, "wager", 1000)
	spin.Game = game.Slots
	events = add(t, events, spin)
	if s, _ = Replay(events); s.Exposure() != 49_000 {
		t.Fatalf("slots exposure %d", s.Exposure())
	}
	events = add(t, events, admin(12, "credit", 500, "house is broke, have some anyway"))
	if s, _ = Replay(events); s.Accounts[1].Available != 904 {
		t.Fatalf("credit beyond the house balance: %d", s.Accounts[1].Available)
	}
	for _, bad := range []Event{{Game: game.Roulette, Bet: "RED"}, {Game: game.Roulette}, {Game: game.Slots, Bet: "red"}, {Game: "poker"}} {
		e := command(13, "wager", 1)
		e.Game, e.Bet = bad.Game, bad.Bet
		mustFail(t, events, e)
	}
}
func TestPlayParsing(t *testing.T) {
	for body, want := range map[string]string{"/roulette 10 RED": "roulette red 10", "/roulette 5 19-36": "roulette high 5", "/slots 3": "slots  3", "/wager 3": "  0"} {
		name, bet, n, err := Play(body)
		if err != nil || fmt.Sprintf("%s %s %d", name, bet, n) != want {
			t.Fatalf("%q parsed as %q %q %d %v", body, name, bet, n, err)
		}
	}
	for _, body := range []string{"/roulette 10", "/roulette red 10", "/roulette 10 37", "/slots", "/slots 10 red", "/slots 0"} {
		if name, _, _, err := Play(body); name == "" || err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
}
func TestWithdrawalsNeedTreasuryFundsFirstComeFirstServed(t *testing.T) {
	events := setup(t, 0, 40) // alice 39, treasury 40
	events = add(t, events, admin(10, "credit", 100, "jackpot bonus"))
	mustFail(t, events, command(11, "withdraw", 42))     // pays 41 > 40 held
	events = add(t, events, command(11, "withdraw", 31)) // pays 30 of 40
	s, _ := Replay(events)
	if s.Payable() != 10 || s.HouseBalance() != 40-139 {
		t.Fatalf("payable %d house %d", s.Payable(), s.HouseBalance())
	}
	mustFail(t, events, command(12, "withdraw", 12))
	add(t, events, command(12, "withdraw", 11))
}
