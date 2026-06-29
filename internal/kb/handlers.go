package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// listParams holds the query parameters for listing articles.
type listParams struct {
	Page     int    `json:"page,omitempty"`
	PerPage  int    `json:"per_page,omitempty"`
	Category string `json:"category,omitempty"`
}

// listResult is the response for kb_list.
type listResult struct {
	Articles []articleSummary `json:"articles"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PerPage  int              `json:"per_page"`
}

// articleSummary is a truncated article for list views.
type articleSummary struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Category   string `json:"category"`
	Visibility string `json:"visibility"`
	UpdatedAt  string `json:"updated_at"`
}

// handleList returns a paginated list of KB articles scoped to the caller's org.
func (p *Plugin) handleList(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return nil, fmt.Errorf("host API not available")
	}

	params := listParams{Page: 1, PerPage: 20}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid list params: %w", err)
		}
	}
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PerPage < 1 || params.PerPage > 100 {
		params.PerPage = 20
	}

	orgID := p.host.OrgID(ctx)
	offset := (params.Page - 1) * params.PerPage

	query := "SELECT id, title, summary, category, visibility, updated_at FROM gk_kb_articles WHERE org_id = ? AND status = 'published' ORDER BY updated_at DESC LIMIT ? OFFSET ?"
	rows, err := p.host.DBQuery(ctx, query, orgID, params.PerPage, offset)
	if err != nil {
		return nil, fmt.Errorf("query articles: %w", err)
	}

	articles := make([]articleSummary, 0, len(rows))
	for _, row := range rows {
		articles = append(articles, articleSummary{
			ID:         toInt64(row["id"]),
			Title:      toString(row["title"]),
			Summary:    toString(row["summary"]),
			Category:   toString(row["category"]),
			Visibility: toString(row["visibility"]),
			UpdatedAt:  toString(row["updated_at"]),
		})
	}

	countQuery := "SELECT COUNT(*) as total FROM gk_kb_articles WHERE org_id = ? AND status = 'published'"
	countRows, err := p.host.DBQuery(ctx, countQuery, orgID)
	if err != nil {
		return nil, fmt.Errorf("count articles: %w", err)
	}
	total := 0
	if len(countRows) > 0 {
		total = int(toInt64(countRows[0]["total"]))
	}

	result := listResult{
		Articles: articles,
		Total:    total,
		Page:     params.Page,
		PerPage:  params.PerPage,
	}
	return json.Marshal(result)
}

// handleRecentWidget returns recent articles for the dashboard widget.
func (p *Plugin) handleRecentWidget(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return nil, fmt.Errorf("host API not available")
	}

	orgID := p.host.OrgID(ctx)
	query := "SELECT id, title, summary FROM gk_kb_articles WHERE org_id = ? AND status = 'published' ORDER BY updated_at DESC LIMIT 5"
	rows, err := p.host.DBQuery(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("query recent articles: %w", err)
	}

	articles := make([]articleSummary, 0, len(rows))
	for _, row := range rows {
		articles = append(articles, articleSummary{
			ID:    toInt64(row["id"]),
			Title: toString(row["title"]),
		})
	}

	return json.Marshal(map[string]any{"articles": articles})
}

// searchParams holds the query parameters for searching articles.
type searchParams struct {
	Query string `json:"query"`
	Page  int    `json:"page,omitempty"`
}

// searchResult is the response for kb_search.
type searchResult struct {
	Results []searchHit `json:"results"`
	Total   int         `json:"total"`
	Query   string      `json:"query"`
}

// searchHit is a single search result.
type searchHit struct {
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	Summary  string  `json:"summary"`
	Score    float64 `json:"score"`
	Category string  `json:"category"`
}

// handleSearch performs a full-text search via zinc, scoped to the caller's org.
func (p *Plugin) handleSearch(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return nil, fmt.Errorf("host API not available")
	}

	params := searchParams{Page: 1}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid search params: %w", err)
	}
	if params.Query == "" {
		return nil, fmt.Errorf("kb:invalid_search: query is required")
	}
	if params.Page < 1 {
		params.Page = 1
	}

	orgID := p.host.OrgID(ctx)

	// Search via zinc HTTP API using HostAPI.HTTPRequest.
	// The zinc index includes org_id for multi-tenant isolation.
	searchBody := fmt.Sprintf(`{"search":{"query":"%s","org_id":%d}}`, jsonEscape(params.Query), orgID)
	status, body, err := p.host.HTTPRequest(ctx, "POST", "http://zinc:4080/api/v1/kb_articles/_search", nil, []byte(searchBody))
	if err != nil {
		return nil, fmt.Errorf("zinc search request failed: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("zinc search returned status %d: %s", status, string(body))
	}

	// Parse zinc response — the exact structure depends on zinc's API.
	// We extract hits and map to searchHit.
	var zincResp struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				Score  float64 `json:"_score"`
				Source struct {
					ID       int64  `json:"id"`
					Title    string `json:"title"`
					Summary  string `json:"summary"`
					Category string `json:"category"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &zincResp); err != nil {
		return nil, fmt.Errorf("parse zinc response: %w", err)
	}

	hits := make([]searchHit, 0, len(zincResp.Hits.Hits))
	for _, h := range zincResp.Hits.Hits {
		hits = append(hits, searchHit{
			ID:       h.Source.ID,
			Title:    h.Source.Title,
			Summary:  h.Source.Summary,
			Score:    h.Score,
			Category: h.Source.Category,
		})
	}

	result := searchResult{
		Results: hits,
		Total:   zincResp.Hits.Total.Value,
		Query:   params.Query,
	}
	return json.Marshal(result)
}

// articleParams holds the query parameters for fetching a single article.
type articleParams struct {
	ID int64 `json:"id"`
}

// articleDetail is the full article response.
type articleDetail struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	Visibility string `json:"visibility"`
	Author     string `json:"author"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// handleArticle returns a single KB article after permission verification.
func (p *Plugin) handleArticle(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return nil, fmt.Errorf("host API not available")
	}

	params := articleParams{}
	if err := json.Unmarshal(args, &params); err != nil {
		// Try parsing as string (path param)
		var idStr string
		if err2 := json.Unmarshal(args, &idStr); err2 == nil {
			params.ID, _ = strconv.ParseInt(idStr, 10, 64)
		} else {
			return nil, fmt.Errorf("kb:invalid_article_id: %w", err)
		}
	}
	if params.ID < 1 {
		return nil, fmt.Errorf("kb:invalid_article_id: ID must be positive")
	}

	orgID := p.host.OrgID(ctx)

	// Fetch article with org_id check — prevents cross-org access (IDOR protection).
	query := "SELECT id, title, content, category, visibility, author, created_at, updated_at FROM gk_kb_articles WHERE id = ? AND org_id = ? AND status = 'published'"
	rows, err := p.host.DBQuery(ctx, query, params.ID, orgID)
	if err != nil {
		return nil, fmt.Errorf("query article: %w", err)
	}
	if len(rows) == 0 {
		// Return 404 — don't leak existence of articles from other orgs.
		return nil, fmt.Errorf("kb:article_not_found: article %d not found", params.ID)
	}

	row := rows[0]
	article := articleDetail{
		ID:         toInt64(row["id"]),
		Title:      toString(row["title"]),
		Content:    toString(row["content"]),
		Category:   toString(row["category"]),
		Visibility: toString(row["visibility"]),
		Author:     toString(row["author"]),
		CreatedAt:  toString(row["created_at"]),
		UpdatedAt:  toString(row["updated_at"]),
	}

	return json.Marshal(article)
}

// handleImport validates import requests and refuses execution until the schema milestone is complete.
func (p *Plugin) handleImport(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return nil, fmt.Errorf("host API not available")
	}

	orgID := p.host.OrgID(ctx)
	p.host.Log(ctx, "info", "OTRS FAQ import started", map[string]any{
		"org_id": orgID,
	})

	return nil, fmt.Errorf("kb:import_failed: import requires KB schema and importer implementation")
}

// --- helpers ---

func toInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case []byte:
		n, _ := strconv.ParseInt(string(val), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	}
	return 0
}

func toString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
