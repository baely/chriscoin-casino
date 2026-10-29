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
		return fmt.Sprintf("This seat is taken! Head over to [your casino account](%s).", p.issueURL(a.Issue))
	}
	return fmt.Sprintf("This seat is taken! [Open your own casino account](https://github.com/%s/issues/new?template=account.md) to get started.", p.Repository)
}
func (p *Processor) welcome(treasuryLogin string, balance int64) string {
	return fmt.Sprintf(`🎰 Welcome! Pull up a seat. This is your own little corner of ChrisCoin Casino.

**Balance: %d CC** · available to play

Your balance here updates as your coins move. To get started:

- `+"`/deposit 100`"+` — grab some chips: pay 100 CC and get 99 CC to play with.
- `+"`/wager 10`"+` — put 10 CC on the table; you could get back anything from 0 to 20 CC, including your stake.
- `+"`/roulette 10 red`"+` — spin the wheel. Bet red, black, odd, even, low, high, 1-12, 13-24, 25-36 (pays 3x), or a single number 0–36 (pays 36x). Colour, odd/even, and low/high bets pay 2x.
- `+"`/slots 10`"+` — pull the lever. Three 7️⃣ pay 50x, ⭐ 25x, 🔔 12x, 🍋 8x, 🍒 3x, and any two 🍒 pay 2x.
- `+"`/withdraw 50`"+` — cash out 50 CC and take 49 CC back to ChrisCoin.
- `+"`/balance`"+` — check your chips.

Deposits only count through the payment link from `+"`/deposit`"+`, and the chips go to whoever pays it. CC sent straight to `+"`%s`"+` in [ChrisCoin](https://github.com/%s) is kept as a tip for the house. Each deposit and successful withdrawal costs 1 CC. A deposit of 1 CC leaves no casino credit. Wagers have no extra transfer fee.

This is your seat: the user who opened this issue owns the account. Only you can use it. Drop a fresh comment for each command and give the dealer a moment to respond. New accounts start at zero.

You may close this issue. If you open another account issue, it will link back here, close, and reopen this original issue.`, balance, treasuryLogin, p.Config.UpstreamRepository)
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

// closedIssues answers issues opened while the casino is closed without
// opening an account; they are handled normally once the casino reopens.
func (p *Processor) closedIssues(ctx context.Context, issues []gh.Issue) error {
	for _, issue := range issues {
		a := p.state.Accounts[issue.User.ID]
		if _, handled := p.state.Events[fmt.Sprintf("duplicate-%d", issue.Number)]; issue.User.Type != "User" || issue.State != "open" || handled || (a != nil && a.Issue == issue.Number) {
			continue
		}
		if err := p.reply(ctx, issue.Number, "closed", movedNotice); err != nil {
			return err
		}
	}
	return nil
}
func (p *Processor) duplicateReply(ctx context.Context, duplicate, original int) error {
	return p.reply(ctx, duplicate, "duplicate", fmt.Sprintf("Your seat is saved! [use your original issue](%s). This duplicate will be closed.", p.issueURL(original)))
}

func containsMarker(body, marker string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}
