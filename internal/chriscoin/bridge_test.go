package chriscoin

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"chriscoin-casino/internal/ledger"
)

var testNow = time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)

func upstreamEvent(n int, actor Actor, kind string, c Command, to int64) Event {
	return Event{Version: 1, Sequence: n, ID: fmt.Sprintf("issue-%d", n), Type: kind, Actor: actor, Source: Source{Issue: n, URL: fmt.Sprintf("https://github.com/test/coin/issues/%d", n), CreatedAt: testNow, BodySHA256: strings.Repeat("a", 64), Command: c}, Timestamp: testNow, RecipientID: to}
}
func TestSettledTransfersAndPrefix(t *testing.T) {
	treasury := Actor{ID: 9, Login: "house"}
	alice := Actor{ID: 1, Login: "alice"}
	events := []Event{upstreamEvent(1, treasury, "allowance", Command{Action: "claim"}, 0)}
	opening := ledger.State{Config: ledger.Config{TreasuryID: 9}, UpstreamCount: 1, UpstreamHash: ledger.Hash(Serialize(events)), Withdrawals: map[string]*ledger.Withdrawal{}, Deposits: map[string]*ledger.Deposit{"comment-1": {UpstreamIssue: 5}}}
	events = append(events, upstreamEvent(2, alice, "send", Command{Action: "offer", Recipient: "house", Amount: 20}, 9))
	events = append(events, upstreamEvent(3, alice, "offer", Command{Action: "offer", Recipient: "someone", Amount: 10}, 0))
	events = append(events, upstreamEvent(4, treasury, "accept", Command{Action: "accept", OfferID: "offer-3"}, 0))
	events = append(events, upstreamEvent(5, treasury, "request", Command{Action: "request", Payer: "alice", Amount: 5}, 0))
	events = append(events, upstreamEvent(6, alice, "fulfill", Command{Action: "fulfill", RequestID: "request-5"}, 0))
	// A request the treasury made by hand is not a casino deposit.
	events = append(events, upstreamEvent(7, treasury, "request", Command{Action: "request", Payer: "alice", Amount: 7}, 0))
	events = append(events, upstreamEvent(8, alice, "fulfill", Command{Action: "fulfill", RequestID: "request-7"}, 0))
	up, err := Read(Serialize(events), "test/coin")
	if err != nil {
		t.Fatal(err)
	}
	e, err := up.Sync(&opening, strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	// Only the casino request is credited; the send, accepted offer, and
	// hand-made request are tips to the house.
	if len(e.Credits) != 1 || e.Credits[0].ID != "issue-6" || e.Credits[0].UserID != 1 || e.Credits[0].Amount != 5 || e.Treasury != 142 {
		t.Fatalf("wrong settled credits: %+v", e)
	}
	opening.UpstreamHash = strings.Repeat("0", 64)
	if _, err := up.Sync(&opening, strings.Repeat("b", 40)); err == nil {
		t.Fatal("history rewrite accepted")
	}
	if _, err := Read(Serialize(events), "other/coin"); err == nil {
		t.Fatal("wrong upstream repository accepted")
	}
}
func TestWithdrawalMustMatchIdentityAndAmount(t *testing.T) {
	house := Actor{ID: 9, Login: "house"}
	alice := Actor{ID: 1, Login: "alice"}
	events := []Event{upstreamEvent(1, house, "allowance", Command{Action: "claim"}, 0), upstreamEvent(2, alice, "allowance", Command{Action: "claim"}, 0)}
	opening := ledger.State{Config: ledger.Config{TreasuryID: 9}, UpstreamCount: 2, UpstreamHash: ledger.Hash(Serialize(events)), Withdrawals: map[string]*ledger.Withdrawal{"comment-1": {Event: ledger.Event{UserID: 1, Amount: 20}, UpstreamIssue: 3}}}
	events = append(events, upstreamEvent(3, house, "send", Command{Action: "offer", Recipient: "alice", Amount: 19}, 1))
	up, err := Read(Serialize(events), "test/coin")
	if err != nil {
		t.Fatal(err)
	}
	e, err := up.Sync(&opening, strings.Repeat("b", 40))
	if err != nil || len(e.Payments) != 1 {
		t.Fatalf("payment not confirmed: %+v %v", e, err)
	}
	opening.Withdrawals["comment-1"].Event.Amount = 21
	if _, err := up.Sync(&opening, strings.Repeat("b", 40)); err == nil {
		t.Fatal("wrong withdrawal amount accepted")
	}
}
