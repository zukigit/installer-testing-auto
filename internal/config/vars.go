// Pattern variables (v3.2): {NAME} tokens in installer patterns substitute
// values from --var NAME=VALUE. Patterns only — anywhere else {VAR} is
// literal text (see docs/wildcard-checks-pattern-vars-plan.md, §12).
package config

import "regexp"

// varNameRe is the variable name syntax: [A-Z][A-Z0-9_]*. Lowercase names are
// built-in placeholders ({installer}, {package}, {basename}) that only exist
// after matching, in commands — they are never substituted here.
var varNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ValidVarName reports whether name matches the variable name syntax
// [A-Z][A-Z0-9_]* (used by the --var flag for early, clear usage errors).
func ValidVarName(name string) bool {
	return varNameRe.MatchString(name)
}

// varTokenRe matches one {NAME} token in a pattern string.
var varTokenRe = regexp.MustCompile(`\{[A-Z][A-Z0-9_]*\}`)

// Substitute replaces every {NAME} token in the pattern with vars[NAME],
// defaulting to the empty string for names no --var provides (unset/empty
// variables are not errors: the resulting pattern simply matches nothing —
// the existing "pattern matched no files" load error decides). Substitution
// is literal; the value participates in the glob like any other text.
func Substitute(pattern string, vars map[string]string) string {
	return varTokenRe.ReplaceAllStringFunc(pattern, func(token string) string {
		if v, ok := vars[token[1:len(token)-1]]; ok {
			return v
		}
		return ""
	})
}
