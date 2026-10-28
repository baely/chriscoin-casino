package game

import (
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Named games beside the original /wager game, which has the empty name. Each
// uses fixed whole-number multipliers of the stake, so payouts stay exact.
const (
	Roulette = "roulette"
	Slots    = "slots"
)

// VersionOf returns the current rules version for a game, or 0 if unknown.
func VersionOf(name string) int {
	switch name {
	case "", Roulette, Slots:
		return 1
	}
	return 0
}

// European single-zero roulette: every bet returns 36/37 of the stake.
var redPockets = map[int64]bool{1: true, 3: true, 5: true, 7: true, 9: true, 12: true, 14: true, 16: true, 18: true, 19: true, 21: true, 23: true, 25: true, 27: true, 30: true, 32: true, 34: true, 36: true}
var straightBet = regexp.MustCompile(`^(0|[1-9]|[12][0-9]|3[0-6])$`)
var rouletteAliases = map[string]string{"1-18": "low", "19-36": "high"}

// Slots: three reels over one 20-stop strip. Three of a kind pays the table;
// exactly two cherries anywhere pay 2x. The exact return is 7597/8000.
const (
	Seven  = "7️⃣"
	Star   = "⭐"
	Bell   = "🔔"
	Lemon  = "🍋"
	Cherry = "🍒"
)

var SlotStrip = []string{Cherry, Lemon, Bell, Cherry, Star, Lemon, Cherry, Bell, Lemon, Seven, Cherry, Lemon, Bell, Cherry, Star, Lemon, Cherry, Bell, Lemon, Cherry}
var slotTriples = map[string]int64{Seven: 50, Star: 25, Bell: 12, Lemon: 8, Cherry: 3}

const slotPair = 2

// NormalizeBet returns the canonical ledger form of a bet.
func NormalizeBet(name, bet string) (string, error) {
	bet = strings.ToLower(strings.TrimSpace(bet))
	switch name {
	case "":
		if bet != "" {
			return "", fmt.Errorf("/wager takes no bet")
		}
		return "", nil
	case Slots:
		if bet != "" {
			return "", fmt.Errorf("/slots takes only an amount")
		}
		return "", nil
	case Roulette:
		if alias, ok := rouletteAliases[bet]; ok {
			bet = alias
		}
		if _, err := rouletteMultiplier(bet); err != nil {
			return "", err
		}
		return bet, nil
	}
	return "", fmt.Errorf("unknown game")
}
func rouletteMultiplier(bet string) (int64, error) {
	switch bet {
	case "red", "black", "odd", "even", "low", "high":
		return 2, nil
	case "1-12", "13-24", "25-36":
		return 3, nil
	}
	if straightBet.MatchString(bet) {
		return 36, nil
	}
	return 0, fmt.Errorf("bet on red, black, odd, even, low, high, 1-12, 13-24, 25-36, or a number from 0 to 36")
}
func rouletteWins(bet string, pocket int64) bool {
	switch bet {
	case "red":
		return redPockets[pocket]
	case "black":
		return pocket != 0 && !redPockets[pocket]
	case "odd":
		return pocket%2 == 1
	case "even":
		return pocket != 0 && pocket%2 == 0
	case "low":
		return pocket >= 1 && pocket <= 18
	case "high":
		return pocket >= 19
	case "1-12", "13-24", "25-36":
		low := map[string]int64{"1-12": 1, "13-24": 13, "25-36": 25}[bet]
		return pocket >= low && pocket < low+12
	}
	n, err := strconv.ParseInt(bet, 10, 64)
	return err == nil && n == pocket
}

// PocketColor is the display emoji for a roulette pocket.
func PocketColor(pocket int64) string {
	switch {
	case pocket == 0:
		return "🟢"
	case redPockets[pocket]:
		return "🔴"
	}
	return "⚫"
}
func multiplier(name, bet string) (int64, error) {
	if _, err := NormalizeBet(name, bet); err != nil {
		return 0, err
	}
	switch name {
	case Roulette:
		return rouletteMultiplier(bet)
	case Slots:
		return slotTriples[Seven], nil
	}
	return 2, nil
}

// MaxPayout is the largest possible return, including the stake, which the
// house must reserve before the draw.
func MaxPayout(name, bet string, stake int64) (int64, error) {
	m, err := multiplier(name, bet)
	if err != nil {
		return 0, err
	}
	if stake < 1 || stake > math.MaxInt64/m {
		return 0, fmt.Errorf("stake outside supported range")
	}
	return stake * m, nil
}

// Settle validates a draw for a game and returns its payout.
func Settle(name, bet string, stake int64, d Draw) (int64, error) {
	if _, err := MaxPayout(name, bet, stake); err != nil {
		return 0, err
	}
	switch name {
	case "":
		if d.Pocket != 0 || d.Reels != nil {
			return 0, fmt.Errorf("invalid draw")
		}
		return Payout(stake, d)
	case Roulette:
		if d.Gate != 0 || d.Return != 0 || d.Reels != nil || d.Pocket < 0 || d.Pocket > 36 {
			return 0, fmt.Errorf("invalid draw")
		}
		if !rouletteWins(bet, d.Pocket) {
			return 0, nil
		}
		m, _ := rouletteMultiplier(bet)
		return stake * m, nil
	}
	symbols, err := Reels(d)
	if err != nil {
		return 0, err
	}
	if symbols[0] == symbols[1] && symbols[1] == symbols[2] {
		return stake * slotTriples[symbols[0]], nil
	}
	cherries := 0
	for _, s := range symbols {
		if s == Cherry {
			cherries++
		}
	}
	if cherries == 2 {
		return stake * slotPair, nil
	}
	return 0, nil
}

// Reels returns the symbols of a slots draw.
func Reels(d Draw) ([]string, error) {
	if d.Gate != 0 || d.Return != 0 || d.Pocket != 0 || len(d.Reels) != 3 {
		return nil, fmt.Errorf("invalid draw")
	}
	out := []string{}
	for _, stop := range d.Reels {
		if stop < 0 || stop >= int64(len(SlotStrip)) {
			return nil, fmt.Errorf("invalid draw")
		}
		out = append(out, SlotStrip[stop])
	}
	return out, nil
}

// Roll samples a draw for any game.
func Roll(r io.Reader, name, bet string, stake int64) (Draw, error) {
	if _, err := MaxPayout(name, bet, stake); err != nil {
		return Draw{}, err
	}
	switch name {
	case "":
		return Sample(r, stake)
	case Roulette:
		n, err := rand.Int(r, big.NewInt(37))
		if err != nil {
			return Draw{}, err
		}
		return Draw{Pocket: n.Int64()}, nil
	}
	d := Draw{Reels: []int64{}}
	for range 3 {
		n, err := rand.Int(r, big.NewInt(int64(len(SlotStrip))))
		if err != nil {
			return Draw{}, err
		}
		d.Reels = append(d.Reels, n.Int64())
	}
	return d, nil
}
