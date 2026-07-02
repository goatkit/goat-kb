package kb

import (
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
func renderTemplate(name string, data map[string]any) (string, error) {
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
