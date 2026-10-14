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

// unsetMarker returns the literal substituted for an unset (or empty)
// {NAME}: e.g. "VERSION" -> "{UNSET:VERSION}". It never appears in real
// installer file names and contains no glob metachars, so the pattern the
// marker ends up in matches nothing — an unset variable can never silently
// widen to "all versions" (v3.3). The colon keeps it outside the variable
// name syntax, so the marker is never re-matched by the substitution
// pattern, and the reused "matched no files" load error reads as
// "VERSION was not set".
func unsetMarker(name string) string {
	return "{UNSET:" + name + "}"
}

// Substitute replaces every {NAME} token in the pattern with vars[NAME]. A
// name no --var provides — or provided empty — substitutes the
// never-matching marker: the resulting pattern matches no files and the
// existing "pattern matched no files" load error fires ("act like there is
// no installer file", v3.3). Provided non-empty values substitute
// literally; the value participates in the glob like any other text.
func Substitute(pattern string, vars map[string]string) string {
	return varTokenRe.ReplaceAllStringFunc(pattern, func(token string) string {
		name := token[1 : len(token)-1]
		if v, ok := vars[name]; ok && v != "" {
			return v
		}
		return unsetMarker(name)
	})
}
