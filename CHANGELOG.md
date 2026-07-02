# Changelog

All notable changes to the GoatFlow Knowledge Base plugin are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **Tag chip input** in admin article form — type and press comma/Enter to add removable pill badges, same UX as the goatfictus interests tags. Tags stored as comma-separated in `tags` column, already indexed as full-text in zinc.
- **Tags display** on agent and customer article pages — rendered as styled chips between summary and content.
- **Summary display** on agent and customer article pages — shows the article summary between tags and content.
- **Category taxonomy management** — full admin CRUD at `/admin/kb/categories`: add, rename, delete categories. Article count per category shown. Existing free-text categories that aren't in the managed list still appear as selectable options in the article form dropdown. Deletion is safe (articles keep the name as text).
- **Schema migration v2** — converts existing `org` visibility rows to `agent`.
- **Comprehensive README** covering all routes, visibility model, categories, tags, OTRS import, zinc search, schema, build/deploy, security, and configuration.

### Changed

- **Visibility simplified from 3 levels to 2**: `org` and `agent` were functionally identical (both checked `isAgentOrAdmin()`). Removed `org`. Labels: `public` → "Users & Agents", `agent` → "Agents Only".
- **Category input in admin article form** — changed from free-text `<input>` to `<select>` dropdown populated from `gk_kb_categories` table, with a "Manage" link next to the label.
- **Milestone 10 reflection** — ROADMAP now accurately documents JSON vs HTML dual paradigm, agent experience, customer search, pagination, status support, audit logging, and category taxonomy.

### Fixed

- **Summary not passed to article detail views** — added `summary` to SELECT queries and template data for both agent and customer article handlers.
- **Tags not passed to article detail views** — added `tags` to SELECT queries and template data for both agent and customer article handlers. Tags are pre-split into a `[]string` slice in Go (avoids pongo2 `trim` filter which doesn't exist).
