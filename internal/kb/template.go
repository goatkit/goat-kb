package kb

import (
	"context"
	"embed"
	"fmt"
	"sync"

	"github.com/flosch/pongo2/v6"
)

//go:embed templates/*.pongo2
var templateFS embed.FS

var (
	compiledTemplates map[string]*pongo2.Template
	templateMu        sync.RWMutex
)

// initTemplates compiles all embedded .pongo2 template files at startup.
// Call from InitWithHost (or init()) so rendering paths never fail at runtime.
func initTemplates() error {
	templateMu.Lock()
	defer templateMu.Unlock()

	compiledTemplates = make(map[string]*pongo2.Template)

	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return fmt.Errorf("read template dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		data, err := templateFS.ReadFile("templates/" + name)
		if err != nil {
			return fmt.Errorf("read template %s: %w", name, err)
		}
		tpl, err := pongo2.FromString(string(data))
		if err != nil {
			return fmt.Errorf("compile template %s: %w", name, err)
		}
		compiledTemplates[name] = tpl
	}
	return nil
}

// renderTemplate executes a compiled .pongo2 template with the given data
// and returns the rendered HTML string. Returns an error if the template
// name hasn't been compiled (programming error — all should be compiled).
//
// If the caller did not provide a "t" function in data, a no-op is injected
// that returns "" so pongo2's |default filter yields the English fallback.
// Production callers use Plugin.render() which injects a real translator.
func renderTemplate(name string, data map[string]any) (string, error) {
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["t"]; !ok {
		data["t"] = func(string, ...any) string { return "" }
	}
	templateMu.RLock()
	tpl, ok := compiledTemplates[name]
	templateMu.RUnlock()
	if !ok {
		return "", fmt.Errorf("template not compiled: %s", name)
	}
	out, err := tpl.Execute(data)
	if err != nil {
		return "", fmt.Errorf("render template %s: %w", name, err)
	}
	return out, nil
}

// render injects the i18n "t" function into the template context and renders.
// The t function is backed by HostAPI.Translate, returning "" for missing keys
// so pongo2's |default filter provides the English fallback.
// This is the standard GoatFlow plugin i18n pattern — templates use
// {{ t("kb.key")|default:"English" }}, identical to host template syntax.
func (p *Plugin) render(name string, ctx context.Context, data map[string]any) (string, error) {
	if data == nil {
		data = map[string]any{}
	}
	data["t"] = func(key string, _ ...any) string {
		s := p.host.Translate(ctx, key)
		if s == "" || s == key {
			return ""
		}
		return s
	}
	return renderTemplate(name, data)
}

// tr translates a single key with an English fallback, for use in Go code
// that builds inline HTML (e.g. the admin article form via fmt.Sprintf).
func (p *Plugin) tr(ctx context.Context, key, fallback string) string {
	s := p.host.Translate(ctx, key)
	if s == "" || s == key {
		return fallback
	}
	return s
}
