package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeHTTPHost captures HTTPRequests made to zinc and returns canned
// responses. It simulates the Zinc REST API: PUT /api/<index> (create index),
// PUT /api/<index>/_doc/<id> (index doc), DELETE /api/<index>/_doc/<id>
// (delete doc), POST /api/<index>/_search (search).
type fakeHTTPHost struct {
	mu          sync.Mutex
	requests    []httpCall
	indexedDocs map[string]map[string]any // id -> doc
	searchResp  string                    // canned search response
	enabled     bool
}

type httpCall struct {
	method  string
	url     string
	headers map[string]string
	body    []byte
}

func newFakeHTTPHost() *fakeHTTPHost {
	return &fakeHTTPHost{
		indexedDocs: map[string]map[string]any{},
		enabled:     true,
	}
}

func (h *fakeHTTPHost) HTTPRequest(_ context.Context, method, url string, headers map[string]string, body []byte) (int, []byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, httpCall{method, url, headers, body})

	// PUT /api/<index> — create index
	if method == "PUT" && strings.Contains(url, "/api/gk_kb_articles") && !strings.Contains(url, "_doc") {
		return 200, []byte(`{"index":"gk_kb_articles","created":true}`), nil
	}

	// PUT /api/<index>/_doc/<id> — index document
	if method == "PUT" && strings.Contains(url, "_doc/") {
		parts := strings.Split(url, "_doc/")
		id := parts[len(parts)-1]
		var doc map[string]any
		_ = json.Unmarshal(body, &doc)
		h.indexedDocs[id] = doc
		return 200, []byte(`{"result":"created"}`), nil
	}

	// DELETE /api/<index>/_doc/<id> — delete document
	if method == "DELETE" && strings.Contains(url, "_doc/") {
		parts := strings.Split(url, "_doc/")
		id := parts[len(parts)-1]
		delete(h.indexedDocs, id)
		return 200, []byte(`{"result":"deleted"}`), nil
	}

	// POST /api/<index>/_search — search
	if method == "POST" && strings.Contains(url, "_search") {
		if h.searchResp != "" {
			return 200, []byte(h.searchResp), nil
		}
		// Build a response from the indexed docs, filtered by org_id in the
		// query body.
		return 200, h.buildSearchResponse(body), nil
	}

	return 404, []byte(`{"error":"not found"}`), nil
}

// buildSearchResponse constructs a Zinc-style search response from indexed
// docs, filtering by the org_id in the query body.
func (h *fakeHTTPHost) buildSearchResponse(queryBody []byte) []byte {
	var q struct {
		Query struct {
			Bool struct {
				Filter []struct {
					Term struct {
						OrgID  int64  `json:"org_id"`
						Status string `json:"status"`
					} `json:"term"`
				} `json:"filter"`
			} `json:"bool"`
		} `json:"query"`
		From int `json:"from"`
		Size int `json:"size"`
	}
	_ = json.Unmarshal(queryBody, &q)

	orgID := int64(0)
	statusFilter := ""
	for _, f := range q.Query.Bool.Filter {
		if f.Term.OrgID != 0 {
			orgID = f.Term.OrgID
		}
		if f.Term.Status != "" {
			statusFilter = f.Term.Status
		}
	}

	type hit struct {
		ID     string         `json:"_id"`
		Score  float64        `json:"_score"`
		Source map[string]any `json:"_source"`
	}
	hits := []hit{}
	for id, doc := range h.indexedDocs {
		docOrgID, _ := doc["org_id"].(float64)
		if orgID != 0 && int64(docOrgID) != orgID {
			continue
		}
		if statusFilter != "" {
			if s, _ := doc["status"].(string); s != statusFilter {
				continue
			}
		}
		hits = append(hits, hit{ID: id, Score: 1.0, Source: doc})
	}

	resp := map[string]any{
		"took": 1,
		"hits": map[string]any{
			"total": map[string]any{"value": len(hits)},
			"hits":  hits,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func (h *fakeHTTPHost) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func (h *fakeHTTPHost) getDoc(id string) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.indexedDocs[id]
}

// --- zinc client construction ---

func TestNewZincClientDisabledWhenNoURL(t *testing.T) {
	c := newZincClient(map[string]string{}, newFakeHTTPHost())
	if c.enabled {
		t.Fatal("expected zinc disabled when zinc_url is empty")
	}
}

func TestNewZincClientEnabledWithConfig(t *testing.T) {
	config := map[string]string{
		"zinc_url":      "http://zinc:4080",
		"zinc_user":     "admin",
		"zinc_password": "secret",
	}
	c := newZincClient(config, newFakeHTTPHost())
	if !c.enabled {
		t.Fatal("expected zinc enabled when zinc_url is set")
	}
	if c.baseURL != "http://zinc:4080" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
}

func TestNewZincClientTrimsTrailingSlash(t *testing.T) {
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080/"}, newFakeHTTPHost())
	if c.baseURL != "http://zinc:4080" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
}

// --- index creation ---

func TestEnsureIndexCreatesIndex(t *testing.T) {
	h := newFakeHTTPHost()
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, h)
	if err := c.ensureIndex(context.Background()); err != nil {
		t.Fatalf("ensureIndex: %v", err)
	}
	if h.callCount() != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", h.callCount())
	}
	req := h.requests[0]
	if req.method != "PUT" {
		t.Errorf("method = %s, want PUT", req.method)
	}
	if !strings.Contains(req.url, "/api/gk_kb_articles") {
		t.Errorf("url = %s, expected to contain /api/gk_kb_articles", req.url)
	}
	// Verify the mapping sets text fields for analysis.
	var body map[string]any
	_ = json.Unmarshal(req.body, &body)
	mappings, _ := body["mappings"].(map[string]any)
	props, _ := mappings["properties"].(map[string]any)
	if title, ok := props["title"].(map[string]any); !ok || title["type"] != "text" {
		t.Error("expected title field mapped as text")
	}
	if orgIDField, ok := props["org_id"].(map[string]any); !ok || orgIDField["type"] != "long" {
		t.Error("expected org_id field mapped as long")
	}
}

func TestEnsureIndexNoopWhenDisabled(t *testing.T) {
	h := newFakeHTTPHost()
	c := newZincClient(map[string]string{}, h)
	if err := c.ensureIndex(context.Background()); err != nil {
		t.Fatalf("ensureIndex: %v", err)
	}
	if h.callCount() != 0 {
		t.Fatalf("expected 0 HTTP calls when disabled, got %d", h.callCount())
	}
}

// --- document indexing ---

func TestIndexDocumentPutsDocWithOrgID(t *testing.T) {
	h := newFakeHTTPHost()
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, h)
	doc := zincDocument{
		ID: 42, OrgID: 7, Title: "Test Article", Summary: "A summary",
		Content: "Full content", Category: "Help", Tags: "tag1,tag2",
		Visibility: "public", Status: "published",
	}
	if err := c.indexDocument(context.Background(), doc); err != nil {
		t.Fatalf("indexDocument: %v", err)
	}
	stored := h.getDoc("42")
	if stored == nil {
		t.Fatal("document not indexed")
	}
	if stored["org_id"].(float64) != 7 {
		t.Errorf("org_id = %v, want 7", stored["org_id"])
	}
	if stored["title"] != "Test Article" {
		t.Errorf("title = %v", stored["title"])
	}
}

func TestIndexDocumentNoopWhenDisabled(t *testing.T) {
	h := newFakeHTTPHost()
	c := newZincClient(map[string]string{}, h)
	doc := zincDocument{ID: 1, OrgID: 1, Title: "x"}
	if err := c.indexDocument(context.Background(), doc); err != nil {
		t.Fatalf("indexDocument: %v", err)
	}
	if h.callCount() != 0 {
		t.Fatalf("expected 0 calls when disabled, got %d", h.callCount())
	}
}

// --- document deletion ---

func TestDeleteDocumentRemovesDoc(t *testing.T) {
	h := newFakeHTTPHost()
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, h)
	// Index a doc first.
	c.indexDocument(context.Background(), zincDocument{ID: 5, OrgID: 1, Title: "x"})
	if h.getDoc("5") == nil {
		t.Fatal("doc not indexed")
	}
	// Delete it.
	if err := c.deleteDocument(context.Background(), 5); err != nil {
		t.Fatalf("deleteDocument: %v", err)
	}
	if h.getDoc("5") != nil {
		t.Fatal("doc not deleted")
	}
}

func TestDeleteDocument404IsNotError(t *testing.T) {
	// Deleting a non-existent doc should be a no-op, not an error.
	// The errorHTTPHost returns a fixed 404 for all requests.
	h := &errorHTTPHost{status: 404}
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, h)
	if err := c.deleteDocument(context.Background(), 999); err != nil {
		t.Fatalf("deleteDocument 404 should not error, got %v", err)
	}
}

// errorHTTPHost returns a fixed status code for all requests.
type errorHTTPHost struct {
	status int
}

func (h *errorHTTPHost) HTTPRequest(_ context.Context, _, _ string, _ map[string]string, _ []byte) (int, []byte, error) {
	return h.status, []byte(fmt.Sprintf(`{"error":"status %d"}`, h.status)), nil
}

func TestIndexDocumentFailsOn500(t *testing.T) {
	h := &errorHTTPHost{status: 500}
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, h)
	err := c.indexDocument(context.Background(), zincDocument{ID: 1, OrgID: 1, Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

// --- search ---

func TestSearchReturnsHitsFilteredByOrgID(t *testing.T) {
	h := newFakeHTTPHost()
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, h)
	// Index two docs in different orgs.
	c.indexDocument(context.Background(), zincDocument{ID: 1, OrgID: 1, Title: "Org1 Article", Status: "published"})
	c.indexDocument(context.Background(), zincDocument{ID: 2, OrgID: 2, Title: "Org2 Article", Status: "published"})

	// Search as org 1 — should only get org 1's doc.
	res, err := c.search(context.Background(), "article", 1, 1, 10, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Hits.Total.Value != 1 {
		t.Fatalf("total = %d, want 1", res.Hits.Total.Value)
	}
	if len(res.Hits.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(res.Hits.Hits))
	}
	// Verify the hit is from org 1.
	orgID, _ := res.Hits.Hits[0].Source.OrgID.(float64)
	if int64(orgID) != 1 {
		t.Errorf("hit org_id = %v, want 1", res.Hits.Hits[0].Source.OrgID)
	}
}

func TestSearchFiltersDraftArticles(t *testing.T) {
	h := newFakeHTTPHost()
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, h)
	// One published, one draft in the same org.
	c.indexDocument(context.Background(), zincDocument{ID: 1, OrgID: 1, Title: "Published", Status: "published"})
	c.indexDocument(context.Background(), zincDocument{ID: 2, OrgID: 1, Title: "Draft", Status: "draft"})

	res, err := c.search(context.Background(), "test", 1, 1, 10, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.Hits.Total.Value != 1 {
		t.Fatalf("total = %d, want 1 (drafts excluded)", res.Hits.Total.Value)
	}
}

func TestSearchFailsWhenDisabled(t *testing.T) {
	c := newZincClient(map[string]string{}, newFakeHTTPHost())
	_, err := c.search(context.Background(), "test", 1, 1, 10, "")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected not-configured error, got %v", err)
	}
}

func TestSearchSendsOrgIDFilterInQuery(t *testing.T) {
	h := newFakeHTTPHost()
	c := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, h)
	_, _ = c.search(context.Background(), "test", 42, 1, 10, "")
	// The last request should be the search POST.
	last := h.requests[len(h.requests)-1]
	var q map[string]any
	_ = json.Unmarshal(last.body, &q)
	query, _ := q["query"].(map[string]any)
	boolQ, _ := query["bool"].(map[string]any)
	filters, _ := boolQ["filter"].([]any)
	for _, f := range filters {
		term, _ := f.(map[string]any)["term"].(map[string]any)
		if orgID, ok := term["org_id"].(float64); ok && int64(orgID) == 42 {
			return // found the org_id filter
		}
	}
	t.Error("org_id filter not found in search query body")
}

// --- handleSearch via Plugin.Call ---

func TestCallSearchReturnsEmptyWhenZincDisabled(t *testing.T) {
	p := New()
	p.host = newFakeHost(dialectMySQL)
	p.zinc = newZincClient(map[string]string{}, p.host) // disabled
	args, _ := json.Marshal(map[string]any{"query": "test"})
	result, err := p.Call("kb_search", args)
	if err != nil {
		t.Fatalf("Call kb_search: %v", err)
	}
	var res searchResult
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("total = %d, want 0 when zinc disabled", res.Total)
	}
	if len(res.Results) != 0 {
		t.Fatalf("results = %d, want 0", len(res.Results))
	}
}

func TestCallSearchRequiresQuery(t *testing.T) {
	p := New()
	p.host = newFakeHost(dialectMySQL)
	p.zinc = newZincClient(map[string]string{}, p.host)
	result, err := p.Call("kb_search", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	var body map[string]any
	_ = json.Unmarshal(result, &body)
	if int(body["status"].(float64)) != 400 {
		t.Fatalf("status = %v, want 400", body["status"])
	}
}

// --- import with zinc indexing ---

func TestImportIndexesToZincWhenEnabled(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	httpHost := newFakeHTTPHost()
	zc := newZincClient(map[string]string{"zinc_url": "http://zinc:4080"}, httpHost)

	_, err := importOTRSFAQ(context.Background(), h, zc, 1, []byte(otrsFAQXML))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// Two articles imported → two docs indexed (plus 0 index-creation calls
	// since we didn't call ensureIndex).
	if len(httpHost.indexedDocs) != 2 {
		t.Fatalf("indexed docs = %d, want 2", len(httpHost.indexedDocs))
	}
	for id, doc := range httpHost.indexedDocs {
		if doc["org_id"].(float64) != 1 {
			t.Errorf("doc %s org_id = %v, want 1", id, doc["org_id"])
		}
		if doc["source"] == nil && doc["title"] == "" {
			t.Errorf("doc %s missing fields", id)
		}
		if doc["title"] == "" {
			t.Errorf("doc %s missing title", id)
		}
	}
}

func TestImportSkipsZincWhenDisabled(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	httpHost := newFakeHTTPHost()
	zc := newZincClient(map[string]string{}, httpHost) // disabled

	_, err := importOTRSFAQ(context.Background(), h, zc, 1, []byte(otrsFAQXML))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(httpHost.indexedDocs) != 0 {
		t.Fatalf("indexed docs = %d, want 0 when zinc disabled", len(httpHost.indexedDocs))
	}
	if httpHost.callCount() != 0 {
		t.Fatalf("HTTP calls = %d, want 0 when zinc disabled", httpHost.callCount())
	}
}

// --- basicAuth ---

func TestBasicAuth(t *testing.T) {
	got := basicAuth("admin", "secret")
	// "admin:secret" base64 is "YWRtaW46c2VjcmV0"
	if got != "Basic YWRtaW46c2VjcmV0" {
		t.Errorf("basicAuth = %q, want 'Basic YWRtaW46c2VjcmV0'", got)
	}
}

func TestBase64Encode(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"f":      "Zg==",
		"fo":     "Zm8=",
		"foo":    "Zm9v",
		"foob":   "Zm9vYg==",
		"fooba":  "Zm9vYmE=",
		"foobar": "Zm9vYmFy",
	}
	for in, want := range cases {
		if got := base64Encode(in); got != want {
			t.Errorf("base64Encode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestZincHeadersIncludeAuth(t *testing.T) {
	c := newZincClient(map[string]string{
		"zinc_url":      "http://zinc:4080",
		"zinc_user":     "admin",
		"zinc_password": "pass",
	}, newFakeHTTPHost())
	h := c.headers()
	if h["Authorization"] != "Basic YWRtaW46cGFzcw==" {
		t.Errorf("auth header = %q", h["Authorization"])
	}
	if h["Content-Type"] != "application/json" {
		t.Errorf("content-type = %q", h["Content-Type"])
	}
}
