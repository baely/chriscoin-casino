package processor

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

var balancePattern = regexp.MustCompile(`\n\n\*\*Balance: -?[0-9]+ CC\*\* · available to play`)

func TestOnlyWelcomeAndLatestReplyShowCurrentBalance(t *testing.T) {
	p, m, u := fixture(t)
	replyFor := func(id string) string {
		t.Helper()
		for _, c := range m.comments[1] {
			if c.User.Type == "Bot" && strings.Contains(c.Body, "<!-- casino-result:"+id+" -->") {
				return c.Body
			}
		}
		t.Fatalf("no reply for %s", id)
		return ""
	}
	balanceIn := func(id string) string { return strings.TrimSpace(balancePattern.FindString(replyFor(id))) }
	shown := func(n int64) string { return fmt.Sprintf("**Balance: %d CC** · available to play", n) }
	check := func(want int64, older map[string]int64) {
		t.Helper()
		if got := m.state(t).Accounts[alice.ID].Available; got != want {
			t.Fatalf("balance = %d, want %d", got, want)
		}
		if !strings.Contains(balanceIn("account"), fmt.Sprintf("**Balance: %d CC**", want)) {
			t.Fatalf("welcome card shows %q, want %d", balanceIn("account"), want)
		}
		for id, n := range older {
			if got := balanceIn(id); got != shown(n) {
				t.Fatalf("%s shows %q, want %d", id, got, n)
			}
		}
	}

	comment(m, 10, "/balance", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	check(39, map[string]int64{"comment-10": 39, "comment-9000": 39})
	comment(m, 11, "/wager 10", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// The latest reply is current; the older /balance reply is left alone.
	check(46, map[string]int64{"comment-11": 46, "comment-10": 39})
	if !hasReply(m, 1, "**7 CC up**") || !hasReply(m, 1, "**17 CC**") {
		t.Fatal("winning round must distinguish the profit from the payout")
	}

	// A deposit link that settles after newer commands still updates its
	// status, but keeps the balance it showed when it was the latest.
	comment(m, 12, "/deposit 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	number := m.state(t).Deposits["comment-12"].UpstreamIssue
	comment(m, 13, "/balance", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	payDeposit(u, alice, alice, number, 20)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	check(65, map[string]int64{"comment-13": 65, "comment-12": 46, "comment-11": 46, "comment-10": 39})
	if !strings.Contains(replyFor("comment-12"), "Chips are in!") {
		t.Fatal("older deposit reply did not update its status")
	}

	// Two commands in one run: the earlier reply shows the balance just
	// before the later command, not the balance after it.
	comment(m, 14, "/balance", alice)
	comment(m, 15, "/wager 10", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	check(72, map[string]int64{"comment-15": 72, "comment-14": 65, "comment-13": 65})

	// Replies written before this rule showed the then-current balance.
	// Replay restores each one to the balance it would have shown.
	for i, c := range m.comments[1] {
		if strings.Contains(c.Body, "<!-- casino-result:comment-10 -->") {
			m.comments[1][i].Body = balancePattern.ReplaceAllLiteralString(c.Body, balanceLine(72))
		}
	}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	check(72, map[string]int64{"comment-10": 39})

	s := m.state(t)
	if s.Events["comment-10"].Amount != 39 || s.Wagers["comment-11"].Payout != 17 {
		t.Fatal("refresh changed the recorded balance snapshot or round result")
	}
	bodies := map[int64]string{}
	for _, c := range m.comments[1] {
		bodies[c.ID] = c.Body
	}
	events := len(s.Events)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.state(t).Events) != events || len(m.comments[1]) != len(bodies) {
		t.Fatal("refresh duplicated a command or bot reply")
	}
	for _, c := range m.comments[1] {
		if bodies[c.ID] != c.Body {
			t.Fatalf("an unchanged run edited a reply: %s", c.Body)
		}
	}
}
