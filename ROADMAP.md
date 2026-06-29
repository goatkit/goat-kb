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
- [ ] Integrate zinc for full-text search indexing of KB articles
- [ ] Index title, content, tags fields for search
- [ ] Include org_id in zinc documents for multi-tenant isolation
- [ ] Implement search handler that accepts query parameter
- [ ] Return search results formatted for kb_search.pongo2 template
- [ ] Optimize for sub-second response times
- [ ] Test search relevance and performance

## Milestone 4: Security & Access Control
- [ ] Implement Secure By Design following OWASP guidelines
- [ ] Require authentication for all endpoints
- [ ] Implement fine-grained authorization based on org_id and user roles
- [ ] Support visibility levels: public, org-only, agent-specific
- [ ] Prevent information leakage (don't reveal existence of unauthorized articles)
- [ ] Validate and sanitize all inputs (prevent injection, XSS)
- [ ] Implement CSRF protection where applicable
- [ ] Log security-relevant events (access attempts, failures)
- [ ] Respect HostAPI rate limiting protections

## Milestone 5: Handler Implementation
- [ ] kb_list: Query articles with pagination, org_id filter, permission-based visibility
- [ ] kb_search: Accept search query, use zinc for full-text search, return results
- [ ] kb_article: Extract ID, verify permissions, fetch single article
- [ ] kb_import: (Admin only) Process OTRS FAQ XML/CSV, map to KB schema, insert with org_id
- [ ] All handlers convert database results to template-friendly data structures
- [ ] Implement proper error handling (404, 400, 500, unauthorized)
- [ ] Avoid information disclosure in error messages

## Milestone 6: Template Integration
- [ ] KB plugin uses existing templates in goatflow/templates/
- [ ] Create/populate: templates/pages/customer/knowledge_base.pongo2
- [ ] Create/populate: templates/pages/customer/kb_search.pongo2
- [ ] Create/populate: templates/pages/customer/kb_article.pongo2
- [ ] Templates designed to work with multi-tenancy (receive org_id context)
- [ ] Templates respect user permissions (don't show unauthorized UI elements)

## Milestone 7: Performance Optimization
- [ ] Use prepared statements pattern via HostAPI (underlying sqlx)
- [ ] Implement caching layer for frequent queries (HostAPI.CacheSet/Get)
- [ ] Minimize database calls per request
- [ ] Efficient JSON serialization for API responses
- [ ] gRPC binary optimized (strip debug symbols, -ldflags="-s -w")
- [ ] Zinc index includes org_id for efficient multi-tenant search
- [ ] Database indexes on org_id, visibility, and query columns
- [ ] Import processing optimized for batch operations with transactions
- [ ] Achieve sub-second search response times with zinc

## Milestone 8: Testing & Quality Assurance
- [ ] Unit tests for plugin interface methods
- [ ] Integration tests using HostAPI mock (multi-tenant scenarios)
- [ ] End-to-end test scenarios:
  - [ ] Multi-tenancy: Orgs A and B, verify isolation
  - [ ] Article listing with empty database
  - [ ] Article listing with data (varied visibility settings)
  - [ ] Search functionality with various queries
  - [ ] Single article retrieval (valid/invalid IDs, permission checks)
  - [ ] OTRS import functionality (valid/invalid files)
  - [ ] Permission testing: org users vs cross-org access
  - [ ] Agent vs customer access differences
  - [ ] Error cases (malformed requests, db errors, import errors, auth failures)
- [ ] Verify template data rendering with correct permission context
- [ ] Performance benchmarks (verify sub-second search with zinc)
- [ ] Verify LLM plugin can leverage zinc search index (org-scoped)
- [ ] Security testing: Attempt SQLi, XSS, IDOR, verify protections
- [ ] OWASP top 10 compliance verification
- [ ] Verify import correctly assigns org_id to imported articles
- [ ] Verify zinc search respects org_id boundaries (no cross-org leakage)

## Milestone 9: Final Verification & Release
- [ ] Build KB plugin gRPC binary using existing toolchain (go build, not tinygo)
- [ ] Deploy as directory in goatflow plugins/ (plugin.yaml + binary) and verify auto-discovery
- [ ] Start GoatFlow and check plugin appears in manager
- [ ] GET /customer/knowledge-base returns 200 with knowledge_base.pongo2 template
- [ ] GET /customer/kb/search?q=test returns 200 with kb_search.pongo2 template
- [ ] GET /customer/kb/article/1 returns 200 with kb_article.pongo2 template (or 404 if not found)
- [ ] POST /admin/kb/import with valid OTRS file returns 200 and imports data
- [ ] Invalid requests return appropriate error codes (400, 404, 500)
- [ ] Plugin appears in admin plugin list with correct metadata
- [ ] Dashboard widget displays when added to layout
- [ ] Full test suite passes: `make test`
- [ ] Linter passes: `make lint-platform`
- [ ] Zinc search returns accurate full-text search results (org-scoped)
- [ ] OTRS import correctly maps source data to GoatFlow KB schema (with org_id)
- [ ] LLM plugin can successfully query KB via zinc search interface (respecting org boundaries)
- [ ] Multi-tenancy verified: Orgs A/B show isolation
- [ ] RBAC verified: Users see only permitted content based on roles
- [ ] Security verified: OWASP top 10 protections in place
- [ ] Performance verified: Sub-second search response with zinc
- [ ] Import verified: Correctly transforms and preserves org_id context

## Dependencies
- GoatFlow platform (internal/platform/*)
- HostAPI for database/cache/email/etc. access
- Zinc for full-text search
- OTRS FAQ schema for import mapping
- GoatFlow theming engine and dynamic routing system
- GoatFlow security framework (OWASP compliance)
- GoatFlow multi-tenancy patterns (org_id isolation)