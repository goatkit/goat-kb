// Package kb implements the GoatFlow Knowledge Base plugin.
//
// The KB plugin provides article management, full-text search via zinc,
// OTRS FAQ import, and multi-tenant RBAC-aware access control.
package kb

import (
	"context"
	"encoding/json"
	"fmt"

	plugin "github.com/goatkit/goatflow/pkg/plugin"
)

// Plugin implements the GoatFlow KB plugin as a gRPC plugin.
type Plugin struct {
	host    plugin.HostAPI
	dialect dialect
}

// New returns a new KB plugin instance.
func New() *Plugin {
	return &Plugin{}
}

// GKRegister returns the plugin self-description.
func (p *Plugin) GKRegister() (*plugin.GKRegistration, error) {
	return &plugin.GKRegistration{
		Name:        "kb",
		Version:     "0.1.0",
		Description: "Knowledge Base plugin with zinc search, OTRS import, and multi-tenant RBAC",
		Author:      "GoatKit Team",
		License:     "Apache-2.0",
		Homepage:    "https://github.com/goatkit/goatflow-kb",

		MinHostVersion: "0.9.0",

		Groups: []plugin.GroupSpec{
			{Name: "kb-admin", Description: "KB administrators — manage articles and imports"},
			{Name: "kb-editor", Description: "KB editors — create and edit articles"},
		},

		Routes: []plugin.RouteSpec{
			{
				Method:      "GET",
				Path:        "/knowledge-base",
				Handler:     "kb_list",
				Middleware:  []string{"auth"},
				Description: "List KB articles (org-scoped, permission-filtered)",
			},
			{
				Method:      "GET",
				Path:        "/kb/search",
				Handler:     "kb_search",
				Middleware:  []string{"auth"},
				Description: "Full-text search KB articles via zinc (org-scoped)",
			},
			{
				Method:      "GET",
				Path:        "/kb/article/:id",
				Handler:     "kb_article",
				Middleware:  []string{"auth"},
				Description: "View a single KB article (permission-checked)",
			},
			{
				Method:      "POST",
				Path:        "/admin/kb/import",
				Handler:     "kb_import",
				Middleware:  []string{"admin"},
				Description: "Import OTRS FAQ articles (admin only)",
			},
		},

		MenuItems: []plugin.MenuItemSpec{
			{
				ID:       "kb-admin",
				Label:    "Knowledge Base",
				Icon:     "book",
				Path:     "/admin/kb",
				Location: "admin",
				Order:    50,
			},
		},

		Widgets: []plugin.WidgetSpec{
			{
				ID:          "kb-recent",
				Title:       "Recent KB Articles",
				Description: "Shows recently published knowledge base articles",
				Handler:     "kb_widget_recent",
				Location:    "agent_home",
				Size:        "medium",
				Refreshable: true,
				RefreshSec:  300,
			},
		},

		ErrorCodes: []plugin.ErrorCodeSpec{
			{Code: "article_not_found", Message: "Knowledge base article not found", HTTPStatus: 404},
			{Code: "invalid_search", Message: "Invalid search query", HTTPStatus: 400},
			{Code: "import_failed", Message: "OTRS FAQ import failed", HTTPStatus: 500},
			{Code: "unauthorized", Message: "You do not have access to this article", HTTPStatus: 403},
			{Code: "invalid_article_id", Message: "Invalid article ID", HTTPStatus: 400},
		},

		Resources: &plugin.ResourceRequest{
			MemoryMB:        256,
			CallTimeout:     "30s",
			InitTimeout:     "10s",
			ShutdownTimeout: "5s",
			Permissions: []plugin.Permission{
				{Type: "db", Access: "readwrite", Scope: []string{"gk_kb_*"}},
				{Type: "cache", Access: "readwrite"},
				{Type: "http", Access: "readwrite", Scope: []string{"*"}},
				{Type: "config", Access: "read"},
				{Type: "plugin_call", Access: "read", Scope: []string{"*"}},
			},
		},
	}, nil
}

// InitWithHost receives the HostAPI from the platform and brings the KB
// schema up to the current version before the plugin serves requests.
func (p *Plugin) InitWithHost(config map[string]string, host plugin.HostAPI) error {
	p.host = host
	p.dialect = detectDialect(context.Background(), host)
	host.Log(context.Background(), "info", "KB plugin initialized", map[string]any{
		"version": "0.1.0",
		"dialect": string(p.dialect),
	})
	if err := migrateSchema(context.Background(), host, p.dialect); err != nil {
		return fmt.Errorf("KB plugin init: %w", err)
	}
	return nil
}

// Init is the fallback for hosts that don't provide HostAPI.
func (p *Plugin) Init(config map[string]string) error {
	return fmt.Errorf("KB plugin requires HostAPI — host must support GKPluginWithHost")
}

// Call dispatches handler calls from the host.
func (p *Plugin) Call(fn string, args json.RawMessage) (json.RawMessage, error) {
	ctx := context.Background()
	switch fn {
	case "kb_list":
		return p.handleList(ctx, args)
	case "kb_search":
		return p.handleSearch(ctx, args)
	case "kb_article":
		return p.handleArticle(ctx, args)
	case "kb_import":
		return p.handleImport(ctx, args)
	case "kb_widget_recent":
		return p.handleRecentWidget(ctx, args)
	default:
		return nil, fmt.Errorf("unknown function: %s", fn)
	}
}

// Shutdown cleans up plugin resources.
func (p *Plugin) Shutdown() error {
	if p.host != nil {
		p.host.Log(context.Background(), "info", "KB plugin shutting down", nil)
	}
	return nil
}
