// Adapted from ChrisCoin; copyright (c) 2026 Chris Lewis. See THIRD_PARTY_NOTICES.md.
// Package github is the small REST client used by the trusted Actions worker.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type User struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}
type Issue struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	State       string          `json:"state"`
	HTMLURL     string          `json:"html_url"`
	User        User            `json:"user"`
	CreatedAt   time.Time       `json:"created_at"`
	PullRequest json.RawMessage `json:"pull_request"`
}
type Comment struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	User      User      `json:"user"`
}
type PullRequest struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	HTMLURL string `json:"html_url"`
	User    User   `json:"user"`
	Head    struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

type Client struct {
	Repository, Token, BaseURL string
	HTTP                       *http.Client
	RetryDelay                 time.Duration
}
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub API returned %d: %s", e.Status, e.Message)
}
func IsStatus(err error, status int) bool { e, ok := err.(*APIError); return ok && e.Status == status }

func New(repository, token string) *Client {
	return &Client{Repository: repository, Token: token, BaseURL: "https://api.github.com", HTTP: &http.Client{Timeout: 45 * time.Second}, RetryDelay: 2 * time.Second}
}

func (c *Client) Do(ctx context.Context, method, path string, input, output any) error {
	return c.Global(ctx, method, "/repos/"+c.Repository+path, input, output)
}
func (c *Client) Global(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ChrisCoin-Casino")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(&detail)
		return &APIError{Status: res.StatusCode, Message: detail.Message}
	}
	if output == nil {
		_, err = io.Copy(io.Discard, res.Body)
		return err
	}
	return json.NewDecoder(res.Body).Decode(output)
}

func (c *Client) Issue(ctx context.Context, number int) (Issue, error) {
	var issue Issue
	err := c.Do(ctx, "GET", fmt.Sprintf("/issues/%d", number), nil, &issue)
	return issue, err
}
func (c *Client) Issues(ctx context.Context) ([]Issue, error) {
	all := []Issue{}
	for page := 1; ; page++ {
		var batch []Issue
		if err := c.Do(ctx, "GET", fmt.Sprintf("/issues?state=all&sort=created&direction=asc&per_page=100&page=%d", page), nil, &batch); err != nil {
			return nil, err
		}
		for _, issue := range batch {
			if len(issue.PullRequest) == 0 || string(issue.PullRequest) == "null" {
				all = append(all, issue)
			}
		}
		if len(batch) < 100 {
			return all, nil
		}
	}
}
func (c *Client) Comments(ctx context.Context, number int) ([]Comment, error) {
	all := []Comment{}
	for page := 1; ; page++ {
		var batch []Comment
		if err := c.Do(ctx, "GET", fmt.Sprintf("/issues/%d/comments?per_page=100&page=%d", number, page), nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
}
func (c *Client) Comment(ctx context.Context, number int, body string) error {
	return c.Do(ctx, "POST", fmt.Sprintf("/issues/%d/comments", number), map[string]string{"body": body}, nil)
}
func (c *Client) CloseIssue(ctx context.Context, number int) error {
	return c.Do(ctx, "PATCH", fmt.Sprintf("/issues/%d", number), map[string]string{"state": "closed"}, nil)
}
func (c *Client) Main(ctx context.Context) (string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := c.Do(ctx, "GET", "/git/ref/heads/main", nil, &ref)
	return ref.Object.SHA, err
}
func (c *Client) File(ctx context.Context, path, ref string) ([]byte, error) {
	var file struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	// The Git blob endpoint also handles ledgers exceeding the Contents API's 1 MB inline limit.
	var entry struct {
		SHA      string `json:"sha"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.Do(ctx, "GET", "/contents/"+path+"?ref="+url.QueryEscape(ref), nil, &entry); err != nil {
		return nil, err
	}
	file.Content, file.Encoding = entry.Content, entry.Encoding
	if file.Encoding != "base64" {
		if err := c.Do(ctx, "GET", "/git/blobs/"+entry.SHA, nil, &file); err != nil {
			return nil, err
		}
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
}
func (c *Client) FindPR(ctx context.Context, branch string) (*PullRequest, error) {
	owner := strings.Split(c.Repository, "/")[0]
	var pulls []PullRequest
	err := c.Do(ctx, "GET", "/pulls?state=all&head="+url.QueryEscape(owner+":"+branch)+"&per_page=100", nil, &pulls)
	if err != nil {
		return nil, err
	}
	for _, pr := range pulls {
		if pr.Head.Ref == branch && pr.Head.Repo.FullName == c.Repository {
			return &pr, nil
		}
	}
	return nil, nil
}

// WriteBranch checkpoints only the casino ledger on a deterministic branch.
func (c *Client) WriteBranch(ctx context.Context, base, branch string, events []byte, message string) (string, error) {
	var parent struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.Do(ctx, "GET", "/git/commits/"+base, nil, &parent); err != nil {
		return "", err
	}
	var blob, tree, commit struct {
		SHA string `json:"sha"`
	}
	if err := c.Do(ctx, "POST", "/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString(events), "encoding": "base64"}, &blob); err != nil {
		return "", err
	}
	entries := []map[string]string{{"path": "ledger/events.jsonl", "mode": "100644", "type": "blob", "sha": blob.SHA}}
	if err := c.Do(ctx, "POST", "/git/trees", map[string]any{"base_tree": parent.Tree.SHA, "tree": entries}, &tree); err != nil {
		return "", err
	}
	if err := c.Do(ctx, "POST", "/git/commits", map[string]any{"message": message, "tree": tree.SHA, "parents": []string{base}}, &commit); err != nil {
		return "", err
	}
	err := c.Do(ctx, "PATCH", "/git/refs/heads/"+branch, map[string]any{"sha": commit.SHA, "force": true}, nil)
	if IsStatus(err, 404) || IsStatus(err, 422) {
		err = c.Do(ctx, "POST", "/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": commit.SHA}, nil)
	}
	return commit.SHA, err
}

// CommitFile commits content at path on top of base and fast-forwards main to
// it. A 422 means main moved past base; retry from the new head.
func (c *Client) CommitFile(ctx context.Context, base, path string, content []byte, message string) (string, error) {
	var parent struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.Do(ctx, "GET", "/git/commits/"+base, nil, &parent); err != nil {
		return "", err
	}
	var blob, tree, commit struct {
		SHA string `json:"sha"`
	}
	if err := c.Do(ctx, "POST", "/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64"}, &blob); err != nil {
		return "", err
	}
	entries := []map[string]string{{"path": path, "mode": "100644", "type": "blob", "sha": blob.SHA}}
	if err := c.Do(ctx, "POST", "/git/trees", map[string]any{"base_tree": parent.Tree.SHA, "tree": entries}, &tree); err != nil {
		return "", err
	}
	if err := c.Do(ctx, "POST", "/git/commits", map[string]any{"message": message, "tree": tree.SHA, "parents": []string{base}}, &commit); err != nil {
		return "", err
	}
	return commit.SHA, c.Do(ctx, "PATCH", "/git/refs/heads/main", map[string]any{"sha": commit.SHA, "force": false}, nil)
}
func (c *Client) CreatePR(ctx context.Context, branch, title, body string) (*PullRequest, error) {
	var pr PullRequest
	err := c.Do(ctx, "POST", "/pulls", map[string]string{"head": branch, "base": "main", "title": title, "body": body}, &pr)
	return &pr, err
}
func (c *Client) ClosePR(ctx context.Context, number int) error {
	return c.Do(ctx, "PATCH", fmt.Sprintf("/pulls/%d", number), map[string]string{"state": "closed"}, nil)
}
func (c *Client) Merge(ctx context.Context, number int, sha string) error {
	var last error
	for attempt := 0; attempt < 8; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(c.RetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		// GitHub's PR view can lag a just-updated Git ref. Never relax the SHA guard.
		var current PullRequest
		if err := c.Do(ctx, "GET", fmt.Sprintf("/pulls/%d", number), nil, &current); err != nil {
			return err
		}
		if current.Head.SHA != sha {
			last = fmt.Errorf("waiting for GitHub to observe the expected PR head")
			continue
		}
		var result struct {
			Merged  bool   `json:"merged"`
			Message string `json:"message"`
		}
		err := c.Do(ctx, "PUT", fmt.Sprintf("/pulls/%d/merge", number), map[string]string{"sha": sha, "merge_method": "squash"}, &result)
		if IsStatus(err, 409) || IsStatus(err, 405) {
			last = err
			continue
		}
		if err != nil {
			return err
		}
		if !result.Merged {
			return fmt.Errorf("PR was not merged: %s", result.Message)
		}
		return nil
	}
	return last
}
