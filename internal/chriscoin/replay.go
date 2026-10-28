// Package chriscoin replays the upstream v1 ledger.
// Adapted from cdlewis/ChrisCoin at fc9a755a24bfedd28dd018a35f958d3be3a62aca.
// Copyright (c) 2026 Chris Lewis. See THIRD_PARTY_NOTICES.md.
package chriscoin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const Allowance int64 = 100
const MaxCoins int64 = 9007199254740991 // Exact in browser JavaScript, too.
// MintAccountID is cdlewis's immutable GitHub ID. Its accepted offers issue new
// coins instead of drawing from an available or reserved balance.
const MintAccountID int64 = 553949

type Actor struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type Command struct {
	Action    string `json:"action"`
	Recipient string `json:"recipient,omitempty"`
	Amount    int64  `json:"amount,omitempty"`
	OfferID   string `json:"offerId,omitempty"`
	Payer     string `json:"payer,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type Source struct {
	Issue      int       `json:"issue"`
	URL        string    `json:"url"`
	BodySHA256 string    `json:"bodySha256"`
	CreatedAt  time.Time `json:"createdAt"`
	Command    Command   `json:"command"`
}

type Event struct {
	Version     int       `json:"version"`
	Sequence    int       `json:"sequence"`
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Actor       Actor     `json:"actor"`
	Source      Source    `json:"source"`
	Timestamp   time.Time `json:"timestamp"`
	PullRequest int       `json:"pullRequest,omitempty"`
	RecipientID int64     `json:"recipientId,omitempty"`
}

type Account struct {
	Actor
	Available int64 `json:"available"`
	Reserved  int64 `json:"reserved"`
}

type Offer struct {
	ID             string `json:"id"`
	Sender         int64  `json:"sender"`
	RecipientLabel string `json:"recipientLabel"`
	Amount         int64  `json:"amount"`
	Status         string `json:"status"`
	AcceptedBy     int64  `json:"acceptedBy,omitempty"`
	Event          Event  `json:"event"`
}

type State struct {
	Accounts map[int64]*Account         `json:"accounts"`
	Offers   map[string]*Offer          `json:"offers"`
	Requests map[string]*PaymentRequest `json:"requests"`
	Events   []Event                    `json:"events"`
	Issued   int64                      `json:"issued"`
}

type PaymentRequest struct {
	ID         string `json:"id"`
	Requester  int64  `json:"requester"`
	PayerLabel string `json:"payerLabel"`
	Amount     int64  `json:"amount"`
	Status     string `json:"status"`
	PaidBy     int64  `json:"paidBy,omitempty"`
	Event      Event  `json:"event"`
}

var offerPattern = regexp.MustCompile(`^offer-[1-9][0-9]*$`)
var paymentRequestPattern = regexp.MustCompile(`^request-[1-9][0-9]*$`)
var loginPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,38}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var urlPattern = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/issues/[1-9][0-9]*$`)

func DecodeStrict(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// UnmarshalJSON also rejects irrelevant fields, including a forged sender.
func (c *Command) UnmarshalJSON(data []byte) error {
	type plain Command
	var p plain
	if err := DecodeStrict(data, &p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{"action": true}
	switch p.Action {
	case "offer":
		allowed["recipient"], allowed["amount"] = true, true
	case "accept", "cancel":
		allowed["offerId"] = true
	case "request":
		allowed["payer"], allowed["amount"] = true, true
	case "fulfill", "cancel-request":
		allowed["requestId"] = true
	case "claim":
	default:
		return errors.New("unknown action")
	}
	for field := range fields {
		if !allowed[field] {
			return errors.New("unexpected command field; use only the documented fields")
		}
	}
	*c = Command(p)
	return c.validateStructure()
}

func (c Command) validateStructure() error {
	switch c.Action {
	case "claim":
		if c.Amount != 0 || c.Recipient != "" || c.OfferID != "" || c.Payer != "" || c.RequestID != "" {
			return errors.New("claim takes no additional fields")
		}
	case "offer", "request":
		if c.Amount <= 0 || c.Amount > MaxCoins {
			return errors.New("amount must be a positive whole number within the safe integer range")
		}
		name := c.Recipient
		if c.Action == "request" {
			name = c.Payer
		}
		if strings.TrimSpace(name) == "" || len([]rune(name)) > 100 {
			return errors.New("invalid recipient or payer name")
		}
		for _, r := range name {
			if r < 32 || r == 127 {
				return errors.New("recipient label cannot contain control characters")
			}
		}
		if c.OfferID != "" || c.RequestID != "" || (c.Action == "offer" && c.Payer != "") || (c.Action == "request" && c.Recipient != "") {
			return errors.New("unexpected fields for offer or request")
		}
	case "accept", "cancel":
		if !offerPattern.MatchString(c.OfferID) {
			return errors.New("invalid offer ID")
		}
		if c.Amount != 0 || c.Recipient != "" || c.Payer != "" || c.RequestID != "" {
			return errors.New("accept and cancel only take an offer ID")
		}
	case "fulfill", "cancel-request":
		if !paymentRequestPattern.MatchString(c.RequestID) {
			return errors.New("invalid payment request ID")
		}
		if c.Amount != 0 || c.Recipient != "" || c.Payer != "" || c.OfferID != "" {
			return errors.New("payment and request cancellation only take a request ID")
		}
	default:
		return errors.New("unknown action")
	}
	return nil
}

func Parse(data []byte) ([]Event, error) {
	events := []Event{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var event Event
		if err := DecodeStrict(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("ledger line %d: %w", len(events)+1, err)
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}

func Serialize(events []Event) []byte {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	for _, event := range events {
		_ = encoder.Encode(event)
	}
	return out.Bytes()
}

func Replay(events []Event) (*State, error) {
	state := &State{Accounts: map[int64]*Account{}, Offers: map[string]*Offer{}, Requests: map[string]*PaymentRequest{}, Events: []Event{}}
	seen := map[string]bool{}
	for _, e := range events {
		if e.Version != 1 || e.Sequence != len(state.Events)+1 {
			return nil, errors.New("invalid ledger version or sequence")
		}
		if e.Actor.ID <= 0 || !loginPattern.MatchString(e.Actor.Login) {
			return nil, errors.New("invalid GitHub account")
		}
		if e.Source.Issue <= 0 || e.ID != fmt.Sprintf("issue-%d", e.Source.Issue) || seen[e.ID] {
			return nil, errors.New("duplicate or invalid request ID")
		}
		if !urlPattern.MatchString(e.Source.URL) || !hashPattern.MatchString(e.Source.BodySHA256) || e.Source.CreatedAt.IsZero() || e.Timestamp.IsZero() || e.PullRequest < 0 {
			return nil, errors.New("invalid event provenance")
		}
		c := e.Source.Command
		if err := c.validateStructure(); err != nil {
			return nil, err
		}
		kind := c.Action
		if kind == "claim" {
			kind = "allowance"
		}
		if e.Type == "send" && c.Action == "offer" {
			target := state.Accounts[e.RecipientID]
			if target == nil || !strings.EqualFold(target.Login, c.Recipient) {
				return nil, errors.New("direct payment recipient must match an existing account")
			}
			kind = "send"
		} else if e.RecipientID != 0 {
			return nil, errors.New("unexpected payment recipient ID")
		}
		if e.Type != kind {
			return nil, errors.New("event type does not match its command")
		}
		account := state.Accounts[e.Actor.ID]
		firstAppearance := account == nil
		if firstAppearance {
			if state.Issued > MaxCoins-Allowance {
				return nil, errors.New("maximum coin supply reached")
			}
			account = &Account{Actor: e.Actor, Available: Allowance}
			state.Accounts[e.Actor.ID] = account
			state.Issued += Allowance
		}
		account.Login = e.Actor.Login
		switch c.Action {
		case "claim":
			// This is just an onboarding entry. Every first valid action already
			// receives the starting balance; account presence is the only check.
			if !firstAppearance {
				return nil, errors.New("this GitHub ID already has an account and its starting balance")
			}
		case "offer":
			if e.Type == "send" {
				if account.ID == MintAccountID {
					if state.Issued > MaxCoins-c.Amount {
						return nil, errors.New("maximum coin supply reached")
					}
					state.Issued += c.Amount
				} else {
					if account.Available < c.Amount {
						return nil, errors.New("not enough available ChrisCoin")
					}
					account.Available -= c.Amount
				}
				state.Accounts[e.RecipientID].Available += c.Amount
				id := fmt.Sprintf("offer-%d", e.Source.Issue)
				state.Offers[id] = &Offer{ID: id, Sender: account.ID, RecipientLabel: c.Recipient, Amount: c.Amount, Status: "accepted", AcceptedBy: e.RecipientID, Event: e}
				break
			}
			if account.ID != MintAccountID {
				if account.Available < c.Amount {
					return nil, errors.New("not enough available ChrisCoin")
				}
				account.Available -= c.Amount
				account.Reserved += c.Amount
			}
			id := fmt.Sprintf("offer-%d", e.Source.Issue)
			state.Offers[id] = &Offer{ID: id, Sender: account.ID, RecipientLabel: c.Recipient, Amount: c.Amount, Status: "open", Event: e}
		case "request":
			id := fmt.Sprintf("request-%d", e.Source.Issue)
			state.Requests[id] = &PaymentRequest{ID: id, Requester: account.ID, PayerLabel: c.Payer, Amount: c.Amount, Status: "open", Event: e}
		case "fulfill", "cancel-request":
			request := state.Requests[c.RequestID]
			if request == nil {
				return nil, errors.New("that payment request does not exist")
			}
			if request.Status != "open" {
				return nil, fmt.Errorf("that payment request has already been %s", request.Status)
			}
			if c.Action == "cancel-request" {
				if account.ID != request.Requester {
					return nil, errors.New("only the requester can cancel this payment request")
				}
				request.Status = "cancelled"
			} else {
				if account.ID == MintAccountID {
					if state.Issued > MaxCoins-request.Amount {
						return nil, errors.New("maximum coin supply reached")
					}
					state.Issued += request.Amount
				} else {
					if account.Available < request.Amount {
						return nil, errors.New("not enough available ChrisCoin")
					}
					account.Available -= request.Amount
				}
				state.Accounts[request.Requester].Available += request.Amount
				request.Status = "paid"
				request.PaidBy = account.ID
			}
		case "accept", "cancel":
			offer := state.Offers[c.OfferID]
			if offer == nil {
				return nil, errors.New("that offer does not exist")
			}
			if offer.Status != "open" {
				return nil, fmt.Errorf("that offer has already been %s", offer.Status)
			}
			sender := state.Accounts[offer.Sender]
			if c.Action == "cancel" {
				if sender.ID != account.ID {
					return nil, errors.New("only the sender can cancel this offer")
				}
				offer.Status = "cancelled"
				if sender.ID != MintAccountID {
					sender.Available += offer.Amount
				}
			} else {
				if sender.ID == MintAccountID {
					if state.Issued > MaxCoins-offer.Amount {
						return nil, errors.New("maximum coin supply reached")
					}
					state.Issued += offer.Amount
				}
				offer.Status = "accepted"
				offer.AcceptedBy = account.ID
				account.Available += offer.Amount
			}
			if sender.ID != MintAccountID {
				sender.Reserved -= offer.Amount
			}
		}
		seen[e.ID] = true
		state.Events = append(state.Events, e)
	}
	var sum int64
	for _, account := range state.Accounts {
		sum += account.Available + account.Reserved
	}
	if sum != state.Issued {
		return nil, errors.New("coin conservation check failed")
	}
	return state, nil
}
