# Changelog

All notable changes to the GoatFlow Knowledge Base plugin are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - 2026-07-04

First release. Production-ready knowledge base plugin with multi-tenant RBAC, zinc full-text search, OTRS FAQ import, and comprehensive security hardening.

### Added

- **In-page article search** with text highlighting — TreeWalker-based DOM-safe highlighting (avoids `innerHTML.replace` breaking code blocks). Segmented toggle between "In Page" and "All Articles" modes. Hit count, position indicator, prev/next navigation, and clear button.
- **Keyboard shortcuts** — `n`/`p` (Vim-style) for next/previous match navigation, gated on non-editable focus.
- **Article detail sidebar** — sticky sidebar with Related Articles (same category, limit 5) and Recent Articles (org-wide, limit 5), scoped by `visibilityClause()` per audience. Hidden ≤960px.
- **Summary display** in search results and list views — truncated italic summary under title in all list table rows.
- **Tag chip input** in admin article form — type and press comma/Enter to add removable pill badges. Tags stored as comma-separated in `tags` column, indexed as full-text in zinc.
- **Tags display** on agent and customer article pages — rendered as styled chips between summary and content.
- **Category taxonomy management** — full admin CRUD at `/admin/kb/categories`: add, rename, delete categories. Article count per category shown. Free-text categories still appear as selectable options.
- **Schema migration v2** — converts existing `org` visibility rows to `agent`.
- **Comprehensive README** covering all routes, visibility model, categories, tags, OTRS import, zinc search, schema, build/deploy, security, and configuration.
- **Trivy security scanning** — `make trivy-scan` target scanning for vulnerabilities, secrets, and misconfigurations.
- **Security regression test suite** — 31 tests across 3 files covering XSS sanitization, LIKE escaping, control-char stripping, enum validation, SPA navigation patterns, and source-level checks for inline JS security.

### Changed

- **Template DRY unification** — merged 5 duplicated customer/agent templates into 2 unified files: `kb_list.pongo2` (admin/agent/customer via `{% if IsAdmin %}` conditionals) and `article_detail.pongo2` (agent/customer). Template count reduced from 6 to 4.
- **Customer editorial layout adopted platform-wide** — 70ch centered, left gradient accent, ornamental `◇` divider, `kbFadeUp` animation.
- **Agent tag style adopted platform-wide** — `border-radius: 9999px`, `background: var(--gk-primary-subtle)`.
- **Visibility simplified from 3 levels to 2** — `org` and `agent` were functionally identical. Removed `org`. Labels: `public` → "Users & Agents", `agent` → "Agents Only".
- **Theme-aware styling** — all CSS uses `--gk-*` custom properties (`--gk-primary-subtle`, `color-mix(in srgb, var(--gk-primary) N%, transparent)`) instead of non-existent `--gk-primary-rgb` triplet variables.
- **Agent handler now SELECTs `visibility` column** (was dead code before).
- **Breadcrumb fix** — list page `←` changed from self-referencing route to `/dashboard`.
- **golang.org/x/net** bumped v0.53.0 → v0.55.0 (4 HIGH CVEs: CVE-2026-25681, CVE-2026-27136, CVE-2026-39821, CVE-2026-42502).
- **Category input** changed from free-text `<input>` to `<select>` dropdown.
- **Audit logging** — article deletion now logged via `p.host.Log` with structured fields (was `log.Printf` to stdout).

### Fixed

- **Summary not passed to article detail views** — added `summary` to SELECT queries and template data.
- **Tags not passed to article detail views** — added `tags` to SELECT queries. Tags pre-split into `[]string` in Go.
- **Search input focus shadow** fixed from broken `rgba(var(--gk-primary-rgb), 0.15)` → `var(--gk-primary-subtle)`.
- **4 badge backgrounds** in list page fixed from `rgba(var(--gk-*-rgb), 0.15)` → `var(--gk-*-subtle)`.
- **Prev/next wiring** — copy-paste bug had prev button calling `nextHit` instead of `prevHit`.
- **Duplicate `.kb-hit-active` CSS block** removed (cascade would have overridden the theme fix).
- **SQL syntax error on MariaDB** — removed `ESCAPE '\\'` clauses from all LIKE queries. Go's `'\\'` produces SQL `'\'` which MariaDB interprets as escaping the closing quote, breaking the query. Backslash is already the default escape — the clause was unnecessary.
- **SPA navigation bypass** — `window.location.href` and `location.reload()` bypass GoatFlow's SPA router, returning raw JSON instead of HTML. Replaced with `<form>.submit()` and `<a>.click()` across all JS navigation paths (article search, admin save, category actions, delete refresh).
- **Dialect-agnostic migration v2** — removed MySQL-specific `IGNORE` keyword from migration v2 UPDATE (no unique key involved, so `IGNORE` was pointless). Both mysql/postgres fields now identical.
- **Plugin name test** — updated `TestGKRegisterMilestoneOneContract` to expect `"goat-kb"` instead of stale `"kb"`.
- **Schema test fakeHost** — added handler for migration v2 `UPDATE ... SET visibility` statement to prevent panic on `args[10]`.
- **Delete refresh preserves query params** — kb_list delete handler now uses `pathname + search` instead of just `pathname`, so filters aren't lost after deleting an article.

### Security

- **Stored XSS prevention** — article content sanitized via bluemonday v1.0.26 (policy matching GoatFlow host). Applied at render-time (`sanitiseHTML()` in `renderArticleDetail`) and storage-time (`handleAdminArticleUpdate`) for defense-in-depth. Strips `<script>`, `<iframe>`, `on*` handlers, `javascript:` URLs.
- **Admin form attribute injection** — `value="%s"` attributes now use `html.EscapeString()` instead of `sanitiseText()` (which only strips control chars, doesn't escape `"`).
- **Tag chip DOM XSS** — `addArticleTag()` rewritten from `innerHTML` to `textContent` + `createTextNode()`, preventing HTML injection from tag names.
- **JavaScript string injection** — `loginJS` now escapes `\`, `"`, `\n`, `\r`, and `</` → `<\/` (prevents `</script>` tag breakout).
- **LIKE wildcard escaping** — `escapeLike()` escapes `\` → `\\`, `%` → `\%`, `_` → `\_` on all LIKE queries. No explicit `ESCAPE` clause (backslash is the default escape in MySQL/MariaDB/PostgreSQL).
- **Category name storage sanitization** — `sanitiseText()` applied in add + rename cases of `handleAdminCategoryAction`.
- **Enum validation** — `visibility` validated against `["public", "agent"]`, `status` against `["draft", "published", "archived"]`.
- **Caller-supplied ID on insert** removed — all inserts use auto-increment only.
- **Dead CSRFToken field** removed from `importParams` struct (was never validated, implied false protection; real CSRF mitigation is JSON Content-Type preflight).
- **Post-query visibility check** (`canSeeVisibility`) retained as defense-in-depth alongside SQL WHERE clause filtering.

### Removed

- **Deleted templates**: `customer_kb_article.pongo2`, `agent_kb_article.pongo2`, `customer_kb_list.pongo2`, `agent_kb_list.pongo2`, `customer_kb_search.pongo2` (merged into unified templates).
- **Deleted `handlers.go.backup`** — stale pre-edit backup.
- **Removed dead code**: `buildSelectOptions()` helper, `net/url` import, duplicate CSS blocks.
- **Deleted `cookies.txt`** — dev artifact containing stale JWT session tokens.
