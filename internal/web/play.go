package web

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"chriscoin-casino/internal/game"
)

// The games, by their names in the API. "lucky" is the original /wager game.
var games = map[string]string{"roulette": game.Roulette, "slots": game.Slots, "lucky": ""}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,40}$`)

// Draw is what the page animates.
type Draw struct {
	Pocket  *int64   `json:"pocket,omitempty"`
	Color   string   `json:"color,omitempty"`
	Stops   []int64  `json:"stops,omitempty"`
	Symbols []string `json:"symbols,omitempty"`
	Gate    *int64   `json:"gate,omitempty"`
}

func catalogue() map[string]any {
	red := []int64{}
	for p := int64(1); p <= 36; p++ {
		if game.PocketColor(p) == "🔴" {
			red = append(red, p)
		}
	}
	return map[string]any{
		"slotStrip":  game.SlotStrip,
		"slotPays":   map[string]int64{game.Seven: 50, game.Star: 25, game.Bell: 12, game.Lemon: 8, game.Cherry: 3},
		"redPockets": red,
	}
}

func colorName(pocket int64) string {
	switch game.PocketColor(pocket) {
	case "🔴":
		return "red"
	case "⚫":
		return "black"
	}
	return "green"
}

// play draws a game and has the bank settle it before replying.
func (s *Server) play(w http.ResponseWriter, r *http.Request, sess *session) {
	var in struct {
		Game   string `json:"game"`
		Bet    string `json:"bet"`
		Stake  int64  `json:"stake"`
		Bucket string `json:"bucket"`
		Key    string `json:"key"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That request wasn't understood.")
		return
	}
	name, ok := games[in.Game]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_game", "Pick roulette, slots or lucky.")
		return
	}
	if !keyPattern.MatchString(in.Key) {
		writeError(w, http.StatusBadRequest, "bad_key", "Missing bet key.")
		return
	}
	bet, err := game.NormalizeBet(name, in.Bet)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_bet", err.Error())
		return
	}
	if in.Stake < 1 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_amount", "Bet a whole number of coins, 1 or more.")
		return
	}
	maxPayout, err := game.MaxPayout(name, bet, in.Stake)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_amount", "That bet is too large.")
		return
	}
	d, err := game.Roll(rand.Reader, name, bet, in.Stake)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Couldn't draw the game. Nothing was bet.")
		return
	}
	payout, err := game.Settle(name, bet, in.Stake, d)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Couldn't settle the game. Nothing was bet.")
		return
	}
	draw, memo := describe(in.Game, bet, d)
	res, err := s.cfg.Bank.Wager(r.Context(), Wager{Player: sess.ID, Bucket: in.Bucket, Stake: in.Stake, Payout: payout,
		MaxPayout: maxPayout, Memo: memo, Key: fmt.Sprintf("%d-%s", sess.ID, in.Key)})
	if err != nil {
		s.bankFailed(w, err)
		return
	}
	out := map[string]any{"game": in.Game, "bet": bet, "stake": res.Transaction.Stake, "payout": res.Transaction.Payout,
		"memo": res.Transaction.Memo, "account": res.Account, "house": res.House, "duplicate": res.Duplicate}
	if !res.Duplicate {
		// A retry reports the original settlement; this fresh draw never counted.
		out["draw"] = draw
	}
	s.cfg.Log.Info("bet", "player", sess.Login, "game", in.Game, "bet", bet, "stake", in.Stake, "payout", res.Transaction.Payout, "duplicate", res.Duplicate)
	writeJSON(w, 200, out)
}

func describe(name, bet string, d game.Draw) (Draw, string) {
	switch name {
	case "roulette":
		p := d.Pocket
		c := colorName(p)
		return Draw{Pocket: &p, Color: c}, fmt.Sprintf("Roulette · %s · landed %d %s", bet, p, c)
	case "slots":
		symbols, _ := game.Reels(d)
		return Draw{Stops: d.Reels, Symbols: symbols}, "Slots · " + strings.Join(symbols, "")
	}
	g := d.Gate
	return Draw{Gate: &g}, "Lucky Dip"
}
