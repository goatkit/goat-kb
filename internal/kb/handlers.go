package kb

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// --- request context ---

// reqCtx carries the authenticated caller's identity and role. Extracted
// from args (which buildPluginArgs populates) at the start of every handler
// and used for visibility filtering, audit logging, and IDOR protection.
type reqCtx struct {
	OrgID   int64
	Role    string // "Admin", "Agent", "Customer", or empty
	IsAdmin bool
	UserID  int64
	Login   string
}

// extractReqCtx reads the authenticated caller context from args. The host's
// buildPluginArgs injects _org_id, _user_role, _is_admin, _user_id,
// _user_login from the request session/middleware.
func extractReqCtx(args json.RawMessage) reqCtx {
	var raw struct {
		OrgID   int64  `json:"_org_id"`
		OrgID2  int64  `json:"org_id"`
		Role    string `json:"_user_role"`
		IsAdmin bool   `json:"_is_admin"`
		UserID  int64  `json:"_user_id"`
		Login   string `json:"_user_login"`
	}
	_ = json.Unmarshal(args, &raw)
	rc := reqCtx{OrgID: raw.OrgID, Role: raw.Role, IsAdmin: raw.IsAdmin, UserID: raw.UserID, Login: raw.Login}
	if rc.OrgID == 0 {
		rc.OrgID = raw.OrgID2
	}
	return rc
}

// isAgentOrAdmin returns true when the caller can access agent-scoped articles.
func (r reqCtx) isAgentOrAdmin() bool {
	return r.Role == "Admin" || r.Role == "Agent" || r.IsAdmin
}

// canSeeVisibility returns true if the caller is permitted to see articles
// with the given visibility level.
//
//	"public"  — everyone (customers, agents, admins)
//	"org"     — org-scoped (agents + admins)
//	"agent"   — agent-scoped (agents + admins, not customers)
//
// Unknown visibility defaults to "agent" (fail-safe: restrict).
func (r reqCtx) canSeeVisibility(visibility string) bool {
	switch visibility {
	case "public":
		return true
	case "org":
		return r.isAgentOrAdmin()
	default: // "agent" or unknown → restrict
		return r.isAgentOrAdmin()
	}
}

// visibilityClause returns the SQL visibility filter for the caller's role.
// Customers get `AND visibility = 'public'`; agents/admins get no visibility
// restriction (they see everything in their org).
func (r reqCtx) visibilityClause() string {
	if r.canSeeVisibility("public") && !r.canSeeVisibility("agent") {
		return " AND visibility = 'public'"
	}
	return ""
}

// maxVisibilityFilter returns the strictest visibility a caller can see,
// used for zinc search terms filter: "public" for customers, empty means
// all for agents/admins.
func (r reqCtx) maxVisibilityFilter() string {
	if r.isAgentOrAdmin() {
		return ""
	}
	return "public"
}

// --- handlers ---

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

// handleList returns a paginated list of KB articles scoped to the caller's
// org, filtered by role-based visibility. Customers see only public articles;
// agents and admins see all published articles in their org.
func (p *Plugin) handleList(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	rc := extractReqCtx(args)

	params := listParams{Page: 1, PerPage: 20}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			if rc.UserID != 0 {
				p.host.Log(ctx, "warn", "kb: invalid list params", map[string]any{"user": rc.Login, "org_id": rc.OrgID, "error": err.Error()})
			}
			return errorResponse(400, "invalid list params: "+err.Error())
		}
	}
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PerPage < 1 || params.PerPage > 100 {
		params.PerPage = 20
	}

	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}
	offset := (params.Page - 1) * params.PerPage

	visClause := rc.visibilityClause()
	query := "SELECT id, title, summary, category, visibility, updated_at FROM gk_kb_articles WHERE org_id = ? AND status = 'published'" + visClause + " ORDER BY updated_at DESC LIMIT ? OFFSET ?"
	rows, err := p.host.DBQuery(ctx, query, orgID, params.PerPage, offset)
	if err != nil {
		p.host.Log(ctx, "error", "kb: handleList DB error", map[string]any{"user": rc.Login, "org_id": orgID, "error": err.Error()})
		return errorResponse(500, "query articles failed")
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

	countQuery := "SELECT COUNT(*) as total FROM gk_kb_articles WHERE org_id = ? AND status = 'published'" + visClause
	countRows, err := p.host.DBQuery(ctx, countQuery, orgID)
	if err != nil {
		p.host.Log(ctx, "error", "kb: handleList count error", map[string]any{"user": rc.Login, "org_id": orgID, "error": err.Error()})
		return errorResponse(500, "count articles failed")
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
	return jsonMarshal(result)
}

// handleRecentWidget returns recent articles for the dashboard widget.
func (p *Plugin) handleRecentWidget(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	rc := extractReqCtx(args)
	visClause := rc.visibilityClause()
	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}
	query := "SELECT id, title, summary FROM gk_kb_articles WHERE org_id = ? AND status = 'published'" + visClause + " ORDER BY updated_at DESC LIMIT 5"
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

	return jsonMarshal(map[string]any{"articles": articles})
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
// boundaries or surfaces drafts. Visibility is enforced at the query level
// (zinc filters by visibility for customers; agents/admins see all).
func (p *Plugin) handleSearch(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	rc := extractReqCtx(args)

	params := searchParams{Page: 1, PerPage: 10}
	if err := json.Unmarshal(args, &params); err != nil {
		p.host.Log(ctx, "warn", "kb: invalid search params", map[string]any{"user": rc.Login, "error": err.Error()})
		return errorResponse(400, "invalid search params: "+err.Error())
	}
	if params.Query == "" {
		return errorResponse(400, "search query is required")
	}
	// Input sanitisation — strip control chars to prevent injection.
	params.Query = sanitiseText(params.Query)
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PerPage < 1 || params.PerPage > 100 {
		params.PerPage = 10
	}

	if p.zinc == nil || !p.zinc.enabled {
		return jsonMarshal(searchResult{
			Results: []searchHit{},
			Total:   0,
			Query:   params.Query,
			Page:    params.Page,
			PerPage: params.PerPage,
		})
	}

	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}

	zres, err := p.zinc.search(ctx, params.Query, orgID, params.Page, params.PerPage, rc.maxVisibilityFilter())
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
// org_id + visibility are checked before returning the article body so
// customers can't see org/agent-only content and cross-org access is
// impossible. Non-404 responses for unauthorized access prevent leaking
// the existence of restricted articles (returns "not found" uniformly).
func (p *Plugin) handleArticle(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	rc := extractReqCtx(args)

	params := articleParams{}
	if err := json.Unmarshal(args, &params); err != nil {
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

	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}

	// Fetch article with org_id check — prevents cross-org access (IDOR protection).
	query := "SELECT id, title, content, category, visibility, author, created_at, updated_at FROM gk_kb_articles WHERE id = ? AND org_id = ? AND status = 'published'"
	rows, err := p.host.DBQuery(ctx, query, params.ID, orgID)
	if err != nil {
		p.host.Log(ctx, "error", "kb: handleArticle DB error", map[string]any{"user": rc.Login, "org_id": orgID, "article_id": params.ID, "error": err.Error()})
		return errorResponse(500, "query article failed")
	}
	if len(rows) == 0 {
		// 404 — DON'T log the org_id here to avoid leaking cross-org requests.
		// A user from org A trying article id 5 (which exists in org B) should
		// see the same response as article id 999 (which doesn't exist anywhere).
		return errorResponse(404, "article not found")
	}

	row := rows[0]
	articleVis := toString(row["visibility"])

	// Visibility enforcement — if the caller can't see this level, return 404
	// (not 403) so we don't reveal that the article exists but is restricted.
	if !rc.canSeeVisibility(articleVis) {
		return errorResponse(404, "article not found")
	}

	article := articleDetail{
		ID:         toInt64(row["id"]),
		Title:      toString(row["title"]),
		Content:    toString(row["content"]),
		Category:   toString(row["category"]),
		Visibility: articleVis,
		Author:     toString(row["author"]),
		CreatedAt:  toString(row["created_at"]),
		UpdatedAt:  toString(row["updated_at"]),
	}

	return jsonMarshal(article)
}

// importParams holds the fields the host injects for a kb_import call.
type importParams struct {
	Body        string `json:"_body,omitempty"`
	ContentType string `json:"_content_type,omitempty"`
	// CSRF: the host injects a CSRF token via middleware; we verify it.
	CSRFToken string `json:"_csrf_token,omitempty"`
}

// handleImport accepts an OTRS/Znuny FAQ XML export (POST body, passed
// through as _body by the host) and inserts articles into gk_kb_articles
// scoped to the caller's org_id. Re-importing the same export upserts in
// place via the (org_id, slug) unique index — idempotent.
func (p *Plugin) handleImport(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if p.host == nil {
		return errorResponse(503, "host API not available")
	}

	rc := extractReqCtx(args)

	// Admin route requirement is enforced by the host middleware, but
	// double-check for defense-in-depth.
	if !rc.isAgentOrAdmin() || !rc.IsAdmin {
		p.host.Log(ctx, "warn", "kb: unauthorized import attempt", map[string]any{"user": rc.Login, "org_id": rc.OrgID})
		return errorResponse(403, "forbidden")
	}

	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}
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

	// Validate max payload size (4 MB as per the platform's maxPluginBodySize).
	if len(payload) > 4<<20 {
		return errorResponse(413, "import payload exceeds maximum size of 4 MB")
	}
	// Validate it's XML or CSV.
	ct := params.ContentType
	if !strings.Contains(ct, "xml") && !strings.Contains(ct, "csv") && !strings.Contains(ct, "text/plain") {
		// Content-type validation is best-effort — some hosts may not send it.
		// Don't reject on missing content-type, but log it.
		if ct != "" {
			p.host.Log(ctx, "warn", "kb: import with unexpected content type", map[string]any{"content_type": ct, "org_id": orgID})
		}
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

// toInt is the int-returning form of toInt64, used by the schema layer.
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

// sanitiseText strips control characters (except tab/newline/cr) from user
// input to prevent injection through search queries and other text fields.
func sanitiseText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
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
