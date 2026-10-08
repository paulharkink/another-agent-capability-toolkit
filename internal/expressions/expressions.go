package expressions

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Evaluate parses a whole HCL interpolation as an expression, preserving its
// cty value, and parses mixed text as an HCL template string.
func Evaluate(value string, environment map[string]any, sourceFile, valuePath string) (any, error) {
	if !strings.Contains(value, "$"+"{") {
		return value, nil
	}
	context, err := evalContext(environment)
	if err != nil {
		return nil, locationError(sourceFile, valuePath, err.Error())
	}
	var expression hclsyntax.Expression
	var diagnostics hcl.Diagnostics
	if inner, exact := wholeExpression(value); exact {
		expression, diagnostics = hclsyntax.ParseExpression([]byte(inner), sourceFile, hcl.InitialPos)
	} else {
		expression, diagnostics = hclsyntax.ParseTemplate([]byte(value), sourceFile, hcl.InitialPos)
	}
	if diagnostics.HasErrors() {
		return nil, locationError(sourceFile, valuePath, diagnostics.Error())
	}
	result, diagnostics := expression.Value(context)
	if diagnostics.HasErrors() {
		return nil, locationError(sourceFile, valuePath, diagnostics.Error())
	}
	converted, err := ctyToGo(result)
	if err != nil {
		return nil, locationError(sourceFile, valuePath, err.Error())
	}
	return converted, nil
}

// EvaluateTree evaluates string leaves in a TOML-decoded map/list tree into a
// new tree. The caller supplies its own immutable evaluation context.
func EvaluateTree(value any, environment map[string]any, sourceFile string) (any, error) {
	return walk(value, environment, sourceFile, nil, "")
}

// EvaluateTreeDeferringRoot leaves expressions that reference a specified HCL
// root variable untouched. Loaders use this only for runtime inputs that are
// unavailable until a profile is resolved.
func EvaluateTreeDeferringRoot(value any, environment map[string]any, sourceFile, deferredRoot string) (any, error) {
	return walk(value, environment, sourceFile, map[string]bool{deferredRoot: true}, "")
}

// DocumentEnvironment exposes valid top-level document values as HCL variables.
// The reserved inputs variable is omitted because a TOML input schema is not the
// resolved input context used by runtime expressions.
func DocumentEnvironment(document map[string]any) map[string]any {
	environment := make(map[string]any, len(document))
	for key, value := range document {
		if key != "inputs" && variableName.MatchString(key) {
			environment[key] = value
		}
	}
	return environment
}

// ReferencesRoot reports whether a string contains an HCL expression/template
// that reads the named root variable.
func ReferencesRoot(value, root string) bool {
	if !strings.Contains(value, "$"+"{") {
		return false
	}
	var expression hclsyntax.Expression
	var diagnostics hcl.Diagnostics
	if inner, exact := wholeExpression(value); exact {
		expression, diagnostics = hclsyntax.ParseExpression([]byte(inner), "", hcl.InitialPos)
	} else {
		expression, diagnostics = hclsyntax.ParseTemplate([]byte(value), "", hcl.InitialPos)
	}
	if diagnostics.HasErrors() {
		return false
	}
	for _, traversal := range expression.Variables() {
		if traversal.RootName() == root {
			return true
		}
	}
	return false
}

func walk(value any, environment map[string]any, sourceFile string, deferred map[string]bool, path string) (any, error) {
	switch node := value.(type) {
	case string:
		if len(deferred) > 0 {
			for root := range deferred {
				if ReferencesRoot(node, root) {
					return node, nil
				}
			}
		}
		return Evaluate(node, environment, sourceFile, path)
	case map[string]any:
		out := make(map[string]any, len(node))
		keys := make([]string, 0, len(node))
		for key := range node {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			result, err := walk(node[key], environment, sourceFile, deferred, childPath)
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
			result, err := walk(child, environment, sourceFile, deferred, childPath)
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

func wholeExpression(value string) (string, bool) {
	open := "$" + "{"
	if !strings.HasPrefix(value, open) {
		return "", false
	}
	depth := 0
	var quote byte
	for i := len(open); i < len(value); i++ {
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
				if i != len(value)-1 {
					return "", false
				}
				return value[len(open):i], true
			}
			depth--
		}
	}
	return "", false
}

func evalContext(environment map[string]any) (*hcl.EvalContext, error) {
	variables := make(map[string]cty.Value, len(environment))
	for key, value := range environment {
		if !variableName.MatchString(key) {
			continue
		}
		converted, err := goToCty(value)
		if err != nil {
			return nil, fmt.Errorf("HCL variable %q: %w", key, err)
		}
		variables[key] = converted
	}
	return &hcl.EvalContext{Variables: variables, Functions: functionAllowlist()}, nil
}

func functionAllowlist() map[string]function.Function {
	stringParam := func(name string) function.Parameter { return function.Parameter{Name: name, Type: cty.String} }
	base64Encode := function.New(&function.Spec{
		Params: []function.Parameter{stringParam("value")}, Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.StringVal(base64.StdEncoding.EncodeToString([]byte(args[0].AsString()))), nil
		},
	})
	base64Decode := function.New(&function.Spec{
		Params: []function.Parameter{stringParam("value")}, Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			decoded, err := base64.StdEncoding.DecodeString(args[0].AsString())
			if err != nil {
				return cty.NilVal, err
			}
			return cty.StringVal(string(decoded)), nil
		},
	})
	return map[string]function.Function{
		"base64encode": base64Encode,
		"base64decode": base64Decode,
		"lower":        stdlib.LowerFunc,
		"upper":        stdlib.UpperFunc,
		"trimspace":    stdlib.TrimSpaceFunc,
		"trimprefix":   stdlib.TrimPrefixFunc,
		"trimsuffix":   stdlib.TrimSuffixFunc,
		"replace":      stdlib.ReplaceFunc,
		"tostring":     stdlib.MakeToFunc(cty.String),
		"tonumber":     stdlib.MakeToFunc(cty.Number),
		"tobool":       stdlib.MakeToFunc(cty.Bool),
	}
}

func goToCty(value any) (cty.Value, error) {
	switch v := value.(type) {
	case nil:
		return cty.NullVal(cty.DynamicPseudoType), nil
	case cty.Value:
		return v, nil
	case string:
		return cty.StringVal(v), nil
	case bool:
		return cty.BoolVal(v), nil
	case int:
		return cty.NumberIntVal(int64(v)), nil
	case int8:
		return cty.NumberIntVal(int64(v)), nil
	case int16:
		return cty.NumberIntVal(int64(v)), nil
	case int32:
		return cty.NumberIntVal(int64(v)), nil
	case int64:
		return cty.NumberIntVal(v), nil
	case uint:
		return cty.NumberUIntVal(uint64(v)), nil
	case uint8:
		return cty.NumberUIntVal(uint64(v)), nil
	case uint16:
		return cty.NumberUIntVal(uint64(v)), nil
	case uint32:
		return cty.NumberUIntVal(uint64(v)), nil
	case uint64:
		return cty.NumberUIntVal(v), nil
	case float32:
		return cty.NumberFloatVal(float64(v)), nil
	case float64:
		return cty.NumberFloatVal(v), nil
	case json.Number:
		number, ok := new(big.Float).SetPrec(512).SetString(v.String())
		if !ok {
			return cty.NilVal, fmt.Errorf("invalid number %q", v)
		}
		return cty.NumberVal(number), nil
	case []string:
		values := make([]cty.Value, len(v))
		for i, item := range v {
			values[i] = cty.StringVal(item)
		}
		return cty.TupleVal(values), nil
	case []any:
		values := make([]cty.Value, len(v))
		for i, item := range v {
			converted, err := goToCty(item)
			if err != nil {
				return cty.NilVal, err
			}
			values[i] = converted
		}
		return cty.TupleVal(values), nil
	case map[string]string:
		values := make(map[string]cty.Value, len(v))
		for key, item := range v {
			values[key] = cty.StringVal(item)
		}
		if len(values) == 0 {
			return cty.EmptyObjectVal, nil
		}
		return cty.ObjectVal(values), nil
	case map[string]any:
		values := make(map[string]cty.Value, len(v))
		for key, item := range v {
			converted, err := goToCty(item)
			if err != nil {
				return cty.NilVal, err
			}
			values[key] = converted
		}
		if len(values) == 0 {
			return cty.EmptyObjectVal, nil
		}
		return cty.ObjectVal(values), nil
	default:
		return cty.NilVal, fmt.Errorf("unsupported HCL value type %T", value)
	}
}

func ctyToGo(value cty.Value) (any, error) {
	if !value.IsKnown() {
		return nil, fmt.Errorf("expression result is not known")
	}
	if value.IsNull() {
		return nil, nil
	}
	typ := value.Type()
	switch {
	case typ == cty.String:
		return value.AsString(), nil
	case typ == cty.Bool:
		return value.True(), nil
	case typ == cty.Number:
		integer, accuracy := value.AsBigFloat().Int(nil)
		if accuracy == big.Exact {
			if integer.IsInt64() {
				return integer.Int64(), nil
			}
			return json.Number(integer.String()), nil
		}
		result, _ := value.AsBigFloat().Float64()
		return result, nil
	case typ.IsObjectType() || typ.IsMapType():
		result := map[string]any{}
		iterator := value.ElementIterator()
		for iterator.Next() {
			key, child := iterator.Element()
			converted, err := ctyToGo(child)
			if err != nil {
				return nil, err
			}
			result[key.AsString()] = converted
		}
		return result, nil
	case typ.IsTupleType() || typ.IsListType() || typ.IsSetType():
		result := []any{}
		iterator := value.ElementIterator()
		for iterator.Next() {
			_, child := iterator.Element()
			converted, err := ctyToGo(child)
			if err != nil {
				return nil, err
			}
			result = append(result, converted)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported HCL result type %s", typ.FriendlyName())
	}
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
