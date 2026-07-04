# GoatKit Knowledge Base Plugin

A multi-tenant knowledge base plugin for GoatFlow with article management, managed category taxonomy, tag chips, zinc full-text search, OTRS/Znuny FAQ import, and role-based access control (customer/agent/admin).

## Quick Start

```sh
# Build the gRPC plugin binary (uses Docker — no local Go needed)
make build

# Package plugin.yaml + binary into a ZIP
make package

# Deploy to the local GoatFlow instance (reads ADMIN_API_KEY from ../goatflow/.env)
make deploy

# Deploy to a remote instance
make deploy GOATFLOW_URL=https://kb.example.com

# Run the test suite
make test
```

## Routes

Two endpoint families serve different consumers:

### JSON API (`/knowledge-base`, `/kb/search`, `/kb/article/:id`)

Machine-readable responses for programmatic access — dashboard widgets, LLM queries, external integrations. Always returns `{"html": "...", "title": "...", "active_page": "..."}` or `{"error": "...", "status": N}`.

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/knowledge-base` | `kb_list` | List articles (org-scoped, permission-filtered) |
| GET | `/kb/search` | `kb_search` | Full-text search via zinc (org-scoped) |
| GET | `/kb/article/:id` | `kb_article` | Single article (permission-checked) |

### HTML Pages (human users)

Rendered via embedded pongo2 templates. Returned as `{"html": "..."}` fragments that GoatFlow wraps in the base layout.

| Method | Path | Handler | Audience |
|--------|------|---------|----------|
| GET | `/admin/kb` | `handleAdminList` | Admin — list all articles with CRUD controls |
| GET | `/admin/kb/article/:id` | `handleAdminArticle` | Admin — create/edit article with TipTap editor |
| POST | `/admin/kb/article` | `handleAdminArticleUpdate` | Admin — save article (create or update) |
| DELETE | `/admin/kb/article/:id` | `handleAdminArticleDelete` | Admin — delete article |
| GET | `/admin/kb/categories` | `handleAdminCategories` | Admin — manage category taxonomy |
| POST | `/admin/kb/categories` | `handleAdminCategories` | Admin — add/rename/delete categories |
| POST | `/admin/kb/import` | `kb_import` | Admin — import OTRS FAQ XML |
| GET | `/agent/kb` | `handleAgentList` | Agent — article list (all visibilities) |
| GET | `/agent/kb/article/:id` | `handleAgentArticle` | Agent — article detail |
| GET | `/customer/kb` | `handleCustomerList` | Customer — article list (public only) |
| GET | `/customer/kb/article/:id` | `handleCustomerArticle` | Customer — article detail |


### Dashboard Widget

| ID | Location | Description |
|----|----------|-------------|
| `kb-recent` | `/dashboard` | Recent 5 published articles, role-filtered |

## Article Visibility

All articles belong to an organisation (`org_id` is always set; no `org_id = 0`).  
Visibility controls access within the owning organisation:

| Visibility | Who sees it within owning org? |
|------------|-------------------------------|
| `public`   | All users (customers, agents, admins) |
| `agent`    | Agents and admins only |

Customers see only `public` articles. Agents see both. Admins see all regardless of visibility. Cross-org access is blocked at the query level — every SELECT includes `org_id = ?`.

## Managed Category Taxonomy

Categories are managed through `/admin/kb/categories` (admin only):

- **Add** a category via the inline form
- **Rename** categories in-place
- **Delete** safely — articles keep the category name as free-text, only the managed record is removed
- **Article count** shown per category in the management table
- **Article form** uses a `<select>` dropdown populated from the category table, with a "Manage" link next to the label. Existing free-text categories not in the managed list still appear as selectable options.

Underlying table: `gk_kb_categories (id, org_id, name, parent_id, created_at)` — `parent_id` is reserved for future hierarchy support.

## Tags

Tags are comma-separated (stored in `tags VARCHAR(500)`). The admin article form provides a chip-style input: type a tag and press comma or Enter to add it as a removable pill. Tags are indexed as full-text in zinc for boosted search relevance. OTRS `Keywords` maps to tags on import.

## OTRS / Znuny FAQ Import

`POST /admin/kb/import` accepts an OTRS/Znuny XML export as the request body. The importer:

- Parses `<FAQExport>`/`<FAQData>` envelopes containing `<FAQItem>`/`<FAQDataItem>` entries
- Maps `Title`, `Field_1` (body), `Field_2` (summary), `Keywords` (tags), `Category`, `Author`, `ValidID` (draft if not 1)
- Slugifies the title for URL-safe unique keys
- Idempotent — re-importing the same data upserts via the `(org_id, slug)` unique index
- Preserves `org_id` on every row
- Indexes imported articles in zinc when enabled

Maximum payload: 4 MB (platform-enforced).

## Zinc Full-Text Search

Optional integration with [ZincSearch](https://zincsearch-docs.zinc.dev/). Configured via environment variables:

| Env var | Description |
|---------|-------------|
| `GOATFLOW_PLUGIN_KB_ZINC_URL` | Zinc HTTP endpoint (e.g. `http://zinc:4080`) |
| `GOATFLOW_PLUGIN_KB_ZINC_USER` | Zinc basic auth username |
| `GOATFLOW_PLUGIN_KB_ZINC_PASSWORD` | Zinc basic auth password |

When unconfigured, search returns empty results gracefully — the KB still works via direct DB queries. When enabled, the client:

- Creates an index with field mappings on startup (`title`/`summary`/`content`/`tags` as analysed text)
- Indexes articles on import
- Runs queries against `title^3`, `summary^2`, `content`, `tags` with `org_id` + `status=published` + visibility filters
- Ensures cross-org isolation at the query engine level

## Database Schema

Versioned migrations run from `InitWithHost` and tracked in `gk_kb_schema_version`. Dialect-aware (MySQL/MariaDB and PostgreSQL). Current schema version: 2.

### Tables

| Table | Purpose |
|-------|---------|
| `gk_kb_articles` | Core articles: `id`, `org_id`, `title`, `slug`, `summary`, `content`, `category`, `visibility`, `author`, `status`, `tags`, `source`, `source_id`, `created_at`, `updated_at`. Unique index on `(org_id, slug)`. Indexes on `(org_id, status)`, `(org_id, category)`, `(org_id, visibility)`. |
| `gk_kb_categories` | Managed taxonomy: `id`, `org_id`, `name`, `parent_id`, `created_at`. Unique on `(org_id, name)`. |
| `gk_kb_attachments` | File attachments: `id`, `org_id`, `article_id`, `file_key`, `filename`, `mime_type`, `size_bytes`, `created_at`. Bytes stored in HostAPI file storage. |
| `gk_kb_schema_version` | Migration tracking: `version`, `applied_at`. |

## Build & Deploy

Build occurs inside a Docker container so there's no local Go toolchain requirement. The `go.mod` has replace directives for sibling repos (goatflow); both the project and goatflow are mounted during the container build.

```sh
make build       # Build the binary
make test        # Run all tests
make package     # Create bin/kb.zip (binary + plugin.yaml)
make deploy      # Upload to GoatFlow via API
```

The plugin hot-reloads on deploy — GoatFlow unloads the old binary, extracts the new package, and the plugin starts serving immediately.

## Project Structure
```
cmd/kb-plugin/          # Main entry point
internal/kb/
  plugin.go             # Plugin registration, routes, dispatch
  handlers.go           # All request handlers (admin, agent, customer)
  schema.go             # DB schema + migrations (dialect-aware)
  template.go           # Embedded pongo2 template compilation/rendering
  zinc.go               # Zinc REST client
  import.go             # OTRS/Znuny FAQ XML import
  templates/*.pongo2    # Embedded page templates
    kb_list.pongo2           # Unified article list (admin/agent/customer)
    article_detail.pongo2    # Article detail page (agent + customer)
    admin_kb_categories.pongo2  # Category management
    widget_recent.pongo2     # Dashboard recent-articles widget
```

## Security

### Authentication & Authorization

- All endpoints require authentication (session or token) via middleware
- Admin write endpoints (`/admin/kb/article`, `DELETE`, import, categories) require `admin` middleware
- Agent/customer endpoints require `auth` middleware (no admin-level access)

### Cross-Org Protection (IDOR)

Every query includes `org_id = ?` to prevent cross-org data access. The `reqCtx.visibilityClause()` helper appends `AND visibility = 'public'` for customers while agents and admins see all articles in their org.

Information disclosure prevention: 404 vs 403 is not distinguished — unauthorised and non-existent articles return the same response so attackers cannot probe for the existence of restricted content.

### Stored XSS Prevention

Article content is authored via TipTap (rich text editor) and rendered with `{{ Content|safe }}` in pongo2 templates — auto-escaping is intentionally bypassed because the content carries formatting HTML. To prevent XSS, all content is sanitized through **bluemonday** (v1.0.26) with a policy matching the GoatFlow host platform:

- **Render-time sanitization**: `sanitiseHTML()` applied in `renderArticleDetail` before passing to the template — protects all viewers immediately, including existing articles
- **Storage-time sanitization**: Applied in `handleAdminArticleUpdate` before INSERT/UPDATE — defense-in-depth

**Allowed elements:** `b`, `strong`, `i`, `em`, `u`, `s`, `strike`, `del`, `h1`–`h6`, `p`, `br`, `hr`, `ul`, `ol`, `li`, `blockquote`, `code`, `pre`, `table`, `thead`, `tbody`, `tfoot`, `tr`, `th`, `td` (with `colspan`/`rowspan`), `img` (with `src`/`alt`/`title`/`width`/`height`, `http`/`https`/`data` URLs), `a` (with `href`, `http`/`https`/`mailto` URLs, `nofollow` + `noreferrer` + `target=_blank` applied).

**Stripped:** `<script>`, `<iframe>`, `<object>`, `<embed>`, `<form>`, `<input>`, `<button>`, `<select>`, `<textarea>`, `<style>`, `<link>`, `<meta>`, `<base>`, all `on*` event handlers, `javascript:` URLs.

**`class`** allowed on common elements (space-separated tokens only). **`style`** allowed on `span` and `mark` for color/highlight.

### HTML Attribute Injection

Fields rendered into `value="%s"` attributes use `html.EscapeString()` rather than `sanitiseText()` — the latter only strips control characters and does **not** escape `"`, `&`, or `<`, making it unsuitable for attribute contexts.

### DOM XSS in Admin UI

The admin article edit form uses inline JavaScript for tag chip management. Tag names are set via `document.createTextNode()` + `insertAdjacentHTML()` rather than `innerHTML` — preventing parsed HTML injection from tag names like `<img src=x onerror=alert(1)>`.

### JavaScript String Injection

The admin form embeds the session login as a JS string literal (`author: "..."`). All JavaScript-special characters are escaped: `\` → `\\`, `"` → `\"`, newlines → `\n`/`\r`, and `</` → `<\/` (prevents `</script>` tag breakout).

### LIKE Wildcard Search Escaping

Free-text search uses SQL `LIKE` with `%` wildcards. Search terms containing `%` or `_` are escaped via `escapeLike()` to prevent unintended broad matching:

| Character | Escaped to | Reason |
|-----------|------------|--------|
| `\` | `\\` | Escape the escape character |
| `%` | `\%` | Prevents wildcard matching any sequence |
| `_` | `\_` | Prevents wildcard matching any single char |

No explicit `ESCAPE` clause is needed — `\` is the default LIKE escape character in MySQL, MariaDB, and PostgreSQL (GoatFlow's supported databases).

### CSRF

Admin write endpoints accept `Content-Type: application/json` which triggers a CORS preflight on cross-origin requests, preventing form-based CSRF. No explicit CSRF token is needed — the preflight requirement is the mitigation.

### Audit Logging

Article deletion is logged via `p.host.Log` with structured fields (`org_id`, `user`, `user_id`, `article_id`, `title`) — visible in the host's operational logs.

### Input Validation

- All text fields: control characters stripped via `sanitiseText()`
- Article content: HTML sanitized via `sanitiseHTML()` (bluemonday)
- Article `Status` validated against `["draft", "published", "archived"]`
- Article `Visibility` validated against `["public", "agent"]`
- Pagination `PerPage` clamped to `[1, 100]`
- Import payload limited to 4 MB
- Article IDs validated as positive integers
- Category names limited to 100 characters
- Category names sanitized via `sanitiseText()` at storage time (add + rename)

## Configuration

The plugin expects no mandatory configuration. Optional Zinc search is configured via env vars prefixed with `GOATFLOW_PLUGIN_KB_`. All database access goes through the HostAPI — no direct database connections.

### Deployment Secrets

Deployment credentials and runtime secrets are stored in the GoatFlow host's `.env` file (`../goatflow/.env`), never in the plugin repo or in code. The `make deploy` target reads these automatically:

| Variable | Required | Purpose |
|----------|----------|---------|
| `ADMIN_API_KEY` | One of the two | Bearer token for plugin upload API |
| `ADMIN_PASSWORD` | (alternative) | Password fallback — `make deploy` auto-logs in with `ADMIN_USER` to obtain a token |
| `ADMIN_USER` | If using password | Login username for token exchange |
| `GOATFLOW_URL` | No (default: `http://localhost:8080`) | Target GoatFlow instance |

The `.env` file must **not** be committed to version control — it's in GoatFlow's `.gitignore`. The plugin itself stores no credentials; it inherits authentication from the host's session/middleware at runtime.
