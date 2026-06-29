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
