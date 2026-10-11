package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Version is an installer version: MAJOR.MINOR.PATCH[-Suffix].
type Version struct {
	Major, Minor, Patch int
	Suffix              string // "" when absent
	HasSuffix           bool
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.HasSuffix {
		s += "-" + v.Suffix
	}
	return s
}

var versionCoreRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-(.*))?$`)

// ParseVersion validates and parses a "MAJOR.MINOR.PATCH[-Suffix]" string.
func ParseVersion(v string) (Version, bool) {
	m := versionCoreRe.FindStringSubmatch(v)
	if m == nil {
		return Version{}, false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	patch, err3 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return Version{}, false
	}
	out := Version{Major: major, Minor: minor, Patch: patch}
	if m[4] != "" {
		out.Suffix = m[4]
		out.HasSuffix = true
	}
	return out, true
}

// ExtractVersionFromFilename parses the version out of an installer file name:
//   - .rpm: name-VERSION.rpm          (VERSION = MAJOR.MINOR.PATCH[-suffix];
//     a trailing .arch segment is stripped when needed — candidates are
//     enumerated and must parse)
//   - .deb: name_VERSION_arch.deb     (VERSION segment)
//   - anything else: not supported
func ExtractVersionFromFilename(base string) (Version, bool) {
	lower := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lower, ".rpm"):
		trimmed := base[:len(base)-4]
		// candidate variants: with and without a trailing .arch segment
		variants := []string{trimmed}
		if idx := strings.LastIndex(trimmed, "."); idx > strings.LastIndex(trimmed, "-") {
			variants = []string{trimmed[:idx], trimmed} // arch-stripped first
		}
		for _, variant := range variants {
			parts := strings.Split(variant, "-")
			if len(parts) < 2 {
				continue
			}
			// version is either the last dash segment alone, or the last two
			// combined ("version-release"); first parse wins
			for _, ver := range []string{
				parts[len(parts)-2] + "-" + parts[len(parts)-1],
				parts[len(parts)-1],
			} {
				if v, ok := ParseVersion(ver); ok {
					return v, true
				}
			}
		}
		return Version{}, false
	case strings.HasSuffix(lower, ".deb"):
		parts := strings.Split(base[:len(base)-4], "_")
		if len(parts) < 2 {
			return Version{}, false
		}
		return ParseVersion(parts[1])
	default:
		return Version{}, false
	}
}

// CompareInstallerVersions reports whether (version a, file a) is older (-1),
// equal (0) or newer (+1) relative to b — following the v3 plan:
//
//  1. MAJOR.MINOR.PATCH numeric compare decides when they differ.
//  2. Same core, the suffix decides:
//     - identical suffix strings        -> equal (no upgrade pair)
//     - both numeric                    -> numeric compare (0.1.1-1 < 0.1.1-2)
//     - no-suffix vs numeric            -> no-suffix ranks above (0.1.1 > 0.1.1-1)
//     - otherwise (non-numeric in play) -> the numeric prefix of the suffix
//     decides when it differs (rpm style: -1.el9 < -2.el9), else the files'
//     created dates (mtime): older file = older version; equal -> no pair
//
// 0 = "equal" means: not an upgrade pair.
func CompareInstallerVersions(aVersion, bVersion string, aMtime, bMtime time.Time) int {
	a, okA := ParseVersion(aVersion)
	b, okB := ParseVersion(bVersion)
	if !okA || !okB {
		return 0
	}
	switch {
	case a.Major != b.Major:
		return cmpInt(a.Major, b.Major)
	case a.Minor != b.Minor:
		return cmpInt(a.Minor, b.Minor)
	case a.Patch != b.Patch:
		return cmpInt(a.Patch, b.Patch)
	}
	if a.Suffix == b.Suffix {
		return 0
	}
	aNum, aIsNum := numericSuffix(a.Suffix)
	bNum, bIsNum := numericSuffix(b.Suffix)
	switch {
	case aIsNum && bIsNum:
		return cmpInt(aNum, bNum)
	case a.Suffix == "" && bIsNum:
		return 1 // no-suffix ranks above
	case b.Suffix == "" && aIsNum:
		return -1
	default:
		// numeric prefix tie-break (rpm style: -1.el9 < -2.el9),
		// otherwise the created dates decide
		if aNum != bNum {
			return cmpInt(aNum, bNum)
		}
		if aMtime.Before(bMtime) {
			return -1
		}
		if aMtime.After(bMtime) {
			return 1
		}
		return 0
	}
}

// numericSuffix returns the leading digits of the suffix (0 when none) and
// whether the suffix is purely numeric.
func numericSuffix(s string) (int, bool) {
	if s == "" || !isNumeric(s) {
		return numericPrefix(s), false
	}
	return atoint(s), true
}

// numericPrefix counts the leading digits of a suffix ("1.el9" -> 1).
func numericPrefix(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func atoint(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
