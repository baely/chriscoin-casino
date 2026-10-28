package processor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	cc "chriscoin-casino/internal/chriscoin"
	"chriscoin-casino/internal/game"
	gh "chriscoin-casino/internal/github"
	"chriscoin-casino/internal/ledger"
)

var testNow = time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
var alice = gh.User{ID: 1, Login: "alice", Type: "User"}
var house = gh.User{ID: 9, Login: "house", Type: "User"}
var bot = gh.User{ID: 41898282, Login: "github-actions[bot]", Type: "Bot"}
var ctx = context.Background()

type memory struct {
	reopens   map[int]int
	failClose bool
	main      string
	files     map[string][]byte
	prs       map[string]*gh.PullRequest
	issues    []gh.Issue
	comments  map[int][]gh.Comment
	next      int
	failType  string
	loseType  string
	failReply bool
}

func newMemory() *memory {
	return &memory{main: strings.Repeat("a", 40), files: map[string][]byte{strings.Repeat("a", 40): {}}, prs: map[string]*gh.PullRequest{}, comments: map[int][]gh.Comment{}}
}
func (m *memory) Main(context.Context) (string, error) { return m.main, nil }
func (m *memory) File(_ context.Context, _ string, ref string) ([]byte, error) {
	v, ok := m.files[ref]
	if !ok {
		return nil, &gh.APIError{Status: 404}
	}
	return append([]byte{}, v...), nil
}
func (m *memory) WriteBranch(_ context.Context, base, branch string, data []byte, _ string) (string, error) {
	if base != m.main {
		return "", errors.New("stale base")
	}
	m.next++
	sha := fmt.Sprintf("%040x", m.next)
	m.files[branch] = append([]byte{}, data...)
	m.files[sha] = append([]byte{}, data...)
	if pr := m.prs[branch]; pr != nil {
		pr.Head.SHA = sha
	}
	return sha, nil
}
func (m *memory) FindPR(_ context.Context, branch string) (*gh.PullRequest, error) {
	return m.prs[branch], nil
}
func (m *memory) CreatePR(_ context.Context, branch, _, _ string) (*gh.PullRequest, error) {
	pr := &gh.PullRequest{Number: len(m.prs) + 1, State: "open", User: bot}
	pr.Head.Ref = branch
	pr.Head.Repo.FullName = "test/casino"
	for sha, data := range m.files {
		if len(sha) == 40 && string(data) == string(m.files[branch]) {
			pr.Head.SHA = sha
		}
	}
	m.prs[branch] = pr
	return pr, nil
}
func (m *memory) Merge(_ context.Context, number int, sha string) error {
	for _, pr := range m.prs {
		if pr.Number == number {
			if pr.Head.SHA != sha {
				return errors.New("unexpected head")
			}
			events, err := ledger.Parse(m.files[sha])
			if err != nil {
				return err
			}
			kind := events[len(events)-1].Type
			if m.failType == kind {
				m.failType = ""
				return errors.New("interrupted before merge")
			}
			m.main = sha
			pr.Merged = true
			pr.State = "closed"
			if m.loseType == kind {
				m.loseType = ""
				return errors.New("merge succeeded; response lost")
			}
			return nil
		}
	}
	return errors.New("missing PR")
}
func (m *memory) Issues(context.Context) ([]gh.Issue, error) { return m.issues, nil }
func (m *memory) Comments(_ context.Context, issue int) ([]gh.Comment, error) {
	return append([]gh.Comment{}, m.comments[issue]...), nil
}
func (m *memory) Comment(_ context.Context, issue int, body string) error {
	if m.failReply {
		m.failReply = false
		return errors.New("reply failed")
	}
	m.next++
	m.comments[issue] = append(m.comments[issue], gh.Comment{ID: int64(10000 + m.next), User: bot, Body: body})
	return nil
}
func (m *memory) UpdateComment(_ context.Context, id int64, body string) error {
	for i, comments := range m.comments {
		for j := range comments {
			if comments[j].ID == id {
				m.comments[i][j].Body = body
				return nil
			}
		}
	}
	return errors.New("missing comment")
}
func (m *memory) CloseIssue(_ context.Context, n int) error {
	if m.failClose {
		m.failClose = false
		return errors.New("close failed")
	}
	for i := range m.issues {
		if m.issues[i].Number == n {
			m.issues[i].State = "closed"
		}
	}
	return nil
}
func (m *memory) state(t *testing.T) *ledger.State {
	t.Helper()
	e, err := ledger.Parse(m.files[m.main])
	if err != nil {
		t.Fatal(err)
	}
	s, err := ledger.Replay(e)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type upstream struct {
	events     []cc.Event
	issues     []gh.Issue
	created    int
	loseCreate bool
	rejected   bool
	user       gh.User
}

func (u *upstream) Main(context.Context) (string, error) {
	return fmt.Sprintf("%040x", 1000+len(u.events)), nil
}
func (u *upstream) File(_ context.Context, _ string, ref string) ([]byte, error) {
	var number int
	if _, err := fmt.Sscanf(ref, "%x", &number); err != nil {
		return nil, err
	}
	count := number - 1000
	if count < 0 || count > len(u.events) {
		return nil, &gh.APIError{Status: 404}
	}
	return cc.Serialize(u.events[:count]), nil
}
func (u *upstream) Issues(context.Context) ([]gh.Issue, error) { return u.issues, nil }
func (u *upstream) Me(context.Context) (gh.User, error) {
	if u.user.ID != 0 {
		return u.user, nil
	}
	return house, nil
}
func (u *upstream) User(_ context.Context, login string) (gh.User, error) {
	if login == "house" {
		return house, nil
	}
	return alice, nil
}
func (u *upstream) CreateIssue(_ context.Context, title, body string) (gh.Issue, error) {
	u.created++
	i := gh.Issue{Number: 100 + u.created, Title: title, Body: body, User: house, State: "open"}
	u.issues = append(u.issues, i)
	if u.loseCreate {
		u.loseCreate = false
		return gh.Issue{}, errors.New("issue created; response lost")
	}
	return i, nil
}
func (u *upstream) add(user gh.User, kind string, command cc.Command, to int64, issue int) {
	n := len(u.events) + 1
	if issue == 0 {
		issue = n
	}
	u.events = append(u.events, cc.Event{Version: 1, Sequence: n, ID: fmt.Sprintf("issue-%d", issue), Type: kind, Actor: cc.Actor{ID: user.ID, Login: user.Login}, RecipientID: to, Source: cc.Source{Issue: issue, URL: fmt.Sprintf("https://github.com/test/coin/issues/%d", issue), CreatedAt: testNow, BodySHA256: strings.Repeat("a", 64), Command: command}, Timestamp: testNow})
}
func fixture(t *testing.T) (*Processor, *memory, *upstream) {
	t.Helper()
	m := newMemory()
	u := &upstream{}
	u.add(house, "allowance", cc.Command{Action: "claim"}, 0, 0)
	p := &Processor{Store: m, Upstream: u, Payer: u, Repository: "test/casino", Config: ledger.Config{UpstreamRepository: "test/coin", TreasuryID: 9, StartingCommit: fmt.Sprintf("%040x", 1001)}, Now: func() time.Time { return testNow }, Sample: func(ledger.Event) (game.Draw, error) { return game.Draw{Gate: 50, Return: 17}, nil }}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	m.issues = []gh.Issue{{Number: 1, User: alice, Body: "/open", State: "open"}}
	deposit(t, p, m, u, alice, 1, alice, 9000, 40)
	return p, m, u
}

// deposit funds a player the only way the casino credits: owner comments
// /deposit on their issue, then payer pays the resulting casino request.
// Its upstream issues are numbered from 10*comment, clear of other tests.
func deposit(t *testing.T, p *Processor, m *memory, u *upstream, owner gh.User, issue int, payer gh.User, id int64, amount int64) {
	t.Helper()
	m.comments[issue] = append(m.comments[issue], gh.Comment{ID: id, Body: fmt.Sprintf("/deposit %d", amount), User: owner, CreatedAt: testNow, UpdatedAt: testNow})
	created, issues := u.created, len(u.issues)
	u.created = int(id)*10 - 101
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	number := m.state(t).Deposits[fmt.Sprintf("comment-%d", id)].UpstreamIssue
	u.add(house, "request", cc.Command{Action: "request", Payer: owner.Login, Amount: amount}, 0, number)
	u.add(payer, "fulfill", cc.Command{Action: "fulfill", RequestID: fmt.Sprintf("request-%d", number)}, 0, number+1)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	u.created, u.issues = created, u.issues[:issues]
}
func comment(m *memory, id int64, body string, user gh.User) {
	m.comments[1] = append(m.comments[1], gh.Comment{ID: id, Body: body, User: user, CreatedAt: testNow, UpdatedAt: testNow})
}
func TestDepositWagerWithdrawalAndReplay(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/wager 10", alice)
	comment(m, 11, "/withdraw 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Accounts[1].Available != 26 || s.Fees != 1 || !s.Wagers["comment-10"].Settled || u.created != 1 {
		t.Fatalf("unexpected state: %+v created=%d", s, u.created)
	}
	u.add(house, "send", cc.Command{Action: "offer", Recipient: "alice", Amount: 19}, 1, 101)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s = m.state(t)
	if !s.Withdrawals["comment-11"].Paid || s.Fees != 2 || s.Treasury != 121 {
		t.Fatalf("withdrawal not confirmed: %+v", s)
	}
	events := len(s.Events)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.state(t).Events) != events || u.created != 1 {
		t.Fatal("replay duplicated a payment or event")
	}
}
func TestFrozenDrawSurvivesFailureAndDeletedComment(t *testing.T) {
	p, m, _ := fixture(t)
	calls := 0
	p.Sample = func(ledger.Event) (game.Draw, error) { calls++; return game.Draw{Gate: 50, Return: 17}, nil }
	comment(m, 10, "/wager 10", alice)
	m.failType = "settle"
	if err := p.Run(ctx); err == nil {
		t.Fatal("expected injected merge failure")
	}
	comments := m.comments[1][:0]
	for _, c := range m.comments[1] {
		if c.ID != 10 {
			comments = append(comments, c)
		}
	}
	m.comments[1] = comments
	m.issues[0].State = "closed"
	p.Sample = func(ledger.Event) (game.Draw, error) { t.Fatal("rerolled a frozen draw"); return game.Draw{}, nil }
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if calls != 1 || s.Wagers["comment-10"].Payout != 17 || s.Accounts[1].Available != 46 {
		t.Fatal("failed to preserve wager result")
	}
}
func TestLostWithdrawalResponseNeverSendsTwice(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/withdraw 20", alice)
	u.loseCreate = true
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 1 || !m.state(t).Withdrawals["comment-10"].Attempted {
		t.Fatal("missing durable attempt")
	}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 1 || m.state(t).Withdrawals["comment-10"].UpstreamIssue != 101 {
		t.Fatal("did not recover original upstream issue")
	}
}
func TestAmbiguousAttemptStaysLocked(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/withdraw 20", alice)
	m.loseType = "attempt"
	if err := p.Run(ctx); err == nil {
		t.Fatal("expected lost merge response")
	}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	w := m.state(t).Withdrawals["comment-10"]
	if u.created != 0 || !w.Attempted || w.Paid || m.state(t).Accounts[1].Available != 19 {
		t.Fatal("ambiguous attempt was refunded or sent automatically")
	}
}
func TestOwnershipEditsAndRejectionsStayRejected(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/wager 10", gh.User{ID: 2, Login: "bob", Type: "User"})
	comment(m, 11, "/wager 100", alice)
	comment(m, 12, "/wager 10", alice)
	m.comments[1][len(m.comments[1])-1].UpdatedAt = testNow.Add(time.Second)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	for _, id := range []string{"comment-10", "comment-11", "comment-12"} {
		if s.Events[id].Type != "rejected" {
			t.Fatal("unsafe command accepted", id)
		}
	}
	deposit(t, p, m, u, alice, 1, alice, 9001, 50)
	if len(m.state(t).Wagers) != 0 {
		t.Fatal("previous rejection executed after new funding")
	}
}
func TestMissingTokenLeavesWithdrawalPending(t *testing.T) {
	p, m, u := fixture(t)
	p.Payer = nil
	comment(m, 10, "/withdraw 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	w := m.state(t).Withdrawals["comment-10"]
	if w.Attempted || u.created != 0 {
		t.Fatal("submitted without token")
	}
}
func TestWrongTreasuryToken(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/withdraw 20", alice)
	u.user = alice
	if err := p.Run(ctx); err == nil {
		t.Fatal("accepted wrong treasury token")
	}
	if u.created != 0 {
		t.Fatal("spent using wrong account")
	}
}

func (u *upstream) Comments(_ context.Context, issue int) ([]gh.Comment, error) {
	if !u.rejected {
		return nil, nil
	}
	return []gh.Comment{{ID: 900, User: bot, Body: fmt.Sprintf("Request rejected.\n<!-- chriscoin-result:issue=%d;status=rejected -->", issue)}}, nil
}
func TestRejectedWithdrawalRestoresFunds(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/withdraw 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	u.rejected = true
	u.issues[0].State = "closed"
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if !s.Withdrawals["comment-10"].Released || s.Accounts[1].Available != 39 || s.Fees != 1 {
		t.Fatalf("rejection did not restore funds: %+v", s)
	}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Accounts[1].Available != 39 {
		t.Fatal("funds released twice")
	}
}
func TestFrozenAcceptanceIgnoresLaterEdits(t *testing.T) {
	p, m, _ := fixture(t)
	comment(m, 10, "/wager 10", alice)
	m.failType = "wager"
	if err := p.Run(ctx); err == nil {
		t.Fatal("expected interruption")
	}
	for i, c := range m.comments[1] {
		if c.ID == 10 {
			m.comments[1][i].Body = "/withdraw 20"
			m.comments[1][i].UpdatedAt = testNow.Add(time.Second)
		}
	}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Wagers["comment-10"].Payout != 17 || len(m.state(t).Withdrawals) != 0 {
		t.Fatal("edited command replaced checkpoint")
	}
}

func TestDepositWaitsForIssueOpenedByPayer(t *testing.T) {
	p, m, u := fixture(t)
	bob := gh.User{ID: 2, Login: "bob", Type: "User"}
	before := len(m.issues)
	// bob pays alice's casino request before opening an issue of his own.
	deposit(t, p, m, u, alice, 1, bob, 9001, 20)
	s := m.state(t)
	if s.Accounts[bob.ID].Issue != 0 || s.Accounts[bob.ID].Available != 19 || len(m.issues) != before {
		t.Fatal("deposit created or assigned an issue")
	}
	// A bot-authored marker must not be allowed to claim the depositor's account.
	m.issues = append(m.issues, gh.Issue{Number: 2, User: bot, State: "open", Body: "<!-- casino-account:2 -->"})
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Accounts[bob.ID].Issue != 0 {
		t.Fatal("bot-authored issue claimed account")
	}
	m.issues = append(m.issues, gh.Issue{Number: 3, User: bob, State: "open", Body: "hello"})
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s = m.state(t)
	if s.Accounts[bob.ID].Issue != 3 || s.Accounts[bob.ID].Available != 19 || s.Fees != 2 {
		t.Fatal("first user-opened issue did not recover existing balance")
	}
	if !hasReply(m, 3, "Welcome!") {
		t.Fatal("opener did not receive welcome")
	}
	count := len(s.Events)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.state(t).Events) != count {
		t.Fatal("opening account duplicated deposit")
	}
}
func TestReplyFailureDoesNotRepeatWager(t *testing.T) {
	p, m, _ := fixture(t)
	comment(m, 10, "/wager 10", alice)
	m.loseType = "settle"
	if err := p.Run(ctx); err == nil {
		t.Fatal("expected lost settlement response")
	}
	p.Sample = func(ledger.Event) (game.Draw, error) { t.Fatal("settled wager rerolled"); return game.Draw{}, nil }
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Accounts[1].Available != 46 {
		t.Fatal("settlement repeated")
	}
}

func (m *memory) ReopenIssue(_ context.Context, n int) error {
	if m.reopens == nil {
		m.reopens = map[int]int{}
	}
	m.reopens[n]++
	for i := range m.issues {
		if m.issues[i].Number == n {
			m.issues[i].State = "open"
			return nil
		}
	}
	return fmt.Errorf("issue %d not found", n)
}

func TestFirstRunTreatsPaymentsAfterBaselineAsTips(t *testing.T) {
	m := newMemory()
	u := &upstream{}
	u.add(house, "allowance", cc.Command{Action: "claim"}, 0, 0)
	starting, _ := u.Main(ctx)
	u.add(alice, "send", cc.Command{Action: "offer", Recipient: "house", Amount: 40}, house.ID, 0)
	p := &Processor{Store: m, Upstream: u, Repository: "test/casino", Config: ledger.Config{UpstreamRepository: "test/coin", TreasuryID: house.ID, StartingCommit: starting}, Now: func() time.Time { return testNow }}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Events["init"].UpstreamCommit != starting || s.Events["init"].Treasury != 100 || s.Events["init"].UpstreamCount != 1 {
		t.Fatal("initialization drifted to live head")
	}
	if len(s.Accounts) != 0 || s.Fees != 0 || s.Treasury != 140 {
		t.Fatalf("direct payment between pinning and launch became a credit: %+v", s)
	}
	count := len(s.Events)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.state(t).Events) != count {
		t.Fatal("baseline imports duplicated on recovery")
	}
}
func TestFirstRunDoesNotCreditPaymentsBeforeBaseline(t *testing.T) {
	m := newMemory()
	u := &upstream{}
	u.add(house, "allowance", cc.Command{Action: "claim"}, 0, 0)
	u.add(alice, "send", cc.Command{Action: "offer", Recipient: "house", Amount: 40}, house.ID, 0)
	starting, _ := u.Main(ctx)
	p := &Processor{Store: m, Upstream: u, Repository: "test/casino", Config: ledger.Config{UpstreamRepository: "test/coin", TreasuryID: house.ID, StartingCommit: starting}, Now: func() time.Time { return testNow }}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Treasury != 140 || s.Liabilities() != 0 || s.Fees != 0 || len(s.Accounts) != 0 {
		t.Fatal("historical transfer became player credit")
	}
}

func TestWithdrawalUsesUpstreamFormat(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/withdraw 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	issue := u.issues[0]
	if issue.Title != "ChrisCoin: offer 19 to alice" || strings.Contains(strings.ToLower(issue.Body), "casino") || !strings.HasPrefix(issue.Body, "```chriscoin\n") {
		t.Fatalf("unexpected upstream issue: %+v", issue)
	}
}

func TestWithdrawalRecoveryAcceptsLegacyReference(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/withdraw 20", alice)
	u.loseCreate = true
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	u.issues[0].Body = "<!-- casino-withdrawal:" + p.Repository + ":comment-10 -->\n\n```chriscoin\n{\"action\":\"offer\",\"recipient\":\"alice\",\"amount\":19}\n```\n"
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 1 || m.state(t).Withdrawals["comment-10"].UpstreamIssue != 101 {
		t.Fatal("legacy withdrawal was not recovered")
	}
}
func TestTreasuryFreezesAndCreditsPlayers(t *testing.T) {
	p, m, _ := fixture(t)
	comment(m, 10, "Thanks for playing!", house)
	comment(m, 11, "/credit 5 sorry about the outage", gh.User{ID: 2, Login: "bob", Type: "User"})
	comment(m, 12, "/credit 5 sorry about the outage", house)
	comment(m, 13, "/credit 500", house)
	comment(m, 14, "/freeze spam", house)
	comment(m, 15, "/wager 10", alice)
	comment(m, 16, "/withdraw 10", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if _, ok := s.Events["comment-10"]; ok {
		t.Fatal("treasury chat was recorded as a command")
	}
	for id, want := range map[string]string{"comment-11": "rejected", "comment-12": "credit", "comment-13": "rejected", "comment-14": "freeze", "comment-15": "rejected", "comment-16": "withdraw"} {
		if s.Events[id].Type != want {
			t.Fatalf("%s = %s, want %s", id, s.Events[id].Type, want)
		}
	}
	if !s.Accounts[alice.ID].Frozen || s.Accounts[alice.ID].Available != 34 || s.Granted != 5 {
		t.Fatalf("unexpected state: %+v", s.Accounts[alice.ID])
	}
	if !hasReply(m, 1, "🎁 The house sent you **5 CC**. Reason: sorry about the outage") || !hasReply(m, 1, "Reason: spam") || !hasReply(m, 1, "This account is frozen by the operator") || !hasReply(m, 1, "🧊 **Frozen by the operator.**") {
		t.Fatal("missing admin replies")
	}
	comment(m, 17, "/unfreeze", house)
	comment(m, 18, "/wager 10", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if s = m.state(t); s.Accounts[alice.ID].Frozen || s.Events["comment-18"].Type != "wager" {
		t.Fatal("unfreeze did not restore play")
	}
}
func TestRouletteAndSlots(t *testing.T) {
	p, m, _ := fixture(t)
	p.Sample = func(w ledger.Event) (game.Draw, error) {
		if w.Game == game.Roulette {
			return game.Draw{Pocket: 17}, nil
		}
		return game.Draw{Reels: []int64{9, 9, 9}}, nil
	}
	comment(m, 10, "/roulette 1 17", alice)
	comment(m, 11, "/roulette 5 RED", alice)
	comment(m, 12, "/roulette 5 green", alice)
	comment(m, 13, "/slots 1", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Wagers["comment-10"].Payout != 36 || s.Wagers["comment-11"].Payout != 0 || s.Events["comment-11"].Bet != "red" || s.Events["comment-12"].Type != "rejected" || s.Wagers["comment-13"].Payout != 50 {
		t.Fatalf("unexpected games: %+v", s.Wagers)
	}
	if s.Accounts[alice.ID].Available != 39-1+36-5-1+50 {
		t.Fatalf("balance %d", s.Accounts[alice.ID].Available)
	}
	for _, want := range []string{"🎡 The ball lands on **17 ⚫**", "That pays **36 CC**! You’re **35 CC up**!", "lost your **5 CC** bet", "bet on red, black", "**[ 7️⃣ | 7️⃣ | 7️⃣ ]** 💥 JACKPOT!", "`/slots 10`"} {
		if !hasReply(m, 1, want) {
			t.Fatalf("missing reply %q", want)
		}
	}
}
func TestBigWinsAreHonouredAndWithdrawalsNeedTreasuryFunds(t *testing.T) {
	p, m, u := fixture(t) // alice 39, treasury 140
	p.Sample = func(ledger.Event) (game.Draw, error) { return game.Draw{Pocket: 17}, nil }
	comment(m, 10, "/roulette 39 17", alice)
	comment(m, 11, "/withdraw 200", alice)
	comment(m, 12, "/withdraw 100", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Wagers["comment-10"].Payout != 1404 || s.HouseBalance() != 140-1304-100 {
		t.Fatalf("win not honoured: %+v house %d", s.Wagers["comment-10"], s.HouseBalance())
	}
	if s.Events["comment-11"].Type != "rejected" || s.Events["comment-12"].Type != "withdraw" || u.created != 1 || s.Accounts[alice.ID].Available != 1304 {
		t.Fatalf("withdrawals: %s %s created=%d", s.Events["comment-11"].Type, s.Events["comment-12"].Type, u.created)
	}
	if !hasReply(m, 1, "The treasury can’t pay that cash-out right now") {
		t.Fatal("missing treasury rejection reply")
	}
}
