package chriscoin

import (
	"strings"
	"testing"
)

func TestUpstreamRequestFormatting(t *testing.T) {
	cases := []struct {
		command Command
		title   string
	}{
		{Command{Action: "request", Payer: "alice", Amount: 20}, "ChrisCoin: request 20 from alice"},
		{Command{Action: "offer", Recipient: "alice", Amount: 19}, "ChrisCoin: offer 19 to alice"},
		{Command{Action: "fulfill", RequestID: "request-101"}, "ChrisCoin: fulfill"},
	}
	for _, tt := range cases {
		if got := RequestTitle(tt.command); got != tt.title {
			t.Fatalf("wrong title: %s", got)
		}
		marker := RequestMarker("test/casino", "deposit", "comment-123")
		body := RequestBody(tt.command, marker)
		if strings.Contains(strings.ToLower(body), "casino") || strings.Contains(body, "comment-123") {
			t.Fatal("request exposes originating application")
		}
		if !strings.HasPrefix(body, "```chriscoin\n{\n  \"action\": ") || !strings.HasSuffix(body, "\n```\n\n"+marker+"\n") {
			t.Fatal("body does not match upstream format")
		}
		if len(marker) != len("<!-- request-ref: -->")+64 {
			t.Fatal("reference is not an opaque SHA256")
		}
	}
	expected := "```chriscoin\n{\n  \"action\": \"fulfill\",\n  \"requestId\": \"request-101\"\n}\n```\n"
	if got := RequestBody(Command{Action: "fulfill", RequestID: "request-101"}, ""); got != expected {
		t.Fatalf("unexpected Pay link body: %q", got)
	}
}
func TestWithdrawalRecoveryAcceptsOldAndOpaqueReferences(t *testing.T) {
	for _, body := range []string{WithdrawalBody("test/casino", "comment-123", "alice", 19), "<!-- casino-withdrawal:test/casino:comment-123 -->"} {
		if !Marked(body, "test/casino", "comment-123") {
			t.Fatal("failed to recover existing withdrawal")
		}
		if Marked(body, "test/casino", "comment-124") {
			t.Fatal("reference matched another withdrawal")
		}
	}
}
