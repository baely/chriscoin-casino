package processor

import (
	"net/url"
	"strings"
	"testing"

	cc "chriscoin-casino/internal/chriscoin"
	gh "chriscoin-casino/internal/github"
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
	if !hasReply(m, 1, "paid by GitHub user ID 2") {
		t.Fatal("request result named the intended depositor rather than payer")
	}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Accounts[bob.ID].Available != 19 || u.created != 1 {
		t.Fatal("deposit or request duplicated")
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
func TestDepositWithoutTokenOffersDirectPayment(t *testing.T) {
	p, m, u := fixture(t)
	p.Payer = nil
	comment(m, 10, "/deposit 20", alice)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 0 || !hasReply(m, 1, "send CC directly") {
		t.Fatal("missing direct-payment fallback")
	}
	u.add(alice, "send", cc.Command{Action: "offer", Recipient: "house", Amount: 10}, 9, 0)
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.state(t).Accounts[alice.ID].Available != 48 {
		t.Fatal("direct deposit stopped working")
	}
}
func TestForeignDepositCannotCreatePaymentRequest(t *testing.T) {
	p, m, u := fixture(t)
	comment(m, 10, "/deposit 20", gh.User{ID: 2, Login: "bob", Type: "User"})
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if u.created != 0 || len(m.state(t).Deposits) != 0 {
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
