package web

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeBank is the bank's app API with one player and the house.
type fakeBank struct {
	mu      sync.Mutex
	player  Account
	house   House
	wagers  []Wager
	seenKey map[string]WagerReply
}

func (f *fakeBank) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer secret" {
		w.WriteHeader(401)
		return
	}
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	switch {
	case r.Method == "POST" && r.URL.Path == "/api/app/auth":
		var in struct{ Code string }
		json.NewDecoder(r.Body).Decode(&in)
		if in.Code != "good-code" {
			w.WriteHeader(401)
			reply(map[string]any{"error": map[string]string{"code": "invalid_code", "message": "That sign-in code is invalid or expired."}})
			return
		}
		reply(map[string]any{"id": f.player.ID, "login": f.player.Login})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/app/players/"):
		reply(playerReply{f.player, f.house})
	case r.Method == "POST" && r.URL.Path == "/api/app/wagers":
		var in Wager
		json.NewDecoder(r.Body).Decode(&in)
		if old, ok := f.seenKey[in.Key]; ok {
			old.Duplicate = true
			reply(old)
			return
		}
		if in.Stake > f.player.Buckets[0].Balance {
			w.WriteHeader(422)
			reply(map[string]any{"error": map[string]string{"code": "insufficient_funds", "message": "Not enough available balance."}})
			return
		}
		f.wagers = append(f.wagers, in)
		f.player.Buckets[0].Balance += in.Payout - in.Stake
		f.house.Balance += in.Stake - in.Payout
		out := WagerReply{Transaction: Tx{Stake: in.Stake, Payout: in.Payout, Memo: in.Memo}, Account: f.player, House: f.house}
		f.seenKey[in.Key] = out
		reply(out)
	default:
		w.WriteHeader(404)
	}
}

func setup(t *testing.T) (*fakeBank, *http.Client, string, string) {
	t.Helper()
	fb := &fakeBank{player: Account{ID: 7, Login: "alice", Buckets: []Bucket{{"Main", 100}}}, house: House{Login: "baely", Bucket: "Casino", Balance: 100000}, seenKey: map[string]WagerReply{}}
	bank := httptest.NewServer(fb)
	t.Cleanup(bank.Close)
	ts := httptest.NewServer(New(Config{BankURL: "https://bank.example", Bank: &Bank{URL: bank.URL, Token: "secret"}}).Handler())
	t.Cleanup(ts.Close)
	c := signIn(t, ts.URL, "good-code")
	res, _ := c.Get(ts.URL + "/api/me")
	var me struct {
		ID   int64
		CSRF string
	}
	json.NewDecoder(res.Body).Decode(&me)
	res.Body.Close()
	if me.ID != 7 {
		t.Fatalf("signed in as %d, want the bank's ID 7", me.ID)
	}
	return fb, c, ts.URL, me.CSRF
}

// signIn goes to the bank and comes back with code, as the bank would send it.
func signIn(t *testing.T, base, code string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(base + "/auth/start")
	if err != nil {
		t.Fatal(err)
	}
	to, _ := url.Parse(res.Header.Get("Location"))
	if to.Host != "bank.example" || to.Path != "/auth/app" {
		t.Fatalf("sign-in went to %s, not the bank", to)
	}
	q := url.Values{"code": {code}, "state": {to.Query().Get("state")}}
	res, _ = c.Get(base + "/auth/callback?" + q.Encode())
	res.Body.Close()
	return c
}

func TestSignInNeedsTheBanksCodeAndOurState(t *testing.T) {
	_, _, base, _ := setup(t)
	for _, code := range []string{"forged", ""} {
		res, _ := signIn(t, base, code).Get(base + "/api/me")
		if res.StatusCode != 401 {
			t.Errorf("code %q signed in: %d", code, res.StatusCode)
		}
	}
	// A code delivered without our state cookie (someone else's sign-in link) is refused.
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	c.Get(base + "/auth/callback?code=good-code&state=anything-at-all-123")
	if res, _ := c.Get(base + "/api/me"); res.StatusCode != 401 {
		t.Errorf("sign-in without state = %d", res.StatusCode)
	}
}

func post(t *testing.T, c *http.Client, base, csrf string, body map[string]any, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+"/api/play", strings.NewReader(string(b)))
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestEveryGameSettlesThroughTheBank(t *testing.T) {
	fb, c, base, csrf := setup(t)
	for i, g := range []map[string]any{
		{"game": "roulette", "bet": "red", "stake": 2},
		{"game": "roulette", "bet": "17", "stake": 1},
		{"game": "slots", "stake": 1},
		{"game": "lucky", "stake": 3},
	} {
		g["key"] = "key-000000" + string(rune('a'+i))
		var out struct {
			Stake, Payout int64
			Draw          *Draw
			Memo          string
		}
		if code := post(t, c, base, csrf, g, &out); code != 200 || out.Draw == nil || out.Stake != int64(g["stake"].(int)) {
			t.Fatalf("%v = %d %+v", g, code, out)
		}
		w := fb.wagers[len(fb.wagers)-1]
		if w.Player != 7 || w.Payout != out.Payout || w.Payout > w.MaxPayout || !strings.HasPrefix(w.Key, "7-") {
			t.Fatalf("wager sent to the bank = %+v", w)
		}
	}
	if m := fb.wagers[0].MaxPayout; m != 4 {
		t.Fatalf("red pays at most 2x: %d", m)
	}
	if m := fb.wagers[1].MaxPayout; m != 36 {
		t.Fatalf("a straight-up number pays at most 36x: %d", m)
	}
}

func TestRetryReturnsTheOriginalBet(t *testing.T) {
	fb, c, base, csrf := setup(t)
	bet := map[string]any{"game": "slots", "stake": 5, "key": "same-key-123"}
	var first, again struct {
		Payout    int64
		Duplicate bool
		Draw      *Draw
	}
	post(t, c, base, csrf, bet, &first)
	post(t, c, base, csrf, bet, &again)
	if !again.Duplicate || again.Draw != nil || again.Payout != first.Payout || len(fb.wagers) != 1 {
		t.Fatalf("retry = %+v (first %+v), %d wagers", again, first, len(fb.wagers))
	}
}

func TestRefusals(t *testing.T) {
	_, c, base, csrf := setup(t)
	for _, tc := range []struct {
		body map[string]any
		code int
	}{
		{map[string]any{"game": "poker", "stake": 1, "key": "k-00000001"}, 400},
		{map[string]any{"game": "roulette", "bet": "purple", "stake": 1, "key": "k-00000002"}, 422},
		{map[string]any{"game": "slots", "stake": 0, "key": "k-00000003"}, 422},
		{map[string]any{"game": "slots", "stake": 1, "key": "x"}, 400},
		{map[string]any{"game": "slots", "stake": 1000, "key": "k-00000004"}, 422}, // the bank refuses
	} {
		if code := post(t, c, base, csrf, tc.body, nil); code != tc.code {
			t.Errorf("%v = %d, want %d", tc.body, code, tc.code)
		}
	}
	if code := post(t, c, base, "wrong", map[string]any{"game": "slots", "stake": 1, "key": "k-00000005"}, nil); code != 403 {
		t.Errorf("bad csrf = %d", code)
	}
}
