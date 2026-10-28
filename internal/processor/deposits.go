package processor

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"

	cc "chriscoin-casino/internal/chriscoin"
	"chriscoin-casino/internal/ledger"
)

const treasuryDepositMessage = "The treasury account cannot deposit into itself. Paying yourself moves no CC and creates no casino credit. Use a different GitHub account to play; no deposit fee is charged."

func depositMarker(repository, id string) string {
	return cc.RequestMarker(repository, "deposit", id)
}
func (p *Processor) submitDeposits(ctx context.Context) error {
	if p.Payer == nil {
		return nil
	}
	ids := []string{}
	for id, d := range p.state.Deposits {
		if d.UpstreamIssue == 0 && d.Event.UserID != p.Config.TreasuryID {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	me, err := p.Payer.Me(ctx)
	if err != nil {
		return err
	}
	if me.Type != "User" || me.ID != p.Config.TreasuryID {
		return fmt.Errorf("deposit request token must authenticate as the treasury user")
	}
	issues, err := p.Upstream.Issues(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		d := p.state.Deposits[id]
		number := 0
		marker := depositMarker(p.Repository, id)
		for _, issue := range issues {
			if issue.User.ID == me.ID && issue.User.Type == "User" && (containsMarker(issue.Body, marker) || containsMarker(issue.Body, "<!-- casino-deposit:"+p.Repository+":"+id+" -->")) {
				if number == 0 || issue.Number < number {
					number = issue.Number
				}
			}
		}
		if number == 0 {
			command := cc.Command{Action: "request", Payer: d.Event.Login, Amount: d.Event.Amount}
			body := cc.RequestBody(command, marker)
			issue, err := p.Payer.CreateIssue(ctx, cc.RequestTitle(command), body)
			if err != nil {
				log.Printf("Deposit request %s could not be confirmed: %v; recovery will look for the existing request before creating another", id, err)
				continue
			}
			if issue.User.ID != me.ID || issue.User.Type != "User" {
				return fmt.Errorf("unexpected deposit request author")
			}
			number = issue.Number
		}
		if err := p.commit(ctx, ledger.Event{ID: "deposit-submitted-" + id, Type: "deposit-submitted", Ref: id, Issue: number}); err != nil {
			return err
		}
	}
	return nil
}
func (p *Processor) depositResult(e ledger.Event, up cc.Snapshot) string {
	d := p.state.Deposits[e.ID]
	direct := fmt.Sprintf("You can also send CC directly to the treasury in [ChrisCoin](https://github.com/%s).", p.Config.UpstreamRepository)
	if d.UpstreamIssue == 0 {
		if e.UserID == p.Config.TreasuryID {
			return treasuryDepositMessage
		}
		return "🪙 Getting your chips ready — your payment link is on its way. " + direct
	}
	requestID := fmt.Sprintf("request-%d", d.UpstreamIssue)
	issueURL := fmt.Sprintf("https://github.com/%s/issues/%d", p.Config.UpstreamRepository, d.UpstreamIssue)
	r := up.State.Requests[requestID]
	if r == nil {
		return fmt.Sprintf("🪙 Your %d CC chip order is in! Waiting for [ChrisCoin](%s) to get the payment link ready.", e.Amount, issueURL)
	}
	if r.Requester != p.Config.TreasuryID || r.Amount != e.Amount {
		return "Deposit request does not match the expected treasury or amount. Do not pay it; contact the operator. " + direct
	}
	switch r.Status {
	case "paid":
		if r.PaidBy == p.Config.TreasuryID {
			return "This request was paid by the treasury account to itself. No CC moved, no casino credit was added, and no deposit fee was charged. Use a different GitHub account to play."
		}
		for _, payment := range up.Events {
			if payment.Type == "fulfill" && payment.Source.Command.RequestID == requestID && payment.Actor.ID == r.PaidBy && p.state.Imported[payment.ID] {
				return fmt.Sprintf("🪙 Chips are in! **%d CC** added to **@%s**’s balance. The %d CC payment included a 1 CC fee.", e.Amount-1, p.state.Accounts[r.PaidBy].Login, e.Amount)
			}
		}
		return "ChrisCoin records this request as paid, but the casino ledger has not recorded the deposit. No casino credit is confirmed yet; contact the operator if this persists."
	case "cancelled":
		return "That chip order was cancelled. Use `/deposit N` to start a fresh one. " + direct
	case "open":
		command := cc.Command{Action: "fulfill", RequestID: requestID}
		body := cc.RequestBody(command, "")
		q := url.Values{"template": {"request.md"}, "title": {cc.RequestTitle(command)}, "body": {body}}
		return fmt.Sprintf("🪙 Your chips are waiting! [Pay %d CC](https://github.com/%s/issues/new?%s) to add **%d CC** to your balance after the 1 CC fee. Open the link and submit the prefilled ChrisCoin issue while signed in. This payment link can only be used once.\n\n[View the payment request](%s).", e.Amount, p.Config.UpstreamRepository, q.Encode(), e.Amount-1, issueURL)
	default:
		return "Deposit request status is unavailable. " + direct
	}
}
