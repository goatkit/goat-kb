package kb

import (
	"context"
	"encoding/json"
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
		return errorResponse(503, "host API not available")
	}

	params := listParams{Page: 1, PerPage: 20}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return errorResponse(400, "invalid list params: "+err.Error())
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
		return errorResponse(500, "query articles: "+err.Error())
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
		return errorResponse(500, "count articles: "+err.Error())
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
		return errorResponse(503, "host API not available")
	}

	orgID := p.host.OrgID(ctx)
	query := "SELECT id, title, summary FROM gk_kb_articles WHERE org_id = ? AND status = 'published' ORDER BY updated_at DESC LIMIT 5"
	rows, err := p.host.DBQuery(ctx, query, orgID)
	if err != nil {
		return errorResponse(500, "query recent articles: "+err.Error())
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
	Query   string `json:"query"`
	Page    int    `json:"page,omitempty"`
	PerPage int    `json:"per_page,omitempty"`
}

// searchResult is the response for kb_search.
type searchResult struct {
	Results []searchHit `json:"results"`
	Total   int         `json:"total"`
	Query   string      `json:"query"`
	Page    int         `json:"page"`
	PerPage int         `json:"per_page"`
}

// searchHit is a single search result.
type searchHit struct {
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	Summary  string  `json:"summary"`
	Score    float64 `json:"score"`
	Category string  `json:"category"`
}

// handleSearch performs a full-text search via zinc, scoped to the caller's
// org. The query matches title, summary, content, and tags; results are
// filtered by org_id and status=published so a search never crosses tenant
// boundaries or surfaces drafts. Returns results formatted for the
// kb_search.pongo2 template (results, total, query, page, per_page).
func (p *Plugin) handleSearch(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	params := searchParams{Page: 1, PerPage: 10}
	if err := json.Unmarshal(args, &params); err != nil {
		return errorResponse(400, "invalid search params: "+err.Error())
	}
	if params.Query == "" {
		return errorResponse(400, "search query is required")
	}
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PerPage < 1 || params.PerPage > 100 {
		params.PerPage = 10
	}

	// If zinc isn't configured, return an empty result set with a clear
	// message rather than failing — the KB list still works via the DB.
	if p.zinc == nil || !p.zinc.enabled {
		return jsonMarshal(searchResult{
			Results: []searchHit{},
			Total:   0,
			Query:   params.Query,
			Page:    params.Page,
			PerPage: params.PerPage,
		})
	}

	orgID := p.host.OrgID(ctx)

	zres, err := p.zinc.search(ctx, params.Query, orgID, params.Page, params.PerPage)
	if err != nil {
		return errorResponse(502, "zinc search failed: "+err.Error())
	}

	hits := make([]searchHit, 0, len(zres.Hits.Hits))
	for _, h := range zres.Hits.Hits {
		hits = append(hits, searchHit{
			ID:       toInt64(h.Source.ID),
			Title:    h.Source.Title,
			Summary:  h.Source.Summary,
			Score:    h.Score,
			Category: h.Source.Category,
		})
	}

	result := searchResult{
		Results: hits,
		Total:   int(zres.Hits.Total.Value),
		Query:   params.Query,
		Page:    params.Page,
		PerPage: params.PerPage,
	}
	return jsonMarshal(result)
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
		return errorResponse(503, "host API not available")
	}

	params := articleParams{}
	if err := json.Unmarshal(args, &params); err != nil {
		// Try parsing as string (path param)
		var idStr string
		if err2 := json.Unmarshal(args, &idStr); err2 == nil {
			params.ID, _ = strconv.ParseInt(idStr, 10, 64)
		} else {
			return errorResponse(400, "invalid article id: "+err.Error())
		}
	}
	if params.ID < 1 {
		return errorResponse(400, "invalid article id: ID must be positive")
	}

	orgID := p.host.OrgID(ctx)

	// Fetch article with org_id check — prevents cross-org access (IDOR protection).
	query := "SELECT id, title, content, category, visibility, author, created_at, updated_at FROM gk_kb_articles WHERE id = ? AND org_id = ? AND status = 'published'"
	rows, err := p.host.DBQuery(ctx, query, params.ID, orgID)
	if err != nil {
		return errorResponse(500, "query article: "+err.Error())
	}
	if len(rows) == 0 {
		// 404 — don't leak existence of articles from other orgs.
		return errorResponse(404, "article not found")
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

// importParams holds the fields the host injects for a kb_import call.
// The raw OTRS FAQ XML body arrives as _body (buildPluginArgs passes
// non-JSON payloads through verbatim); _content_type lets us validate.
type importParams struct {
	Body        string `json:"_body,omitempty"`
	ContentType string `json:"_content_type,omitempty"`
}
// handleImport accepts an OTRS/Znuny FAQ XML export (POST body, passed
// through as _body by the host) and inserts articles into gk_kb_articles
// scoped to the caller's org_id. Re-importing the same export upserts in
// place via the (org_id, slug) unique index — idempotent.
//
// Returns a JSON import summary: {imported, skipped, errors, org_id}.
// On a hard failure (no org, unparseable payload, schema error) it returns
// an error-status JSON body so the host maps it to the right HTTP code.
func (p *Plugin) handleImport(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	orgID := p.host.OrgID(ctx)
	if orgID <= 0 {
		return errorResponse(400, "no active organisation — cannot import without org_id")
	}

	params := importParams{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}
	payload := []byte(params.Body)
	if len(payload) == 0 {
		return errorResponse(400, "import payload is empty — POST the OTRS FAQ XML as the request body")
	}

	p.host.Log(ctx, "info", "OTRS FAQ import started", map[string]any{
		"org_id":       orgID,
		"bytes":        len(payload),
		"content_type": params.ContentType,
	})

	res, err := importOTRSFAQ(ctx, p.host, p.zinc, orgID, payload)
	if err != nil {
		p.host.Log(ctx, "error", "OTRS FAQ import failed", map[string]any{
			"org_id": orgID,
			"error":  err.Error(),
		})
		return errorResponse(500, err.Error())
	}

	p.host.Log(ctx, "info", "OTRS FAQ import complete", map[string]any{
		"org_id":   orgID,
		"imported": res.Imported,
		"skipped":  res.Skipped,
	})
	return jsonMarshal(res)
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

// toInt is the int-returning form of toInt64, used by the schema layer
// (schema_version rows can arrive as int64, int, or float64).
func toInt(v any) int { return int(toInt64(v)) }

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


// errorResponse builds a JSON error body with an HTTP status code. The host's
// dynamic router honours {"error": msg, "status": N} (N in 400–599) and maps
// it to the matching HTTP status, so plugins can signal 400/404/500 etc.
// without the host falling back to 200-OK-with-error-body.
func errorResponse(code int, msg string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"error":  msg,
		"status": code,
	})
}

// jsonMarshal wraps json.Marshal so callers return (json.RawMessage, error)
// without rewriting the two-line boilerplate at every handler return.
func jsonMarshal(v any) (json.RawMessage, error) { return json.Marshal(v) }
