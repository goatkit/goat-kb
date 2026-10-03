package kb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A failing host DB call must answer 500 without leaking the driver error to
// the client, while still logging the detail server-side.
func TestInternalErrorDoesNotLeakDetail(t *testing.T) {
	const secret = "dial tcp 10.9.8.7:3306: secret-detail"
	h := newFakeHost(dialectMySQL)
	h.queryErr = errors.New(secret)
	p := New()
	p.host = h
	p.dialect = dialectMySQL

	out, err := p.handleAdminCategories(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var resp struct {
		Error  string `json:"error"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	if resp.Status != 500 {
		t.Fatalf("status = %d, want 500 (%s)", resp.Status, out)
	}
	if strings.Contains(string(out), "secret-detail") || strings.Contains(string(out), "10.9.8.7") {
		t.Fatalf("response leaks internal error: %s", out)
	}
	logged := false
	for _, l := range h.logs {
		if l.level == "error" && l.fields["error"] == secret {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("error detail not logged server-side: %+v", h.logs)
	}
}
