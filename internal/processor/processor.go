// Package processor runs trusted GitHub issue commands under one Actions lock.
package processor

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	cc "chriscoin-casino/internal/chriscoin"
	"chriscoin-casino/internal/game"
	gh "chriscoin-casino/internal/github"
	"chriscoin-casino/internal/ledger"
)

const LedgerPath = "ledger/events.jsonl"
const editedMessage = "Edited comments are not accepted; submit a new comment."

type Store interface {
	Main(context.Context) (string, error)
	File(context.Context, string, string) ([]byte, error)
	WriteBranch(context.Context, string, string, []byte, string) (string, error)
	FindPR(context.Context, string) (*gh.PullRequest, error)
	CreatePR(context.Context, string, string, string) (*gh.PullRequest, error)
	Merge(context.Context, int, string) error
	Issues(context.Context) ([]gh.Issue, error)
	Comments(context.Context, int) ([]gh.Comment, error)
	Comment(context.Context, int, string) error
	UpdateComment(context.Context, int64, string) error
	CloseIssue(context.Context, int) error
	ReopenIssue(context.Context, int) error
}
type Upstream interface {
	User(context.Context, string) (gh.User, error)
	Comments(context.Context, int) ([]gh.Comment, error)
	Main(context.Context) (string, error)
	File(context.Context, string, string) ([]byte, error)
	Issues(context.Context) ([]gh.Issue, error)
}
type Payer interface {
	Me(context.Context) (gh.User, error)
	User(context.Context, string) (gh.User, error)
	CreateIssue(context.Context, string, string) (gh.Issue, error)
}
type Processor struct {
	Store      Store
	Upstream   Upstream
	Payer      Payer
	Repository string
	Config     ledger.Config
	Now        func() time.Time
	Sample     func(wager ledger.Event) (game.Draw, error)
	Limit      int
	events     []ledger.Event
	state      *ledger.State
	base       string
}

func (p *Processor) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}
func (p *Processor) load(ctx context.Context) error {
	base, err := p.Store.Main(ctx)
	if err != nil {
		return err
	}
	data, err := p.Store.File(ctx, LedgerPath, base)
	if err != nil {
		return err
	}
	events, err := ledger.Parse(data)
	if err != nil {
		return err
	}
	s, err := ledger.Replay(events)
	if err != nil {
		return err
	}
	if s.Initialized && (s.Config != p.Config || s.Repository != p.Repository) {
		return fmt.Errorf("configuration differs from canonical initialization")
	}
	p.base = base
	p.events = events
	p.state = s
	return nil
}
func eventBranch(id string) string { return "casino/event-" + ledger.Hash([]byte(id))[:24] }

// commit freezes an event on its branch before merging. Reusing the checkpoint
// preserves a published random draw even if a workflow stops before the merge.
func (p *Processor) commit(ctx context.Context, e ledger.Event) error {
	if err := p.load(ctx); err != nil {
		return err
	}
	if _, ok := p.state.Events[e.ID]; ok {
		return nil
	}
	branch := eventBranch(e.ID)
	pr, err := p.Store.FindPR(ctx, branch)
	if err != nil {
		return err
	}
	if pr != nil {
		if pr.User.Type != "Bot" || pr.User.Login != "github-actions[bot]" || pr.Head.Repo.FullName != p.Repository || pr.Head.Ref != branch || pr.State != "open" || pr.Merged {
			return fmt.Errorf("unexpected PR provenance or state for %s", branch)
		}
	}
	ref := branch
	if pr != nil {
		ref = pr.Head.SHA
	}
	data, err := p.Store.File(ctx, LedgerPath, ref)
	if err == nil {
		checkpoint, err := ledger.Parse(data)
		if err != nil || len(checkpoint) == 0 {
			return fmt.Errorf("invalid checkpoint for %s", e.ID)
		}
		frozen := checkpoint[len(checkpoint)-1]
		if frozen.ID != e.ID || frozen.Type != e.Type || frozen.UserID != e.UserID || frozen.Ref != e.Ref || frozen.Comment != e.Comment {
			return fmt.Errorf("checkpoint identity mismatch")
		}
		e = frozen
	} else if !gh.IsStatus(err, 404) {
		return err
	} else if pr != nil {
		return fmt.Errorf("PR checkpoint is missing")
	}
	e.Version = 1
	e.Sequence = len(p.events) + 1
	if e.At.IsZero() {
		e.At = p.now()
	}
	next := append(append([]ledger.Event{}, p.events...), e)
	if _, err := ledger.Replay(next); err != nil {
		return err
	}
	title := fmt.Sprintf("Casino %s: %s", e.Type, e.ID)
	sha, err := p.Store.WriteBranch(ctx, p.base, branch, ledger.Serialize(next), title)
	if err != nil {
		return err
	}
	if pr == nil {
		pr, err = p.Store.CreatePR(ctx, branch, title, "Trusted worker validated this append-only casino event.\n\nEvent: `"+e.ID+"`.")
		if err != nil {
			return err
		}
	}
	current, err := p.Store.Main(ctx)
	if err != nil {
		return err
	}
	if current != p.base {
		return fmt.Errorf("main changed while processing; retry required")
	}
	if err := p.Store.Merge(ctx, pr.Number, sha); err != nil {
		return err
	}
	return p.load(ctx)
}
func (p *Processor) Run(ctx context.Context) error {
	if err := p.Config.Validate(); err != nil {
		return err
	}
	if p.Limit == 0 {
		p.Limit = 25
	}
	if err := p.load(ctx); err != nil {
		return err
	}
	// Discover attempted withdrawals before importing payments, so a payment and
	// its recovered upstream issue association enter the ledger in the right order.
	// Link deposit requests first too: only payments of linked casino
	// requests are credited, and an import is never revisited.
	if p.state.Initialized {
		if err := p.recoverSubmissions(ctx); err != nil {
			return err
		}
		if err := p.submitDeposits(ctx); err != nil {
			return err
		}
	}
	commit, err := p.Upstream.Main(ctx)
	if err != nil {
		return err
	}
	data, err := p.Upstream.File(ctx, LedgerPath, commit)
	if err != nil {
		return err
	}
	up, err := cc.Read(data, p.Config.UpstreamRepository)
	if err != nil {
		return err
	}
	treasury := up.State.Accounts[p.Config.TreasuryID]
	if treasury == nil {
		return fmt.Errorf("treasury must open a ChrisCoin account before casino initialization")
	}
	identity, err := p.Upstream.User(ctx, treasury.Login)
	if err != nil {
		return err
	}
	if identity.ID != p.Config.TreasuryID || identity.Type != "User" {
		return fmt.Errorf("treasury username does not resolve to its configured numeric ID; refresh its upstream account")
	}
	if !p.state.Initialized {
		openingData, err := p.Upstream.File(ctx, LedgerPath, p.Config.StartingCommit)
		if err != nil {
			return fmt.Errorf("load configured starting commit: %w", err)
		}
		opening, err := cc.Read(openingData, p.Config.UpstreamRepository)
		if err != nil {
			return fmt.Errorf("validate starting ledger: %w", err)
		}
		account := opening.State.Accounts[p.Config.TreasuryID]
		if account == nil {
			return fmt.Errorf("treasury did not have a ChrisCoin account at the configured starting commit")
		}
		if len(up.Events) < len(opening.Events) || ledger.Hash(cc.Serialize(up.Events[:len(opening.Events)])) != opening.Hash {
			return fmt.Errorf("current upstream ledger does not preserve the configured starting ledger")
		}
		cfg := p.Config
		if err := p.commit(ctx, ledger.Event{ID: "init", Type: "init", Config: &cfg, Repository: p.Repository, UpstreamCommit: cfg.StartingCommit, UpstreamCount: len(opening.Events), UpstreamHash: opening.Hash, Treasury: account.Available}); err != nil {
			return err
		}
	}
	// Import payments after the fixed baseline even on the first live run.
	if commit != p.state.UpstreamCommit {
		sync, err := up.Sync(p.state, commit)
		if err != nil {
			return err
		}
		if err := p.commit(ctx, sync); err != nil {
			return err
		}
	}
	if err := p.releaseRejected(ctx, up); err != nil {
		return err
	}
	if err := p.settle(ctx); err != nil {
		return err
	}
	issues, err := p.Store.Issues(ctx)
	if err != nil {
		return err
	}
	if err := p.ensureAccounts(ctx, issues); err != nil {
		return err
	}
	accounts := []*ledger.Account{}
	for _, a := range p.state.Accounts {
		if a.Issue > 0 {
			accounts = append(accounts, a)
		}
	}
	// Include duplicate user issues for rejection and redirection, without
	// allowing their comments to transact against the original account.
	seen := map[int]bool{}
	for _, a := range accounts {
		seen[a.Issue] = true
	}
	for _, issue := range issues {
		if issue.User.Type == "User" && !seen[issue.Number] {
			accounts = append(accounts, &ledger.Account{ID: issue.User.ID, Issue: issue.Number})
			seen[issue.Number] = true
		}
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Issue < accounts[j].Issue })
	processed := 0
	for _, a := range accounts {
		comments, err := p.Store.Comments(ctx, a.Issue)
		if err != nil {
			return err
		}
		sort.Slice(comments, func(i, j int) bool { return comments[i].ID < comments[j].ID })
		for _, c := range comments {
			// The treasury may chat on player issues; only its slash commands count.
			admin := c.User.ID == p.Config.TreasuryID
			if c.User.Type != "User" || ((c.User.ID == a.ID || admin) && !strings.HasPrefix(strings.TrimSpace(c.Body), "/")) {
				continue
			}
			id := fmt.Sprintf("comment-%d", c.ID)
			if _, ok := p.state.Events[id]; ok {
				continue
			}
			if processed >= p.Limit {
				break
			}
			processed++
			e := ledger.Event{ID: id, Type: "rejected", UserID: c.User.ID, Login: c.User.Login, Issue: a.Issue, Comment: c.ID, BodyHash: ledger.Hash([]byte(c.Body))}
			adminCommand, adminAmount, adminReason, adminErr := ledger.AdminCommand(c.Body)
			command, amount, parseErr := ledger.Command(c.Body)
			gameName, bet, stake, gameErr := ledger.Play(c.Body)
			switch {
			case admin && adminCommand != "":
				target := p.state.Accounts[a.ID]
				switch {
				case a.ID == p.Config.TreasuryID || target == nil || target.Issue != a.Issue:
					e.Reason = "Admin commands only work on a player’s own account issue."
				case !c.UpdatedAt.Equal(c.CreatedAt):
					e.Reason = editedMessage
				case adminErr != nil:
					e.Reason = adminErr.Error()
				default:
					e.Type, e.Target, e.Amount, e.Reason = adminCommand, a.ID, adminAmount, adminReason
				}
			case c.User.ID != a.ID || p.state.Accounts[c.User.ID] == nil || p.state.Accounts[c.User.ID].Issue != a.Issue:
				e.Reason = p.redirect(c.User.ID)
			case !c.UpdatedAt.Equal(c.CreatedAt):
				e.Reason = editedMessage
			case gameName != "" && gameErr != nil:
				e.Reason = gameErr.Error()
			case gameName != "":
				e.Type, e.Game, e.Bet, e.Amount, e.GameVersion = "wager", gameName, bet, stake, game.VersionOf(gameName)
			case parseErr != nil:
				e.Reason = "Use /deposit N, /wager N, /roulette N bet, /slots N, /withdraw N, or /balance with positive whole coins."
			case command == "deposit" && c.User.ID == p.Config.TreasuryID:
				e.Reason = treasuryDepositMessage
			default:
				e.Type = command
				e.Amount = amount
				if command == "balance" {
					e.Amount = p.state.Accounts[a.ID].Available
				}
				if command == "wager" {
					e.GameVersion = game.Version
				}
			}
			if e.Type != "rejected" {
				candidate := e
				candidate.Version = 1
				candidate.Sequence = len(p.events) + 1
				candidate.At = p.now()
				if _, err := ledger.Replay(append(append([]ledger.Event{}, p.events...), candidate)); err != nil {
					e.Type = "rejected"
					e.Reason = err.Error()
					e.Amount = 0
					e.GameVersion = 0
					e.Game, e.Bet = "", ""
					e.Target = 0
				}
			}
			if data, err := p.Store.File(ctx, LedgerPath, eventBranch(e.ID)); err == nil {
				frozen, err := ledger.Parse(data)
				if err != nil || len(frozen) == 0 {
					return fmt.Errorf("invalid command checkpoint")
				}
				original := frozen[len(frozen)-1]
				if original.ID != e.ID || original.UserID != e.UserID || original.Issue != e.Issue || original.Comment != e.Comment {
					return fmt.Errorf("command checkpoint provenance mismatch")
				}
				e = original
			} else if !gh.IsStatus(err, 404) {
				return err
			}
			if err := p.commit(ctx, e); err != nil {
				return err
			}
			if err := p.settle(ctx); err != nil {
				return err
			}
		}
	}
	if err := p.submitDeposits(ctx); err != nil {
		return err
	}
	if err := p.submitWithdrawals(ctx, up); err != nil {
		return err
	}
	return p.results(ctx, up)
}
func (p *Processor) settle(ctx context.Context) error {
	ids := []string{}
	for id, w := range p.state.Wagers {
		if !w.Settled {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		w := p.state.Wagers[id]
		branch := eventBranch("settle-" + id)
		data, err := p.Store.File(ctx, LedgerPath, branch)
		var d game.Draw
		if err == nil {
			events, err := ledger.Parse(data)
			if err != nil || len(events) == 0 {
				return fmt.Errorf("invalid wager checkpoint")
			}
			d = events[len(events)-1].Draw
		} else if !gh.IsStatus(err, 404) {
			return err
		} else {
			if p.Sample != nil {
				d, err = p.Sample(w.Event)
			} else {
				d, err = game.Roll(rand.Reader, w.Event.Game, w.Event.Bet, w.Event.Amount)
			}
			if err != nil {
				return err
			}
		}
		payout, err := game.Settle(w.Event.Game, w.Event.Bet, w.Event.Amount, d)
		if err != nil {
			return err
		}
		if err := p.commit(ctx, ledger.Event{ID: "settle-" + id, Type: "settle", Ref: id, Amount: payout, GameVersion: w.Event.GameVersion, Draw: d}); err != nil {
			return err
		}
	}
	return nil
}
func (p *Processor) recoverSubmissions(ctx context.Context) error {
	ids := []string{}
	for id, w := range p.state.Withdrawals {
		if w.Attempted && w.UpstreamIssue == 0 && !w.Paid && !w.Released {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	issues, err := p.Upstream.Issues(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		var match *gh.Issue
		for _, issue := range issues {
			if issue.User.ID == p.Config.TreasuryID && issue.User.Type == "User" && cc.Marked(issue.Body, p.Repository, id) {
				if match != nil {
					return fmt.Errorf("multiple upstream issues for withdrawal %s", id)
				}
				copy := issue
				match = &copy
			}
		}
		if match != nil {
			if err := p.commit(ctx, ledger.Event{ID: "submitted-" + id, Type: "submitted", Ref: id, Issue: match.Number}); err != nil {
				return err
			}
		} else {
			log.Printf("Withdrawal %s has an uncertain submission; funds remain locked. Inspect upstream and recover the original issue; no automatic resend.", id)
		}
	}
	return nil
}
func (p *Processor) submitWithdrawals(ctx context.Context, up cc.Snapshot) error {
	if p.Payer == nil {
		return nil
	}
	ids := []string{}
	for id, w := range p.state.Withdrawals {
		if !w.Attempted && !w.Paid && !w.Released {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	me, err := p.Payer.Me(ctx)
	if err != nil {
		return err
	}
	if me.ID != p.Config.TreasuryID || me.Type != "User" {
		return fmt.Errorf("treasury token must authenticate as the configured ordinary user")
	}
	sort.Strings(ids)
	for _, id := range ids {
		w := p.state.Withdrawals[id]
		a := up.State.Accounts[w.Event.UserID]
		if a == nil {
			return fmt.Errorf("withdrawal recipient has no ChrisCoin account")
		}
		user, err := p.Payer.User(ctx, a.Login)
		if err != nil {
			return err
		}
		if user.ID != a.ID || user.Type != "User" {
			return fmt.Errorf("withdrawal recipient username no longer matches numeric ID")
		}
		matches := 0
		for _, other := range up.State.Accounts {
			if strings.EqualFold(other.Login, a.Login) {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("ambiguous upstream recipient username")
		}
		if err := p.commit(ctx, ledger.Event{ID: "attempt-" + id, Type: "attempt", Ref: id}); err != nil {
			return err
		}
		issue, err := p.Payer.CreateIssue(ctx, cc.RequestTitle(cc.Command{Action: "offer", Recipient: a.Login, Amount: w.Event.Amount - 1}), cc.WithdrawalBody(p.Repository, id, a.Login, w.Event.Amount-1))
		if err != nil {
			log.Printf("Submission of %s is uncertain: %v. Recovery will inspect upstream; it will not resend.", id, err)
			continue
		}
		if issue.User.ID != p.Config.TreasuryID || issue.User.Type != "User" || issue.Number <= 0 {
			return fmt.Errorf("unexpected upstream issue author")
		}
		if err := p.commit(ctx, ledger.Event{ID: "submitted-" + id, Type: "submitted", Ref: id, Issue: issue.Number}); err != nil {
			return err
		}
	}
	return nil
}
func (p *Processor) reply(ctx context.Context, issue int, id, message string) error {
	marker := "<!-- casino-result:" + id + " -->"
	body := message + "\n\n" + marker
	comments, err := p.Store.Comments(ctx, issue)
	if err != nil {
		return err
	}
	for _, c := range comments {
		if c.User.Type == "Bot" && c.User.Login == "github-actions[bot]" && strings.Contains(c.Body, marker) {
			if c.Body == body {
				return nil
			}
			return p.Store.UpdateComment(ctx, c.ID, body)
		}
	}
	return p.Store.Comment(ctx, issue, body)
}

// A ChrisCoin bot rejection is terminal for that issue in the upstream worker.
// Closed issues without this proof are not sufficient to release locked funds.
func (p *Processor) releaseRejected(ctx context.Context, up cc.Snapshot) error {
	ids := []string{}
	for id, w := range p.state.Withdrawals {
		if w.UpstreamIssue > 0 && !w.Paid && !w.Released {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	issues, err := p.Upstream.Issues(ctx)
	if err != nil {
		return err
	}
	byNumber := map[int]gh.Issue{}
	for _, i := range issues {
		byNumber[i.Number] = i
	}
	for _, id := range ids {
		w := p.state.Withdrawals[id]
		issue, ok := byNumber[w.UpstreamIssue]
		if !ok || issue.State != "closed" || issue.User.ID != p.Config.TreasuryID {
			continue
		}
		recorded := false
		for _, e := range up.Events {
			if e.Source.Issue == w.UpstreamIssue {
				recorded = true
			}
		}
		if recorded {
			continue
		}
		comments, err := p.Upstream.Comments(ctx, w.UpstreamIssue)
		if err != nil {
			return err
		}
		marker := fmt.Sprintf("<!-- chriscoin-result:issue=%d;status=rejected -->", w.UpstreamIssue)
		for _, c := range comments {
			if c.User.Type == "Bot" && c.User.Login == "github-actions[bot]" && strings.Contains(c.Body, marker) {
				if err := p.commit(ctx, ledger.Event{ID: "release-" + id, Type: "release", Ref: id, Issue: w.UpstreamIssue, Reason: fmt.Sprintf("Terminal ChrisCoin rejection in upstream comment %d", c.ID)}); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}
