package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	plugin "github.com/goatkit/goatflow/pkg/plugin"
)

// fakeHost is an in-memory HostAPI for testing schema + importer logic without
// a real database. It simulates ? placeholder binding, a per-table row store,
// and a gk_organisation sentinel table so detectDialect can probe it.
type fakeHost struct {
	mu       sync.Mutex
	dialect  dialect
	tables   map[string][]map[string]any
	execErr  error
	queryErr error
	logs     []logEntry
	nextID   int64
}

type logEntry struct {
	level   string
	msg     string
	fields  map[string]any
}

func newFakeHost(d dialect) *fakeHost {
	h := &fakeHost{tables: map[string][]map[string]any{}, dialect: d, nextID: 1}
	// gk_organisation exists on every install; detectDialect probes it.
	h.tables["gk_organisation"] = []map[string]any{{"id": int64(1), "name": "test-org"}}
	return h
}

func (h *fakeHost) DBQuery(_ context.Context, query string, args ...any) ([]map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.queryErr != nil {
		return nil, h.queryErr
	}
	q := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.Contains(q, "1::INT AS V FROM GK_KB_SCHEMA_VERSION"), strings.Contains(q, "1::INT AS V FROM GK_ORGANISATION"):
		// Postgres cast probe.
		if h.dialect == dialectPostgres {
			return []map[string]any{{"v": int64(1)}}, nil
		}
		return nil, fmt.Errorf("syntax error")
	case strings.HasPrefix(q, "SELECT 1::INT"):
		if h.dialect == dialectPostgres {
			return []map[string]any{{"v": int64(1)}}, nil
		}
		return nil, fmt.Errorf("syntax error")
	case strings.HasPrefix(q, "SELECT 1 AS V FROM GK_ORGANISATION"):
		return []map[string]any{{"v": int64(1)}}, nil
	case strings.HasPrefix(q, "SELECT 1 AS V"):
		return []map[string]any{{"v": int64(1)}}, nil
	case strings.Contains(q, "COALESCE(MAX(VERSION)"):
		return []map[string]any{{"v": int64(len(h.appliedVersions()))}}, nil
	case strings.HasPrefix(q, "SELECT ID FROM GK_KB_ARTICLES"):
		return h.queryRows("gk_kb_articles", query, args), nil
	case strings.HasPrefix(q, "SELECT ID, TITLE, SUMMARY, CATEGORY"):
		return h.queryRows("gk_kb_articles", query, args), nil
	case strings.HasPrefix(q, "SELECT ID, TITLE, CONTENT"):
		return h.queryRows("gk_kb_articles", query, args), nil
	case strings.HasPrefix(q, "SELECT COUNT("):
		rows := h.filterByOrg("gk_kb_articles", args)
		return []map[string]any{{"total": int64(len(rows))}}, nil
	}
	return nil, fmt.Errorf("fakeHost: unhandled query: %s", query)
}

func (h *fakeHost) DBExec(_ context.Context, query string, args ...any) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.execErr != nil {
		return 0, h.execErr
	}
	q := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(q, "CREATE TABLE"):
		// DDL is a no-op in the fake; the table is created lazily on first row.
		return 1, nil
	case strings.HasPrefix(q, "INSERT INTO GK_KB_SCHEMA_VERSION"):
		v := toInt64(args[0])
		h.tables["gk_kb_schema_version"] = append(h.tables["gk_kb_schema_version"], map[string]any{"version": v})
		return 1, nil
	case strings.HasPrefix(q, "INSERT INTO GK_KB_ARTICLES"):
		row := map[string]any{}
		// Column order in insertArticle: org_id, title, slug, summary, content,
		// category, visibility, author, status, tags, source, source_id.
		cols := []string{"org_id", "title", "slug", "summary", "content", "category", "visibility", "author", "status", "tags", "source", "source_id"}
		for i, c := range cols {
			row[c] = args[i]
		}
		row["id"] = h.nextID
		h.nextID++
		h.tables["gk_kb_articles"] = append(h.tables["gk_kb_articles"], row)
		return 1, nil
	case strings.HasPrefix(q, "UPDATE GK_KB_ARTICLES SET VISIBILITY"):
		// Migration v2: convert org → agent. Dialect-agnostic plain UPDATE, no args.
		return 0, nil
	case strings.HasPrefix(q, "UPDATE GK_KB_ARTICLES"):
		// args: title, summary, content, category, visibility, author, status,
		// tags, source, source_id, id, org_id
		id := toInt64(args[10])
		orgID := toInt64(args[11])
		for i, row := range h.tables["gk_kb_articles"] {
			if toInt64(row["id"]) == id && toInt64(row["org_id"]) == orgID {
				cols := []string{"title", "summary", "content", "category", "visibility", "author", "status", "tags", "source", "source_id"}
				for j, c := range cols {
					row[c] = args[j]
				}
				h.tables["gk_kb_articles"][i] = row
				return 1, nil
			}
		}
		return 0, nil
	}
	return 0, fmt.Errorf("fakeHost: unhandled exec: %s", query)
}

func (h *fakeHost) Log(_ context.Context, level, msg string, fields map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, logEntry{level, msg, fields})
}

func (h *fakeHost) OrgID(_ context.Context) int64 { return 1 }

// queryRows scans the fake table and returns rows whose org_id matches the
// first arg (the org_id placeholder). For the source lookup query the second
// and third args are source and source_id.
func (h *fakeHost) queryRows(table, query string, args []any) []map[string]any {
	q := strings.ToUpper(query)
	if strings.Contains(q, "SOURCE = ? AND SOURCE_ID = ?") {
		orgID, source, sourceID := toInt64(args[0]), toString(args[1]), toString(args[2])
		var out []map[string]any
		for _, row := range h.tables[table] {
			if toInt64(row["org_id"]) == orgID && toString(row["source"]) == source && toString(row["source_id"]) == sourceID {
				out = append(out, row)
				break
			}
		}
		return out
	}
	if strings.Contains(q, "SLUG = ?") {
		orgID, slug := toInt64(args[0]), toString(args[1])
		var out []map[string]any
		for _, row := range h.tables[table] {
			if toInt64(row["org_id"]) == orgID && toString(row["slug"]) == slug {
				out = append(out, row)
				break
			}
		}
		return out
	}
	return h.filterByOrg(table, args)
}

func (h *fakeHost) filterByOrg(table string, args []any) []map[string]any {
	orgID := toInt64(args[0])
	var out []map[string]any
	for _, row := range h.tables[table] {
		if toInt64(row["org_id"]) == orgID {
			out = append(out, row)
		}
	}
	return out
}

func (h *fakeHost) appliedVersions() []map[string]any {
	return h.tables["gk_kb_schema_version"]
}

// The remaining HostAPI methods are not exercised by schema/import tests;
// stubs satisfy plugin.HostAPI so fakeHost can be assigned to Plugin.host.
func (h *fakeHost) CacheGet(_ context.Context, _ string) ([]byte, bool, error) { return nil, false, nil }
func (h *fakeHost) CacheSet(_ context.Context, _ string, _ []byte, _ int) error { return nil }
func (h *fakeHost) CacheDelete(_ context.Context, _ string) error { return nil }
func (h *fakeHost) HTTPRequest(_ context.Context, _, _ string, _ map[string]string, _ []byte) (int, []byte, error) {
	return 0, nil, fmt.Errorf("not implemented in fake")
}
func (h *fakeHost) SendEmail(_ context.Context, _, _, _ string, _ bool) error { return nil }
func (h *fakeHost) ConfigGet(_ context.Context, _ string) (string, error) { return "", nil }
func (h *fakeHost) Translate(_ context.Context, _ string, _ ...any) string { return "" }
func (h *fakeHost) CallPlugin(_ context.Context, _, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("not implemented in fake")
}
func (h *fakeHost) PublishEvent(_ context.Context, _, _, _ string) error { return nil }
func (h *fakeHost) EntitySoftDelete(_ context.Context, _ string, _ int64, _ string) error { return nil }
func (h *fakeHost) EntityRestore(_ context.Context, _ string, _ int64) error { return nil }
func (h *fakeHost) EntityHardDelete(_ context.Context, _ string, _ int64, _ string) error { return nil }
func (h *fakeHost) RecycleBinList(_ context.Context, _ string) (json.RawMessage, error) { return nil, nil }
func (h *fakeHost) SecureConfigGet(_ context.Context, _ string) (string, error) { return "", nil }
func (h *fakeHost) SecureConfigSet(_ context.Context, _, _ string) error { return nil }
func (h *fakeHost) CustomFieldsGet(_ context.Context, _ string, _ int64, _ []string) (map[string]any, error) {
	return nil, nil
}
func (h *fakeHost) CustomFieldsSet(_ context.Context, _ string, _ int64, _ map[string]any) error { return nil }
func (h *fakeHost) CustomFieldsQuery(_ context.Context, _ string, _ []plugin.CustomFieldFilter) ([]int64, error) {
	return nil, nil
}
func (h *fakeHost) StoreFile(_ context.Context, _ string, _ []byte, _ map[string]string) error { return nil }
func (h *fakeHost) GetFile(_ context.Context, _ string) ([]byte, map[string]string, error) {
	return nil, nil, fmt.Errorf("not found")
}
func (h *fakeHost) DeleteFile(_ context.Context, _ string) error { return nil }
func (h *fakeHost) ListFiles(_ context.Context, _ string) ([]plugin.FileInfo, error) { return nil, nil }

// --- dialect detection ---

func TestDetectDialectPostgres(t *testing.T) {
	h := newFakeHost(dialectPostgres)
	d := detectDialect(context.Background(), h)
	if d != dialectPostgres {
		t.Fatalf("dialect = %s, want postgres", d)
	}
}

func TestDetectDialectMySQL(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	d := detectDialect(context.Background(), h)
	if d != dialectMySQL {
		t.Fatalf("dialect = %s, want mysql", d)
	}
}

// --- schema migration ---

func TestMigrateSchemaCreatesVersionTable(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	if err := migrateSchema(context.Background(), h, dialectMySQL); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}
	if len(h.appliedVersions()) == 0 {
		t.Fatal("expected at least one applied version")
	}
	// v1 should be recorded.
	var sawV1 bool
	for _, row := range h.appliedVersions() {
		if toInt(row["version"]) == 1 {
			sawV1 = true
		}
	}
	if !sawV1 {
		t.Fatal("version 1 not recorded")
	}
}

func TestMigrateSchemaIdempotent(t *testing.T) {
	h := newFakeHost(dialectPostgres)
	if err := migrateSchema(context.Background(), h, dialectPostgres); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	versionsAfterFirst := len(h.appliedVersions())
	if err := migrateSchema(context.Background(), h, dialectPostgres); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := len(h.appliedVersions()); got != versionsAfterFirst {
		t.Fatalf("second migration added versions: got %d, want %d", got, versionsAfterFirst)
	}
}

func TestMigrateSchemaUnknownDialectFails(t *testing.T) {
	h := newFakeHost(dialectUnknown)
	err := migrateSchema(context.Background(), h, dialectUnknown)
	if err == nil || !strings.Contains(err.Error(), "could not detect") {
		t.Fatalf("expected detect error, got %v", err)
	}
}

// --- OTRS FAQ import mapping ---

func TestMapOTRSFAQItem(t *testing.T) {
	item := otrsFAQItem{
		Title:    "How to Reset Your Password",
		Category: "Account",
		Field1:   "<p>Click the reset link.</p>",
		Field2:   "Reset your password via the link.",
		Keywords: "password, reset, account",
		Author:   "admin",
		FAQID:    "42",
		ValidID:  1,
	}
	art := mapOTRSFAQItem(item)
	if art.Title != "How to Reset Your Password" {
		t.Errorf("title = %q", art.Title)
	}
	if art.Slug != "how-to-reset-your-password" {
		t.Errorf("slug = %q, want how-to-reset-your-password", art.Slug)
	}
	if art.Source != "otrs_faq" {
		t.Errorf("source = %q", art.Source)
	}
	if art.SourceID != "42" {
		t.Errorf("source_id = %q", art.SourceID)
	}
	if art.Status != "published" {
		t.Errorf("status = %q, want published", art.Status)
	}
	if art.Tags != "password,reset,account" {
		t.Errorf("tags = %q", art.Tags)
	}
}

func TestMapOTRSFAQItemInvalidStateMapsToDraft(t *testing.T) {
	item := otrsFAQItem{Title: "Old", FAQID: "9", ValidID: 2}
	art := mapOTRSFAQItem(item)
	if art.Status != "draft" {
		t.Fatalf("status = %q, want draft for invalid OTRS state", art.Status)
	}
}

func TestMapOTRSFAQItemFallsBackToUntitled(t *testing.T) {
	item := otrsFAQItem{FAQID: "7"}
	art := mapOTRSFAQItem(item)
	if art.Title != "Untitled FAQ 7" {
		t.Fatalf("title = %q, want Untitled FAQ 7", art.Title)
	}
	if art.Slug != "untitled-faq-7" {
		t.Fatalf("slug = %q, want untitled-faq-7", art.Slug)
	}
}

func TestMapOTRSFAQItemDerivesSummaryFromBody(t *testing.T) {
	item := otrsFAQItem{
		Title:  "Test",
		Field1: "<b>Some <i>HTML</i> body</b> with tags",
		FAQID:  "1",
	}
	art := mapOTRSFAQItem(item)
	// stripHTML should remove tags; summary derived from stripped body.
	if strings.Contains(art.Summary, "<") {
		t.Errorf("summary should be stripped of HTML, got %q", art.Summary)
	}
	if art.Summary == "" {
		t.Error("summary should be derived from body when Field2 is empty")
	}
}

// --- import flow ---

const otrsFAQXML = `<?xml version="1.0" encoding="UTF-8"?>
<FAQExport>
  <FAQItem>
    <Title>How to Reset Your Password</Title>
    <Category>Account</Category>
    <Field_1>Click the reset link sent to your email.</Field_1>
    <Field_2>Reset your password via the email link.</Field_2>
    <Keywords>password, reset</Keywords>
    <Author>admin</Author>
    <FAQID>101</FAQID>
    <ValidID>1</ValidID>
  </FAQItem>
  <FAQItem>
    <Title>Configuring Notifications</Title>
    <Category>Settings</Category>
    <Field_1>Go to Settings &gt; Notifications.</Field_1>
    <Field_2>Notification preferences.</Field_2>
    <Keywords>notifications, settings</Keywords>
    <Author>admin</Author>
    <FAQID>102</FAQID>
    <ValidID>1</ValidID>
  </FAQItem>
</FAQExport>`

func TestImportOTRSFAQInsertsArticles(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	res, err := importOTRSFAQ(context.Background(), h, nil, 1, []byte(otrsFAQXML))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Imported != 2 {
		t.Fatalf("imported = %d, want 2", res.Imported)
	}
	rows := h.tables["gk_kb_articles"]
	if len(rows) != 2 {
		t.Fatalf("stored rows = %d, want 2", len(rows))
	}
	// org_id preserved on every row.
	for _, row := range rows {
		if toInt64(row["org_id"]) != 1 {
			t.Errorf("row org_id = %v, want 1", row["org_id"])
		}
		if toString(row["source"]) != "otrs_faq" {
			t.Errorf("row source = %q, want otrs_faq", row["source"])
		}
	}
}

func TestImportOTRSFAQIsIdempotent(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	if _, err := importOTRSFAQ(context.Background(), h, nil, 1, []byte(otrsFAQXML)); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := importOTRSFAQ(context.Background(), h, nil, 1, []byte(otrsFAQXML)); err != nil {
		t.Fatalf("second import: %v", err)
	}
	rows := h.tables["gk_kb_articles"]
	if len(rows) != 2 {
		t.Fatalf("after re-import, rows = %d, want 2 (upsert not duplicate)", len(rows))
	}
}

func TestImportOTRSFAQPreservesOrgIDIsolation(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	// Import into org 1.
	if _, err := importOTRSFAQ(context.Background(), h, nil, 1, []byte(otrsFAQXML)); err != nil {
		t.Fatalf("import org 1: %v", err)
	}
	// Import into org 2 — should not collide with org 1's slugs.
	if _, err := importOTRSFAQ(context.Background(), h, nil, 2, []byte(otrsFAQXML)); err != nil {
		t.Fatalf("import org 2: %v", err)
	}
	rows := h.tables["gk_kb_articles"]
	if len(rows) != 4 {
		t.Fatalf("total rows = %d, want 4 (2 per org)", len(rows))
	}
	org1 := 0
	org2 := 0
	for _, row := range rows {
		switch toInt64(row["org_id"]) {
		case 1:
			org1++
		case 2:
			org2++
		}
	}
	if org1 != 2 || org2 != 2 {
		t.Fatalf("org distribution = %d/%d, want 2/2", org1, org2)
	}
}

func TestImportOTRSFAQRejectsZeroOrg(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	_, err := importOTRSFAQ(context.Background(), h, nil, 0, []byte(otrsFAQXML))
	if err == nil || !strings.Contains(err.Error(), "no active org") {
		t.Fatalf("expected no-active-org error, got %v", err)
	}
}

func TestImportOTRSFAQRejectsEmptyPayload(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	_, err := importOTRSFAQ(context.Background(), h, nil, 1, nil)
	if err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func TestImportOTRSFAQRejectsUnparseableXML(t *testing.T) {
	h := newFakeHost(dialectMySQL)
	_, err := importOTRSFAQ(context.Background(), h, nil, 1, []byte("not xml at all <<<<"))
	if err == nil || !strings.Contains(err.Error(), "no FAQItem") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

// --- handleImport via Plugin.Call ---

func TestCallImportRoutesToImporter(t *testing.T) {
	p := New()
	p.host = newFakeHost(dialectMySQL)
	p.dialect = dialectMySQL
	args, _ := json.Marshal(map[string]any{
		"_body":         otrsFAQXML,
		"_content_type": "application/xml",
		"_is_admin":     true,
		"_user_role":    "Admin",
	})
	result, err := p.Call("kb_import", args)
	if err != nil {
		t.Fatalf("Call kb_import: %v", err)
	}
	var res importResult
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Imported != 2 {
		t.Fatalf("imported = %d, want 2", res.Imported)
	}
	if res.OrgID != 1 {
		t.Fatalf("org_id = %d, want 1", res.OrgID)
	}
}

func TestCallImportNoBodyReturns400(t *testing.T) {
	p := New()
	p.host = newFakeHost(dialectMySQL)
	p.dialect = dialectMySQL
	result, err := p.Call("kb_import", json.RawMessage(`{"_is_admin":true,"_user_role":"Admin","_body":""}`))
	if err != nil {
		t.Fatalf("Call returned go error: %v (expected JSON error body)", err)
	}
	var body map[string]any
	_ = json.Unmarshal(result, &body)
	status, _ := body["status"].(float64)
	if int(status) != 400 {
		t.Fatalf("status = %v, want 400", body["status"])
	}
}


// --- error response convention ---

func TestErrorResponseSetsStatus(t *testing.T) {
	raw, err := errorResponse(404, "not found")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "not found" {
		t.Errorf("error = %v", body["error"])
	}
	if int(body["status"].(float64)) != 404 {
		t.Errorf("status = %v, want 404", body["status"])
	}
}

// --- slugify ---

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":          "hello-world",
		"  spaced  ":          "spaced",
		"How to / Reset.Pass": "how-to-reset-pass",
		"":                     "article",
		"---":                  "article",
		"UPPER Case":          "upper-case",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}