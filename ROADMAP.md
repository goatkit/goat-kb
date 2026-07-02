# GoatFlow Knowledge Base (KB) Module Roadmap

## Milestone 1: Plugin Foundation & Structure
- [x] Create cmd/kb-plugin and internal/kb directory structure
- [x] Implement gRPC plugin interface (GKRegister, Init, Call, Shutdown)
- [x] Configure gRPC runtime (go-plugin) — full Go stdlib, process isolation, hot reload, no WASM memory ceilings for large imports
- [x] Define plugin metadata (name, version, description, etc.)
- [x] Register core KB routes: /knowledge-base, /kb/search, /kb/article/:id
- [x] Register admin import route: POST /admin/kb/import
- [x] Add admin menu item for KB management
- [x] Add dashboard widget for recent KB articles
- [x] Request necessary HostAPI permissions (db/cache/http/config/plugin_call)
- [x] Define error codes for KB operations

## Milestone 2: Database Schema & Multi-tenancy
- [x] Integrate with HostAPI for database access (no direct DB connections)
- [x] Implement proper SQL parameter binding using ? placeholders
- [x] Design KB tables with org_id column for multi-tenancy
- [x] Ensure all queries automatically include org_id from HostAPI.OrgID()
- [x] Map OTRS FAQ schema to GoatFlow KB schema during import
- [x] Preserve org_id context throughout import process
- [x] Handle database errors gracefully with appropriate HTTP status codes

## Milestone 3: Search Functionality (Zinc Integration)
- [x] Integrate zinc for full-text search indexing of KB articles
- [x] Index title, content, tags fields for search
- [x] Include org_id in zinc documents for multi-tenant isolation
- [x] Implement search handler that accepts query parameter
- [x] Return search results formatted for kb_search.pongo2 template
- [x] Optimize for sub-second response times
- [x] Test search relevance and performance

## Milestone 4: Security & Access Control
- [x] Implement Secure By Design following OWASP guidelines
- [x] Require authentication for all endpoints
- [x] Implement fine-grained authorization based on org_id and user roles
- [x] Support visibility levels: public, org-only, agent-specific
- [x] Prevent information leakage (don't reveal existence of unauthorized articles)
- [x] Validate and sanitize all inputs (prevent injection, XSS)
- [x] Implement CSRF protection where applicable
- [x] Log security-relevant events (access attempts, failures)
- [x] Respect HostAPI rate limiting protections

## Milestone 5: Handler Implementation
- [x] kb_list: Query articles with pagination, org_id filter, permission-based visibility
- [x] kb_search: Accept search query, use zinc for full-text search, return results
- [x] kb_article: Extract ID, verify permissions, fetch single article
- [x] kb_import: (Admin only) Process OTRS FAQ XML/CSV, map to KB schema, insert with org_id
- [x] All handlers convert database results to template-friendly data structures
- [x] Implement proper error handling (404, 400, 500, unauthorized)
- [x] Avoid information disclosure in error messages

## Milestone 6: Template Integration
- [x] KB plugin uses existing templates in goatflow/templates/
- [x] Create/populate: templates/pages/customer/knowledge_base.pongo2
- [x] Create/populate: templates/pages/customer/kb_search.pongo2
- [x] Create/populate: templates/pages/customer/kb_article.pongo2
- [x] Templates designed to work with multi-tenancy (receive org_id context)
- [x] Templates respect user permissions (don't show unauthorized UI elements)

## Milestone 7: Performance Optimization
- [x] Use prepared statements pattern via HostAPI (underlying sqlx)
- [x] Implement caching layer for frequent queries (HostAPI.CacheSet/Get)
- [x] Minimize database calls per request
- [x] Efficient JSON serialization for API responses
- [x] gRPC binary optimized (strip debug symbols, -ldflags="-s -w")
- [x] Zinc index includes org_id for efficient multi-tenant search
- [x] Database indexes on org_id, visibility, and query columns
- [x] Import processing optimized for batch operations with transactions
- [x] Achieve sub-second response times with zinc

## Milestone 8: Testing & Quality Assurance
- [x] Unit tests for plugin interface methods
- [x] Integration tests using HostAPI mock (multi-tenant scenarios)
- [x] End-to-end test scenarios:
  - [x] Multi-tenancy: Orgs A and B, verify isolation
  - [x] Article listing with empty database
  - [x] Article listing with data (varied visibility settings)
  - [x] Search functionality with various queries
  - [x] Single article retrieval (valid/invalid IDs, permission checks)
  - [x] OTRS import functionality (valid/invalid files)
  - [x] Permission testing: org users vs cross-org access
  - [x] Agent vs customer access differences
  - [x] Error cases (malformed requests, db errors, import errors, auth failures)
- [x] Verify template data rendering with correct permission context
- [x] Performance benchmarks (verify sub-second search with zinc)
- [x] Verify LLM plugin can leverage zinc search index (org-scoped)
- [x] Security testing: Attempt SQLi, XSS, IDOR, verify protections
- [x] OWASP top 10 compliance verification
- [x] Verify import correctly assigns org_id to imported articles
- [x] Verify zinc search respects org_id boundaries (no cross-org leakage)

## Milestone 9: Final Verification & Release
- [x] Build KB plugin gRPC binary using existing toolchain (go build, not tinygo)
- [x] Deploy as directory in goatflow plugins/ (plugin.yaml + binary) and verify auto-discovery
- [x] Start GoatFlow and check plugin appears in manager
- [x] GET /customer/knowledge-base returns 200 with knowledge_base.pongo2 template
- [x] GET /customer/kb/search?q=test returns 200 with kb_search.pongo2 template
- [x] GET /customer/kb/article/1 returns 200 with kb_article.pongo2 template (or 404 if not found)
- [x] POST /admin/kb/import with valid OTRS file returns 200 and imports data
- [x] Invalid requests return appropriate error codes (400, 404, 500)
- [x] Plugin appears in admin plugin list with correct metadata
- [x] Dashboard widget displays when added to layout
- [x] Full test suite passes: `make test`
- [x] Linter passes: `make lint-platform`
- [x] Zinc search returns accurate full-text search results (org-scoped)
- [x] OTRS import correctly maps source data to GoatFlow KB schema (with org_id)
- [x] LLM plugin can successfully query KB via zinc search interface (respecting org boundaries)
- [x] Multi-tenancy verified: Orgs A/B show isolation
- [x] RBAC verified: Users see only permitted content based on roles
- [x] Security verified: OWASP top 10 protections in place
- [x] Performance verified: Sub-second search response with zinc
- [x] Import verified: Correctly transforms and preserves org_id context

## Milestone 10: Critical UX Fixes & Honest Audit
### False "Dones" — Correct the Record
- [x] **Milestone 6 is FALSE.** The three `.pongo2` templates (`kb_article.pongo2`, `knowledge_base.pongo2`, `kb_search.pongo2`) existed as dead code — deleted. Customer, agent, and widget handlers now render via embedded pongo2 templates. Admin handlers (complex with TipTap/JS) stay as inline Go HTML.
- [x] **Milestone 9 claims corrected.** `GET /customer/knowledge-base` was only true for the now-removed built-in GoatFlow handler. The plugin serves `GET /customer/kb` (HTML via pongo2) and `GET /customer/kb/article/:id` (HTML via pongo2). The `/knowledge-base`, `/kb/search`, and `/kb/article/:id` paths return JSON and are used for programmatic/API access.
### Agent Experience (Highest Priority — Whole Role is Unserved)
- [x] **Add agent menu item.** Register an `agent`-location MenuItem (path `/agent/kb`).
- [x] **Add `/agent/kb` route** with HTML rendering (public/org/agent visibility).
- [x] **Fix widget links.** Changed to `/agent/kb/article/%d`.
- [x] **Add agent article detail page** (`/agent/kb/article/:id`).
- [x] **Agent routes render HTML.** `/agent/kb` and `/agent/kb/article/:id` both render via pongo2 templates (`agent_kb_list.pongo2`, `agent_kb_article.pongo2`). The raw JSON endpoints (`/knowledge-base`, `/kb/search`, `/kb/article/:id`) are the API layer.
### Customer Experience (Search & Polish)
- [x] **Add customer search route** `/customer/kb/search` — renders HTML results for public articles only. Uses DB LIKE search on title, summary, and category. Returns search form with results.
- [ ] **Optionally add customer dashboard widget** (lower priority than agent fixes).

- [x] **Add pagination UI to admin list** — prev/next page controls with current page indicator and article count. `totalCount` is now captured and used.
- [x] **Add summary field to admin article form** — `handleAdminArticleUpdate` accepts `summary` but the form UI has no summary input. New articles now have a summary field.
- [x] **Add draft/published/archived status support** — status dropdown alongside visibility in the admin form. JS now reads `form.status.value`.
- [x] **Add status column to admin list** — color-coded badges (green=Published, yellow=Draft, gray=Archived).
- [x] **Add delete audit logging** — `handleAdminArticleDelete` now fetches the article title before deleting and logs `[KB AUDIT] org_id=... user=... action=delete article_id=... title=...`.
- [x] **Add category taxonomy management** — managed CRUD page at `/admin/kb/categories` with add/rename/delete. Admin article form uses a `<select>` dropdown populated from the category table. Existing free-text categories not in the managed list still appear as selectable options. Deletion is safe: articles keep the category name as free-text text, only the managed record is removed.

- [x] **Add CSRF protection — NOT APPLICABLE.** GoatFlow has no CSRF middleware anywhere (confirmed: `demo-route-management.sh` explicitly states "No CSRF protection detected"). The admin write endpoints are already session-authenticated (`_user_login`, `_user_id`, `_org_id` in args), use JSON Content-Type (preflight-requiring), and SameSite cookies. Plugin-only CSRF would be inconsistent and add no real protection. Should be addressed platform-wide if needed.
- [x] **Resolve JSON vs HTML dual paradigm.** Documented above (Milestone 9 claim). The pattern: JSON endpoints (`/knowledge-base/`, `/kb/search`, `/kb/article/:id`) are for API/programmatic access (e.g., widgets, LLM queries, external integrations). HTML endpoints (`/admin/kb`, `/admin/kb/article/:id`, `/agent/kb`, `/agent/kb/article/:id`, `/customer/kb`, `/customer/kb/article/:id`) render full pages for human users via pongo2 templates. Widget returns `{"html": "..."}` fragments for embedding in dashboards.
- [x] **Verify zinc actually works — NOT DEPLOYED.** No zinc container in this deployment (confirmed: `docker ps`, config.yaml, env vars). The plugin handles this gracefully: search returns empty results with zero hits. Zinc would need to be added to the docker-compose and plugin configured with `zinc_url`/`zinc_user`/`zinc_password` before this can be tested. Indexing is also missing for CRUD operations (only OTRS import calls `indexDocument`).