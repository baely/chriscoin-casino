package processor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gh "chriscoin-casino/internal/github"
	"chriscoin-casino/internal/ledger"
)

func (p *Processor) issueURL(number int) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d", p.Repository, number)
}
func (p *Processor) redirect(userID int64) string {
	if a := p.state.Accounts[userID]; a != nil && a.Issue > 0 {
		return fmt.Sprintf("This is not your account issue. Please use [your casino account](%s).", p.issueURL(a.Issue))
	}
	return fmt.Sprintf("This is not your account issue. [Open your own casino account](https://github.com/%s/issues/new?template=account.md) to get started.", p.Repository)
}
func (p *Processor) welcome(treasuryLogin string) string {
	return fmt.Sprintf(`Welcome! This is your personal ChrisCoin Casino account.

Comment here to interact:

- `+"`/deposit 100`"+` — get a ChrisCoin payment link; paying 100 CC credits 99 CC here.
- `+"`/wager 10`"+` — stake 10 CC for a return between 0 and 20 CC, including the stake.
- `+"`/withdraw 50`"+` — withdraw 50 CC from your casino balance and receive 49 CC in ChrisCoin.
- `+"`/balance`"+` — check your available balance.

You can also send CC directly to `+"`%s`"+` in [ChrisCoin](https://github.com/%s); the casino credits the actual payer after settlement. Each deposit and successful withdrawal costs 1 CC. A deposit of 1 CC leaves no casino credit. Wagers have no extra transfer fee.

The user who opened this issue owns this account. Only you can use it. Use a new comment for each command; results arrive asynchronously. Casino accounts start at zero.

You may close this issue. If you open another account issue, it will link back here, close, and reopen this original issue.`, treasuryLogin, p.Config.UpstreamRepository)
}

func (p *Processor) ensureAccounts(ctx context.Context, issues []gh.Issue) error {
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	// Issue authors are authoritative. Assignees, titles, and body markers do
	// not establish ownership, even if a ledger record points at the issue.
	byNumber := map[int]gh.Issue{}
	for _, issue := range issues {
		byNumber[issue.Number] = issue
	}
	for _, account := range p.state.Accounts {
		if account.Issue == 0 {
			continue
		}
		issue, ok := byNumber[account.Issue]
		if !ok || issue.User.Type != "User" || issue.User.ID != account.ID {
			return fmt.Errorf("account issue %d does not match its recorded opener", account.Issue)
		}
	}
	for _, issue := range issues {
		if issue.User.Type != "User" {
			continue
		}
		a := p.state.Accounts[issue.User.ID]
		if a == nil || a.Issue == 0 {
			if err := p.commit(ctx, ledger.Event{ID: fmt.Sprintf("account-%d", issue.User.ID), Type: "account", UserID: issue.User.ID, Login: issue.User.Login, Issue: issue.Number}); err != nil {
				return err
			}
			continue
		}
		if a.Issue == issue.Number {
			continue
		}
		id := fmt.Sprintf("duplicate-%d", issue.Number)
		if _, ok := p.state.Events[id]; !ok {
			if err := p.commit(ctx, ledger.Event{ID: id, Type: "duplicate", UserID: issue.User.ID, Issue: issue.Number, TargetIssue: a.Issue}); err != nil {
				return err
			}
		}
		// A newly discovered duplicate counts even if its owner already closed it.
		// Reopening a handled duplicate must not reopen its original again.
		if _, done := p.state.Events["resolved-"+id]; done && issue.State == "open" {
			if err := p.duplicateReply(ctx, issue.Number, a.Issue); err != nil {
				return err
			}
			if err := p.Store.CloseIssue(ctx, issue.Number); err != nil {
				return err
			}
		}
	}
	// Complete recorded lifecycle changes even if a previous run closed the
	// duplicate before stopping. Completion prevents later recovery reopening it.
	pending := []ledger.Event{}
	for _, e := range p.events {
		if e.Type == "duplicate" {
			if _, done := p.state.Events["resolved-"+e.ID]; !done {
				pending = append(pending, e)
			}
		}
	}
	for _, e := range pending {
		if err := p.duplicateReply(ctx, e.Issue, e.TargetIssue); err != nil {
			return err
		}
		if err := p.Store.ReopenIssue(ctx, e.TargetIssue); err != nil {
			return err
		}
		if err := p.Store.CloseIssue(ctx, e.Issue); err != nil {
			return err
		}
		if err := p.commit(ctx, ledger.Event{ID: "resolved-" + e.ID, Type: "duplicate-resolved", Ref: e.ID}); err != nil {
			return err
		}
	}
	return nil
}
func (p *Processor) duplicateReply(ctx context.Context, duplicate, original int) error {
	return p.reply(ctx, duplicate, "duplicate", fmt.Sprintf("You already have a casino account: [use your original issue](%s). This duplicate will be closed.", p.issueURL(original)))
}

func containsMarker(body, marker string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}
