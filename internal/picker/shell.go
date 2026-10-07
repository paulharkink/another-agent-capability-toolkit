package picker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	shellprocess "github.com/paulharkink/another-agent-capability-toolkit/internal/process"
)

const (
	shellStdoutLimit = 64 << 10
	shellStderrLimit = 2 << 10
)

type shellKind uint8

const (
	shellPOSIX shellKind = iota
	shellFish
	shellPowerShell
	shellCMD
)

type shellSpec struct {
	path string
	kind shellKind
}

// ResolveShellPath resolves a typed address-bar expression using the user's
// invoking shell and returns its single absolute path result.
func ResolveShellPath(ctx context.Context, typedExpression, cwd string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	spec, err := discoverPickerShell()
	if err != nil {
		return "", err
	}
	return resolveShellPath(ctx, typedExpression, cwd, spec)
}

func resolveShellPath(ctx context.Context, typedExpression, cwd string, shell shellSpec) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(typedExpression) == "" {
		return "", errors.New("enter a path")
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve picker working directory: %w", err)
	}
	info, err := os.Stat(absCWD)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not a directory")
		}
		return "", fmt.Errorf("picker working directory %q: %w", absCWD, err)
	}
	if shell.path == "" {
		return "", errors.New("invoking shell is unavailable; set AACT_PICKER_SHELL to an explicit supported shell path")
	}
	if _, err := os.Stat(shell.path); err != nil {
		return "", fmt.Errorf("picker shell %q is unavailable: %w", shell.path, err)
	}

	args, script, envName, envValue := shellCommand(shell, typedExpression)
	argv := append([]string{shell.path}, args...)
	if len(argv) > 1 {
		// POSIX and PowerShell scripts receive the expression through the child
		// environment. CMD requires its native FOR syntax in the /c command text.
		argv[len(argv)-1] = script
	}
	env := make(map[string]string, 1)
	if envName != "" {
		env[envName] = envValue
	}
	var stderr limitedBuffer
	stderr.limit = shellStderrLimit
	stdout, err := shellprocess.Run(ctx, argv, absCWD, nil, env, func(chunk []byte) {
		_, _ = stderr.Write(chunk)
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, shellprocess.ErrOutputLimit) {
			return "", errors.New("shell path expression produced too much output; narrow it to one path")
		}
		diagnostic := boundedShellDiagnostic(stderr.String())
		if diagnostic == "" {
			diagnostic = "no shell diagnostic was produced"
		}
		return "", fmt.Errorf("shell path expression failed in %s: %w: %s", shellDisplayName(shell), err, diagnostic)
	}
	if len(stdout) > shellStdoutLimit {
		return "", errors.New("shell path expression produced too many results; narrow it to one path")
	}
	if len(stdout) == 0 && stderr.Len() > 0 {
		diagnostic := boundedShellDiagnostic(stderr.String())
		if diagnostic != "" {
			return "", fmt.Errorf("shell path expression failed in %s: %s", shellDisplayName(shell), diagnostic)
		}
	}
	results := shellResults(stdout, shell.kind)
	if len(results) == 1 && len(results[0]) == 0 {
		return "", errors.New("shell path expression did not select a path")
	}
	if len(results) != 1 {
		return "", fmt.Errorf("shell path expression matched %d paths; narrow it to one path", len(results))
	}
	path := string(results[0])
	if path == "" {
		return "", errors.New("shell path expression did not select a path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(absCWD, path)
	}
	return filepath.Clean(path), nil
}

func shellResults(output []byte, kind shellKind) [][]byte {
	if kind == shellCMD {
		lines := strings.Split(strings.TrimRight(string(output), "\r\n"), "\n")
		results := make([][]byte, 0, len(lines))
		for _, line := range lines {
			results = append(results, []byte(strings.TrimSuffix(line, "\r")))
		}
		return results
	}
	return bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
}

func shellDisplayName(shell shellSpec) string {
	name := filepath.Base(shell.path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "selected shell"
	}
	return name
}

func boundedShellDiagnostic(diagnostic string) string {
	var clean strings.Builder
	for _, r := range diagnostic {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			clean.WriteRune(r)
		}
	}
	text := strings.TrimSpace(clean.String())
	const maxDiagnosticRunes = 1024
	runes := []rune(text)
	if len(runes) > maxDiagnosticRunes {
		return string(runes[:maxDiagnosticRunes]) + "…"
	}
	return text
}

func shellCommand(shell shellSpec, expression string) (args []string, script, envName, envValue string) {
	envName, envValue = "AACT_PICKER_TYPED_EXPRESSION", expression
	if runtime.GOOS == "windows" {
		switch shell.kind {
		case shellPOSIX:
			return []string{"-c", ""}, `if [ -e "$AACT_PICKER_TYPED_EXPRESSION" ]; then set -- "$AACT_PICKER_TYPED_EXPRESSION"; else eval "set -- $AACT_PICKER_TYPED_EXPRESSION" || exit; fi; for p do converted=$(cygpath -aw -- "$p") || exit; printf '%s\0' "$converted"; done`, envName, envValue
		case shellFish:
			return []string{"-c", ""}, `if test -e "$AACT_PICKER_TYPED_EXPRESSION"; set -l paths "$AACT_PICKER_TYPED_EXPRESSION"; else; set -l paths (eval echo $AACT_PICKER_TYPED_EXPRESSION); end; for p in $paths; set -l converted (cygpath -aw -- "$p"); or exit; printf '%s\0' (string join '' $converted); end`, envName, envValue
		}
	}
	switch shell.kind {
	case shellFish:
		return []string{"-c", ""}, `if test -e "$AACT_PICKER_TYPED_EXPRESSION"; printf '%s\0' "$AACT_PICKER_TYPED_EXPRESSION"; else; eval "printf '%s\0' $AACT_PICKER_TYPED_EXPRESSION"; end`, envName, envValue
	case shellPowerShell:
		return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", ""}, `$p=$env:AACT_PICKER_TYPED_EXPRESSION; if ($p -notmatch '^(~|\$)' -and (Test-Path -LiteralPath $p)) { $r=@(Resolve-Path -LiteralPath $p -ErrorAction Stop | ForEach-Object { $_.Path }) } else { $r=@(Invoke-Expression -Command ('Resolve-Path -Path ' + $p) | ForEach-Object { $_.Path }) }; $s=[Console]::OpenStandardOutput(); foreach($x in $r) { $b=[Text.Encoding]::UTF8.GetBytes([string]$x+[char]0); $s.Write($b,0,$b.Length) }`, envName, envValue
	case shellCMD:
		set := expression
		if !strings.Contains(set, `"`) {
			set = `"` + set + `"`
		}
		return []string{"/d", "/c", ""}, `for %P in (` + set + `) do @echo %~fP`, "", ""
	default:
		return []string{"-c", ""}, `if [ -e "$AACT_PICKER_TYPED_EXPRESSION" ]; then printf '%s\0' "$AACT_PICKER_TYPED_EXPRESSION"; else eval "set -- $AACT_PICKER_TYPED_EXPRESSION" || exit; printf '%s\0' "$@"; fi`, envName, envValue
	}
}

type limitedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	if n > remaining {
		b.truncated = true
	}
	return n, nil
}

func (b *limitedBuffer) Bytes() []byte  { return b.data }
func (b *limitedBuffer) String() string { return string(b.data) }
func (b *limitedBuffer) Len() int       { return len(b.data) }
