package processor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	cc "chriscoin-casino/internal/chriscoin"
	"chriscoin-casino/internal/ledger"
)

func wagerResult(w *ledger.Wager) string {
	stake := w.Event.Amount
	switch {
	case !w.Settled:
		return fmt.Sprintf("🎲 Your **%d CC** is on the table. The dealer is rolling…", stake)
	case w.Payout == 0:
		return fmt.Sprintf("🎲 No luck this round — you lost your **%d CC** wager.", stake)
	case w.Payout < stake:
		return fmt.Sprintf("🎲 You got **%d CC** back from your %d CC wager. Down **%d CC** this round.", w.Payout, stake, stake-w.Payout)
	case w.Payout == stake:
		return fmt.Sprintf("🤝 All square! Your **%d CC** is back. You broke even this round.", stake)
	case w.Payout == 2*stake:
		return fmt.Sprintf("🎉 Double up! Your %d CC wager brought back **%d CC**. You’re **%d CC up**!", stake, w.Payout, w.Payout-stake)
	default:
		return fmt.Sprintf("🎉 Nice hit! Your %d CC wager brought back **%d CC**. You’re **%d CC up**!", stake, w.Payout, w.Payout-stake)
	}
}

func rejectionResult(e ledger.Event) string {
	reason := strings.TrimPrefix(e.Reason, "event "+e.ID+": ")
	switch reason {
	case "invalid wager or insufficient funds":
		return "That wager didn’t go through. Choose a positive whole amount covered by your balance. Your coins haven’t moved."
	case "house reserve is insufficient":
		return "The table can’t cover that wager right now. Your coins haven’t moved."
	case "invalid withdrawal, insufficient funds, or treasury shortfall":
		return "That cash-out didn’t go through. It needs to be at least 2 CC and covered by your balance and the treasury. Your coins haven’t moved."
	default:
		return "Hold up! " + reason
	}
}

func (p *Processor) results(ctx context.Context, up cc.Snapshot) error {
	for _, e := range p.events {
		if e.Comment == 0 {
			continue
		}
		var message string
		switch e.Type {
		case "rejected":
			message = rejectionResult(e)
		case "deposit":
			message = p.depositResult(e, up)
		case "balance":
			message = "🪙 Here’s your chip count."
		case "wager":
			message = wagerResult(p.state.Wagers[e.ID])
		case "withdraw":
			w := p.state.Withdrawals[e.ID]
			switch {
			case w.Released:
				message = fmt.Sprintf("Your cash-out couldn’t go through. **%d CC** is back in your balance, with no withdrawal fee charged.", e.Amount)
			case w.Paid:
				message = fmt.Sprintf("💸 Cashed out! **%d CC** is back in ChrisCoin. Your %d CC cash-out included a 1 CC fee.", e.Amount-1, e.Amount)
			case w.UpstreamIssue > 0:
				message = fmt.Sprintf("💸 Cash-out on its way! **%d CC** is set aside while [the payment](https://github.com/%s/issues/%d) clears. You’ll receive **%d CC** after the 1 CC fee.", e.Amount, p.Config.UpstreamRepository, w.UpstreamIssue, e.Amount-1)
			case w.Attempted:
				message = fmt.Sprintf("Your cash-out needs a check from the operator. **%d CC** stays set aside until the payment is confirmed; it won’t be sent twice.", e.Amount)
			default:
				message = fmt.Sprintf("💸 Cash-out queued! **%d CC** is set aside. You’ll receive **%d CC** in ChrisCoin after the 1 CC fee.", e.Amount, e.Amount-1)
			}
		default:
			continue
		}
		if a := p.state.Accounts[e.UserID]; a != nil && a.Issue == e.Issue {
			message += fmt.Sprintf("\n\n**Balance: %d CC** · available to play", a.Available)
		}
		if err := p.reply(ctx, e.Issue, e.ID, message); err != nil {
			return err
		}
	}
	// Refresh the account card after processing, using the same current balance
	// as the command replies. Deposits without a command update it too.
	accounts := []*ledger.Account{}
	for _, a := range p.state.Accounts {
		if a.Issue > 0 {
			accounts = append(accounts, a)
		}
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Issue < accounts[j].Issue })
	for _, a := range accounts {
		if err := p.reply(ctx, a.Issue, "account", p.welcome(up.State.Accounts[p.Config.TreasuryID].Login, a.Available)); err != nil {
			return err
		}
	}
	return nil
}
