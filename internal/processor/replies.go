package processor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	cc "chriscoin-casino/internal/chriscoin"
	"chriscoin-casino/internal/game"
	"chriscoin-casino/internal/ledger"
)

func wagerResult(w *ledger.Wager) string {
	stake := w.Event.Amount
	switch w.Event.Game {
	case game.Roulette:
		return rouletteResult(w)
	case game.Slots:
		return slotsResult(w)
	}
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

func gameOutcome(w *ledger.Wager, lost string) string {
	switch stake := w.Event.Amount; {
	case w.Payout == 0:
		return lost
	case w.Payout == stake:
		return fmt.Sprintf("🤝 Your **%d CC** is back. You broke even this round.", stake)
	}
	return fmt.Sprintf("🎉 That pays **%d CC**! You’re **%d CC up**!", w.Payout, w.Payout-w.Event.Amount)
}
func rouletteResult(w *ledger.Wager) string {
	stake, bet := w.Event.Amount, w.Event.Bet
	if !w.Settled {
		return fmt.Sprintf("🎡 Your **%d CC** on **%s** is on the wheel. No more bets…", stake, bet)
	}
	pocket := w.Draw.Pocket
	return fmt.Sprintf("🎡 The ball lands on **%d %s**. You bet %d CC on **%s**.\n\n", pocket, game.PocketColor(pocket), stake, bet) +
		gameOutcome(w, fmt.Sprintf("No luck this spin — you lost your **%d CC** bet.", stake))
}
func slotsResult(w *ledger.Wager) string {
	stake := w.Event.Amount
	if !w.Settled {
		return fmt.Sprintf("🎰 Your **%d CC** spin is whirring…", stake)
	}
	reels, err := game.Reels(w.Draw)
	if err != nil {
		return "🎰 Spin settled."
	}
	headline := ""
	if reels[0] == reels[1] && reels[1] == reels[2] {
		headline = " Three of a kind!"
		if reels[0] == game.Seven {
			headline = " 💥 JACKPOT!"
		}
	}
	return fmt.Sprintf("🎰 **[ %s ]**%s\n\n", strings.Join(reels, " | "), headline) +
		gameOutcome(w, fmt.Sprintf("No match — you lost your **%d CC** spin.", stake))
}
func rejectionResult(e ledger.Event) string {
	reason := strings.TrimPrefix(e.Reason, "event "+e.ID+": ")
	switch reason {
	case "invalid wager or insufficient funds":
		return "That wager didn’t go through. Choose a positive whole amount covered by your balance. Your coins haven’t moved."
	case "house reserve is insufficient":
		return "The table can’t cover that wager right now. Your coins haven’t moved."
	case "account is frozen":
		return "This account is frozen by the operator. Wagers and deposit requests are paused; `/withdraw` and `/balance` still work. Your coins haven’t moved."
	case "house reserve cannot cover the credit":
		return "The house reserve can’t cover that credit right now. No coins moved."
	case "treasury cannot pay this withdrawal":
		return "The treasury can’t pay that cash-out right now. Your coins haven’t moved and your balance is still yours; try a smaller amount or try again later."
	case "invalid withdrawal or insufficient funds", "invalid withdrawal, insufficient funds, or treasury shortfall":
		return "That cash-out didn’t go through. It needs to be at least 2 CC and no more than your balance. Your coins haven’t moved."
	default:
		return "Hold up! " + reason
	}
}

// balanceLine ends command replies on the player's own account issue.
func balanceLine(n int64) string {
	return fmt.Sprintf("\n\n**Balance: %d CC** · available to play", n)
}

// shownBalances replays the ledger to find the balance each command reply
// shows: the account's balance just before its next command, or now for the
// latest reply. Old replies therefore never change once a newer one exists.
func shownBalances(events []ledger.Event) (map[string]int64, error) {
	shown := map[string]int64{}
	latest := map[int64]string{} // account ID -> its latest reply
	_, err := ledger.ReplayEach(events, func(e ledger.Event, s *ledger.State) {
		if e.Comment != 0 {
			id := e.UserID
			if e.Target != 0 {
				id = e.Target
			}
			if a := s.Accounts[id]; a != nil && a.Issue == e.Issue {
				latest[id] = e.ID
			}
		}
		for id, reply := range latest {
			shown[reply] = s.Accounts[id].Available
		}
	})
	return shown, err
}

func (p *Processor) results(ctx context.Context, up cc.Snapshot) error {
	shown, err := shownBalances(p.events)
	if err != nil {
		return err
	}
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
		case "credit":
			message = fmt.Sprintf("🎁 The house sent you **%d CC**. Reason: %s", e.Amount, e.Reason)
		case "freeze":
			message = fmt.Sprintf("🧊 This account has been frozen by the operator. Reason: %s\n\nWagers and deposit requests are paused. Your balance is still yours: `/withdraw` and `/balance` keep working.", e.Reason)
		case "unfreeze":
			message = "🔓 This account has been unfrozen. You’re back at the table!"
			if e.Reason != "" {
				message += " Reason: " + e.Reason
			}
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
		if n, ok := shown[e.ID]; ok {
			message += balanceLine(n)
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
		card := p.welcome(up.State.Accounts[p.Config.TreasuryID].Login, a.Available)
		if a.Frozen {
			card += "\n\n🧊 **Frozen by the operator.** Wagers and deposit requests are paused; `/withdraw` and `/balance` still work."
		}
		if err := p.reply(ctx, a.Issue, "account", card); err != nil {
			return err
		}
	}
	return nil
}
