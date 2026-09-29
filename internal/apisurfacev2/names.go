package apisurface

import (
	"strings"
	"unicode"
)

// SnakeCase converts an identifier to snake_case. Acronym boundaries are
// preserved: "getURLStatus" becomes "get_url_status", "serverId" becomes
// "server_id" and "X-Idempotency-Key" becomes "x_idempotency_key".
func SnakeCase(s string) string { return strings.Join(words(s), "_") }

// KebabCase converts an identifier or phrase to kebab-case with the same word
// boundaries as SnakeCase: "Stacks - Specs" becomes "stacks-specs".
func KebabCase(s string) string { return strings.Join(words(s), "-") }

// words splits s at separators, lower-to-upper transitions and the end of an
// upper-case acronym that is followed by a capitalized word. Digits stay with
// the preceding word.
func words(s string) []string {
	rs := []rune(s)
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prev := cur[len(cur)-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}
