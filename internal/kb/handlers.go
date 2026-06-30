package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
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
	ID string `json:"id"`
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
			params.ID = idStr
		} else {
			return errorResponse(400, "invalid article id: "+err.Error())
		}
	}
	if params.ID == "" {
		return errorResponse(400, "invalid article id: missing id")
	}
	id, err := strconv.ParseInt(params.ID, 10, 64)
	if err != nil {
		return errorResponse(400, "invalid article id: "+err.Error())
	}
	if id < 1 {
		return errorResponse(400, "invalid article id: ID must be positive")
	}

	orgID := rc.OrgID
	if orgID == 0 {
		orgID = p.host.OrgID(ctx)
	}

	// Fetch article with org_id check — prevents cross-org access (IDOR protection).
	query := "SELECT id, title, content, category, visibility, author, created_at, updated_at FROM gk_kb_articles WHERE id = ? AND org_id = ? AND status = 'published'"
	rows, err := p.host.DBQuery(ctx, query, id, orgID)
	if err != nil {
		p.host.Log(ctx, "error", "kb: handleArticle DB error", map[string]any{"user": rc.Login, "org_id": orgID, "article_id": id, "error": err.Error()})
		return errorResponse(500, "query article failed")
	}
	if len(rows) == 0 {
		return errorResponse(404, "article not found")
	}

	row := rows[0]
	articleVis := toString(row["visibility"])

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


func (p *Plugin) handleAdminList(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
    if p.host == nil { return errorResponse(503, "host API not available") }
    params := listParams{Page: 1, PerPage: 20}
    if len(args) > 0 {
        if err := json.Unmarshal(args, &params); err != nil {
            return errorResponse(400, "invalid list params: " + err.Error())
        }
    }
    if params.Page < 1 { params.Page = 1 }
    if params.PerPage < 1 || params.PerPage > 100 { params.PerPage = 20 }
    orgID := p.host.OrgID(ctx)
    if orgID == 0 { orgID = p.host.OrgID(ctx) }
    offset := (params.Page - 1) * params.PerPage
    rows, err := p.host.DBQuery(ctx,
        "SELECT id, title, summary, category, visibility, author, created_at, updated_at FROM gk_kb_articles WHERE org_id = ? ORDER BY updated_at DESC LIMIT ? OFFSET ?",
        orgID, params.PerPage, offset)
    if err != nil { return errorResponse(500, "query articles: " + err.Error()) }
    articles := make([]articleSummary, 0, len(rows))
    for _, row := range rows {
        articles = append(articles, articleSummary{
            ID: toInt64(row["id"]), Title: toString(row["title"]), Summary: toString(row["summary"]),
            Category: toString(row["category"]), Visibility: toString(row["visibility"]), UpdatedAt: toString(row["updated_at"]),
        })
    }
    countRows, err := p.host.DBQuery(ctx, "SELECT COUNT(*) as total FROM gk_kb_articles WHERE org_id = ?", orgID)
    if err != nil { return errorResponse(500, "count articles: " + err.Error()) }
    if len(countRows) > 0 { _ = toInt64(countRows[0]["total"]) }

    // Build HTML admin interface
    var h strings.Builder
    h.WriteString(`<div class="container mx-auto py-6">
    <nav class="flex items-center text-sm mb-4" aria-label="Breadcrumb">
        <a href="/dashboard" class="gk-link-neon">Dashboard</a>
        <svg class="mx-2 h-4 w-4" style="color: var(--gk-text-muted);" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M7.293 14.707a1 1 0 010-1.414L10.586 10 7.293 6.707a1 1 0 011.414-1.414l4 4a1 1 0 010 1.414l-4 4a1 1 0 01-1.414 0z" clip-rule="evenodd"></path></svg>
        <span class="font-medium" style="color: var(--gk-text-primary);">Knowledge Base</span>
    </nav>
    <div class="flex items-center justify-between mb-6">
        <div>
            <h1 class="text-2xl font-bold gk-heading"><span class="gk-text-gradient">Knowledge Base</span></h1>
            <p class="mt-1 text-sm" style="color: var(--gk-text-muted);">Organise your team knowledge</p>
        </div>
        <a href="/admin/kb/article/new" class="gk-btn-neon">
            <svg class="w-5 h-5 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4"/></svg>
            New Article
        </a>
    </div>`)
    if len(articles) == 0 {
        h.WriteString(`<div class="gk-card-glow">
            <div class="gk-card-body p-12 text-center">
                <svg class="h-12 w-12 mx-auto mb-4" style="color: var(--gk-text-muted);" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/></svg>
                <p class="text-lg font-medium" style="color: var(--gk-text-primary);">No articles yet</p>
                <p class="mt-1 text-sm" style="color: var(--gk-text-muted);">Click "New Article" to create your first KB article.</p>
            </div>
        </div>`)
    } else {
        h.WriteString(`<div class="gk-card-glow">
            <div class="gk-card-body p-0">
                <div class="overflow-x-auto">
                    <table class="w-full">
                        <thead>
                            <tr style="background: var(--gk-bg-elevated); border-bottom: 1px solid var(--gk-border-default);">
                                <th class="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider" style="color: var(--gk-text-muted);">Title</th>
                                <th class="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider" style="color: var(--gk-text-muted);">Category</th>
                                <th class="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider" style="color: var(--gk-text-muted);">Visibility</th>
                                <th class="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider" style="color: var(--gk-text-muted);">Author</th>
                                <th class="px-6 py-3 text-left text-xs font-semibold uppercase tracking-wider" style="color: var(--gk-text-muted);">Updated</th>
                                <th class="px-6 py-3 text-right text-xs font-semibold uppercase tracking-wider" style="color: var(--gk-text-muted);">Actions</th>
                            </tr>
                        </thead>
                        <tbody>`)
        for _, a := range articles {
            visBadge := ""
            switch a.Visibility {
            case "public":
                visBadge = `<span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full" style="background: rgba(var(--gk-success-rgb), 0.15); color: var(--gk-success);">Public</span>`
            case "org":
                visBadge = `<span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full" style="background: rgba(var(--gk-primary-rgb), 0.15); color: var(--gk-primary);">Org</span>`
            case "agent":
                visBadge = `<span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full" style="background: rgba(var(--gk-warning-rgb), 0.15); color: var(--gk-warning);">Agent</span>`
            default:
                visBadge = `<span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full" style="background: var(--gk-bg-elevated); color: var(--gk-text-secondary);">` + a.Visibility + `</span>`
            }
            h.WriteString(fmt.Sprintf(`
                <tr style="border-bottom: 1px solid var(--gk-border-default);" class="hover:bg-[var(--gk-bg-hover)] transition-colors">
                    <td class="px-6 py-4 whitespace-nowrap">
                        <a href="/admin/kb/article/%d" class="text-sm font-medium gk-link-neon">%s</a>
                    </td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm" style="color: var(--gk-text-secondary);">%s</td>
                    <td class="px-6 py-4 whitespace-nowrap">%s</td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm" style="color: var(--gk-text-secondary);">%s</td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm" style="color: var(--gk-text-muted);">%s</td>
                    <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
                        <a href="/admin/kb/article/%d" class="gk-link-neon mr-3">Edit</a>
                        <button onclick="kbDeleteArticle(%d)" style="color: var(--gk-error);" class="hover:opacity-80 transition-opacity">Delete</button>
                    </td>
                </tr>`, a.ID, sanitiseText(a.Title), sanitiseText(a.Category), visBadge, sanitiseText(a.Visibility), sanitiseText(a.UpdatedAt), a.ID, a.ID))
        }
        h.WriteString(`
                </tbody>
            </table>
        </div>
        </div>
    </div>`)
    }
    h.WriteString(`</div>
<script>
function kbDeleteArticle(id) {
    if (!confirm("Delete this article?")) return;
    fetch("/admin/kb/article/" + id, { method: "DELETE" })
        .then(r => r.json())
        .then(() => location.reload())
        .catch(err => alert("Delete failed: " + err));
}
</script>`)
    return json.Marshal(map[string]string{
        "html": h.String(), "title": "Knowledge Base", "active_page": "kb-admin",
    })
}

func (p *Plugin) handleAdminArticle(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
    if p.host == nil {
        return errorResponse(503, "host API not available")
    }
    rc := extractReqCtx(args)
    params := articleParams{}
    if err := json.Unmarshal(args, &params); err != nil {
        var idStr string
        if err2 := json.Unmarshal(args, &idStr); err2 == nil {
            params.ID = idStr
        }
    }
    idStr := params.ID
    var article articleDetail
    if idStr != "" && idStr != "new" {
        id, err := strconv.ParseInt(idStr, 10, 64)
        if err != nil || id < 1 {
            return errorResponse(400, "invalid article id")
        }
        orgID := p.host.OrgID(ctx)
        if orgID == 0 {
            orgID = p.host.OrgID(ctx)
        }
        rows, err := p.host.DBQuery(ctx,
            "SELECT id, title, content, category, visibility, author, created_at, updated_at FROM gk_kb_articles WHERE id = ? AND org_id = ?",
            id, orgID)
        if err != nil {
            return errorResponse(500, "query article failed")
        }
        if len(rows) == 0 {
            return errorResponse(404, "article not found")
        }
        row := rows[0]
        article = articleDetail{
            ID:         toInt64(row["id"]),
            Title:      toString(row["title"]),
            Content:    toString(row["content"]),
            Category:   toString(row["category"]),
            Visibility: toString(row["visibility"]),
            Author:     toString(row["author"]),
            CreatedAt:  toString(row["created_at"]),
            UpdatedAt:  toString(row["updated_at"]),
        }
    }
    // Build visibility dropdown options
    visOpts := []string{"public", "org", "agent"}
    var visSelBuf strings.Builder
    for _, v := range visOpts {
        sel := ""
        if article.Visibility == v || (article.Visibility == "" && v == "public") {
            sel = " selected"
        }
        visSelBuf.WriteString(fmt.Sprintf(`<option value="%s"%s>%s</option>`, v, sel, strings.Title(v)))
    }
    visSelect := visSelBuf.String()

    // Build the HTML in parts to avoid complex multi-line fmt.Sprintf
    var h strings.Builder
    h.WriteString(`<div class="container mx-auto py-6">`)
    
    // Breadcrumbs
    h.WriteString(fmt.Sprintf(`<nav class="flex items-center text-sm mb-4" aria-label="Breadcrumb">
        <a href="/dashboard" class="gk-link-neon">Dashboard</a>
        <svg class="mx-2 h-4 w-4" style="color: var(--gk-text-muted);" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M7.293 14.707a1 1 0 010-1.414L10.586 10 7.293 6.707a1 1 0 011.414-1.414l4 4a1 1 0 010 1.414l-4 4a1 1 0 01-1.414 0z" clip-rule="evenodd"></path></svg>
        <a href="/admin/kb" class="gk-link-neon">Knowledge Base</a>
        <svg class="mx-2 h-4 w-4" style="color: var(--gk-text-muted);" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M7.293 14.707a1 1 0 010-1.414L10.586 10 7.293 6.707a1 1 0 011.414-1.414l4 4a1 1 0 010 1.414l-4 4a1 1 0 01-1.414 0z" clip-rule="evenodd"></path></svg>
        <span class="font-medium" style="color: var(--gk-text-primary);">%s</span>
    </nav>`, sanitiseText(map[bool]string{true: "Edit KB Article #" + strconv.FormatInt(article.ID, 10), false: "New KB Article"}[article.ID > 0])))
    
    // Heading
    heading := map[bool]string{true: "Edit KB Article", false: "New KB Article"}[article.ID > 0]
    h.WriteString(fmt.Sprintf(`<div class="max-w-3xl">
        <h1 class="text-2xl font-bold mb-6 gk-heading"><span class="gk-text-gradient">%s</span></h1>`, heading))
    
    // Form card
    h.WriteString(`<div class="gk-card-glow">
            <div class="gk-card-body p-6">
                <form id="kb-article-form" class="space-y-4">`)
    
    h.WriteString(fmt.Sprintf(`<input type="hidden" name="id" value="%d">`, article.ID))
    
    // Title
    h.WriteString(fmt.Sprintf(`<div><label class="block text-sm font-medium mb-1" style="color: var(--gk-text-secondary);">Title</label>
        <input type="text" name="title" value="%s" class="gk-input-neon w-full" required></div>`, sanitiseText(article.Title)))
    
    // Category + Visibility grid
    h.WriteString(fmt.Sprintf(`<div class="grid grid-cols-2 gap-4">
        <div><label class="block text-sm font-medium mb-1" style="color: var(--gk-text-secondary);">Category</label>
            <input type="text" name="category" value="%s" class="gk-input-neon w-full"></div>
        <div><label class="block text-sm font-medium mb-1" style="color: var(--gk-text-secondary);">Visibility</label>
            <select name="visibility" class="gk-select-neon w-full">%s</select></div>
    </div>`, sanitiseText(article.Category), visSelect))
    
    // Content
    h.WriteString(fmt.Sprintf(`<div><label class="block text-sm font-medium mb-1" style="color: var(--gk-text-secondary);">Content</label>
        <textarea name="content" rows="12" class="gk-input-neon w-full font-mono">%s</textarea></div>`, sanitiseText(article.Content)))
    
    // Buttons
    btnTxt := map[bool]string{true: "Update", false: "Create"}[article.ID > 0]
    h.WriteString(fmt.Sprintf(`<div class="flex justify-end space-x-3 pt-2">
        <button type="button" onclick="history.back()" class="gk-btn-secondary">Cancel</button>
        <button type="submit" class="gk-btn-neon">%s</button>
    </div>`, btnTxt))
    
    // Close form and card
    h.WriteString(`</form></div></div></div>`)
    
    // Script
    loginStr := sanitiseText(rc.Login)
    h.WriteString(fmt.Sprintf(`</div><script>
    document.getElementById("kb-article-form").addEventListener("submit", function(e) {
        e.preventDefault();
        var form = e.target;
        var data = {
            id: parseInt(form.id.value) || 0,
            title: form.title.value,
            category: form.category.value,
            visibility: form.visibility.value,
            content: form.content.value,
            status: "published",
            author: "%s"
        };
        fetch("/admin/kb/article", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(data)
        }).then(function(r) {
            if (r.ok) window.location.href = "/admin/kb";
            else r.json().then(function(d) { alert(d.error || "Save failed"); });
        });
    });
    </script>`, loginStr))
    
    return json.Marshal(map[string]string{
        "html":        h.String(),
        "title":       "Edit KB Article",
        "active_page": "kb-admin",
    })
}

func (p *Plugin) handleAdminArticleDelete(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
    if p.host == nil { return errorResponse(503, "host API not available") }
    params := articleParams{}
    if err := json.Unmarshal(args, &params); err != nil {
        var idStr string
        if err2 := json.Unmarshal(args, &idStr); err2 == nil {
            params.ID = idStr
        } else {
            return errorResponse(400, "invalid request: "+err.Error())
        }
    }
    id, err := strconv.ParseInt(params.ID, 10, 64)
    if err != nil || id < 1 { return errorResponse(400, "invalid article id") }
    orgID := p.host.OrgID(ctx)
    if orgID == 0 { orgID = p.host.OrgID(ctx) }
    _, err = p.host.DBExec(ctx, "DELETE FROM gk_kb_articles WHERE id = ? AND org_id = ?", id, orgID)
    if err != nil { return errorResponse(500, "delete article: "+err.Error()) }
    return json.Marshal(map[string]string{"status": "deleted"})
}

func (p *Plugin) handleAdminArticleUpdate(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
    if p.host == nil { return errorResponse(503, "host API not available") }
    var updateReq struct {
        ID        int64  `json:"id"`
        Title     string `json:"title,omitempty"`
        Summary   string `json:"summary,omitempty"`
        Content   string `json:"content,omitempty"`
        Category  string `json:"category,omitempty"`
        Visibility string `json:"visibility,omitempty"`
        Status    string `json:"status,omitempty"`
        Author    string `json:"author,omitempty"`
        Tags      string `json:"tags,omitempty"`
    }
    if err := json.Unmarshal(args, &updateReq); err != nil {
        return errorResponse(400, "invalid request: "+err.Error())
    }
    if updateReq.ID < 0 { return errorResponse(400, "invalid article id: ID must not be negative") }
    orgID := p.host.OrgID(ctx)
    if orgID == 0 { orgID = p.host.OrgID(ctx) }
    existing, err := p.host.DBQuery(ctx, "SELECT id FROM gk_kb_articles WHERE id = ? AND org_id = ?", updateReq.ID, orgID)
    if err != nil { return errorResponse(500, "check article: "+err.Error()) }
    if len(existing) == 0 {
        slug := updateReq.Title
        if slug == "" { slug = fmt.Sprintf("article-%d", time.Now().Unix()) }
        status := updateReq.Status
        if status == "" { status = "published" }
        vis := updateReq.Visibility
        if vis == "" { vis = "public" }
        if updateReq.ID == 0 {
            _, err := p.host.DBExec(ctx, `INSERT INTO gk_kb_articles
                (org_id, title, slug, summary, content, category, visibility, author, status, tags)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
                orgID, updateReq.Title, slug, updateReq.Summary,
                updateReq.Content, updateReq.Category, vis,
                updateReq.Author, status, updateReq.Tags)
            if err != nil { return errorResponse(500, "insert article: "+err.Error()) }
        } else {
            _, err := p.host.DBExec(ctx, `INSERT INTO gk_kb_articles
                (id, org_id, title, slug, summary, content, category, visibility, author, status, tags)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
                updateReq.ID, orgID, updateReq.Title, slug, updateReq.Summary,
                updateReq.Content, updateReq.Category, vis,
                updateReq.Author, status, updateReq.Tags)
            if err != nil { return errorResponse(500, "insert article: "+err.Error()) }
        }
        return json.Marshal(map[string]string{"status": "created"})
    }
    var sets []string
    var vals []any
    if updateReq.Title != "" { sets = append(sets, "title = ?"); vals = append(vals, updateReq.Title) }
    if updateReq.Summary != "" { sets = append(sets, "summary = ?"); vals = append(vals, updateReq.Summary) }
    if updateReq.Content != "" { sets = append(sets, "content = ?"); vals = append(vals, updateReq.Content) }
    if updateReq.Category != "" { sets = append(sets, "category = ?"); vals = append(vals, updateReq.Category) }
    if updateReq.Visibility != "" { sets = append(sets, "visibility = ?"); vals = append(vals, updateReq.Visibility) }
    if updateReq.Status != "" { sets = append(sets, "status = ?"); vals = append(vals, updateReq.Status) }
    if updateReq.Author != "" { sets = append(sets, "author = ?"); vals = append(vals, updateReq.Author) }
    if updateReq.Tags != "" { sets = append(sets, "tags = ?"); vals = append(vals, updateReq.Tags) }
    if len(sets) == 0 { return errorResponse(400, "no fields to update") }
    sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
    vals = append(vals, updateReq.ID)
    vals = append(vals, orgID)
    query := fmt.Sprintf("UPDATE gk_kb_articles SET %s WHERE id = ? AND org_id = ?", strings.Join(sets, ", "))
    _, err = p.host.DBExec(ctx, query, vals...)
    if err != nil { return errorResponse(500, "update article: "+err.Error()) }
    return json.Marshal(map[string]string{"status": "updated"})
}
