// Package kb implements the GoatFlow Knowledge Base plugin.
//
// schema.go manages the KB database schema: dialect detection and
// versioned migrations run from InitWithHost against the host database
// via HostAPI.DBExec. DDL is dialect-aware (MySQL/MariaDB and PostgreSQL)
// because the platform supports both and ConvertPlaceholders only rewrites
// ? placeholders, not DDL keywords such as AUTO_INCREMENT vs SERIAL.
package kb

import (
	"context"
	"fmt"
	"strings"
)

// schemaVersion tracks the KB schema revision. Bump when adding a migration
// entry below. The host persists applied versions in gk_kb_schema_version.
const schemaVersion = 2

// dialect is the detected SQL dialect: "mysql" (MariaDB included) or "postgres".
type dialect string

const (
	dialectMySQL    dialect = "mysql"
	dialectPostgres dialect = "postgres"
	dialectUnknown  dialect = "unknown"
)

// detectDialect probes the host database by selecting from a table that
// exists on every GoatFlow install (gk_organisation). A syntax error on the
// LIMIT/OFFSET form distinguishes PostgreSQL from MySQL/MariaDB; if the
// probe can't be parsed at all we fall back to a feature probe.
//
// We can't use internal/platform/database from a plugin (platform boundary),
// and HostAPI exposes no dialect accessor, so this is the cheapest reliable
// signal. Both supported drivers accept the base SELECT.
func detectDialect(ctx context.Context, host hostQuerier) dialect {
	// gk_organisation is created by the platform on every install.
	if _, err := host.DBQuery(ctx, "SELECT 1 AS v FROM gk_organisation LIMIT 1"); err == nil {
		// MySQL/MariaDB and PostgreSQL both accept LIMIT 1 here. Try a
		// PostgreSQL-only construct to disambiguate. ::int is a Postgres
		// cast; MySQL rejects it with a syntax error.
		if _, err := host.DBQuery(ctx, "SELECT 1::int AS v FROM gk_organisation LIMIT 1"); err == nil {
			return dialectPostgres
		}
		return dialectMySQL
	}
	// gk_organisation not present (fresh test DB, single-org dev). Probe
	// with a literal cast instead.
	if _, err := host.DBQuery(ctx, "SELECT 1::int AS v"); err == nil {
		return dialectPostgres
	}
	if _, err := host.DBQuery(ctx, "SELECT 1 AS v"); err == nil {
		return dialectMySQL
	}
	return dialectUnknown
}

// hostQuerier is the subset of plugin.HostAPI schema code needs. Declared
// locally so tests can pass a fake and so the rest of the package can compose
// on it without importing the plugin package here.
type hostQuerier interface {
	DBQuery(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	DBExec(ctx context.Context, query string, args ...any) (int64, error)
	Log(ctx context.Context, level, message string, fields map[string]any)
}

// migrateSchema brings the KB tables to schemaVersion. Idempotent: it records
// each applied version in gk_kb_schema_version and skips already-applied ones.
func migrateSchema(ctx context.Context, host hostQuerier, d dialect) error {
	if d == dialectUnknown {
		return fmt.Errorf("kb: could not detect database dialect")
	}

	// Version tracking table. Keep the DDL portable: INTEGER PRIMARY KEY
	// auto-increments on MySQL when it's the PK, and becomes a plain int
	// PK on Postgres (rows are inserted with explicit version numbers, so
	// auto-increment is not required).
	if _, err := host.DBExec(ctx, versionTableDDL); err != nil {
		return fmt.Errorf("kb: create schema_version table: %w", err)
	}

	current, err := currentSchemaVersion(ctx, host)
	if err != nil {
		return fmt.Errorf("kb: read schema version: %w", err)
	}
	if current >= schemaVersion {
		host.Log(ctx, "info", fmt.Sprintf("KB schema up to date (v%d)", current), nil)
		return nil
	}

	for v := current + 1; v <= schemaVersion; v++ {
		stmts, ok := migrations[v]
		if !ok {
			return fmt.Errorf("kb: no migration for version %d", v)
		}
		host.Log(ctx, "info", fmt.Sprintf("KB applying schema migration v%d", v), nil)
		for i, stmt := range stmts {
			if _, err := host.DBExec(ctx, d.render(stmt)); err != nil {
				return fmt.Errorf("kb: migration v%d statement %d: %w", v, i+1, err)
			}
		}
		if _, err := host.DBExec(ctx,
			"INSERT INTO gk_kb_schema_version (version) VALUES (?)", v); err != nil {
			return fmt.Errorf("kb: record migration v%d: %w", v, err)
		}
		host.Log(ctx, "info", fmt.Sprintf("KB schema migration v%d applied", v), nil)
	}
	return nil
}

// currentSchemaVersion reads the highest applied version, or 0 if none.
func currentSchemaVersion(ctx context.Context, host hostQuerier) (int, error) {
	rows, err := host.DBQuery(ctx, "SELECT COALESCE(MAX(version), 0) AS v FROM gk_kb_schema_version")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return toInt(rows[0]["v"]), nil
}

// versionTableDDL is the version-tracking table, dialect-neutral.
const versionTableDDL = `CREATE TABLE IF NOT EXISTS gk_kb_schema_version (
		version INTEGER NOT NULL,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (version)
	)`

// migrations is the ordered set of DDL migrations. Each statement is a
// dialetTemplate rendered with the active dialect before execution.
// v1 creates the core KB tables. All tables carry org_id for multi-tenancy.
var migrations = map[int][]dialectTemplate{
	1: {
		// gk_kb_articles: the canonical KB article row. org_id scopes every
		// row to a tenant; the slug is a unique-per-org human key used to
		// recover the auto-generated id after INSERT (DBExec returns
		// rows-affected, not last-insert-id, over the gRPC boundary).
		{
			mysql: `CREATE TABLE IF NOT EXISTS gk_kb_articles (
				id INTEGER PRIMARY KEY AUTO_INCREMENT,
				org_id INTEGER NOT NULL,
				title VARCHAR(255) NOT NULL,
				slug VARCHAR(255) NOT NULL,
				summary VARCHAR(500) NOT NULL DEFAULT '',
				content MEDIUMTEXT NOT NULL,
				category VARCHAR(100) NOT NULL DEFAULT '',
				visibility VARCHAR(20) NOT NULL DEFAULT 'public',
				author VARCHAR(255) NOT NULL DEFAULT '',
				status VARCHAR(20) NOT NULL DEFAULT 'published',
				tags VARCHAR(500) NOT NULL DEFAULT '',
				source VARCHAR(20) NOT NULL DEFAULT 'native',
				source_id VARCHAR(255) NOT NULL DEFAULT '',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
				UNIQUE KEY uk_kb_articles_org_slug (org_id, slug),
				INDEX idx_kb_articles_org_status (org_id, status),
				INDEX idx_kb_articles_org_cat (org_id, category),
				INDEX idx_kb_articles_org_vis (org_id, visibility)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
			postgres: `CREATE TABLE IF NOT EXISTS gk_kb_articles (
				id SERIAL PRIMARY KEY,
				org_id INTEGER NOT NULL,
				title VARCHAR(255) NOT NULL,
				slug VARCHAR(255) NOT NULL,
				summary VARCHAR(500) NOT NULL DEFAULT '',
				content TEXT NOT NULL,
				category VARCHAR(100) NOT NULL DEFAULT '',
				visibility VARCHAR(20) NOT NULL DEFAULT 'public',
				author VARCHAR(255) NOT NULL DEFAULT '',
				status VARCHAR(20) NOT NULL DEFAULT 'published',
				tags VARCHAR(500) NOT NULL DEFAULT '',
				source VARCHAR(20) NOT NULL DEFAULT 'native',
				source_id VARCHAR(255) NOT NULL DEFAULT '',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				UNIQUE (org_id, slug),
				INDEX idx_kb_articles_org_status (org_id, status),
				INDEX idx_kb_articles_org_cat (org_id, category),
				INDEX idx_kb_articles_org_vis (org_id, visibility)
			)`,
		},
		// gk_kb_categories: optional category grouping, org-scoped.
		{
			mysql: `CREATE TABLE IF NOT EXISTS gk_kb_categories (
				id INTEGER PRIMARY KEY AUTO_INCREMENT,
				org_id INTEGER NOT NULL,
				name VARCHAR(100) NOT NULL,
				parent_id INTEGER DEFAULT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				UNIQUE KEY uk_kb_cat_org_name (org_id, name),
				INDEX idx_kb_cat_org (org_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
			postgres: `CREATE TABLE IF NOT EXISTS gk_kb_categories (
				id SERIAL PRIMARY KEY,
				org_id INTEGER NOT NULL,
				name VARCHAR(100) NOT NULL,
				parent_id INTEGER DEFAULT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				UNIQUE (org_id, name),
				INDEX idx_kb_cat_org (org_id)
			)`,
		},
		// gk_kb_attachments: one row per article attachment. The file bytes
		// live in the HostAPI file-storage layer (local or S3, swappable via
		// config); this table only stores the linkage + the file_key the
		// plugin uses to retrieve them. Multiple rows per article_id means
		// an article can carry any number of attachments. org_id is
		// denormalised onto every row so tenant isolation is enforceable in
		// a single WHERE without a join, and so cross-org leakage is
		// impossible even if a stale article_id is passed.
		{
			mysql: `CREATE TABLE IF NOT EXISTS gk_kb_attachments (
				id INTEGER PRIMARY KEY AUTO_INCREMENT,
				org_id INTEGER NOT NULL,
				article_id INTEGER NOT NULL,
				file_key VARCHAR(500) NOT NULL,
				filename VARCHAR(255) NOT NULL,
				content_type VARCHAR(100) NOT NULL DEFAULT '',
				size BIGINT NOT NULL DEFAULT 0,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				UNIQUE KEY uk_kb_attach_article_key (article_id, file_key),
				INDEX idx_kb_attach_org_article (org_id, article_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
			postgres: `CREATE TABLE IF NOT EXISTS gk_kb_attachments (
				id SERIAL PRIMARY KEY,
				org_id INTEGER NOT NULL,
				article_id INTEGER NOT NULL,
				file_key VARCHAR(500) NOT NULL,
				filename VARCHAR(255) NOT NULL,
				content_type VARCHAR(100) NOT NULL DEFAULT '',
				size BIGINT NOT NULL DEFAULT 0,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				UNIQUE (article_id, file_key),
				INDEX idx_kb_attach_org_article (org_id, article_id)
			)`,
		},
	},
	2: {
		// Normalise visibility: "org" was identical to "agent" (same RBAC check)
		// but confusing. Migrate existing org rows to agent, then remove the
		// unused value from the UI dropdown.
		{mysql: `UPDATE gk_kb_articles SET visibility = 'agent' WHERE visibility = 'org'`,
			postgres: `UPDATE gk_kb_articles SET visibility = 'agent' WHERE visibility = 'org'`},
	},
}

// dialectTemplate carries a DDL statement per dialect. Statements that are
// identical across dialects set the same string in both fields.
type dialectTemplate struct {
	mysql    string
	postgres string
}

// render returns the DDL for the active dialect.
func (d dialect) render(t dialectTemplate) string {
	switch d {
	case dialectPostgres:
		return t.postgres
	default:
		return t.mysql
	}
}

// slugify converts a title (or OTRS FAQ subject) into a URL-safe, unique-key
// slug. Used for import idempotency: the (org_id, slug) unique index means a
// re-import of the same OTRS article upserts instead of duplicating.
func slugify(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDash := true // trim leading dashes
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_' || r == '/' || r == '.':
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "article"
	}
	return out
}