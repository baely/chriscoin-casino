package game

import (
	"bytes"
	"fmt"
	"testing"
)

func TestRouletteReturnsThirtySixThirtySevenths(t *testing.T) {
	bets := []string{"red", "black", "odd", "even", "low", "high", "1-12", "13-24", "25-36"}
	for n := 0; n <= 36; n++ {
		bets = append(bets, fmt.Sprint(n))
	}
	for _, bet := range bets {
		var total int64
		for pocket := int64(0); pocket <= 36; pocket++ {
			p, err := Settle(Roulette, bet, 10, Draw{Pocket: pocket})
			if err != nil {
				t.Fatal(err)
			}
			total += p
		}
		max, _ := MaxPayout(Roulette, bet, 10)
		if total != 360 || max < 20 {
			t.Fatalf("%s: total %d over 37 pockets, max %d", bet, total, max)
		}
	}
}
func TestSlotsExactReturn(t *testing.T) {
	var total int64
	n := int64(len(SlotStrip))
	for a := int64(0); a < n; a++ {
		for b := int64(0); b < n; b++ {
			for c := int64(0); c < n; c++ {
				p, err := Settle(Slots, "", 10, Draw{Reels: []int64{a, b, c}})
				if err != nil {
					t.Fatal(err)
				}
				total += p
			}
		}
	}
	if n != 20 || total != 7597*10 {
		t.Fatalf("slots returned %d over %d combinations", total, n*n*n)
	}
}
func TestNamedGameBetsAndDraws(t *testing.T) {
	for in, want := range map[string]string{"RED": "red", "1-18": "low", "19-36": "high", "0": "0", "36": "36"} {
		if got, err := NormalizeBet(Roulette, in); err != nil || got != want {
			t.Fatalf("%q normalized to %q %v", in, got, err)
		}
	}
	for _, bet := range []string{"", "37", "00", "07", "green", "-1"} {
		if _, err := NormalizeBet(Roulette, bet); err == nil {
			t.Fatalf("accepted roulette bet %q", bet)
		}
	}
	if _, err := NormalizeBet(Slots, "red"); err == nil {
		t.Fatal("slots accepted a bet")
	}
	for _, d := range []Draw{{Pocket: 37}, {Pocket: -1}, {Pocket: 1, Gate: 1}, {Reels: []int64{1, 2, 3}}} {
		if _, err := Settle(Roulette, "red", 10, d); err == nil {
			t.Fatalf("invalid roulette draw accepted: %+v", d)
		}
	}
	for _, d := range []Draw{{Reels: []int64{1, 2}}, {Reels: []int64{1, 2, 20}}, {Reels: []int64{1, 2, 3}, Pocket: 1}} {
		if _, err := Settle(Slots, "", 10, d); err == nil {
			t.Fatalf("invalid slots draw accepted: %+v", d)
		}
	}
	if _, err := Settle("", "", 10, Draw{Gate: 50, Return: 3, Pocket: 1}); err == nil {
		t.Fatal("original game accepted a roulette draw")
	}
	if max, _ := MaxPayout(Slots, "", 10); max != 500 {
		t.Fatalf("slots max %d", max)
	}
	d, err := Roll(bytes.NewReader(make([]byte, 32)), Roulette, "0", 1)
	if p, _ := Settle(Roulette, "0", 1, d); err != nil || p != 36 {
		t.Fatalf("roulette roll: %+v %v", d, err)
	}
	d, err = Roll(bytes.NewReader(make([]byte, 32)), Slots, "", 1)
	if reels, _ := Reels(d); err != nil || len(reels) != 3 || reels[0] != Cherry {
		t.Fatalf("slots roll: %+v %v", d, err)
	}
	if _, err := Roll(bytes.NewReader(nil), Slots, "", 1); err == nil {
		t.Fatal("RNG failure ignored")
	}
}
