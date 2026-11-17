package catalog

// InputVisible reports whether an input's declared equality conditions match
// the current values. Multiple conditions are combined with AND. Missing
// controllers do not match, and this helper never changes the values map.
func InputVisible(input Input, values map[string]any) bool {
	for controller, expected := range input.VisibleWhen {
		actual, exists := values[controller]
		if !exists || !visibilityEqual(expected, actual) {
			return false
		}
	}
	return true
}

// VisibleInputs returns the visible definitions in their original order. It
// does not alter values, so hidden fields can retain their saved values.
func VisibleInputs(defs []Input, values map[string]any) []Input {
	visible := make([]Input, 0, len(defs))
	for _, def := range defs {
		if InputVisible(def, values) {
			visible = append(visible, def)
		}
	}
	return visible
}

func visibilityEqual(expected, actual any) bool {
	if expectedNumber, ok := visibilityNumber(expected); ok {
		actualNumber, actualOK := visibilityNumber(actual)
		return actualOK && expectedNumber == actualNumber
	}
	if actual == nil || expected == nil {
		return expected == nil && actual == nil
	}
	if _, isNumber := visibilityNumber(actual); isNumber {
		return false
	}
	switch want := expected.(type) {
	case string:
		got, ok := actual.(string)
		return ok && want == got
	case bool:
		got, ok := actual.(bool)
		return ok && want == got
	default:
		return false
	}
}

func visibilityNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int8:
		return float64(number), true
	case int16:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint8:
		return float64(number), true
	case uint16:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}
