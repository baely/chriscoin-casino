package chriscoin

import (
	"fmt"
	"strings"

	"chriscoin-casino/internal/ledger"
)

type Transfer struct {
	ID       string
	Issue    int
	From, To int64
	Amount   int64
}
type Snapshot struct {
	Events    []Event
	State     *State
	Transfers []Transfer
	Hash      string
}

func Read(data []byte, repository string) (Snapshot, error) {
	events, err := Parse(data)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := Replay(events)
	if err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{Events: events, State: state, Hash: ledger.Hash(Serialize(events))}
	for _, e := range events {
		if e.Source.URL != fmt.Sprintf("https://github.com/%s/issues/%d", repository, e.Source.Issue) {
			return Snapshot{}, fmt.Errorf("upstream event belongs to a different repository")
		}
		var t Transfer
		t.ID = e.ID
		t.Issue = e.Source.Issue
		t.From = e.Actor.ID
		switch e.Type {
		case "send":
			t.To = e.RecipientID
			t.Amount = e.Source.Command.Amount
		case "accept":
			o := state.Offers[e.Source.Command.OfferID]
			t.From = o.Sender
			t.To = e.Actor.ID
			t.Amount = o.Amount
		case "fulfill":
			r := state.Requests[e.Source.Command.RequestID]
			t.To = r.Requester
			t.Amount = r.Amount
		default:
			continue
		}
		out.Transfers = append(out.Transfers, t)
	}
	return out, nil
}

func (u Snapshot) Sync(s *ledger.State, commit string) (ledger.Event, error) {
	if s.UpstreamCount > len(u.Events) || ledger.Hash(Serialize(u.Events[:s.UpstreamCount])) != s.UpstreamHash {
		return ledger.Event{}, fmt.Errorf("upstream history changed before imported checkpoint")
	}
	e := ledger.Event{Type: "sync", ID: "sync-" + commit, UpstreamCommit: commit, UpstreamCount: len(u.Events), UpstreamHash: u.Hash}
	treasury := u.State.Accounts[s.Config.TreasuryID]
	if treasury == nil {
		return e, fmt.Errorf("treasury must first open a ChrisCoin account")
	}
	e.Treasury = treasury.Available
	prior := map[string]bool{}
	for _, ev := range u.Events[:s.UpstreamCount] {
		prior[ev.ID] = true
	}
	for _, t := range u.Transfers {
		if prior[t.ID] {
			continue
		}
		if t.To == s.Config.TreasuryID && t.From != t.To {
			a := u.State.Accounts[t.From]
			e.Credits = append(e.Credits, ledger.Credit{ID: t.ID, UserID: t.From, Login: a.Login, Amount: t.Amount})
		}
		if t.From == s.Config.TreasuryID {
			for id, w := range s.Withdrawals {
				if w.UpstreamIssue == t.Issue && !w.Paid && !w.Released {
					if t.To != w.Event.UserID || t.Amount != w.Event.Amount-1 {
						return e, fmt.Errorf("withdrawal %s paid an unexpected recipient or amount", id)
					}
					e.Payments = append(e.Payments, ledger.Payment{ID: t.ID, Withdrawal: id, Issue: t.Issue, UserID: t.To, Amount: t.Amount})
				}
			}
		}
	}
	// A recorded withdrawal must use a direct send, never a claimable offer.
	for _, w := range s.Withdrawals {
		if w.UpstreamIssue == 0 || w.Paid || w.Released {
			continue
		}
		for _, ev := range u.Events {
			if ev.Source.Issue == w.UpstreamIssue && (ev.Type != "send" || ev.Actor.ID != s.Config.TreasuryID) {
				return e, fmt.Errorf("withdrawal issue %d did not settle as a treasury direct payment", w.UpstreamIssue)
			}
		}
	}
	return e, nil
}

func WithdrawalBody(repository, id, recipient string, amount int64) string {
	return fmt.Sprintf("Casino withdrawal.\n\n<!-- casino-withdrawal:%s:%s -->\n\n```chriscoin\n{\"action\":\"offer\",\"recipient\":%q,\"amount\":%d}\n```\n", repository, id, recipient, amount)
}
func WithdrawalMarker(repository, id string) string {
	return "<!-- casino-withdrawal:" + repository + ":" + id + " -->"
}
func Marked(body, repository, id string) bool {
	return strings.Contains(body, WithdrawalMarker(repository, id))
}
