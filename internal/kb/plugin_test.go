package kb

import (
	"encoding/json"
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

	if reg.Name != "kb" {
		t.Fatalf("Name = %q, want kb", reg.Name)
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

	if len(reg.MenuItems) != 1 || reg.MenuItems[0].Location != "admin" {
		t.Fatalf("admin menu item not registered: %+v", reg.MenuItems)
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
