package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLedgerBlobFallbackAndIssuePagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing token")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/test/casino/contents/ledger/events.jsonl":
			fmt.Fprint(w, `{"sha":"blob","encoding":"none"}`)
		case "/repos/test/casino/git/blobs/blob":
			_ = json.NewEncoder(w).Encode(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("ledger"))})
		case "/repos/test/casino/issues":
			if r.URL.Query().Get("state") != "all" {
				t.Error("closed issues excluded from recovery")
			}
			if r.URL.Query().Get("page") == "1" {
				batch := make([]Issue, 100)
				for i := range batch {
					batch[i].Number = i + 1
				}
				_ = json.NewEncoder(w).Encode(batch)
			} else {
				fmt.Fprint(w, `[{"number":101,"pull_request":{"url":"example"}},{"number":102}]`)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New("test/casino", "test-token")
	c.BaseURL = server.URL
	data, err := c.File(context.Background(), "ledger/events.jsonl", "main")
	if err != nil || string(data) != "ledger" {
		t.Fatalf("blob fallback failed: %s %v", data, err)
	}
	issues, err := c.Issues(context.Background())
	if err != nil || len(issues) != 101 || issues[100].Number != 102 {
		t.Fatalf("pagination/PR filtering: %d %v", len(issues), err)
	}
}
func TestWriteBranchOnlyChangesLedger(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET":
			fmt.Fprint(w, `{"tree":{"sha":"base-tree"}}`)
		case strings.HasSuffix(r.URL.Path, "/git/trees"):
			var body struct {
				BaseTree string              `json:"base_tree"`
				Tree     []map[string]string `json:"tree"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Tree) != 1 || body.Tree[0]["path"] != "ledger/events.jsonl" || body.BaseTree != "base-tree" {
				t.Errorf("unexpected tree mutation: %+v", body)
			}
			fmt.Fprint(w, `{"sha":"next-tree"}`)
		case r.Method == "PATCH":
			fmt.Fprint(w, `{}`)
		default:
			fmt.Fprint(w, `{"sha":"next"}`)
		}
	}))
	defer server.Close()
	c := New("test/casino", "token")
	c.BaseURL = server.URL
	sha, err := c.WriteBranch(context.Background(), "base", "casino/event-test", []byte("data"), "event")
	if err != nil || sha != "next" || requests != 5 {
		t.Fatalf("write branch: %s %v requests=%d", sha, err, requests)
	}
}
func TestMergeRequiresExactHead(t *testing.T) {
	put := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PUT" {
			put = true
			fmt.Fprint(w, `{"merged":true}`)
		} else {
			fmt.Fprint(w, `{"head":{"sha":"wrong-head"}}`)
		}
	}))
	defer server.Close()
	c := New("test/casino", "token")
	c.BaseURL = server.URL
	c.RetryDelay = 0
	if err := c.Merge(context.Background(), 1, "expected-head"); err == nil || put {
		t.Fatal("merge did not enforce exact head")
	}
}

func TestReopenIssueUsesExplicitState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/repos/test/casino/issues/7" {
			t.Errorf("unexpected reopen request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["state"] != "open" {
			t.Error("reopen did not set open state")
		}
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	c := New("test/casino", "token")
	c.BaseURL = server.URL
	if err := c.ReopenIssue(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
}
