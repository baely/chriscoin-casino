package chriscoin

import (
	"encoding/json"
	"fmt"

	"chriscoin-casino/internal/ledger"
)

// RequestTitle matches the titles produced by ChrisCoin's own request links.
func RequestTitle(command Command) string {
	switch command.Action {
	case "offer":
		return fmt.Sprintf("ChrisCoin: offer %d to %s", command.Amount, command.Recipient)
	case "request":
		return fmt.Sprintf("ChrisCoin: request %d from %s", command.Amount, command.Payer)
	default:
		return "ChrisCoin: " + command.Action
	}
}

// RequestBody uses the upstream fenced JSON format. The optional opaque
// reference permits recovery after a lost API response without exposing the
// originating application or its command identifiers in the issue body.
func RequestBody(command Command, reference string) string {
	data, _ := json.MarshalIndent(command, "", "  ")
	body := "```chriscoin\n" + string(data) + "\n```\n"
	if reference != "" {
		body += "\n" + reference + "\n"
	}
	return body
}
func RequestMarker(repository, kind, id string) string {
	reference := ledger.Hash([]byte("chriscoin-request-v1\x00" + repository + "\x00" + kind + "\x00" + id))
	return "<!-- request-ref:" + reference + " -->"
}
