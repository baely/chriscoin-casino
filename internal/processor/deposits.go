package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sort"

	cc "chriscoin-casino/internal/chriscoin"
	"chriscoin-casino/internal/ledger"
)

func depositMarker(repository, id string) string {
	return "<!-- casino-deposit:" + repository + ":" + id + " -->"
}
func (p *Processor) submitDeposits(ctx context.Context) error {
	if p.Payer == nil {
		return nil
	}
	ids := []string{}
	for id, d := range p.state.Deposits {
		if d.UpstreamIssue == 0 {
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
			if issue.User.ID == me.ID && issue.User.Type == "User" && containsMarker(issue.Body, marker) {
				if number == 0 || issue.Number < number {
					number = issue.Number
				}
			}
		}
		if number == 0 {
			command, err := json.Marshal(cc.Command{Action: "request", Payer: d.Event.Login, Amount: d.Event.Amount})
			if err != nil {
				return err
			}
			body := fmt.Sprintf("Casino deposit request. The payer receives the casino credit after settlement.\n\n%s\n\n```chriscoin\n%s\n```\n", marker, command)
			issue, err := p.Payer.CreateIssue(ctx, "Casino deposit "+id, body)
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
		return "Deposit request pending. " + direct
	}
	requestID := fmt.Sprintf("request-%d", d.UpstreamIssue)
	issueURL := fmt.Sprintf("https://github.com/%s/issues/%d", p.Config.UpstreamRepository, d.UpstreamIssue)
	r := up.State.Requests[requestID]
	if r == nil {
		return fmt.Sprintf("Deposit request submitted for %d CC. Waiting for ChrisCoin to record [the request](%s); a Pay link will appear here once it is ready.", e.Amount, issueURL)
	}
	if r.Requester != p.Config.TreasuryID || r.Amount != e.Amount {
		return "Deposit request does not match the expected treasury or amount. Do not pay it; contact the operator. " + direct
	}
	switch r.Status {
	case "paid":
		return fmt.Sprintf("Deposit request paid by GitHub user ID %d: %d CC sent, %d CC credited to the actual payer, 1 CC fee.", r.PaidBy, e.Amount, e.Amount-1)
	case "cancelled":
		return "This deposit request was cancelled. Submit a new /deposit command for another request. " + direct
	case "open":
		command, _ := json.Marshal(cc.Command{Action: "fulfill", RequestID: requestID})
		body := "```chriscoin\n" + string(command) + "\n```\n"
		q := url.Values{"template": {"request.md"}, "title": {"Pay casino deposit " + requestID}, "body": {body}}
		return fmt.Sprintf("[Pay %d CC](https://github.com/%s/issues/new?%s) to receive %d CC casino credit after settlement (1 CC fee). Submit the prefilled ChrisCoin issue while signed in. Reusing this same request cannot pay it twice.\n\n[View the payment request](%s).", e.Amount, p.Config.UpstreamRepository, q.Encode(), e.Amount-1, issueURL)
	default:
		return "Deposit request status is unavailable. " + direct
	}
}
