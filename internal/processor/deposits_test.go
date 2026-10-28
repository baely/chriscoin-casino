package processor

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	cc "chriscoin-casino/internal/chriscoin"
	gh "chriscoin-casino/internal/github"
	"chriscoin-casino/internal/ledger"
)

func TestRequestedDepositCreditsActualPayerOnlyAfterSettlement(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/deposit 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Accounts[alice.ID].Available != 39 || s.Fees != 1 || u.created != 1 || s.Deposits["comment-10"].UpstreamIssue != 101 {
		t.Fatal("request creation moved money or did not link request")
	}
	if u.issues[0].Title != "ChrisCoin: request 20 from alice" || strings.Contains(strings.ToLower(u.issues[0].Body), "casino") {
		t.Fatal("request does not use upstream format")
	}
	body := u.issues[0].Body
	for _, text := range []string{`"action": "request"`, `"payer": "alice"`, `"amount": 20`, depositMarker(p.Repository, "comment-10")} {
		if !strings.Contains(body, text) {
			t.Fatal("wrong request body", body)
		}
	}
	u.add(house, "request", cc.Command{Action: "request", Payer: "alice", Amount: 20}, 0, 101)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Accounts[alice.ID].Available != 39 {
		t.Fatal("unpaid request credited")
	}
	if !hasReply(m, 1, "title="+url.QueryEscape("ChrisCoin: fulfill")) || !hasReply(m, 1, "[Pay 20 CC]") || !hasReply(m, 1, "template=request.md") || !hasReply(m, 1, url.QueryEscape(cc.RequestBody(cc.Command{Action: "fulfill", RequestID: "request-101"}, ""))) {
		t.Fatal("missing prefilled fulfillment link")
	}
	bob := gh.User{ID: 2, Login: "bob", Type: "User"}
	u.add(bob, "fulfill", cc.Command{Action: "fulfill", RequestID: "request-101"}, 0, 102)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s = m.state(t)
	if s.Accounts[alice.ID].Available != 39 || s.Accounts[bob.ID].Available != 19 || s.Fees != 2 || s.Accounts[bob.ID].Issue != 0 {
		t.Fatalf("paid request not credited to actual payer: %+v", s.Accounts)
	}
	if !hasReply(m, 1, "**19 CC** added to **@bob**’s balance") {
		t.Fatal("request result named the intended depositor rather than payer")
	}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Accounts[bob.ID].Available != 19 || u.created != 1 {
		t.Fatal("deposit or request duplicated")
	}
	up, err := cc.Read(cc.Serialize(u.events), p.Config.UpstreamRepository)
	if err != nil {
		t.Fatal(err)
	}
	delete(p.state.Imported, "issue-102")
	if message := p.depositResult(p.state.Deposits["comment-10"].Event, up); !strings.Contains(message, "No casino credit is confirmed yet") || strings.Contains(message, "19 CC credited") {
		t.Fatal("paid status claimed credit without a casino ledger import", message)
	}
}

func TestTreasuryDepositRejectedBeforeCreatingRequest(t *testing.T) {
	p, m, u := fixture(t)
	m.issues = append(m.issues, gh.Issue{Number: 2, User: house, State: "open"})
	m.comments[2] = []gh.Comment{{ID: 10, Body: "/deposit 10", User: house, CreatedAt: testNow, UpdatedAt: testNow}}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Events["comment-10"].Type != "rejected" || s.Deposits["comment-10"] != nil || u.created != 0 || s.Accounts[house.ID].Available != 0 || s.Fees != 1 {
		t.Fatal("treasury deposit created a request or changed funds")
	}
	if !hasReply(m, 2, "The treasury account cannot deposit into itself") {
		t.Fatal("missing self-deposit explanation")
	}
}

func TestLegacyTreasuryDepositRecovery(t *testing.T) {
	for _, paid := range []bool{false, true} {
		t.Run(fmt.Sprintf("paid=%t", paid), func(t *testing.T) {
			p, m, u := fixture(t)
			m.issues = append(m.issues, gh.Issue{Number: 2, User: house, State: "open"})
			if err := p.Run(ctx); err != nil {
				t.Fatal(err)
			}
			// Preserve intents accepted by the previous worker, including a
			// misleading confirmation already posted before this fix.
			e := ledger.Event{ID: "comment-10", Type: "deposit", UserID: house.ID, Login: house.Login, Issue: 2, Comment: 10, BodyHash: ledger.Hash([]byte("/deposit 10")), Amount: 10}
			if err := p.commit(ctx, e); err != nil {
				t.Fatal(err)
			}
			if paid {
				if err := p.commit(ctx, ledger.Event{ID: "deposit-submitted-comment-10", Type: "deposit-submitted", Ref: e.ID, Issue: 101}); err != nil {
					t.Fatal(err)
				}
				u.add(house, "request", cc.Command{Action: "request", Payer: house.Login, Amount: 10}, 0, 101)
				u.add(house, "fulfill", cc.Command{Action: "fulfill", RequestID: "request-101"}, 0, 102)
				if err := p.reply(ctx, 2, e.ID, "9 CC credited to the actual payer, 1 CC fee."); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Run(ctx); err != nil {
				t.Fatal(err)
			}
			s := m.state(t)
			if u.created != 0 || s.Accounts[house.ID].Available != 0 || s.Fees != 1 || s.Treasury != 140 || s.Imported["issue-102"] {
				t.Fatal("legacy self-deposit created credit, fees, or another request")
			}
			if !hasReply(m, 2, "no deposit fee") || hasReply(m, 2, "9 CC credited") {
				t.Fatal("self-deposit confirmation was not corrected")
			}
		})
	}
}
func TestLostDepositRequestResponseRecoversOriginal(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/deposit 20", alice)
	u.loseCreate = true
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Deposits["comment-10"].UpstreamIssue != 0 || u.created != 1 {
		t.Fatal("expected uncertain creation")
	}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 1 || m.state(t).Deposits["comment-10"].UpstreamIssue != 101 {
		t.Fatal("request not recovered")
	}
}
func TestDirectPaymentsAreTipsNotDeposits(t *testing.T) {
	p, m, u := fixture(t)
	p.Payer = nil
	comment(m, 10, "/deposit 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 0 || !hasReply(m, 1, "CC sent straight to the treasury is kept as a tip") {
		t.Fatal("deposit reply does not warn that direct payments are tips")
	}
	// A direct send, an accepted offer, and a hand-made treasury request.
	u.add(alice, "send", cc.Command{Action: "offer", Recipient: "house", Amount: 10}, 9, 0)
	u.add(house, "request", cc.Command{Action: "request", Payer: "alice", Amount: 7}, 0, 500)
	u.add(alice, "fulfill", cc.Command{Action: "fulfill", RequestID: "request-500"}, 0, 501)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	s := m.state(t)
	if s.Accounts[alice.ID].Available != 39 || s.Fees != 1 || s.Treasury != 157 {
		t.Fatalf("tip became a credit: available %d fees %d treasury %d", s.Accounts[alice.ID].Available, s.Fees, s.Treasury)
	}
}
func TestRecoveredRequestIsLinkedBeforeItsPaymentImports(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/deposit 20", alice)
	u.loseCreate = true
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Deposits["comment-10"].UpstreamIssue != 0 {
		t.Fatal("lost response was linked")
	}
	// The request settles and is paid before the casino recovers the link.
	u.add(house, "request", cc.Command{Action: "request", Payer: "alice", Amount: 20}, 0, 101)
	u.add(alice, "fulfill", cc.Command{Action: "fulfill", RequestID: "request-101"}, 0, 102)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if s := m.state(t); s.Accounts[alice.ID].Available != 58 || u.created != 1 {
		t.Fatalf("paid request lost its credit: available %d created %d", s.Accounts[alice.ID].Available, u.created)
	}
}
func TestForeignDepositCannotCreatePaymentRequest(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/deposit 20", gh.User{ID: 2, Login: "bob", Type: "User"})
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 0 || m.state(t).Deposits["comment-10"] != nil {
		t.Fatal("foreign comment created a payment request")
	}
}

func TestDepositRecoveryAcceptsLegacyReference(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/deposit 20", alice)
	u.loseCreate = true
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	u.issues[0].Body = "<!-- casino-deposit:" + p.Repository + ":comment-10 -->\n\n```chriscoin\n{\"action\":\"request\",\"payer\":\"alice\",\"amount\":20}\n```\n"
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 1 || m.state(t).Deposits["comment-10"].UpstreamIssue != 101 {
		t.Fatal("legacy request was duplicated instead of recovered")
	}
}
