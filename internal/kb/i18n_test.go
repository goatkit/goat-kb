package kb

import (
	"regexp"
	"strings"
	"testing"
)

// TestTemplatesHaveNoHardcodedEnglish is a regression guard: it scans all
// embedded pongo2 templates for visible English text that isn't wrapped in
// {{ t("kb.key")|default:"English" }}. Runs as part of `make test` so a
// developer who adds a UI string without a translation call gets immediate
// feedback.
//
// It checks three things:
//  1. Visible text nodes (between > and <) — must be inside {{ }} blocks
//  2. Every t("...") call has a |default:"..." filter
//  3. JS alert/confirm/prompt strings are wrapped in {{ t() }}
func TestTemplatesHaveNoHardcodedEnglish(t *testing.T) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatalf("read template dir: %v", err)
	}

	// English word: 3+ chars, starts uppercase (catches "All", "Search", "Delete" etc.)
	englishWord := regexp.MustCompile(`[A-Z][a-z]{2,}`)
	// pongo2 expression block: {{ ... }} (dotall for multi-line)
	pongoExpr := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	// pongo2 control block: {% ... %}
	pongoCtrl := regexp.MustCompile(`(?s)\{%.*?%\}`)
	// HTML comment
	htmlComment := regexp.MustCompile(`(?s)<!--.*?-->`)
	// style/script blocks
	styleBlock := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	scriptBlock := regexp.MustCompile(`(?s)(<script[^>]*>)(.*?)(</script>)`)
	// HTML tag (including attributes)
	htmlTag := regexp.MustCompile(`<[^>]+>`)
	// HTML entity
	htmlEntity := regexp.MustCompile(`&[a-zA-Z#0-9]+;`)
	// t() call without |default
	tWithoutDefault := regexp.MustCompile(`\{\{\s*t\("[^"]*"\)\s*\}\}`)

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".pongo2") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			raw, err := templateFS.ReadFile("templates/" + name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			src := string(raw)

			// ── Check 1: visible text nodes ──
			// Strip everything that's allowed: {{ }}, {% %}, CSS, JS, comments, tags, entities
			visible := src
			visible = pongoExpr.ReplaceAllString(visible, "")
			visible = pongoCtrl.ReplaceAllString(visible, "")
			visible = regexp.MustCompile(`(?s)\{#.*?#\}`).ReplaceAllString(visible, "")
			visible = styleBlock.ReplaceAllString(visible, "")
			// Capture script content for check 3 before removing
			visible = scriptBlock.ReplaceAllString(visible, "")
			visible = htmlComment.ReplaceAllString(visible, "")
			visible = htmlTag.ReplaceAllString(visible, "")
			visible = htmlEntity.ReplaceAllString(visible, "")
			visible = strings.TrimSpace(visible)
			// Remove pure whitespace and punctuation
			visible = regexp.MustCompile(`[\s\n\r\t]+`).ReplaceAllString(visible, " ")
			if words := englishWord.FindAllString(visible, -1); len(words) > 0 {
				t.Errorf("untranslated visible English text found: %v\n"+
					"Remaining text after stripping (first 200 chars): %q",
					words, truncate(visible, 200))
			}

			// ── Check 2: every t() call has |default ──
			if matches := tWithoutDefault.FindAllString(src, -1); len(matches) > 0 {
				t.Errorf("found %d t() call(s) without |default filter (missing English fallback): %v",
					len(matches), matches)
			}

			// ── Check 3: JS alert/confirm/prompt strings are wrapped ──
			scriptMatches := scriptBlock.FindAllStringSubmatch(src, -1)
			for _, sm := range scriptMatches {
				scriptBody := sm[2]
				// Remove {{ }} blocks from script — what's left is raw JS
				scriptCleaned := pongoExpr.ReplaceAllString(scriptBody, "")
				for _, fn := range []string{"alert", "confirm", "prompt"} {
					// Match: alert("English...  or  alert('English...
					pat := regexp.MustCompile(fn + `\(\s*["'][A-Z][a-z]`)
					if loc := pat.FindStringIndex(scriptCleaned); loc != nil {
						ctx := scriptCleaned[max(0, loc[0]-10):min(len(scriptCleaned), loc[1]+40)]
						t.Errorf("untranslated %s() string in <script> block near: %q", fn, ctx)
					}
				}
			}
		})
	}
}

// TestTranslationKeysHaveDefaults verifies that translation keys used in
// templates and Go code follow the kb.* naming convention.
func TestTranslationKeyNamingConvention(t *testing.T) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatalf("read template dir: %v", err)
	}
	// Match t("kb.something") calls — keys must start with "kb."
	tCall := regexp.MustCompile(`t\("([^"]*)"\)`)
	badKeys := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".pongo2") {
			continue
		}
		raw, _ := templateFS.ReadFile("templates/" + name)
		for _, m := range tCall.FindAllStringSubmatch(string(raw), -1) {
			key := m[1]
			if !strings.HasPrefix(key, "kb.") {
				badKeys = append(badKeys, key)
			}
		}
	}
	if len(badKeys) > 0 {
		t.Errorf("translation keys not following kb.* convention: %v", badKeys)
	}
}
