// Package game implements the versioned 95% expected return game.
package game

import (
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"math/big"
)

const Version = 1

type Draw struct {
	Gate   int64 `json:"gate"`
	Return int64 `json:"return"`
}

func Payout(stake int64, d Draw) (int64, error) {
	if stake < 1 || stake > math.MaxInt64/2 || d.Gate < 0 || d.Gate >= 100 || d.Return < 0 || d.Return > 2*stake {
		return 0, fmt.Errorf("invalid stake or draw")
	}
	if d.Gate < 5 {
		if d.Return != 0 {
			return 0, fmt.Errorf("bust draw must return zero")
		}
		return 0, nil
	}
	return d.Return, nil
}

func Sample(r io.Reader, stake int64) (Draw, error) {
	// This is an overflow guard for 2*stake+1, not a game wager cap.
	if stake < 1 || stake > math.MaxInt64/2 {
		return Draw{}, fmt.Errorf("stake outside supported range")
	}
	gate, err := rand.Int(r, big.NewInt(100))
	if err != nil {
		return Draw{}, err
	}
	d := Draw{Gate: gate.Int64()}
	if d.Gate >= 5 {
		n, err := rand.Int(r, big.NewInt(2*stake+1))
		if err != nil {
			return Draw{}, err
		}
		d.Return = n.Int64()
	}
	return d, nil
}
