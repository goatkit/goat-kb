package kb

import (
	"strings"
	"testing"
)

// TestSanitiseHTML verifies that sanitiseHTML neutralises XSS vectors
// (script/iframe injection, inline event handlers, javascript: URLs) while
// preserving legitimate formatting elements and attributes allowed by the
// bluemonday policy.
func TestSanitiseHTML(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		mustContain    []string
		mustNotContain []string
		wantEmpty      bool
	}{
		{
			name:           "script tag stripped",
			input:          `<script>alert(1)</script>`,
			mustNotContain: []string{"<script", "alert"},
		},
		{
			name:           "iframe tag stripped",
			input:          `<iframe src="evil.com">`,
			mustNotContain: []string{"<iframe"},
		},
		{
			name:           "inline onerror handler stripped",
			input:          `<p onerror="alert(1)">text</p>`,
			mustContain:    []string{"text"},
			mustNotContain: []string{"onerror"},
		},
		{
			name:           "javascript URL scheme stripped",
			input:          `<a href="javascript:alert(1)">click</a>`,
			mustContain:    []string{"click"},
			mustNotContain: []string{"javascript:"},
		},
		{
			name:        "bold formatting preserved",
			input:       `<b>bold</b>`,
			mustContain: []string{`<b>bold</b>`},
		},
		{
			name:        "legitimate class attribute preserved",
			input:       `<p class="legit">hello</p>`,
			mustContain: []string{"hello", `class="legit"`},
		},
		{
			name:        "image src and alt preserved",
			input:       `<img src="https://example.com/img.png" alt="pic">`,
			mustContain: []string{`src="https://`, `alt="pic"`},
		},
		{
			name:      "empty input yields empty output",
			input:     "",
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitiseHTML(tt.input)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("sanitiseHTML(%q) = %q, want empty string", tt.input, got)
				}
				return
			}
			for _, want := range tt.mustContain {
				if !strings.Contains(got, want) {
					t.Errorf("sanitiseHTML(%q) = %q, expected to contain %q", tt.input, got, want)
				}
			}
			for _, bad := range tt.mustNotContain {
				if strings.Contains(got, bad) {
					t.Errorf("sanitiseHTML(%q) = %q, must NOT contain %q", tt.input, got, bad)
				}
			}
		})
	}
}

// TestEscapeLike verifies that escapeLike escapes the SQL LIKE wildcards
// (% and _) and existing backslashes so user-supplied search terms cannot
// alter query semantics.
func TestEscapeLike(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"percent escaped", `test%name`, `test\%name`},
		{"underscore escaped", `test_name`, `test\_name`},
		{"backslash doubled", `test\name`, `test\\name`},
		{"normal unchanged", `normal`, `normal`},
		{"empty string", ``, ``},
		{"percent and underscore together", `50%_off`, `50\%\_off`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeLike(tt.input); got != tt.want {
				t.Errorf("escapeLike(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSanitiseText verifies that sanitiseText strips C0 control characters
// (except tab/newline/cr) while preserving printable text, tab, and DEL
// (0x7F, which is outside the < 0x20 strip range).
func TestSanitiseText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"null byte stripped", "hello\x00world", "helloworld"},
		{"control bytes stripped", "hello\x01\x02world", "helloworld"},
		{"unit separator stripped", "hello\x1fworld", "helloworld"},
		{"del preserved (above range)", "hello\x7fworld", "hello\x7fworld"},
		{"normal text unchanged", "normal text", "normal text"},
		{"tab preserved", "tab\there", "tab\there"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitiseText(tt.input); got != tt.want {
				t.Errorf("sanitiseText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
