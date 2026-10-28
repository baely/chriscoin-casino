package processor

import (
	"fmt"
	"strings"
	"testing"
)

func TestRepliesRefreshCurrentBalanceWithoutReplayingCommands(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/balance", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	checkBalance := func(want int64) {
		t.Helper()
		for _, c := range m.comments[1] {
			if c.User.Type == "Bot" && !strings.Contains(c.Body, fmt.Sprintf("**Balance: %d CC**", want)) {
				t.Fatalf("stale balance in reply: %s", c.Body)
			}
		}
		if got := m.state(t).Accounts[alice.ID].Available; got != want {
			t.Fatalf("balance = %d, want %d", got, want)
		}
	}
	checkBalance(39)
	comment(m, 11, "/wager 10", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	checkBalance(46)
	if !hasReply(m, 1, "**7 CC up**") || !hasReply(m, 1, "**17 CC**") {
		t.Fatal("winning round must distinguish the profit from the payout")
	}
	deposit(t, p, m, u, alice, 1, alice, 9001, 10)
	checkBalance(55)
	// Pending withdrawals are excluded from the playable balance, including
	// in old wager replies and balance commands. Their ledger snapshots stay put.
	p.Payer = nil
	comment(m, 12, "/withdraw 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	checkBalance(35)
	s := m.state(t)
	if s.Events["comment-10"].Amount != 39 || s.Wagers["comment-11"].Payout != 17 {
		t.Fatal("refresh changed the recorded balance snapshot or round result")
	}
	events, replies := len(s.Events), len(m.comments[1])
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	checkBalance(35)
	if len(m.state(t).Events) != events || len(m.comments[1]) != replies {
		t.Fatal("refresh duplicated a command or bot reply")
	}
}
