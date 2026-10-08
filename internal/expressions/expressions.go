package expressions

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/expr-lang/expr"
	exprfile "github.com/expr-lang/expr/file"
)

type part struct {
	text string
	expr bool
}

func Evaluate(value string, environment map[string]any, sourceFile, valuePath string) (any, error) {
	parts, exact, found, err := split(value)
	if err != nil {
		return nil, locationError(sourceFile, valuePath, "invalid interpolation syntax")
	}
	if !found {
		return strings.ReplaceAll(value, "$$"+"{", "$"+"{"), nil
	}
	if exact && len(parts) == 1 {
		return run(parts[0].text, environment, sourceFile, valuePath)
	}
	var out strings.Builder
	for _, item := range parts {
		if !item.expr {
			out.WriteString(item.text)
			continue
		}
		result, err := run(item.text, environment, sourceFile, valuePath)
		if err != nil {
			return nil, err
		}
		out.WriteString(fmt.Sprint(result))
	}
	return out.String(), nil
}

func EvaluateTree(value any, environment map[string]any, sourceFile string) (any, error) {
	return walk(value, environment, sourceFile, "")
}

func walk(value any, env map[string]any, file, path string) (any, error) {
	switch node := value.(type) {
	case string:
		return Evaluate(node, env, file, path)
	case map[string]any:
		out := make(map[string]any, len(node))
		keys := make([]string, 0, len(node))
		for key := range node {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := node[key]
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			result, err := walk(child, env, file, childPath)
			if err != nil {
				return nil, err
			}
			out[key] = result
		}
		return out, nil
	case []any:
		out := make([]any, len(node))
		for index, child := range node {
			childPath := fmt.Sprintf("%s[%d]", path, index)
			result, err := walk(child, env, file, childPath)
			if err != nil {
				return nil, err
			}
			out[index] = result
		}
		return out, nil
	default:
		return value, nil
	}
}

func run(source string, env map[string]any, file, path string) (result any, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = locationError(file, path, "expression evaluation failed")
		}
	}()
	program, err := expr.Compile(source, expr.Env(env), expr.DisableAllBuiltins(),
		expr.Function("toBase64", func(a ...any) (any, error) { return base64.StdEncoding.EncodeToString([]byte(a[0].(string))), nil }, new(func(string) string)),
		expr.Function("fromBase64", func(a ...any) (any, error) {
			b, err := base64.StdEncoding.DecodeString(a[0].(string))
			if err != nil {
				return nil, err
			}
			return string(b), nil
		}, new(func(string) string)),
		expr.Function("lower", func(a ...any) (any, error) { return strings.ToLower(a[0].(string)), nil }, new(func(string) string)),
		expr.Function("upper", func(a ...any) (any, error) { return strings.ToUpper(a[0].(string)), nil }, new(func(string) string)),
		expr.Function("trim", func(a ...any) (any, error) { return strings.TrimSpace(a[0].(string)), nil }, new(func(string) string)),
		expr.Function("trimPrefix", func(a ...any) (any, error) { return strings.TrimPrefix(a[0].(string), a[1].(string)), nil }, new(func(string, string) string)),
		expr.Function("trimSuffix", func(a ...any) (any, error) { return strings.TrimSuffix(a[0].(string), a[1].(string)), nil }, new(func(string, string) string)),
		expr.Function("contains", func(a ...any) (any, error) { return strings.Contains(a[0].(string), a[1].(string)), nil }, new(func(string, string) bool)),
		expr.Function("startsWith", func(a ...any) (any, error) { return strings.HasPrefix(a[0].(string), a[1].(string)), nil }, new(func(string, string) bool)),
		expr.Function("endsWith", func(a ...any) (any, error) { return strings.HasSuffix(a[0].(string), a[1].(string)), nil }, new(func(string, string) bool)),
		expr.Function("replace", func(a ...any) (any, error) {
			return strings.ReplaceAll(a[0].(string), a[1].(string), a[2].(string)), nil
		}, new(func(string, string, string) string)),
		expr.Function("string", func(a ...any) (any, error) { return fmt.Sprint(a[0]), nil }, new(func(any) string)),
	)
	if err != nil {
		return nil, expressionError(file, path, err)
	}
	result, err = expr.Run(program, env)
	if err != nil {
		return nil, expressionError(file, path, err)
	}
	return result, nil
}

func expressionError(file, path string, err error) error {
	var libraryError *exprfile.Error
	if errors.As(err, &libraryError) {
		return locationError(file, path, libraryError.Message)
	}
	return locationError(file, path, err.Error())
}

func split(value string) ([]part, bool, bool, error) {
	open := "$" + "{"
	escapedOpen := "$$" + "{"
	items := []part{}
	var literal strings.Builder
	found, exact := false, false
	for i := 0; i < len(value); {
		if strings.HasPrefix(value[i:], escapedOpen) {
			literal.WriteString(open)
			i += len(escapedOpen)
			continue
		}
		if !strings.HasPrefix(value[i:], open) {
			literal.WriteByte(value[i])
			i++
			continue
		}
		if literal.Len() > 0 {
			items = append(items, part{text: literal.String()})
			literal.Reset()
		}
		end, ok := expressionEnd(value, i+len(open))
		if !ok {
			return nil, false, false, fmt.Errorf("unterminated expression")
		}
		items = append(items, part{text: strings.TrimSpace(value[i+len(open) : end]), expr: true})
		found = true
		if i == 0 && end == len(value)-1 {
			exact = true
		}
		i = end + 1
	}
	if literal.Len() > 0 {
		items = append(items, part{text: literal.String()})
	}
	return items, exact, found, nil
}

func expressionEnd(value string, start int) (int, bool) {
	depth := 0
	var quote byte
	for i := start; i < len(value); i++ {
		current := value[i]
		if quote != 0 {
			if current == '\\' {
				i++
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		switch current {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i, true
			}
			depth--
		}
	}
	return 0, false
}

func locationError(file, path, reason string) error {
	if path == "" {
		path = "<root>"
	}
	if file == "" {
		return fmt.Errorf("%s: %s", path, reason)
	}
	return fmt.Errorf("%s: %s: %s", file, path, reason)
}
