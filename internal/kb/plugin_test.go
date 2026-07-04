package kb

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/goatkit/goatflow/pkg/plugin/grpcutil"
)

func TestPluginImplementsGRPCInterfaces(t *testing.T) {
	var _ grpcutil.GKPluginWithHost = (*Plugin)(nil)
}

func TestGKRegisterMilestoneOneContract(t *testing.T) {
	reg, err := New().GKRegister()
	if err != nil {
		t.Fatalf("GKRegister returned error: %v", err)
	}

	if reg.Name != "goat-kb" {
		t.Fatalf("Name = %q, want goat-kb", reg.Name)
	}
	if reg.Version == "" || reg.Description == "" || reg.Author == "" || reg.License == "" {
		t.Fatalf("registration metadata incomplete: %+v", reg)
	}
	if reg.MinHostVersion != "0.9.0" {
		t.Fatalf("MinHostVersion = %q, want 0.9.0", reg.MinHostVersion)
	}

	wantRoutes := map[string]string{
		"GET /knowledge-base":   "kb_list",
		"GET /kb/search":        "kb_search",
		"GET /kb/article/:id":   "kb_article",
		"POST /admin/kb/import": "kb_import",
	}
	for _, route := range reg.Routes {
		key := route.Method + " " + route.Path
		if wantRoutes[key] == route.Handler {
			delete(wantRoutes, key)
		}
	}
	if len(wantRoutes) > 0 {
		t.Fatalf("missing route registrations: %+v", wantRoutes)
	}

	// Verify admin menu item exists
	var hasAdminMenu, hasCustomerMenu bool
	for _, mi := range reg.MenuItems {
		if mi.ID == "kb-admin" && mi.Location == "admin" {
			hasAdminMenu = true
		}
		if mi.ID == "kb-customer" && mi.Location == "customer" {
			hasCustomerMenu = true
		}
	}
	if !hasAdminMenu {
		t.Fatalf("admin menu item not registered: %+v", reg.MenuItems)
	}
	if !hasCustomerMenu {
		t.Fatalf("customer menu item not registered: %+v", reg.MenuItems)
	}
	if len(reg.Widgets) != 1 || reg.Widgets[0].Handler != "kb_widget_recent" {
		t.Fatalf("recent article widget not registered: %+v", reg.Widgets)
	}

	if reg.Resources == nil {
		t.Fatal("resources missing")
	}
	if reg.Resources.MemoryMB != 256 || reg.Resources.CallTimeout != "30s" {
		t.Fatalf("unexpected resources: %+v", reg.Resources)
	}
	for _, perm := range reg.Resources.Permissions {
		if perm.Type == "db" && perm.Access == "readwrite" && len(perm.Scope) == 1 && perm.Scope[0] == "gk_kb_*" {
			return
		}
	}
	t.Fatalf("db readwrite permission for gk_kb_* missing: %+v", reg.Resources.Permissions)
}

func TestCallUnknownFunctionFails(t *testing.T) {
	_, err := New().Call("missing", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unknown function") {
		t.Fatalf("Call unknown error = %v, want unknown function", err)
	}
}

func TestInitRequiresHostAPI(t *testing.T) {
	err := New().Init(map[string]string{"plugin_name": "kb"})
	if err == nil || !strings.Contains(err.Error(), "requires HostAPI") {
		t.Fatalf("Init error = %v, want HostAPI requirement", err)
	}
}

func TestInitTemplates(t *testing.T) {
	if err := initTemplates(); err != nil {
		t.Fatalf("initTemplates failed: %v", err)
	}

	templateMu.RLock()
	defer templateMu.RUnlock()
	want := map[string]bool{
		"kb_list.pongo2":                true,
		"article_detail.pongo2":         true,
		"admin_kb_categories.pongo2":    true,
		"widget_recent.pongo2":          true,
	}

	for name := range want {
		if _, ok := compiledTemplates[name]; !ok {
			t.Errorf("template %s not compiled", name)
		}
	}

	// Quick render smoke test — render each template with minimal context
	for name := range compiledTemplates {
		_, err := renderTemplate(name, map[string]any{
			"IsAdmin":        true,
			"IsAgent":        false,
			"IsCustomer":     false,
			"articles":       []any{},
			"page":           1,
			"totalPages":     1,
			"totalCount":     0,
			"Title":          "Test",
			"Content":        "<p>test</p>",
			"Summary":        "test summary",
			"Category":       "test",
			"Visibility":     "public",
			"Author":         "tester",
			"Tags":           []string{},
			"DateStr":        "2025-01-01",
			"Query":          "",
			"FilterCategory": "",
			"FilterScope":    "",
			"FilterStatus":   "",
			"Categories":     []string{},
			"RelatedArticles":  []any{},
			"RecentArticles":   []any{},
		})
		if err != nil {
			t.Errorf("renderTemplate(%s) failed: %v", name, err)
		}
	}
}

// TestNoDirectLocationNavigation is a regression test for a class of bugs where
// JS uses window.location.href or location.reload() for navigation/reload after
// fetch-based operations. GoatFlow's SPA router does NOT intercept these, causing
// the browser to hit the server directly and display raw JSON instead of HTML.
//
// The fix: all JS-initiated navigation must use <form>.submit() or <a>.click()
// so the SPA router intercepts it. This test scans ALL rendered template output
// AND the admin article edit form (built inline in handlers.go) for the banned
// patterns.
func TestNoDirectLocationNavigation(t *testing.T) {
	if err := initTemplates(); err != nil {
		t.Fatalf("initTemplates failed: %v", err)
	}

	ctx := map[string]any{
		"IsAdmin": true, "IsAgent": false, "IsCustomer": false,
		"articles": []any{}, "page": 1, "totalPages": 1, "totalCount": 0,
		"Title": "T", "Content": "<p>t</p>", "Summary": "s", "Category": "c",
		"Visibility": "public", "Author": "a", "Tags": []string{}, "DateStr": "2025-01-01",
		"Query": "", "FilterCategory": "", "FilterScope": "", "FilterStatus": "",
		"Categories": []string{}, "RelatedArticles": []any{}, "RecentArticles": []any{},
	}
	banned := []string{
		"window.location.href",
		"window.location.assign",
		"window.location.replace",
		"location.reload()",
	}

	// Check handlers.go inline JS (admin article edit form).
	src, err := os.ReadFile("handlers.go")
	if err != nil {
		t.Logf("skipping handlers.go check: %v", err)
	} else {
		for _, pattern := range banned {
			if strings.Contains(string(src), pattern) {
				t.Errorf("handlers.go contains %q — this bypasses the SPA router. Use form.submit() or a.click() instead.", pattern)
			}
		}
	}

	for name := range compiledTemplates {
		// Render with admin context (covers all {% if %} branches)
		html, err := renderTemplate(name, ctx)
		if err != nil {
			t.Fatalf("renderTemplate(%s) failed: %v", name, err)
		}
		for _, pattern := range banned {
			if strings.Contains(html, pattern) {
				t.Errorf("%s contains %q — this bypasses the SPA router and returns raw JSON. "+
					"Use form.submit() or a.click() instead.", name, pattern)
			}
		}
	}
}
