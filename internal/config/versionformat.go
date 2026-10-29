package config

// version_format (docs/windows-environment-plan.md §8.2): a user-defined
// file-name template used only by the upgrade task to pair old and new
// installers. It never changes the {package} command placeholder.

import (
	"fmt"
	"regexp"
	"strings"
)

// versionFormatTokenRe finds {lowercase} tokens; {UPPER} text is literal
// (pattern variables are not substituted in version_format).
var versionFormatTokenRe = regexp.MustCompile(`\{[a-z_]+\}`)

// versionTokenRe matches {version}: MAJOR.MINOR.PATCH plus an optional suffix
// introduced by "-" or "." (7.2.2, 7.2.2-1, 7.2.2.1234, 2.0.1-beta).
const versionTokenRe = `(\d+\.\d+\.\d+(?:[-.][0-9A-Za-z][0-9A-Za-z.+~_-]*)?)`

// VersionFormat is a compiled version_format template.
type VersionFormat struct {
	re         *regexp.Regexp
	versionIdx int    // submatch index of {version}
	packageIdx int    // submatch index of {package}, 0 = none
	identity   string // fixed identity when there is no {package}
}

// CompileVersionFormat compiles a version_format template. caseInsensitive
// is true for windows environments. Errors are config typos: {version}
// missing or repeated, {package} repeated, unknown {lowercase} token.
func CompileVersionFormat(tmpl string, caseInsensitive bool) (*VersionFormat, error) {
	if strings.Count(tmpl, "{version}") != 1 {
		return nil, fmt.Errorf("version_format %q must contain {version} exactly once", tmpl)
	}
	if strings.Count(tmpl, "{package}") > 1 {
		return nil, fmt.Errorf("version_format %q must contain {package} at most once", tmpl)
	}
	for _, tok := range versionFormatTokenRe.FindAllString(tmpl, -1) {
		if tok != "{version}" && tok != "{package}" {
			return nil, fmt.Errorf("version_format %q: unknown token %s (supported: {version}, {package}, *)", tmpl, tok)
		}
	}

	vf := &VersionFormat{}
	var b strings.Builder
	if caseInsensitive {
		b.WriteString("(?i)")
	}
	b.WriteString("^")
	group := 0
	rest := tmpl
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, "{version}"):
			group++
			vf.versionIdx = group
			b.WriteString(versionTokenRe)
			rest = rest[len("{version}"):]
		case strings.HasPrefix(rest, "{package}"):
			group++
			vf.packageIdx = group
			b.WriteString("(.+?)")
			rest = rest[len("{package}"):]
		case rest[0] == '*':
			b.WriteString(".*")
			rest = rest[1:]
		default:
			// literal run up to the next token or *
			n := len(rest)
			for _, stop := range []string{"{version}", "{package}", "*"} {
				if i := strings.Index(rest, stop); i >= 0 && i < n {
					n = i
				}
			}
			b.WriteString(regexp.QuoteMeta(rest[:n]))
			rest = rest[n:]
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("version_format %q: %w", tmpl, err)
	}
	vf.re = re
	if vf.packageIdx == 0 {
		prefix := tmpl[:strings.Index(tmpl, "{version}")]
		vf.identity = strings.TrimRight(prefix, "-_. ")
		if vf.identity == "" {
			vf.identity = tmpl // no prefix: every file of the entry is one package
		}
	}
	return vf, nil
}

// Match parses a file base name. version is normalized to
// MAJOR.MINOR.PATCH[-Suffix] (a "."-introduced suffix becomes "-"); identity
// is the {package} capture or the template's fixed identity. ok is false
// when the name does not match the template.
func (vf *VersionFormat) Match(base string) (version, identity string, ok bool) {
	m := vf.re.FindStringSubmatch(base)
	if m == nil {
		return "", "", false
	}
	version = normalizeFormatVersion(m[vf.versionIdx])
	if _, ok := ParseVersion(version); !ok {
		return "", "", false
	}
	identity = vf.identity
	if vf.packageIdx > 0 {
		identity = m[vf.packageIdx]
	}
	return version, identity, true
}

// normalizeFormatVersion turns "7.2.2.1234" into "7.2.2-1234"; dash suffixes
// stay as they are.
func normalizeFormatVersion(v string) string {
	dots := 0
	for i := 0; i < len(v); i++ {
		switch {
		case v[i] == '.':
			dots++
			if dots == 3 {
				return v[:i] + "-" + v[i+1:]
			}
		case v[i] == '-':
			return v
		}
	}
	return v
}
