# Changelog

All notable changes to the GoatFlow Knowledge Base plugin are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses semantic versioning.

## [Unreleased]

### Added

- Initial gRPC plugin foundation under `cmd/kb-plugin/` and `internal/kb/`.
- `plugin.yaml` manifest for GoatFlow gRPC plugin discovery.
- `GKRegister`, `Init`, `InitWithHost`, `Call`, and `Shutdown` implementation for the KB plugin.
- Plugin metadata for `kb` version `0.1.0`.
- Route declarations for:
  - `GET /knowledge-base` → `kb_list`
  - `GET /kb/search` → `kb_search`
  - `GET /kb/article/:id` → `kb_article`
  - `POST /admin/kb/import` → `kb_import`
- Admin menu item for Knowledge Base management.
- Dashboard widget declaration for recent KB articles.
- Resource and HostAPI permission requests for database, cache, HTTP, config, and plugin-to-plugin calls.
- Error code declarations for article lookup, search validation, import failure, authorization, and article ID validation.
- Registration tests covering the Milestone 1 plugin contract.
- Makefile targets for containerized build, test, package, and API deployment using `ADMIN_API_KEY` from the GoatFlow `.env`.

### Changed

- Roadmap Milestone 1 marked complete after implementing the gRPC plugin foundation.
- Runtime choice documented as gRPC instead of WASM because the KB module is I/O-heavy: zinc search, database access, OTRS FAQ import, and LLM retrieval.

### Milestone 2 — Database Schema & Multi-tenancy

- Versioned schema migrations (`internal/kb/schema.go`) with a `gk_kb_schema_version` tracking table, run idempotently from `InitWithHost`. DDL is dialect-aware (MySQL/MariaDB and PostgreSQL) via a runtime dialect probe, since `ConvertPlaceholders` rewrites `?` placeholders but not DDL keywords (`AUTO_INCREMENT` vs `SERIAL`).
- KB tables designed for multi-tenancy: `gk_kb_articles`, `gk_kb_categories`, and `gk_kb_attachments`, every row carrying `org_id` with indexes on `(org_id, status)`, `(org_id, category)`, `(org_id, visibility)`, and a unique `(org_id, slug)` key.
- `gk_kb_attachments` links articles to files stored in the HostAPI file-storage layer (local or S3, swappable via platform config), supporting multiple attachments per article. Only linkage + `file_key` live in the DB; bytes stay in the swappable backend.
- OTRS/Znuny FAQ import (`internal/kb/import.go`) parsing `<FAQExport>`/`<FAQItem>` XML, mapping `Title`/`Field_1`/`Field_2`/`Keywords`/`Category`/`ValidID` to the KB schema with safe defaults for missing fields and draft-status for invalid OTRS states.
- Import is idempotent: the `(org_id, source, source_id)` lookup upserts existing articles instead of duplicating, keyed on the OTRS `FAQID`.
- `org_id` is preserved on every imported row from `HostAPI.OrgID()`; cross-org isolation is enforced by the `org_id = ?` predicate on every query.
- Error responses carry HTTP status codes via the `{"error": msg, "status": N}` convention (404/400/500/502/503), surfaced through the dynamic router.
- Schema, dialect-detection, import mapping, idempotency, and multi-tenant isolation test suite (19 new tests).

### Platform improvements (goatflow)

- `buildPluginArgs` now passes non-JSON request bodies (XML, CSV, plain text) through to plugins as `_body` / `_content_type`, capped at 4 MiB. Previously such payloads were silently dropped because only JSON bodies were merged into args.
- The dynamic router honours plugin error responses of the form `{"error": msg, "status": N}` (N in 400–599) and maps them to the matching HTTP status, so plugins can signal not-found / bad-request / server errors instead of every error surfacing as 200-OK-with-body.

### Milestone 3 — Search Functionality (Zinc Integration)

- Zinc REST API client (`internal/kb/zinc.go`) that communicates with Zinc via `HostAPI.HTTPRequest` (basic auth). Created locally because the plugin can't import `internal/platform/zinc` across the platform boundary.
- Client is auto-disabled when `zinc_url` is unset in the plugin config (`GOATFLOW_PLUGIN_KB_ZINC_URL` env var). When disabled, search returns an empty result set instead of failing — the KB list still works via the DB.
- Index creation on `InitWithHost` with a field mapping marking `title`, `summary`, `content`, `tags` as analysed text, `org_id` as long, and `category`/`visibility`/`status` as keywords. Auto-create so Zinc's default `_all` field doesn't cause unexpected query behaviour.
- Articles indexed on import (insert and update), carrying `org_id` in every document. The Zinc bool+filter+must query structure ensures the `org_id` filter is always applied at the engine level — a search never crosses tenant boundaries.
- Search handler rewritten to use the real zinc client. Queries `title^3`, `summary^2`, `content`, `tags` with `org_id` and `status=published` filters. Returns results formatted for the `kb_search.pongo2` template with pagination fields (`page`, `per_page`, `total`, `results`).
- 25 new tests covering client construction, index creation (mapping verification), document indexing/update/delete, search with org_id isolation, draft filtering, disabled-state fallbacks, import-with-indexing, and base64 encoding.
