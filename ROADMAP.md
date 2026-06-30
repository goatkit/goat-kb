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