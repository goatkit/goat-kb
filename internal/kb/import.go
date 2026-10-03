// Package kb implements the GoatFlow Knowledge Base plugin.
//
// import.go maps OTRS/Znuny FAQ articles into the GoatFlow KB schema and
// inserts them with the caller's org_id preserved on every row. The mapping
// is deliberately defensive: missing OTRS fields fall back to safe defaults,
// and the (org_id, slug) unique index makes a re-import upsert rather than
// duplicate.
package kb

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
)

// otrsFAQItem is one FAQ article as exported by the OTRS/Znuny FAQ module.
// The export format carries articles in an <FAQItem> (or <FAQDataItem>)
// envelope with named fields. We accept both element and attribute shapes
// so we don't break on minor Znuny version differences.
type otrsFAQItem struct {
	XMLName  xml.Name `xml:"FAQItem" json:"-"`
	XMLName2 xml.Name `xml:"FAQDataItem" json:"-"` // alternate envelope
	Title    string   `xml:"Title" json:"title"`
	Category string   `xml:"Category" json:"category"`
	// OTRS FAQ stores the article body in Field_1 (long text) and a
	// short summary in Field_2. Keywords maps to tags.
	Field1   string `xml:"Field_1" json:"field_1"`
	Field2   string `xml:"Field_2" json:"field_2"`
	Keywords string `xml:"Keywords" json:"keywords"`
	Author   string `xml:"Author" json:"author"`
	Created  string `xml:"Created" json:"created"`
	Changed  string `xml:"Changed" json:"changed"`
	ValidID  int    `xml:"ValidID" json:"valid_id"`
	State    string `xml:"State" json:"state"`
	// FAQID is the source-system identifier, used for idempotency.
	FAQID string `xml:"FAQID" json:"faq_id"`
	// Language is optional; ignored for now but parsed so the importer
	// doesn't choke on multi-language exports.
	Language string `xml:"Language" json:"language"`
}

// otrsFAQExport is the top-level OTRS FAQ export document. The root can be
// <FAQExport>, <FAQData>, or a bare list of <FAQItem>. We accept any of these.
type otrsFAQExport struct {
	XMLName xml.Name      `xml:"FAQExport" json:"-"`
	Items   []otrsFAQItem `xml:"FAQItem" json:"items"`
	// Alternate root: <FAQData><Item>...</Item></FAQData>
	ItemsAlt []otrsFAQItem `xml:"Item" json:"items_alt"`
}

// kbArticle is the internal representation we insert into gk_kb_articles.
type kbArticle struct {
	Title      string
	Slug       string
	Summary    string
	Content    string
	Category   string
	Visibility string
	Author     string
	Status     string
	Tags       string
	Source     string
	SourceID   string
}

// importResult is reported back to the caller of kb_import.
type importResult struct {
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"`
	Errors   []string `json:"errors,omitempty"`
	OrgID    int64    `json:"org_id"`
}

// importOTRSFAQ parses an OTRS FAQ XML export and inserts articles into
// gk_kb_articles scoped to orgID. It is idempotent: an article with the same
// source_id (OTRS FAQID) for the same org is updated in place rather than
// duplicated, keyed on the (org_id, slug) unique index.
//
// sourceName is "otrs_faq" for the standard export and is stored on each row
// so downstream tooling can tell imported articles from native ones.
//
// If zc is non-nil and enabled, each inserted/updated article is also pushed
// to the Zinc search index carrying org_id for multi-tenant isolation.
// Indexing failures are logged in res.Errors but don't fail the import — the
// DB row is the source of truth and can be re-indexed later.
func importOTRSFAQ(ctx context.Context, host hostQuerier, zc *zincClient, orgID int64, payload []byte) (importResult, error) {
	res := importResult{OrgID: orgID}

	if orgID <= 0 {
		return res, fmt.Errorf("kb:import_failed: no active org — cannot import without org_id")
	}
	if len(payload) == 0 {
		return res, fmt.Errorf("kb:invalid_search: import payload is empty")
	}

	items, err := parseOTRSFAQ(payload)
	if err != nil {
		return res, fmt.Errorf("kb:import_failed: parse OTRS FAQ: %w", err)
	}

	for _, item := range items {
		art := mapOTRSFAQItem(item)
		// Idempotency: if an article with this source_id already exists
		// for this org, update it; otherwise insert.
		existing, err := findArticleBySource(ctx, host, orgID, art.Source, art.SourceID)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("lookup %q: %v", art.SourceID, err))
			res.Skipped++
			continue
		}
		if existing != 0 {
			if err := updateArticle(ctx, host, existing, orgID, art); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("update %q: %v", art.SourceID, err))
				res.Skipped++
				continue
			}
			if zc != nil && zc.enabled {
				if err := zc.indexDocument(ctx, zincDocument{
					ID: existing, OrgID: orgID, Title: art.Title, Summary: art.Summary,
					Content: art.Content, Category: art.Category, Tags: art.Tags,
					Visibility: art.Visibility, Status: art.Status,
				}); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("index %q: %v", art.SourceID, err))
				}
			}
			res.Imported++
			continue
		}
		if err := insertArticle(ctx, host, orgID, art); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("insert %q: %v", art.SourceID, err))
			res.Skipped++
			continue
		}
		// Look up the generated id for indexing. The (org_id, slug) unique
		// key lets us recover it since DBExec returns rows-affected only.
		if zc != nil && zc.enabled {
			if id, err := findArticleBySlug(ctx, host, orgID, art.Slug); err == nil && id != 0 {
				if err := zc.indexDocument(ctx, zincDocument{
					ID: id, OrgID: orgID, Title: art.Title, Summary: art.Summary,
					Content: art.Content, Category: art.Category, Tags: art.Tags,
					Visibility: art.Visibility, Status: art.Status,
				}); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("index %q: %v", art.SourceID, err))
				}
			}
		}
		res.Imported++
	}

	return res, nil
}

// parseOTRSFAQ accepts the three known OTRS FAQ export shapes: a wrapper
// <FAQExport> with <FAQItem> children, a <FAQData> root with <Item> children,
// or a bare sequence of <FAQItem> elements. Returns the flat item list.
func parseOTRSFAQ(payload []byte) ([]otrsFAQItem, error) {
	// First try the canonical <FAQExport> envelope.
	var doc otrsFAQExport
	if err := xml.Unmarshal(payload, &doc); err == nil && (len(doc.Items) > 0 || len(doc.ItemsAlt) > 0) {
		return append(doc.Items, doc.ItemsAlt...), nil
	}

	// Fallback: a bare list of <FAQItem> (no root). Wrap in a synthetic root.
	wrapped := append([]byte("<root>"), payload...)
	wrapped = append(wrapped, []byte("</root>")...)
	var bare struct {
		Items []otrsFAQItem `xml:"FAQItem"`
	}
	if err := xml.Unmarshal(wrapped, &bare); err == nil && len(bare.Items) > 0 {
		return bare.Items, nil
	}

	// Final fallback: <FAQData><Item>...</Item></FAQData> with named children.
	var dataRoot struct {
		Items []otrsFAQItem `xml:"Item"`
	}
	if err := xml.Unmarshal(payload, &dataRoot); err == nil && len(dataRoot.Items) > 0 {
		return dataRoot.Items, nil
	}

	return nil, fmt.Errorf("no FAQItem elements found in payload")
}

// mapOTRSFAQItem converts one OTRS FAQ item to the internal kbArticle,
// applying safe defaults for missing fields and normalising status.
func mapOTRSFAQItem(item otrsFAQItem) kbArticle {
	// OTRS ValidID: 1 = valid/active, 2 = invalid/merged, 3 = invalid/temporary.
	// Map invalid states to "draft" so imported-but-not-live articles don't
	// surface in the public list; active ones come through as "published".
	status := "published"
	if item.ValidID == 2 || item.ValidID == 3 || strings.EqualFold(item.State, "invalid") {
		status = "draft"
	}

	content := item.Field1
	if content == "" {
		content = item.Field2 // some exports put the body in Field_2
	}
	summary := item.Field2
	if summary == "" {
		// Derive a short summary from the body if OTRS didn't carry one.
		summary = truncate(strings.TrimSpace(stripHTML(item.Field1)), 500)
	}

	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "Untitled FAQ " + item.FAQID
	}

	return kbArticle{
		Title:      title,
		Slug:       slugify(title),
		Summary:    summary,
		Content:    content,
		Category:   strings.TrimSpace(item.Category),
		Visibility: "public", // OTRS FAQ is customer-facing by default
		Author:     strings.TrimSpace(item.Author),
		Status:     status,
		Tags:       normaliseTags(item.Keywords),
		Source:     "otrs_faq",
		SourceID:   item.FAQID,
	}
}

// findArticleBySource returns the row id of an existing article with the given
// (source, source_id) for the org, or 0 if none. Used for idempotent upsert.
func findArticleBySource(ctx context.Context, host hostQuerier, orgID int64, source, sourceID string) (int64, error) {
	if sourceID == "" {
		return 0, nil
	}
	rows, err := host.DBQuery(ctx,
		"SELECT id FROM gk_kb_articles WHERE org_id = ? AND source = ? AND source_id = ? LIMIT 1",
		orgID, source, sourceID)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return toInt64(rows[0]["id"]), nil
}

// findArticleBySlug returns the row id of an article with the given
// (org_id, slug), or 0 if none. Used to recover the auto-generated id after
// INSERT since DBExec over gRPC returns rows-affected, not last-insert-id.
func findArticleBySlug(ctx context.Context, host hostQuerier, orgID int64, slug string) (int64, error) {
	rows, err := host.DBQuery(ctx,
		"SELECT id FROM gk_kb_articles WHERE org_id = ? AND slug = ? LIMIT 1",
		orgID, slug)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return toInt64(rows[0]["id"]), nil
}

// insertArticle inserts a new KB article scoped to orgID. Uses ? placeholders
// (ConvertPlaceholders rewrites them per dialect on the host). We then look
// up the generated id via the (org_id, slug) unique key because DBExec over
// gRPC returns rows-affected, not last-insert-id.
func insertArticle(ctx context.Context, host hostQuerier, orgID int64, art kbArticle) error {
	_, err := host.DBExec(ctx, `INSERT INTO gk_kb_articles
		(org_id, title, slug, summary, content, category, visibility, author, status, tags, source, source_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		orgID, art.Title, art.Slug, art.Summary, art.Content, art.Category,
		art.Visibility, art.Author, art.Status, art.Tags, art.Source, art.SourceID)
	if err != nil {
		return err
	}
	return nil
}

// updateArticle overwrites the mutable columns of an existing article.
func updateArticle(ctx context.Context, host hostQuerier, id, orgID int64, art kbArticle) error {
	_, err := host.DBExec(ctx, `UPDATE gk_kb_articles SET
		title = ?, summary = ?, content = ?, category = ?, visibility = ?, author = ?, status = ?, tags = ?, source = ?, source_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND org_id = ?`,
		art.Title, art.Summary, art.Content, art.Category, art.Visibility,
		art.Author, art.Status, art.Tags, art.Source, art.SourceID, id, orgID)
	return err
}

// truncate cuts s to at most n runes, appending an ellipsis when truncated.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// stripHTML removes the most common OTRS/HTML markup so a derived summary
// doesn't carry tags into the list view. This is intentionally lightweight;
// full sanitisation happens at render time (Milestone 4).
func stripHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
			b.WriteRune(' ')
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// normaliseTags collapses whitespace/comma-separated keywords into a
// single comma-joined string. Stored as a VARCHAR for now; zinc indexing of
// tags arrives in Milestone 3.
func normaliseTags(s string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(s), func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	})
	return strings.Join(parts, ",")
}
