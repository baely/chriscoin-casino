package github

import (
	"context"
	"fmt"
	"net/url"
)

func (c *Client) CreateIssue(ctx context.Context, title, body string) (Issue, error) {
	var i Issue
	err := c.Do(ctx, "POST", "/issues", map[string]string{"title": title, "body": body}, &i)
	return i, err
}
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	err := c.Global(ctx, "GET", "/user", nil, &u)
	return u, err
}
func (c *Client) User(ctx context.Context, login string) (User, error) {
	var u User
	err := c.Global(ctx, "GET", "/users/"+url.PathEscape(login), nil, &u)
	return u, err
}
func (c *Client) UpdateComment(ctx context.Context, id int64, body string) error {
	return c.Do(ctx, "PATCH", fmt.Sprintf("/issues/comments/%d", id), map[string]string{"body": body}, nil)
}

func (c *Client) ReopenIssue(ctx context.Context, number int) error {
	return c.Do(ctx, "PATCH", fmt.Sprintf("/issues/%d", number), map[string]string{"state": "open"}, nil)
}
