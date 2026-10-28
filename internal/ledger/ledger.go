// Package ledger defines append-only casino accounting. It never mints CC.
package ledger

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"chriscoin-casino/internal/game"
)

const MaxCoins int64 = 9_007_199_254_740_991

type Config struct {
	UpstreamRepository string `json:"upstream_repository"`
	TreasuryID         int64  `json:"treasury_id"`
	StartingCommit     string `json:"starting_commit"`
}

func (c Config) Validate() error {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(c.UpstreamRepository) || c.TreasuryID <= 0 || c.TreasuryID == 553949 || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(c.StartingCommit) {
		return fmt.Errorf("configure an ordinary treasury user ID, upstream repository, and 40-character starting_commit")
	}
	return nil
}

type Credit struct {
	ID     string `json:"id"`
	UserID int64  `json:"user_id"`
	Login  string `json:"login"`
	Amount int64  `json:"amount"`
}
type Payment struct {
	ID         string `json:"id"`
	Withdrawal string `json:"withdrawal"`
	Issue      int    `json:"issue"`
	UserID     int64  `json:"user_id"`
	Amount     int64  `json:"amount"`
}
type Event struct {
	Version        int       `json:"version"`
	Sequence       int       `json:"sequence"`
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	At             time.Time `json:"at"`
	UserID         int64     `json:"user_id,omitempty"`
	Login          string    `json:"login,omitempty"`
	Issue          int       `json:"issue,omitempty"`
	TargetIssue    int       `json:"target_issue,omitempty"`
	Target         int64     `json:"target,omitempty"`
	Comment        int64     `json:"comment,omitempty"`
	BodyHash       string    `json:"body_hash,omitempty"`
	Amount         int64     `json:"amount,omitempty"`
	Ref            string    `json:"ref,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	GameVersion    int       `json:"game_version,omitempty"`
	Game           string    `json:"game,omitempty"`
	Bet            string    `json:"bet,omitempty"`
	Draw           game.Draw `json:"draw,omitempty"`
	Config         *Config   `json:"config,omitempty"`
	Repository     string    `json:"repository,omitempty"`
	UpstreamCommit string    `json:"upstream_commit,omitempty"`
	UpstreamHash   string    `json:"upstream_hash,omitempty"`
	UpstreamCount  int       `json:"upstream_count,omitempty"`
	Treasury       int64     `json:"treasury,omitempty"`
	Credits        []Credit  `json:"credits,omitempty"`
	Payments       []Payment `json:"payments,omitempty"`
}
type Account struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Issue     int    `json:"issue"`
	Available int64  `json:"available"`
	Frozen    bool   `json:"frozen,omitempty"`
}
type Wager struct {
	Event   Event     `json:"event"`
	Settled bool      `json:"settled"`
	Payout  int64     `json:"payout"`
	Max     int64     `json:"max"`
	Draw    game.Draw `json:"draw"`
}
type Deposit struct {
	Event         Event `json:"event"`
	UpstreamIssue int   `json:"upstream_issue"`
}
type Withdrawal struct {
	Event         Event `json:"event"`
	Attempted     bool  `json:"attempted"`
	UpstreamIssue int   `json:"upstream_issue"`
	Paid          bool  `json:"paid"`
	Released      bool  `json:"released"`
}
type State struct {
	Config         Config                 `json:"config"`
	Repository     string                 `json:"repository"`
	Initialized    bool                   `json:"initialized"`
	Deposits       map[string]*Deposit    `json:"deposits"`
	Accounts       map[int64]*Account     `json:"accounts"`
	Events         map[string]Event       `json:"events"`
	Wagers         map[string]*Wager      `json:"wagers"`
	Withdrawals    map[string]*Withdrawal `json:"withdrawals"`
	Imported       map[string]bool        `json:"-"`
	UpstreamCommit string                 `json:"upstream_commit"`
	UpstreamHash   string                 `json:"upstream_hash"`
	UpstreamCount  int                    `json:"upstream_count"`
	Treasury       int64                  `json:"treasury"`
	Fees           int64                  `json:"fees"`
	Granted        int64                  `json:"granted"`
}

func Hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func Decode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}
func Parse(data []byte) ([]Event, error) {
	out := []Event{}
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 4096), 8*1024*1024)
	for s.Scan() {
		var e Event
		if err := Decode(s.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("line %d: %w", len(out)+1, err)
		}
		out = append(out, e)
	}
	return out, s.Err()
}
func Serialize(events []Event) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, e := range events {
		_ = enc.Encode(e)
	}
	return b.Bytes()
}
func Command(body string) (string, int64, error) {
	f := strings.Fields(strings.TrimSpace(body))
	if len(f) == 1 && f[0] == "/balance" {
		return "balance", 0, nil
	}
	if len(f) != 2 || (f[0] != "/wager" && f[0] != "/withdraw" && f[0] != "/deposit") {
		return "", 0, fmt.Errorf("use /deposit N, /wager N, /withdraw N, or /balance")
	}
	if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(f[1]) {
		return "", 0, fmt.Errorf("amount must be a positive whole number")
	}
	n, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || n > MaxCoins {
		return "", 0, fmt.Errorf("amount outside supported range")
	}
	return strings.TrimPrefix(f[0], "/"), n, nil
}

// AdminCommand parses treasury commands. An empty command means the body is
// not an admin command; an error means it is one with invalid arguments.
func AdminCommand(body string) (string, int64, string, error) {
	f := strings.Fields(strings.TrimSpace(body))
	if len(f) == 0 || (f[0] != "/freeze" && f[0] != "/unfreeze" && f[0] != "/credit") {
		return "", 0, "", nil
	}
	command := strings.TrimPrefix(f[0], "/")
	rest := f[1:]
	var n int64
	if command == "credit" {
		if len(rest) == 0 || !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(rest[0]) {
			return command, 0, "", fmt.Errorf("use /credit N reason with a positive whole amount")
		}
		var err error
		n, err = strconv.ParseInt(rest[0], 10, 64)
		if err != nil || n > MaxCoins {
			return command, 0, "", fmt.Errorf("amount outside supported range")
		}
		rest = rest[1:]
	}
	reason := strings.Join(rest, " ")
	if reason == "" && command != "unfreeze" {
		return command, 0, "", fmt.Errorf("/%s needs a reason; it is shown to the player and kept in the ledger", command)
	}
	if len([]rune(reason)) > 200 || strings.ContainsFunc(reason, func(r rune) bool { return r < 32 || r == 127 }) {
		return command, 0, "", fmt.Errorf("keep the reason to 200 plain characters")
	}
	return command, n, reason, nil
}

// Play parses a named game command. An empty game means the body is not one.
func Play(body string) (string, string, int64, error) {
	f := strings.Fields(strings.TrimSpace(body))
	if len(f) == 0 || (f[0] != "/roulette" && f[0] != "/slots") {
		return "", "", 0, nil
	}
	name := strings.TrimPrefix(f[0], "/")
	usage := "use /roulette N bet, for example /roulette 10 red"
	if name == game.Slots {
		usage = "use /slots N, for example /slots 10"
	}
	if (name == game.Roulette && len(f) != 3) || (name == game.Slots && len(f) != 2) {
		return name, "", 0, fmt.Errorf("%s", usage)
	}
	if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(f[1]) {
		return name, "", 0, fmt.Errorf("amount must be a positive whole number")
	}
	n, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || n > MaxCoins {
		return name, "", 0, fmt.Errorf("amount outside supported range")
	}
	bet := ""
	if len(f) == 3 {
		if bet, err = game.NormalizeBet(name, f[2]); err != nil {
			return name, "", 0, err
		}
	}
	return name, bet, n, nil
}
func (s *State) account(id int64, login string) *Account {
	a := s.Accounts[id]
	if a == nil {
		a = &Account{ID: id, Login: login}
		s.Accounts[id] = a
	}
	return a
}
func (s *State) Liabilities() int64 {
	var n int64
	for _, a := range s.Accounts {
		n += a.Available
	}
	for _, w := range s.Wagers {
		if !w.Settled {
			n += w.Event.Amount
		}
	}
	for _, w := range s.Withdrawals {
		if !w.Paid && !w.Released {
			n += w.Event.Amount
		}
	}
	return n
}

// Exposure is the house's possible loss on open wagers, beyond their stakes.
func (s *State) Exposure() int64 {
	var n int64
	for _, w := range s.Wagers {
		if !w.Settled {
			n += w.Max - w.Event.Amount
		}
	}
	return n
}

// HouseBalance is what the treasury holds beyond everything owed to players.
// Bets are honoured regardless, so it can go negative.
func (s *State) HouseBalance() int64 { return s.Treasury - s.Liabilities() }

// Payable is the treasury CC left after every queued withdrawal is paid.
// Withdrawals are paid first come, first served from this amount.
func (s *State) Payable() int64 {
	n := s.Treasury
	for _, w := range s.Withdrawals {
		if !w.Paid && !w.Released {
			n -= w.Event.Amount - 1
		}
	}
	return n
}
func Replay(events []Event) (*State, error) {
	s := &State{Accounts: map[int64]*Account{}, Events: map[string]Event{}, Wagers: map[string]*Wager{}, Withdrawals: map[string]*Withdrawal{}, Deposits: map[string]*Deposit{}, Imported: map[string]bool{}}
	for i, e := range events {
		if e.Version != 1 || e.Sequence != i+1 || e.ID == "" || e.At.IsZero() {
			return nil, fmt.Errorf("invalid event at sequence %d", i+1)
		}
		if _, ok := s.Events[e.ID]; ok {
			return nil, fmt.Errorf("duplicate event %s", e.ID)
		}
		if err := s.apply(e); err != nil {
			return nil, fmt.Errorf("event %s: %w", e.ID, err)
		}
		s.Events[e.ID] = e
		if err := s.bounds(); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *State) bounds() error {
	var total int64
	for _, a := range s.Accounts {
		if a.Available < 0 || a.Available > MaxCoins || total > MaxCoins-a.Available {
			return fmt.Errorf("account or liability overflow")
		}
		total += a.Available
	}
	for _, w := range s.Wagers {
		if !w.Settled {
			if total > MaxCoins-w.Event.Amount {
				return fmt.Errorf("liability overflow")
			}
			total += w.Event.Amount
		}
	}
	for _, w := range s.Withdrawals {
		if !w.Paid && !w.Released {
			if total > MaxCoins-w.Event.Amount {
				return fmt.Errorf("liability overflow")
			}
			total += w.Event.Amount
		}
	}
	if s.Treasury < 0 || s.Treasury > MaxCoins || s.Fees < 0 || s.Fees > MaxCoins {
		return fmt.Errorf("treasury or fee overflow")
	}
	return nil
}
func (s *State) owner(e Event) error {
	a := s.Accounts[e.UserID]
	if a == nil || a.Issue <= 0 || a.Issue != e.Issue || e.Comment <= 0 || len(e.BodyHash) != 64 || e.ID != fmt.Sprintf("comment-%d", e.Comment) {
		return fmt.Errorf("command provenance does not match account")
	}
	return nil
}

// admin checks that the treasury issued the command on the target's own issue.
func (s *State) admin(e Event) (*Account, error) {
	t := s.Accounts[e.Target]
	if e.UserID != s.Config.TreasuryID || e.Target == s.Config.TreasuryID || t == nil || t.Issue <= 0 || t.Issue != e.Issue || e.Comment <= 0 || len(e.BodyHash) != 64 || e.ID != fmt.Sprintf("comment-%d", e.Comment) {
		return nil, fmt.Errorf("admin command provenance does not match account")
	}
	return t, nil
}
func (s *State) apply(e Event) error {
	if e.Type == "init" {
		if s.Initialized || e.Config == nil || e.Repository == "" || e.UpstreamCommit != e.Config.StartingCommit {
			return fmt.Errorf("invalid initialization")
		}
		if err := e.Config.Validate(); err != nil {
			return err
		}
		s.Config = *e.Config
		s.Repository = e.Repository
		s.Initialized = true
		return s.snapshot(e)
	}
	if !s.Initialized {
		return fmt.Errorf("ledger must initialize first")
	}
	switch e.Type {
	case "sync":
		return s.snapshot(e)
	case "account":
		if e.UserID <= 0 || e.Issue <= 0 || e.Login == "" {
			return fmt.Errorf("invalid account")
		}
		a := s.account(e.UserID, e.Login)
		if a.Issue != 0 {
			return fmt.Errorf("account already has an issue")
		}
		for _, other := range s.Accounts {
			if other.Issue == e.Issue {
				return fmt.Errorf("issue already owned")
			}
		}
		a.Issue = e.Issue
		a.Login = e.Login
	case "duplicate":
		a := s.Accounts[e.UserID]
		if a == nil || a.Issue != e.TargetIssue || e.Issue <= 0 || e.Issue == e.TargetIssue || e.ID != fmt.Sprintf("duplicate-%d", e.Issue) {
			return fmt.Errorf("invalid duplicate issue")
		}
	case "duplicate-resolved":
		original, ok := s.Events[e.Ref]
		if !ok || original.Type != "duplicate" || e.ID != "resolved-"+e.Ref {
			return fmt.Errorf("unknown duplicate issue")
		}
	case "deposit":
		if err := s.owner(e); err != nil {
			return err
		}
		if e.Amount < 1 || e.Amount > MaxCoins {
			return fmt.Errorf("invalid deposit amount")
		}
		if s.Accounts[e.UserID].Frozen {
			return fmt.Errorf("account is frozen")
		}
		s.Deposits[e.ID] = &Deposit{Event: e}
	case "deposit-submitted":
		d := s.Deposits[e.Ref]
		if d == nil || d.UpstreamIssue != 0 || e.Issue <= 0 {
			return fmt.Errorf("invalid deposit submission")
		}
		for _, other := range s.Deposits {
			if other.UpstreamIssue == e.Issue {
				return fmt.Errorf("deposit request already linked")
			}
		}
		for _, w := range s.Withdrawals {
			if w.UpstreamIssue == e.Issue {
				return fmt.Errorf("upstream issue already used for withdrawal")
			}
		}
		d.UpstreamIssue = e.Issue
	case "rejected":
		if e.Comment <= 0 || e.UserID <= 0 || e.ID != fmt.Sprintf("comment-%d", e.Comment) || e.Reason == "" {
			return fmt.Errorf("invalid rejection")
		}
	case "balance":
		if err := s.owner(e); err != nil {
			return err
		}
		if e.Amount != s.Accounts[e.UserID].Available {
			return fmt.Errorf("invalid balance snapshot")
		}
	case "wager":
		if err := s.owner(e); err != nil {
			return err
		}
		a := s.Accounts[e.UserID]
		if a.Frozen {
			return fmt.Errorf("account is frozen")
		}
		if e.GameVersion != game.VersionOf(e.Game) || e.GameVersion == 0 || e.Amount < 1 || e.Amount > a.Available {
			return fmt.Errorf("invalid wager or insufficient funds")
		}
		bet, err := game.NormalizeBet(e.Game, e.Bet)
		if err != nil || bet != e.Bet {
			return fmt.Errorf("invalid bet")
		}
		// Every bet is honoured whatever the house holds; the maximum payout
		// only guards against integer overflow.
		max, err := game.MaxPayout(e.Game, e.Bet, e.Amount)
		if err != nil || max > MaxCoins {
			return fmt.Errorf("invalid wager or insufficient funds")
		}
		a.Available -= e.Amount
		s.Wagers[e.ID] = &Wager{Event: e, Max: max}
	case "settle":
		w := s.Wagers[e.Ref]
		if w == nil || w.Settled || e.GameVersion != w.Event.GameVersion {
			return fmt.Errorf("unknown or settled wager")
		}
		n, err := game.Settle(w.Event.Game, w.Event.Bet, w.Event.Amount, e.Draw)
		if err != nil || n != e.Amount || n > w.Max {
			return fmt.Errorf("invalid payout")
		}
		s.Accounts[w.Event.UserID].Available += n
		w.Settled = true
		w.Payout = n
		w.Draw = e.Draw
	case "withdraw":
		if err := s.owner(e); err != nil {
			return err
		}
		a := s.Accounts[e.UserID]
		if e.Amount < 2 || e.Amount > a.Available {
			return fmt.Errorf("invalid withdrawal or insufficient funds")
		}
		if s.Payable() < e.Amount-1 {
			return fmt.Errorf("treasury cannot pay this withdrawal")
		}
		a.Available -= e.Amount
		s.Withdrawals[e.ID] = &Withdrawal{Event: e}
	case "attempt":
		w := s.Withdrawals[e.Ref]
		if w == nil || w.Paid || w.Released || w.Attempted {
			return fmt.Errorf("invalid submission attempt")
		}
		w.Attempted = true
	case "release":
		w := s.Withdrawals[e.Ref]
		if w == nil || w.Paid || w.Released || w.UpstreamIssue == 0 || e.Issue != w.UpstreamIssue || e.Reason == "" {
			return fmt.Errorf("invalid withdrawal release")
		}
		s.Accounts[w.Event.UserID].Available += w.Event.Amount
		w.Released = true
	case "submitted":
		w := s.Withdrawals[e.Ref]
		if w == nil || w.Paid || w.Released || !w.Attempted || w.UpstreamIssue != 0 || e.Issue <= 0 {
			return fmt.Errorf("invalid upstream submission")
		}
		for _, other := range s.Withdrawals {
			if other.UpstreamIssue == e.Issue {
				return fmt.Errorf("upstream issue already used")
			}
		}
		w.UpstreamIssue = e.Issue
	case "credit":
		a, err := s.admin(e)
		if err != nil {
			return err
		}
		if e.Amount < 1 || e.Amount > MaxCoins || e.Reason == "" || a.Available > MaxCoins-e.Amount {
			return fmt.Errorf("invalid credit")
		}
		a.Available += e.Amount
		s.Granted += e.Amount
	case "freeze":
		a, err := s.admin(e)
		if err != nil {
			return err
		}
		if e.Reason == "" {
			return fmt.Errorf("invalid freeze")
		}
		if a.Frozen {
			return fmt.Errorf("account is already frozen")
		}
		a.Frozen = true
	case "unfreeze":
		a, err := s.admin(e)
		if err != nil {
			return err
		}
		if !a.Frozen {
			return fmt.Errorf("account is not frozen")
		}
		a.Frozen = false
	default:
		return fmt.Errorf("unknown event type %q", e.Type)
	}
	return nil
}
func (s *State) snapshot(e Event) error {
	if e.UpstreamCount < s.UpstreamCount || len(e.UpstreamHash) != 64 || len(e.UpstreamCommit) != 40 || e.Treasury < 0 || e.Treasury > MaxCoins {
		return fmt.Errorf("invalid upstream checkpoint")
	}
	if e.Type == "init" && (len(e.Credits) > 0 || len(e.Payments) > 0) {
		return fmt.Errorf("opening snapshot cannot credit players")
	}
	for _, c := range e.Credits {
		if c.ID == "" || s.Imported[c.ID] || c.UserID <= 0 || c.UserID == s.Config.TreasuryID || c.Amount < 1 || c.Amount > MaxCoins {
			return fmt.Errorf("invalid or duplicate deposit")
		}
		a := s.account(c.UserID, c.Login)
		if a.Available > MaxCoins-(c.Amount-1) {
			return fmt.Errorf("balance overflow")
		}
		a.Available += c.Amount - 1
		s.Fees++
		s.Imported[c.ID] = true
	}
	for _, p := range e.Payments {
		w := s.Withdrawals[p.Withdrawal]
		if p.ID == "" || s.Imported[p.ID] || w == nil || w.Paid || w.Released || w.UpstreamIssue != p.Issue || p.UserID != w.Event.UserID || p.Amount != w.Event.Amount-1 {
			return fmt.Errorf("invalid or duplicate withdrawal payment")
		}
		w.Paid = true
		s.Fees++
		s.Imported[p.ID] = true
	}
	s.UpstreamCommit = e.UpstreamCommit
	s.UpstreamCount = e.UpstreamCount
	s.UpstreamHash = e.UpstreamHash
	s.Treasury = e.Treasury
	return nil
}
