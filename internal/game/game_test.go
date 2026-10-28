package game

import (
	"bytes"
	"math"
	"testing"
)

func TestExactExpectedReturn(t *testing.T) {
	for _, stake := range []int64{1, 2, 10, 100} {
		var total int64
		for gate := int64(0); gate < 100; gate++ {
			for n := int64(0); n <= 2*stake; n++ {
				d := Draw{Gate: gate, Return: n}
				if gate < 5 {
					d.Return = 0
				}
				p, err := Payout(stake, d)
				if err != nil {
					t.Fatal(err)
				}
				total += p
			}
		}
		if total != 95*stake*(2*stake+1) {
			t.Fatalf("wrong expectation for stake %d", stake)
		}
	}
}
func TestDrawAndInvalidBounds(t *testing.T) {
	d, err := Sample(bytes.NewReader(make([]byte, 32)), 10)
	if err != nil || d.Gate != 0 || d.Return != 0 {
		t.Fatalf("bust draw: %+v %v", d, err)
	}
	d, err = Sample(bytes.NewReader([]byte{99, 20}), 10)
	if err != nil || d.Return != 20 {
		t.Fatalf("max payout: %+v %v", d, err)
	}
	if _, err := Sample(bytes.NewReader(nil), 10); err == nil {
		t.Fatal("RNG failure ignored")
	}
	for _, d := range []Draw{{Gate: -1}, {Gate: 100}, {Gate: 0, Return: 1}, {Gate: 50, Return: 21}} {
		if _, err := Payout(10, d); err == nil {
			t.Fatalf("invalid draw accepted: %+v", d)
		}
	}
	if _, err := Sample(bytes.NewReader(nil), math.MaxInt64/2+1); err == nil {
		t.Fatal("stake overflow accepted")
	}
}

func TestLargeStakesHaveNoPolicyCap(t *testing.T) {
	for _, stake := range []int64{1_000_000_001, 4_503_599_627_370_495, math.MaxInt64 / 2} {
		d, err := Sample(bytes.NewReader([]byte{0}), stake)
		if err != nil || d.Return != 0 {
			t.Fatalf("large supported stake %d: %+v %v", stake, d, err)
		}
		payout, err := Payout(stake, Draw{Gate: 99, Return: 2 * stake})
		if err != nil || payout != 2*stake {
			t.Fatalf("large payout for %d: %d %v", stake, payout, err)
		}
	}
	if _, err := Payout(math.MaxInt64/2+1, Draw{Gate: 99}); err == nil {
		t.Fatal("overflow was accepted")
	}
}
