// Package kb implements the GoatFlow Knowledge Base plugin.
//
// zinc.go wraps the Zinc full-text search engine behind the HostAPI HTTP
// boundary. The plugin can't import internal/platform/zinc (platform
// boundary), so it talks to Zinc's REST API directly via HostAPI.HTTPRequest
// with basic-auth credentials from the plugin config map.
//
// Each KB article is indexed as a Zinc document carrying org_id so searches
// can filter at the query level for multi-tenant isolation.
package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// zincIndexName is the Zinc index holding KB article documents. Matches the
// DB table name so the source of truth and the search index line up.
const zincIndexName = "gk_kb_articles"

// zincClient talks to Zinc via the host's HTTP boundary. All requests go
// through HostAPI.HTTPRequest so the sandbox enforces the plugin's http
// permission scope and rate limits.
type zincClient struct {
	host     hostHTTP
	baseURL  string
	user     string
	password string
	enabled  bool
}

// hostHTTP is the subset of plugin.HostAPI the zinc client needs. Declared
// locally so the package composes on small interfaces and tests can fake it.
type hostHTTP interface {
	HTTPRequest(ctx context.Context, method, url string, headers map[string]string, body []byte) (int, []byte, error)
}

// zincDocument is the indexed representation of a KB article. org_id is
// stored on every document so search queries can filter by tenant.
type zincDocument struct {
	ID         int64  `json:"id"`
	OrgID      int64  `json:"org_id"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	Tags       string `json:"tags"`
	Visibility string `json:"visibility"`
	Status     string `json:"status"`
}

// newZincClient builds a zinc client from the plugin config map. The client
// is disabled (enabled=false) when zinc_url is unset, letting the plugin boot
// in environments without a search backend — search returns an empty result
// set with a clear message instead of failing.
func newZincClient(config map[string]string, host hostHTTP) *zincClient {
	c := &zincClient{
		host:     host,
		baseURL:  strings.TrimRight(config["zinc_url"], "/"),
		user:     config["zinc_user"],
		password: config["zinc_password"],
	}
	c.enabled = c.baseURL != ""
	return c
}

// authHeader returns the Basic auth header value, or empty if unconfigured.
func (c *zincClient) authHeader() string {
	if c.user == "" && c.password == "" {
		return ""
	}
	return basicAuth(c.user, c.password)
}

// headers builds the HTTP headers for a zinc request.
func (c *zincClient) headers() map[string]string {
	h := map[string]string{"Content-Type": "application/json"}
	if a := c.authHeader(); a != "" {
		h["Authorization"] = a
	}
	return h
}

// ensureIndex creates the KB article index if it doesn't exist. Zinc
// auto-creates an index on first document PUT, but creating it up front
// lets us set a field mapping so title/content/tags are analysed for
// full-text search rather than treated as keywords.
func (c *zincClient) ensureIndex(ctx context.Context) error {
	if !c.enabled {
		return nil
	}
	// Zinc PUT /api/<index> creates or replaces the index. Use a mapping
	// that marks text fields for analysis.
	mapping := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"title":      map[string]any{"type": "text"},
				"summary":    map[string]any{"type": "text"},
				"content":    map[string]any{"type": "text"},
				"tags":       map[string]any{"type": "text"},
				"category":   map[string]any{"type": "keyword"},
				"org_id":     map[string]any{"type": "long"},
				"visibility": map[string]any{"type": "keyword"},
				"status":     map[string]any{"type": "keyword"},
			},
		},
	}
	body, _ := json.Marshal(mapping)
	url := fmt.Sprintf("%s/api/%s", c.baseURL, zincIndexName)
	status, resp, err := c.host.HTTPRequest(ctx, "PUT", url, c.headers(), body)
	if err != nil {
		return fmt.Errorf("zinc ensure index: %w", err)
	}
	if status >= 400 {
		return fmt.Errorf("zinc ensure index: status %d: %s", status, string(resp))
	}
	return nil
}

// indexDocument upserts a single article document. The doc id is the
// article's DB id as a string — Zinc uses it as the _id, so re-indexing
// the same article overwrites instead of duplicating.
func (c *zincClient) indexDocument(ctx context.Context, doc zincDocument) error {
	if !c.enabled {
		return nil
	}
	body, _ := json.Marshal(doc)
	url := fmt.Sprintf("%s/api/%s/_doc/%d", c.baseURL, zincIndexName, doc.ID)
	status, resp, err := c.host.HTTPRequest(ctx, "PUT", url, c.headers(), body)
	if err != nil {
		return fmt.Errorf("zinc index doc %d: %w", doc.ID, err)
	}
	if status >= 400 {
		return fmt.Errorf("zinc index doc %d: status %d: %s", doc.ID, status, string(resp))
	}
	return nil
}

// deleteDocument removes an article from the index.
func (c *zincClient) deleteDocument(ctx context.Context, id int64) error {
	if !c.enabled {
		return nil
	}
	url := fmt.Sprintf("%s/api/%s/_doc/%d", c.baseURL, zincIndexName, id)
	status, resp, err := c.host.HTTPRequest(ctx, "DELETE", url, c.headers(), nil)
	if err != nil {
		return fmt.Errorf("zinc delete doc %d: %w", id, err)
	}
	// 404 is fine — doc may never have been indexed (zinc disabled at insert time).
	if status == 404 {
		return nil
	}
	if status >= 400 {
		return fmt.Errorf("zinc delete doc %d: status %d: %s", id, status, string(resp))
	}
	return nil
}

// searchResult is the parsed Zinc search response.
type zincSearchResult struct {
	Took int64 `json:"took"`
	Hits struct {
		Total struct {
			Value int64 `json:"value"`
		} `json:"total"`
		Hits []struct {
			ID     string          `json:"_id"`
			Score  float64         `json:"_score"`
			Source zincDocumentRaw `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// zincDocumentRaw mirrors zincDocument but with looser JSON types because
// Zinc returns numbers as float64 over JSON.
type zincDocumentRaw struct {
	ID         any    `json:"id"`
	OrgID      any    `json:"org_id"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	Tags       string `json:"tags"`
	Visibility string `json:"visibility"`
	Status     string `json:"status"`
}

// search performs a full-text query against the KB index, filtered to the
// caller's org_id. Returns the parsed hits and total count.
//
// The Zinc query uses a bool+filter+must structure: the must clause runs
// the match query against title/summary/content/tags, and the filter clause
// pins org_id so a search never crosses tenant boundaries.
func (c *zincClient) search(ctx context.Context, query string, orgID int64, page, perPage int, visibilityFilter string) (*zincSearchResult, error) {
	if !c.enabled {
		return nil, fmt.Errorf("zinc not configured")
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	from := (page - 1) * perPage

	// Build the Zinc/Elasticsearch-style query body.
	filters := []map[string]any{
		{"term": map[string]any{"org_id": orgID}},
		{"term": map[string]any{"status": "published"}},
	}
	if visibilityFilter != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"visibility": visibilityFilter}})
	}
	q := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must":   []map[string]any{{"multi_match": map[string]any{"query": query, "fields": []string{"title^3", "summary^2", "content", "tags"}}}},
				"filter": filters,
			},
		},
		"from": from,
		"size": perPage,
		"sort": []map[string]any{{"_score": "desc"}},
	}
	body, _ := json.Marshal(q)
	url := fmt.Sprintf("%s/api/%s/_search", c.baseURL, zincIndexName)
	status, resp, err := c.host.HTTPRequest(ctx, "POST", url, c.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("zinc search: %w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("zinc search: status %d: %s", status, string(resp))
	}
	var result zincSearchResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("zinc search: parse response: %w", err)
	}
	return &result, nil
}

// basicAuth builds an RFC 7617 Basic auth header value without importing
// encoding/base64 helpers that the sandbox might restrict. We use the
// stdlib net/http implementation pattern.
func basicAuth(user, password string) string {
	return "Basic " + base64Encode(user+":"+password)
}

// base64Encode implements standard base64 encoding for the Basic auth
// header without importing encoding/base64 at the call site.
func base64Encode(s string) string {
	const table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := []byte(s)
	var out strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		var pad int
		for j := 0; j < 3; j++ {
			n <<= 8
			if i+j < len(b) {
				n |= uint32(b[i+j])
			} else {
				pad++
			}
		}
		out.WriteByte(table[(n>>18)&0x3F])
		out.WriteByte(table[(n>>12)&0x3F])
		if pad < 2 {
			out.WriteByte(table[(n>>6)&0x3F])
		} else {
			out.WriteByte('=')
		}
		if pad < 1 {
			out.WriteByte(table[n&0x3F])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}