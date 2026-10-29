package processor

import (
	"encoding/json"
	"strings"
	"testing"

	gh "chriscoin-casino/internal/github"
	"chriscoin-casino/internal/ledger"
)

func issueState(m *memory, n int) string {
	for _, i := range m.issues {
		if i.Number == n {
			return i.State
		}
	}
	return ""
}
func hasReply(m *memory, n int, text string) bool {
	for _, c := range m.comments[n] {
		if c.User.Type == "Bot" && strings.Contains(c.Body, text) {
			return true
		}
	}
	return false
}
func TestDuplicateClosesAndReopensOriginalOnce(t *testing.T) {
	p, m, _ := fixture(t)
	m.issues[0].State = "closed"
	m.issues = append(m.issues, gh.Issue{Number: 2, User: alice, Body: "some other body", State: "open"})
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Accounts[alice.ID].Issue != 1 || issueState(m, 1) != "open" || issueState(m, 2) != "closed" || m.reopens[1] != 1 {
		t.Fatalf("wrong lifecycle: accounts=%+v issues=%+v reopen=%v", s.Accounts, m.issues, m.reopens)
	}
	if !hasReply(m, 2, "[use your original issue](https://github.com/test/casino/issues/1)") {
		t.Fatal("duplicate missing original link")
	}
	m.issues[0].State = "closed"
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if issueState(m, 1) != "closed" || m.reopens[1] != 1 {
		t.Fatal("background recovery reopened original")
	}
	m.issues[1].State = "open"
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if issueState(m, 1) != "closed" || issueState(m, 2) != "closed" || m.reopens[1] != 1 {
		t.Fatal("reopening old duplicate reopened original")
	}
	m.issues = append(m.issues, gh.Issue{Number: 3, User: alice, State: "open"})
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if issueState(m, 1) != "open" || issueState(m, 3) != "closed" || m.reopens[1] != 2 {
		t.Fatal("new duplicate did not reopen original")
	}
}
func TestDuplicateRecoveryDoesNotKeepReopening(t *testing.T) {
	for _, failure := range []string{"close", "resolved-response"} {
		t.Run(failure, func(t *testing.T) {
			p, m, _ := fixture(t)
			m.issues[0].State = "closed"
			m.issues = append(m.issues, gh.Issue{Number: 2, User: alice, State: "open"})
			if failure == "close" {
				m.failClose = true
			} else {
				m.loseType = "duplicate-resolved"
			}
			if err := p.Run(ctx); err == nil {
				t.Fatal("expected lifecycle failure")
			}
			if err := p.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if issueState(m, 2) != "closed" {
				t.Fatal("duplicate not recovered")
			}
			m.issues[0].State = "closed"
			count := m.reopens[1]
			if err := p.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if issueState(m, 1) != "closed" || m.reopens[1] != count {
				t.Fatal("completed duplicate kept reopening original")
			}
		})
	}
}
func TestForeignCommentsRedirectToOwnIssueOrSignup(t *testing.T) {
	p, m, _ := fixture(t)
	bob := gh.User{ID: 2, Login: "bob", Type: "User"}
	carol := gh.User{ID: 3, Login: "carol", Type: "User"}
	m.issues = append(m.issues, gh.Issue{Number: 2, User: bob, Body: "anything", State: "open"})
	comment(m, 10, "hello", bob)
	comment(m, 11, "/wager 10", bob)
	comment(m, 12, "hello", carol)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"comment-10", "comment-11", "comment-12"} {
		if m.state(t).Events[id].Type != "rejected" {
			t.Fatal("foreign comment was not rejected", id)
		}
	}
	if !hasReply(m, 1, "[your casino account](https://github.com/test/casino/issues/2)") {
		t.Fatal("missing link to commenter's issue")
	}
	if !hasReply(m, 1, "[Open your own casino account](https://github.com/test/casino/issues/new?template=account.md)") {
		t.Fatal("missing signup link")
	}
	if len(m.state(t).Wagers) != 0 || m.state(t).Accounts[alice.ID].Available != 39 {
		t.Fatal("foreign comments changed funds")
	}
}
func TestWelcomeContainsCommandsAndOwnershipRules(t *testing.T) {
	p, m, _ := fixture(t)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Welcome!", "/deposit 100", "/wager 10", "/withdraw 50", "/balance", "1 CC", "Only you", "close this issue"} {
		if !hasReply(m, 1, text) {
			t.Errorf("welcome missing %q", text)
		}
	}
	count := 0
	for _, c := range m.comments[1] {
		if strings.Contains(c.Body, "<!-- casino-result:account -->") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("welcome duplicated: %d", count)
	}
}
func TestClosedAccountStaysClosedDuringCommands(t *testing.T) {
	p, m, _ := fixture(t)
	m.issues[0].State = "closed"
	comment(m, 10, "/wager 10", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if issueState(m, 1) != "closed" || m.reopens[1] != 0 || !m.state(t).Wagers["comment-10"].Settled {
		t.Fatal("command changed closure or lost wager")
	}
}
func TestCommandsOnDuplicateRedirectToOriginal(t *testing.T) {
	p, m, _ := fixture(t)
	m.issues = append(m.issues, gh.Issue{Number: 2, User: alice, State: "open"})
	m.comments[2] = []gh.Comment{{ID: 10, User: alice, Body: "/wager 10", CreatedAt: testNow, UpdatedAt: testNow}}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Events["comment-10"].Type != "rejected" || len(m.state(t).Wagers) != 0 || !hasReply(m, 2, "https://github.com/test/casino/issues/1") {
		t.Fatal("duplicate issue accepted wager or lacked redirect")
	}
}
func TestOriginalAccountCannotMove(t *testing.T) {
	p, m, _ := fixture(t)
	candidate := ledger.Event{ID: "account-again", Type: "account", UserID: alice.ID, Login: alice.Login, Issue: 2, Version: 1, Sequence: len(p.events) + 1, At: testNow}
	if _, err := ledger.Replay(append(append([]ledger.Event{}, p.events...), candidate)); err == nil {
		t.Fatal("ledger allowed account issue reassignment")
	}
	if m.state(t).Accounts[alice.ID].Issue != 1 {
		t.Fatal("original ownership changed")
	}
}

func TestDuplicateClosedBeforeWorkerStillReopensOriginalOnce(t *testing.T) {
	p, m, _ := fixture(t)
	m.issues[0].State = "closed"
	m.issues = append(m.issues, gh.Issue{Number: 2, User: alice, State: "closed"})
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if issueState(m, 1) != "open" || m.reopens[1] != 1 || issueState(m, 2) != "closed" {
		t.Fatal("closed duplicate was lost from recovery")
	}
	m.issues[0].State = "closed"
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if issueState(m, 1) != "closed" || m.reopens[1] != 1 {
		t.Fatal("old closed duplicate reopened original again")
	}
}

func TestIssueOpenerOwnsAccountRegardlessOfAssignees(t *testing.T) {
	p, m, _ := fixture(t)
	// Bob is assigned the issue, but Alice opened it.
	raw := []byte(`{"number":1,"state":"open","user":{"id":1,"login":"alice","type":"User"},"assignees":[{"id":2,"login":"bob","type":"User"}],"labels":[{"name":"bob"}],"body":"Account for bob"}`)
	if err := json.Unmarshal(raw, &m.issues[0]); err != nil {
		t.Fatal(err)
	}
	comment(m, 10, "/balance", alice)
	comment(m, 11, "/wager 10", gh.User{ID: 2, Login: "bob", Type: "User"})
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Events["comment-10"].Type != "balance" || s.Events["comment-11"].Type != "rejected" || s.Accounts[alice.ID].Issue != 1 || len(s.Wagers) != 0 {
		t.Fatal("assignee or issue text overrode opener identity")
	}
}
func TestRecordedAccountMustMatchIssueOpener(t *testing.T) {
	p, m, _ := fixture(t)
	m.issues[0].User = bot
	if err := p.Run(ctx); err == nil {
		t.Fatal("accepted an account issue opened by a different actor")
	}
}
func TestClosedCasinoRejectsEverythingAndMovesNothing(t *testing.T) {
	p, m, u := fixture(t)
	bob := gh.User{ID: 2, Login: "bob", Type: "User"}
	before := m.state(t)
	created := u.created
	p.Closed = true
	comment(m, 10, "/wager 10", alice)
	comment(m, 11, "/withdraw 20", alice)
	comment(m, 12, "/deposit 30", alice)
	comment(m, 13, "/credit 5 sorry", house)
	comment(m, 14, "where did the casino go?", alice)
	comment(m, 15, "just chatting", house)
	m.issues = append(m.issues, gh.Issue{Number: 2, User: bob, State: "open"})
	payDeposit(u, alice, alice, 500, 10)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Accounts[1].Available != before.Accounts[1].Available || len(s.Wagers) != 0 || len(s.Withdrawals) != 0 || len(s.Deposits) != len(before.Deposits) || s.UpstreamCommit != before.UpstreamCommit || s.Accounts[2] != nil || u.created != created {
		t.Fatalf("closed casino moved funds: %+v", s)
	}
	for _, id := range []string{"comment-10", "comment-11", "comment-12", "comment-13"} {
		if s.Events[id].Type != "rejected" {
			t.Fatalf("%s was not rejected", id)
		}
	}
	if s.Events["comment-14"].Type != "rejected" {
		t.Fatal("a player's plain comment should get the moved notice too")
	}
	if _, answered := s.Events["comment-15"]; answered {
		t.Fatal("the operator's chat is not a command")
	}
	if !hasReply(m, 1, "The Casino has moved!** https://casino.baileys.dev Your balance is waiting") || !hasReply(m, 2, "The Casino has moved!") {
		t.Fatal("missing moved notice")
	}
	p.Closed = false
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s = m.state(t)
	if s.Events["comment-10"].Type != "rejected" || len(s.Wagers) != 0 || s.Accounts[2] == nil || s.UpstreamCommit == before.UpstreamCommit {
		t.Fatal("reopening should resume normal work without running closed commands")
	}
}
