// Package shell builds the argv and probe scripts for the command language
// spoken inside an environment: POSIX sh (Linux) or Windows PowerShell 5.1
// (Windows). Pure functions, no I/O.
package shell

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

// Dialect builds the argv and probe scripts for one command language.
type Dialect interface {
	// Name is "posix" or "powershell".
	Name() string
	// Command returns the argv that runs script through the dialect's shell
	// (for Environment.Exec).
	Command(script string) []string
	// PathExists is a script exiting 0 when path exists, 1 when it is absent
	// and anything else on error.
	PathExists(path string) string
	// Glob is a script printing one match per line, nothing on no match.
	Glob(pattern string) string
	// MkdirAll is a script creating dir and its parents.
	MkdirAll(dir string) string
	// Quote quotes s as one literal word.
	Quote(s string) string
}

// POSIX is /bin/sh. Its output is byte-identical to the strings used before
// the dialect abstraction existed.
var POSIX Dialect = posix{}

// PowerShell is Windows PowerShell 5.1 (powershell.exe).
var PowerShell Dialect = powershell{}

type posix struct{}

func (posix) Name() string                   { return "posix" }
func (posix) Command(script string) []string { return []string{"/bin/sh", "-c", script} }
func (p posix) PathExists(path string) string {
	return "test -e " + p.Quote(path)
}

// Glob passes the pattern unquoted so /bin/sh expands it (config validation
// restricts glob paths to a shell-safe charset).
func (posix) Glob(pattern string) string { return "ls -d -- " + pattern }
func (p posix) MkdirAll(dir string) string {
	return "mkdir -p " + p.Quote(dir)
}

// Quote wraps s in single quotes, unescaped (the pre-dialect behavior).
func (posix) Quote(s string) string { return "'" + s + "'" }

type powershell struct{}

// psPrelude runs before every script: no progress bars on stdout, UTF-8
// output, and a clean $LASTEXITCODE so rule 2 of the exit-code contract only
// sees native commands of the script itself.
const psPrelude = "$ProgressPreference = 'SilentlyContinue'\n" +
	"try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false } catch {}\n" +
	"$OutputEncoding = New-Object System.Text.UTF8Encoding $false\n" +
	"$global:LASTEXITCODE = 0\n"

// psEpilogue implements the exit-code contract (mirrors `sh -c`):
//  1. an explicit `exit N` in the script ends the process with N (the
//     epilogue never runs);
//  2. otherwise the exit code of the last native command ($LASTEXITCODE);
//  3. otherwise 1 when the last statement failed ($? false), else 0.
//
// $? must be captured by the very first epilogue statement.
const psEpilogue = "\n$__itaOk = $?\n" +
	"if ($global:LASTEXITCODE) { exit $global:LASTEXITCODE }\n" +
	"if (-not $__itaOk) { exit 1 }\n" +
	"exit 0\n"

// psArgs are the powershell.exe arguments before the encoded script.
var psArgs = []string{"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand"}

func (powershell) Name() string { return "powershell" }

// Command passes the script via -EncodedCommand (base64 of UTF-16LE):
// Windows has no argv, so docker exec joins the arguments into one command
// line that powershell.exe re-splits — quotes and newlines inside a -Command
// script would get mangled; the base64 form arrives intact.
func (powershell) Command(script string) []string {
	argv := append([]string{}, psArgs...)
	return append(argv, encodePS(psPrelude+script+psEpilogue))
}

func (p powershell) PathExists(path string) string {
	return "if (Test-Path -LiteralPath " + p.Quote(path) + ") { exit 0 } else { exit 1 }"
}

// Glob lets PowerShell expand the (quoted) wildcard pattern itself: files and
// directories, non-recursive, hidden items included. Provider prefixes are
// stripped (FileSystem::C:\x -> C:\x, Registry::HKEY_LOCAL_MACHINE\x -> HKEY_LOCAL_MACHINE\x).
func (p powershell) Glob(pattern string) string {
	return "Get-Item -Path " + p.Quote(pattern) +
		" -Force -ErrorAction SilentlyContinue | ForEach-Object { $_.PSPath -replace '^.*::', '' }"
}

func (p powershell) MkdirAll(dir string) string {
	return "New-Item -ItemType Directory -Force -Path " + p.Quote(dir) + " | Out-Null"
}

// Quote returns a single-quoted PowerShell string. PowerShell treats the
// typographic single quotes as quote characters too, so every one of them is
// doubled like the ASCII quote.
func (powershell) Quote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		b.WriteRune(r)
		switch r {
		case '\'', '\u2018', '\u2019', '\u201a', '\u201b':
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func encodePS(script string) string {
	units := utf16.Encode([]rune(script))
	buf := make([]byte, 2*len(units))
	for i, u := range units {
		buf[2*i] = byte(u)
		buf[2*i+1] = byte(u >> 8)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func decodePS(enc string) (string, bool) {
	buf, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(buf)%2 != 0 {
		return "", false
	}
	units := make([]uint16, len(buf)/2)
	for i := range units {
		units[i] = uint16(buf[2*i]) | uint16(buf[2*i+1])<<8
	}
	return string(utf16.Decode(units)), true
}

// ScriptOf returns the script an argv built by Command runs — the user
// script without the PowerShell prelude/epilogue. ok is false when argv was
// not built by a known dialect. Used by tests and nothing else.
func ScriptOf(argv []string) (script string, ok bool) {
	switch {
	case len(argv) == 3 && argv[0] == "/bin/sh" && argv[1] == "-c":
		return argv[2], true
	case len(argv) == len(psArgs)+1 && argv[0] == psArgs[0]:
		for i, a := range psArgs {
			if argv[i] != a {
				return "", false
			}
		}
		full, ok := decodePS(argv[len(argv)-1])
		if !ok || !strings.HasPrefix(full, psPrelude) || !strings.HasSuffix(full, psEpilogue) {
			return "", false
		}
		return full[len(psPrelude) : len(full)-len(psEpilogue)], true
	}
	return "", false
}
