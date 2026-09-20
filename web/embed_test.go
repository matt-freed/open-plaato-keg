package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// pages are the UI entry points that must exist for the navigation to work.
var pages = []string{
	"index.html", "taplist.html", "taplist-setup.html", "tap-handles.html",
	"beverages.html", "history.html", "dashboard-setup.html", "setup.html",
	"style.css",
}

func TestPagesArePresent(t *testing.T) {
	static := Static()
	for _, name := range pages {
		data, err := fs.ReadFile(static, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

var scriptBlock = regexp.MustCompile(`(?s)<script(?:\s[^>]*)?>(.*?)</script>`)

// Every brace, bracket and parenthesis in an inline script must balance.
//
// The pages carry no build step, so a stray brace left behind by an edit would
// otherwise only surface as a blank page in a browser.
func TestInlineScriptsAreBalanced(t *testing.T) {
	static := Static()
	for _, name := range pages {
		if !strings.HasSuffix(name, ".html") {
			continue
		}
		data, err := fs.ReadFile(static, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, match := range scriptBlock.FindAllStringSubmatch(string(data), -1) {
			if err := checkBalanced(match[1]); err != nil {
				t.Errorf("%s script %d: %v", name, i, err)
			}
		}
	}
}

// checkBalanced walks JavaScript source tracking the delimiter stack, skipping
// string literals, template literals and comments.
func checkBalanced(src string) error {
	var stack []rune
	pairs := map[rune]rune{')': '(', ']': '[', '}': '{'}

	runes := []rune(src)
	// templateDepth counts template literals that the cursor is inside, so a
	// ${...} substitution is still treated as code.
	var templateDepth int
	// prev is the last character that was actually code, used to tell a regex
	// literal from a division.
	var prev rune

	for i := 0; i < len(runes); i++ {
		c := runes[i]

		switch {
		case c == '/' && i+1 < len(runes) && runes[i+1] == '/':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i += 2
			for i+1 < len(runes) && !(runes[i] == '*' && runes[i+1] == '/') {
				i++
			}
			i++
			continue
		case c == '\'' || c == '"':
			quote := c
			i++
			for i < len(runes) && runes[i] != quote {
				if runes[i] == '\\' {
					i++
				}
				i++
			}
			prev = quote
			continue
		case c == '/' && startsRegex(prev):
			// A regex literal, not division. Its body may contain quotes and
			// brackets that would otherwise be read as code.
			i++
			inClass := false
			for i < len(runes) {
				switch runes[i] {
				case '\\':
					i++
				case '[':
					inClass = true
				case ']':
					inClass = false
				case '/':
					if !inClass {
						prev = '/'
						goto regexDone
					}
				}
				i++
			}
		regexDone:
			continue

		case c == '`':
			templateDepth++
			i++
			for i < len(runes) {
				if runes[i] == '\\' {
					i += 2
					continue
				}
				if runes[i] == '`' {
					templateDepth--
					break
				}
				// A ${...} substitution contains code, including nested
				// templates, so hand it back to the main loop.
				if runes[i] == '$' && i+1 < len(runes) && runes[i+1] == '{' {
					depth := 1
					i += 2
					for i < len(runes) && depth > 0 {
						switch runes[i] {
						case '{':
							depth++
						case '}':
							depth--
						case '`':
							// Skip a nested template wholesale.
							i++
							for i < len(runes) && runes[i] != '`' {
								if runes[i] == '\\' {
									i++
								}
								i++
							}
						}
						i++
					}
					i--
				}
				i++
			}
			continue
		}

		if c == '\'' || c == '"' || c == '`' {
			prev = c
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			prev = c
		}

		if c == '(' || c == '[' || c == '{' {
			stack = append(stack, c)
			continue
		}
		if want, ok := pairs[c]; ok {
			if len(stack) == 0 {
				return unexpectedAt(runes, i, c)
			}
			if stack[len(stack)-1] != want {
				return unexpectedAt(runes, i, c)
			}
			stack = stack[:len(stack)-1]
		}
	}

	if len(stack) != 0 {
		return &balanceError{msg: "unclosed " + string(stack[len(stack)-1])}
	}
	return nil
}

// startsRegex reports whether a "/" at this point begins a regex literal
// rather than a division, judged by what came before it.
func startsRegex(prev rune) bool {
	switch prev {
	case 0, '(', ',', '=', ':', '[', '!', '&', '|', '?', '{', '}', ';', '+', '-', '*', '%', '<', '>', '~', '^':
		return true
	}
	return false
}

type balanceError struct{ msg string }

func (e *balanceError) Error() string { return e.msg }

// unexpectedAt reports a stray delimiter with the line it appeared on.
func unexpectedAt(runes []rune, pos int, c rune) error {
	line := 1
	for i := 0; i < pos; i++ {
		if runes[i] == '\n' {
			line++
		}
	}
	return &balanceError{msg: "unexpected " + string(c) + " on script line " + itoa(line)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// Pages must not link to features this server does not provide.
func TestNoLinksToRemovedPages(t *testing.T) {
	removed := []string{
		"airlock-setup.html", "transfer-scales.html", "server-update.html",
		"/api/airlocks", "/api/transfer-scales", "/api/brewfather",
		"/api/system/update", "/api/firmwares", "/download32.php",
	}

	static := Static()
	for _, name := range pages {
		data, err := fs.ReadFile(static, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, gone := range removed {
			if strings.Contains(string(data), gone) {
				t.Errorf("%s still references %s", name, gone)
			}
		}
	}
}

// The broadcast envelope is tagged, so pages must dispatch on the type rather
// than treating an untagged message as a keg.
func TestWebSocketPagesHandleTaggedMessages(t *testing.T) {
	static := Static()
	for _, name := range []string{"index.html", "taplist.html", "setup.html"} {
		data, err := fs.ReadFile(static, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := string(data)
		if !strings.Contains(body, "new WebSocket('/ws')") && !strings.Contains(body, `new WebSocket("/ws")`) {
			t.Errorf("%s does not open the websocket", name)
			continue
		}
		if !strings.Contains(body, `'keg'`) && !strings.Contains(body, `"keg"`) {
			t.Errorf("%s does not dispatch on the keg message type", name)
		}
	}
}
