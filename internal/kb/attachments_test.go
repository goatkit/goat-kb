package kb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// rowHost is a tiny table store over fakeHost. It answers single-table
// SELECT/INSERT/DELETE statements whose WHERE clause is a chain of
// "col = ?" terms, and, like the real HostAPI, DBExec returns rows affected
// (never an insert id).
type rowHost struct {
	*fakeHost
	org    int64
	tables map[string][]map[string]any
	nextID map[string]int64
}

func newRowHost(org int64) *rowHost {
	return &rowHost{
		fakeHost: newFakeHost(dialectMySQL),
		org:      org,
		tables:   map[string][]map[string]any{},
		nextID:   map[string]int64{},
	}
}

func (h *rowHost) OrgID(context.Context) int64 { return h.org }

func (h *rowHost) add(table string, row map[string]any) int64 {
	h.nextID[table]++
	row["id"] = h.nextID[table]
	h.tables[table] = append(h.tables[table], row)
	return h.nextID[table]
}

var (
	reTable = regexp.MustCompile(`(?i)(?:FROM|INTO)\s+(\w+)`)
	reCond  = regexp.MustCompile(`(\w+) = \?`)
	reCols  = regexp.MustCompile(`(?i)\(([^)]*)\)\s*VALUES`)
)

func (h *rowHost) match(query string, args []any) (string, []int) {
	table := reTable.FindStringSubmatch(query)[1]
	where := query[strings.Index(strings.ToUpper(query), "WHERE")+len("WHERE"):]
	conds := reCond.FindAllStringSubmatch(where, -1)
	argOff := len(args) - len(conds)
	var idx []int
	for i, row := range h.tables[table] {
		ok := true
		for j, c := range conds {
			if fmt.Sprint(row[c[1]]) != fmt.Sprint(args[argOff+j]) {
				ok = false
				break
			}
		}
		if ok && (!strings.Contains(query, "status = 'published'") || row["status"] == "published") {
			idx = append(idx, i)
		}
	}
	return table, idx
}

func (h *rowHost) DBQuery(_ context.Context, query string, args ...any) ([]map[string]any, error) {
	table, idx := h.match(query, args)
	out := make([]map[string]any, 0, len(idx))
	for _, i := range idx {
		out = append(out, h.tables[table][i])
	}
	return out, nil
}

func (h *rowHost) DBExec(_ context.Context, query string, args ...any) (int64, error) {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), "INSERT") {
		table := reTable.FindStringSubmatch(query)[1]
		row := map[string]any{}
		for i, c := range strings.Split(reCols.FindStringSubmatch(query)[1], ",") {
			row[strings.TrimSpace(c)] = args[i]
		}
		h.add(table, row)
		return 1, nil
	}
	table, idx := h.match(query, args)
	for k := len(idx) - 1; k >= 0; k-- {
		i := idx[k]
		h.tables[table] = append(h.tables[table][:i], h.tables[table][i+1:]...)
	}
	return int64(len(idx)), nil
}

func callJSON(t *testing.T, p *Plugin, fn string, args map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := p.Call(fn, raw)
	if err != nil {
		t.Fatalf("%s: %v", fn, err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("%s: decode %s: %v", fn, out, err)
	}
	return m
}

// An upload must answer with the new attachment's id. DBExec only reports
// rows affected (1), so returning that as the id made the editor's delete
// button remove attachment 1 of a different article.
func TestAttachmentUploadReturnsNewRowIDAndDeleteStaysOnArticle(t *testing.T) {
	h := newRowHost(1)
	other := h.add("gk_kb_articles", map[string]any{"org_id": int64(1), "status": "published"})
	mine := h.add("gk_kb_articles", map[string]any{"org_id": int64(1), "status": "published"})
	h.add("gk_kb_attachments", map[string]any{"org_id": int64(1), "article_id": other, "file_key": "kb/1/1/logo.jpeg"})
	p := &Plugin{host: h}

	up := callJSON(t, p, "handleAttachmentUpload", map[string]any{
		"id": fmt.Sprint(mine), "filename": "notes.txt", "content_type": "text/plain",
		"data": base64.StdEncoding.EncodeToString([]byte("hello")),
	})
	newID := int64(up["id"].(float64))
	if newID != 2 {
		t.Fatalf("upload id = %d, want 2 (the inserted row); response %v", newID, up)
	}

	// The other article's attachment cannot be deleted through this article.
	del := callJSON(t, p, "handleAttachmentDelete", map[string]any{"id": fmt.Sprint(mine), "aid": "1"})
	if del["status"] != float64(404) {
		t.Fatalf("cross-article delete = %v, want 404", del)
	}

	del = callJSON(t, p, "handleAttachmentDelete", map[string]any{"id": fmt.Sprint(mine), "aid": fmt.Sprint(newID)})
	if del["status"] != "deleted" {
		t.Fatalf("delete own attachment = %v", del)
	}
	left := h.tables["gk_kb_attachments"]
	if len(left) != 1 || left[0]["article_id"] != other {
		t.Fatalf("remaining attachments = %v, want only the other article's", left)
	}
}

// kb_article must use the same single-org fallback as kb_list: a session
// with no active organisation saw articles in the list but 404 on open.
func TestArticleWithoutActiveOrgUsesDefaultOrg(t *testing.T) {
	h := newRowHost(0)
	id := h.add("gk_kb_articles", map[string]any{
		"org_id": int64(1), "status": "published", "visibility": "public", "title": "Welcome",
	})
	p := &Plugin{host: h}

	got := callJSON(t, p, "kb_article", map[string]any{"id": fmt.Sprint(id), "_user_role": "Agent"})
	if got["title"] != "Welcome" {
		t.Fatalf("kb_article = %v, want the org-1 article", got)
	}
}
