// Package site renders the casino ledger as a static GitHub Pages site.
package site

import (
	"embed"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	cc "chriscoin-casino/internal/chriscoin"
	"chriscoin-casino/internal/game"
	"chriscoin-casino/internal/ledger"
)

//go:embed index.html
var files embed.FS

const boardSize = 30

// TipAmount is the prefilled tip. Direct payments to the treasury are never
// credited to a player, so a tip goes straight to the house.
const TipAmount = 10

type Round struct {
	Login, Game, Bet, Outcome, Color string
	Issue                            int
	Stake, Payout                    int64
	Settled                          bool
	Pocket                           int64
	Reels                            []string
}
type Player struct {
	Login        string
	Issue        int
	Balance, Net int64
	Rounds       int
	Frozen       bool
}
type Cashout struct {
	Login, Status string
	Issue         int
	Amount        int64
}
type Page struct {
	Repository, Upstream, Commit, Updated string
	TipURL                                template.URL
	TipAmount                             int64
	State                                 *ledger.State
	Board                                 []Round
	Players                               []Player
	Cashouts                              []Cashout
	Rounds, Staked, Returned              int64
}

// Build replays the ledger and writes index.html into dir. The output depends
// only on the ledger and commit, so identical inputs produce identical pages.
func Build(data []byte, dir, commit string) error {
	events, err := ledger.Parse(data)
	if err != nil {
		return err
	}
	s, err := ledger.Replay(events)
	if err != nil {
		return err
	}
	if !s.Initialized {
		return fmt.Errorf("ledger is not initialized")
	}
	p := Page{Repository: s.Repository, Upstream: s.Config.UpstreamRepository, Commit: commit, State: s}
	if t := s.Accounts[s.Config.TreasuryID]; t != nil && t.Login != "" {
		command := cc.Command{Action: "offer", Recipient: t.Login, Amount: TipAmount}
		query := url.Values{"template": {"request.md"}, "title": {cc.RequestTitle(command)}, "body": {cc.RequestBody(command, "")}}
		// Encode spaces as %20; a literal + is already escaped as %2B.
		p.TipURL = template.URL("https://github.com/" + p.Upstream + "/issues/new?" + strings.ReplaceAll(query.Encode(), "+", "%20"))
		p.TipAmount = TipAmount
	}
	if len(events) > 0 {
		p.Updated = events[len(events)-1].At.UTC().Format("2 Jan 2006, 15:04 MST")
	}
	login := func(id int64, fallback string) string {
		if a := s.Accounts[id]; a != nil && a.Login != "" {
			return a.Login
		}
		return fallback
	}

	wagers := []*ledger.Wager{}
	for _, w := range s.Wagers {
		wagers = append(wagers, w)
	}
	sort.Slice(wagers, func(i, j int) bool { return wagers[i].Event.Sequence > wagers[j].Event.Sequence })
	net := map[int64]int64{}
	rounds := map[int64]int{}
	for _, w := range wagers {
		e := w.Event
		r := Round{Login: login(e.UserID, e.Login), Issue: e.Issue, Game: e.Game, Bet: e.Bet, Stake: e.Amount, Payout: w.Payout, Settled: w.Settled}
		switch {
		case !w.Settled:
			r.Outcome = "open"
		case w.Payout > e.Amount:
			r.Outcome = "win"
		case w.Payout == e.Amount:
			r.Outcome = "push"
		default:
			r.Outcome = "loss"
		}
		if w.Settled {
			p.Rounds++
			p.Staked += e.Amount
			p.Returned += w.Payout
			net[e.UserID] += w.Payout - e.Amount
			rounds[e.UserID]++
			switch e.Game {
			case game.Roulette:
				r.Pocket = w.Draw.Pocket
				r.Color = map[string]string{"🟢": "green", "🔴": "red", "⚫": "black"}[game.PocketColor(r.Pocket)]
			case game.Slots:
				r.Reels, _ = game.Reels(w.Draw)
			}
		}
		if len(p.Board) < boardSize {
			p.Board = append(p.Board, r)
		}
	}

	for _, a := range s.Accounts {
		if a.ID == s.Config.TreasuryID || (a.Issue == 0 && a.Available == 0) {
			continue
		}
		p.Players = append(p.Players, Player{Login: a.Login, Issue: a.Issue, Balance: a.Available, Net: net[a.ID], Rounds: rounds[a.ID], Frozen: a.Frozen})
	}
	sort.Slice(p.Players, func(i, j int) bool {
		if p.Players[i].Balance != p.Players[j].Balance {
			return p.Players[i].Balance > p.Players[j].Balance
		}
		return strings.ToLower(p.Players[i].Login) < strings.ToLower(p.Players[j].Login)
	})

	withdrawals := []*ledger.Withdrawal{}
	for _, w := range s.Withdrawals {
		withdrawals = append(withdrawals, w)
	}
	sort.Slice(withdrawals, func(i, j int) bool { return withdrawals[i].Event.Sequence > withdrawals[j].Event.Sequence })
	for _, w := range withdrawals {
		c := Cashout{Login: login(w.Event.UserID, w.Event.Login), Issue: w.UpstreamIssue, Amount: w.Event.Amount}
		switch {
		case w.Paid:
			c.Status = "Paid"
		case w.Released:
			c.Status = "Returned to balance"
		case w.UpstreamIssue > 0:
			c.Status = "Clearing in ChrisCoin"
		case w.Attempted:
			c.Status = "Waiting for the operator"
		default:
			c.Status = "Queued"
		}
		p.Cashouts = append(p.Cashouts, c)
	}

	t, err := template.New("index.html").Funcs(template.FuncMap{
		"cc":     coins,
		"signed": signed,
		"short": func(s string) string {
			if len(s) > 7 {
				return s[:7]
			}
			return s
		},
		"times": func(payout, stake int64) string {
			s := strconv.FormatFloat(float64(payout)/float64(stake), 'f', 2, 64)
			return strings.TrimSuffix(strings.TrimRight(s, "0"), ".") + "×"
		},
	}).ParseFS(files, "index.html")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	out, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	defer out.Close()
	if err := t.Execute(out, p); err != nil {
		return err
	}
	return out.Close()
}

// coins formats whole coins with thousands separators.
func coins(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "−", -n
	}
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}
func signed(n int64) string {
	if n > 0 {
		return "+" + coins(n)
	}
	return coins(n)
}
