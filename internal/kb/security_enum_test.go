package kb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// These tests lock in the enum validation that handleAdminArticleUpdate
// performs on the `visibility` ("public"|"agent") and `status`
// ("draft"|"published"|"archived") fields. The values are validated *after*
// unmarshalling but *before* any DB write, so a malicious or buggy caller
// cannot smuggle an arbitrary string into the articles table. Empty values
// must be accepted because the handler applies defaults at insert time.
//
// Note on the "accepted" cases: the shared fakeHost implements the import
// handler's 12-column insert, not this handler's 10-column insert (no
// source/source_id), so letting the insert proceed would panic inside the
// fake. Instead we inject a query error: a valid/empty enum value sails past
// validation and then hits the injected error, yielding a 500 "check article"
// response rather than a 400 validation error. That is exactly the contract
// under test — the value is *not rejected as a validation error*.

// decodeResp unmarshals a handler response envelope into a map. Valid
// responses (e.g. {"status":"created"}) and error envelopes
// ({"error":"...","status":400}) both decode cleanly.
func decodeResp(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response is not a JSON object: %s (err=%v)", string(raw), err)
	}
	return got
}

// expectRejected asserts the handler returned a 400 error whose message
// contains wantSubstr.
func expectRejected(t *testing.T, raw json.RawMessage, wantSubstr string) {
	t.Helper()
	got := decodeResp(t, raw)
	errMsg, _ := got["error"].(string)
	if errMsg == "" {
		t.Fatalf("expected a 400 validation error, got success body: %s", string(raw))
	}
	status, _ := got["status"].(float64)
	if int(status) != 400 {
		t.Errorf("expected status 400, got %v", status)
	}
	if !strings.Contains(errMsg, wantSubstr) {
		t.Errorf("expected error message containing %q, got %q", wantSubstr, errMsg)
	}
}

// expectAccepted asserts the handler did NOT reject the value with a 400
// validation error. The handler may still return a non-validation error
// (e.g. an insert/query failure), but it must not surface "invalid
// visibility" or "invalid status". A clean success envelope (no "error"
// field) is the happy path.
func expectAccepted(t *testing.T, raw json.RawMessage) {
	t.Helper()
	got := decodeResp(t, raw)
	errMsg, _ := got["error"].(string)
	if errMsg == "" {
		return // success envelope, e.g. {"status":"created"} — accepted.
	}
	for _, forbidden := range []string{"invalid visibility", "invalid status"} {
		if strings.Contains(errMsg, forbidden) {
			t.Errorf("expected value to be accepted, but got validation error: %q", errMsg)
		}
	}
}

func TestHandleAdminArticleUpdateEnumValidation(t *testing.T) {
	ctx := context.Background()

	// Fresh plugin with an in-memory fakeHost. Used by the rejection cases,
	// which return at validation time before any DB access.
	newPlugin := func() *Plugin {
		p := New()
		p.host = newFakeHost(dialectMySQL)
		p.dialect = dialectMySQL
		return p
	}

	// Plugin whose host fails the first DB query. Used by the accepted cases
	// so they sail past enum validation and hit a non-validation error
	// (see file doc comment) instead of panicking inside the fakeHost insert.
	newPluginWithQueryErr := func() *Plugin {
		h := newFakeHost(dialectMySQL)
		h.queryErr = errors.New("forced query failure")
		p := New()
		p.host = h
		p.dialect = dialectMySQL
		return p
	}

	t.Run("invalid visibility rejected", func(t *testing.T) {
		p := newPlugin()
		args := json.RawMessage(`{"id":0,"title":"Test","content":"<p>test</p>","visibility":"evil","status":"published"}`)
		raw, err := p.handleAdminArticleUpdate(ctx, args)
		if err != nil {
			t.Fatalf("unexpected Go-level error: %v", err)
		}
		expectRejected(t, raw, "invalid visibility")
	})

	t.Run("invalid status rejected", func(t *testing.T) {
		p := newPlugin()
		// visibility must be valid (or empty) so the status check is reached.
		args := json.RawMessage(`{"id":0,"title":"Test","content":"<p>test</p>","visibility":"public","status":"wrong"}`)
		raw, err := p.handleAdminArticleUpdate(ctx, args)
		if err != nil {
			t.Fatalf("unexpected Go-level error: %v", err)
		}
		expectRejected(t, raw, "invalid status")
	})

	t.Run("valid visibility and status accepted", func(t *testing.T) {
		p := newPluginWithQueryErr()
		args := json.RawMessage(`{"id":0,"title":"Test","content":"<p>test</p>","visibility":"public","status":"published"}`)
		raw, err := p.handleAdminArticleUpdate(ctx, args)
		if err != nil {
			t.Fatalf("unexpected Go-level error: %v", err)
		}
		expectAccepted(t, raw)
	})

	t.Run("empty visibility accepted", func(t *testing.T) {
		p := newPluginWithQueryErr()
		// No visibility field -> skipped by validation, defaults applied later.
		args := json.RawMessage(`{"id":0,"title":"Test","content":"<p>test</p>","status":"published"}`)
		raw, err := p.handleAdminArticleUpdate(ctx, args)
		if err != nil {
			t.Fatalf("unexpected Go-level error: %v", err)
		}
		expectAccepted(t, raw)
	})

	t.Run("empty status accepted", func(t *testing.T) {
		p := newPluginWithQueryErr()
		// No status field -> skipped by validation, defaults applied later.
		args := json.RawMessage(`{"id":0,"title":"Test","content":"<p>test</p>","visibility":"public"}`)
		raw, err := p.handleAdminArticleUpdate(ctx, args)
		if err != nil {
			t.Fatalf("unexpected Go-level error: %v", err)
		}
		expectAccepted(t, raw)
	})
}
