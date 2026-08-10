# Changelog

All notable changes to the GoatFlow Knowledge Base plugin are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.1] - 2026-08-10

### Added

- **DB-agnostic SQL enforcement** — new `make lint` target runs `gk-sql-lint`
  (`github.com/goatkit/sql-lint`), keeping plugin SQL portable across
  MySQL/MariaDB and PostgreSQL. The plugin's HostAPI queries are already
  placeholder-safe at runtime; the lint now also guards against accidental raw
  non-portable SQL and MySQL-only statements (ON DUPLICATE KEY UPDATE /
  INSERT IGNORE / REPLACE INTO) as CI-enforced defense in depth.

### Changed

- Plugin version bumped to v0.1.1 (tagged release).

## [0.1.0] - 2026-07-04

First release. Production-ready knowledge base plugin with multi-tenant RBAC, zinc full-text search, OTRS FAQ import, and comprehensive security hardening.

### Added

- **TipTap image paste/drop** — paste screenshots or drag-drop image files into the editor. The image is uploaded as an attachment and stored as a file reference (`/kb/attachment/{id}`) rather than an inline data URL, keeping the content column size small. Works in richtext mode.
- **Insert Image toolbar button** — now opens a file picker when the URL prompt is cancelled, supporting both URL-based images and local file uploads from the editor toolbar.
- **Attachment lifecycle** (admin CRUD for article files): upload (`POST /admin/kb/article/:id/attachments`), delete (`DELETE /admin/kb/article/:id/attachments/:aid`), download (`GET /kb/attachment/:aid`), and automatic cleanup on article delete.
- **Attachment rendering** — article detail pages for all audiences display attachments with file-type icon SVGs and image thumbnails (loaded as blob URLs from the download endpoint).
- **`imageUploadUrl` config option** for `initTiptapEditor()` — shared paste/drop extension defaults to inline base64 when unset, uploads to the given URL when configured. KB admin sets it to the attachment endpoint; other editors (tickets) fall back to inline.
- **URL path param fallback** in `handleAttachmentUpload` — reads `id` from `args["params"]["id"]` when absent from the JSON body, so a paste/drop upload doesn't need to send the article ID in the body.
- **JS build validation** in GoatFlow `Makefile` — `make js-build` now greps for `createImagePasteExtension`, `imageUploadUrl`, and the extension call site, preventing regression of "defined but not wired" bugs.
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
- **Full 15-language i18n** — all UI strings translated across `ar, de, en, es, fa, fr, he, ja, pl, pt, ru, tlh, uk, ur, zh`. Templates use `{{ t("kb.key")|default:"English" }}` via a `t()` context function backed by `HostAPI.Translate` (identical pattern to GoatFlow host templates). Admin form inline HTML uses `p.tr(ctx, key, fallback)`. 93 translation keys in GoatFlow's `internal/platform/i18n/translations/*.json`. `renderTemplate` auto-injects a no-op `t` so tests pass without a host.

### Changed

- **Template DRY unification** — merged 5 duplicated customer/agent templates into 2 unified files: `kb_list.pongo2` (admin/agent/customer via `{% if IsAdmin %}` conditionals) and `article_detail.pongo2` (agent/customer). Template count reduced from 6 to 4.
- **Customer editorial layout adopted platform-wide** — 70ch centered, left gradient accent, ornamental `◇` divider, `kbFadeUp` animation.
- **Attachment upload ID source** — handler now falls back to URL path params (`args["params"]["id"]`) when the JSON body has no `id` field, enabling paste/drop images to upload without requiring the payload to duplicate the article ID.
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
- **`TestAddArticle_UpdatesTicketChangeTime`** — changed from hardcoded ticket ID 1 to `SELECT id FROM ticket ORDER BY id LIMIT 1` (same fix as `TestAddArticle_EmailHeaders`). Includes safe type assertion for response data.

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
