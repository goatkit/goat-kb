package kb

import (
	"os"
	"strings"
	"testing"
)

// readHandlers loads handlers.go source as a string for source-level
// regression assertions. Tests in this file prevent re-introduction of
// previously fixed security bugs by checking the source text directly.
func readHandlers(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("handlers.go")
	if err != nil {
		t.Fatalf("os.ReadFile(\"handlers.go\") failed: %v", err)
	}
	return string(b)
}

// TestNoEscapeClauseInQueries is a regression test for a MariaDB syntax error
// (Error 1064). The previous code appended an `ESCAPE '\\'` clause to LIKE
// queries, but Go's `'\\'` becomes the SQL string literal `'\'`, which
// prematurely breaks the surrounding SQL string and is a syntax error.
// Backslash is already the default LIKE escape character in MySQL, MariaDB and
// PostgreSQL, so the explicit clause is unnecessary and must not return.
func TestNoEscapeClauseInQueries(t *testing.T) {
	src := readHandlers(t)
	if strings.Contains(src, `"ESCAPE"`) {
		t.Errorf("handlers.go contains \"ESCAPE\" LIKE clause: this causes MariaDB Error 1064 (backslash is the default LIKE escape already)")
	}
}

// TestTagChipUsesCreateTextNode is a regression test for an XSS vector in the
// article-tag chip renderer. The old addArticleTag inserted user-typed tag
// names with `innerHTML=text+`, which parsed attacker input as HTML. The fix
// inserts text via the DOM-safe document.createTextNode, so user input is
// treated as inert text rather than parsed markup.
func TestTagChipUsesCreateTextNode(t *testing.T) {
	src := readHandlers(t)
	if !strings.Contains(src, "createTextNode") {
		t.Errorf("handlers.go does not use createTextNode: tag chips must insert user text via document.createTextNode to avoid XSS")
	}
	// Locate addArticleTag and ensure it does not use the vulnerable pattern.
	if idx := strings.Index(src, "addArticleTag"); idx != -1 {
		// Examine a window starting at addArticleTag; the function body is on
		// the same line, so a substring scan from the function start is enough.
		tail := src[idx:]
		if end := strings.Index(tail, "\n"); end != -1 {
			tail = tail[:end]
		}
		if strings.Contains(tail, "innerHTML=text+") {
			t.Errorf("addArticleTag uses innerHTML=text+: user-supplied tag names would be parsed as HTML (XSS). Use createTextNode instead.")
		}
	}
}

// TestLoginJSEscapesScriptClose is a regression test for a script-breakout
// XSS. The login JS is embedded inside a <script> block; any literal `</`
// inside the payload closes the script tag early. The old code escaped only
// `\` and `'`. The fix additionally rewrites `</` to `<\/` (valid JS that
// evaluates to `</` at runtime) so it cannot terminate the script element.
func TestLoginJSEscapesScriptClose(t *testing.T) {
	src := readHandlers(t)
	if !strings.Contains(src, `<\/`) {
		t.Errorf("handlers.go does not escape </ to <\\/: embedded login JS could break out of its <script> block via a literal </")
	}
}

// TestCategorySelectUsesHtmlEscapeString is a regression test for attribute
// injection. The category <select> builds <option value="%s"> tags from
// category names and article fields; without HTML escaping, a value
// containing `" onmouseover="...` would inject arbitrary attributes. The fix
// wraps both the managed-list names and the article's own category in
// html.EscapeString before interpolation.
func TestCategorySelectUsesHtmlEscapeString(t *testing.T) {
	src := readHandlers(t)
	if !strings.Contains(src, "html.EscapeString(name)") {
		t.Errorf("handlers.go does not call html.EscapeString(name): category option values are vulnerable to attribute injection")
	}
	if !strings.Contains(src, "html.EscapeString(article.Category)") {
		t.Errorf("handlers.go does not call html.EscapeString(article.Category): the article-category option value is vulnerable to attribute injection")
	}
}

// TestNoCSRFTokenField is a regression test for false security. A CSRFToken
// field was previously rendered into forms but never validated on the server,
// giving the appearance of CSRF protection without any actual defence. The
// field was removed entirely; it must not be reintroduced unless paired with
// server-side validation.
func TestNoCSRFTokenField(t *testing.T) {
	src := readHandlers(t)
	if strings.Contains(src, "CSRFToken") {
		t.Errorf("handlers.go references CSRFToken: this field was removed because it was never validated (false security). Do not reintroduce without server-side validation.")
	}
	if strings.Contains(src, "csrf_token") {
		t.Errorf("handlers.go references csrf_token: this field was removed because it was never validated (false security). Do not reintroduce without server-side validation.")
	}
}

// TestInsertAdjacentHTMLHasClosingParen is a regression test for a JS
// SyntaxError that broke all article editing. The addArticleTag function
// calls insertAdjacentHTML('beforeend', '...') — if the closing parenthesis
// is missing, the entire inline <script> block fails to parse, the form submit
// handler never attaches, and the form falls back to GET submission (losing
// data). This was caused by a sed replacement that dropped the closing paren.
func TestInsertAdjacentHTMLHasClosingParen(t *testing.T) {
	src := readHandlers(t)
	// Find every insertAdjacentHTML call and verify each has a closing )
	idx := 0
	for {
		pos := strings.Index(src[idx:], "insertAdjacentHTML(")
		if pos < 0 {
			break
		}
		pos += idx
		// Extract the call — find the matching closing paren by counting
		call := src[pos:]
		depth := 0
		closed := false
		for i, ch := range call {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
				if depth == 0 {
					closed = true
					break
				}
			}
			_ = i
		}
		if !closed {
			t.Errorf("insertAdjacentHTML call at position %d is missing its closing ')': %s", pos, call[:min(len(call), 80)])
		}
		idx = pos + len("insertAdjacentHTML(")
	}
}
