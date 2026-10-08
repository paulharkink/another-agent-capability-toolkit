package forms

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"os"
	"path/filepath"
	"reflect"
)

type Editor struct {
	defs              []catalog.Input
	values, original  map[string]any
	visibilityContext map[string]any
	initialErrors     map[string]error
	cancelled         bool
}

func NewEditor(defs []catalog.Input, prefill map[string]any) *Editor {
	e := &Editor{defs: append([]catalog.Input{}, defs...), values: map[string]any{}, initialErrors: map[string]error{}}
	for _, def := range defs {
		values, err := ResolvePartial([]catalog.Input{def}, prefill)
		if err != nil {
			e.initialErrors[def.Name] = err
			if v, ok := values[def.Name]; ok {
				e.values[def.Name] = v
			}
			continue
		}
		if v, ok := values[def.Name]; ok {
			e.values[def.Name] = v
		}
	}
	e.original = copyAnswers(e.values)
	return e
}
func (e *Editor) Values() map[string]any  { return copyAnswers(e.values) }
func (e *Editor) HasUnsavedChanges() bool { return !answersEqual(e.values, e.original) }
func (e *Editor) MarkClean()              { e.original = copyAnswers(e.values) }
func (e *Editor) SetVisibilityContext(values map[string]any) {
	e.visibilityContext = copyAnswers(values)
}
func (e *Editor) Apply(name string, value any) error {
	if e.cancelled {
		return picker.ErrCancelled
	}
	def, err := e.definition(name)
	if err != nil {
		return err
	}
	if def.Type == "file" || def.Type == "directory" {
		cwd, e := os.Getwd()
		if e != nil {
			return e
		}
		resolved, e := config.ResolveInputPaths([]catalog.Input{def}, map[string]any{name: value}, filepath.Join(cwd, "interactive"))
		if e != nil {
			return e
		}
		value = resolved[name]
	}
	normalized, err := normalize(def, value)
	if err != nil {
		return fmt.Errorf("input %s: %w", name, err)
	}
	editable := def
	editable.Required = false
	editable.MinItems = nil
	editable.VisibleWhen = nil
	if err = Validate([]catalog.Input{editable}, map[string]any{name: normalized}); err != nil {
		return err
	}
	e.values[name] = normalized
	if def.ExclusiveGroup != "" && filled(normalized) {
		for _, other := range e.defs {
			if other.Name == name || other.ExclusiveGroup != def.ExclusiveGroup {
				continue
			}
			if other.Multiple || other.Type == "multichoice" || other.Type == "multiple-choice" {
				e.values[other.Name] = []string{}
			} else if other.Type == "string" || other.Type == "secret" || other.Type == "file" || other.Type == "directory" || other.Type == "choice" {
				e.values[other.Name] = ""
			} else {
				e.values[other.Name] = nil
			}
		}
	}
	delete(e.initialErrors, name)
	return nil
}

// Clear removes a value from the submitted answer map. It is used when a
// lower-precedence source has no value, so an override can be removed without
// persisting an empty string as a replacement.
func (e *Editor) Clear(name string) error {
	if _, err := e.definition(name); err != nil {
		return err
	}
	delete(e.values, name)
	delete(e.initialErrors, name)
	return nil
}
func (e *Editor) Cancel() { e.cancelled = true; e.values = copyAnswers(e.original) }
func (e *Editor) Commit() (map[string]any, error) {
	if e.cancelled {
		return nil, picker.ErrCancelled
	}
	context := copyAnswers(e.visibilityContext)
	for name, value := range e.values {
		context[name] = value
	}
	visible := catalog.VisibleInputs(e.defs, context)
	for _, def := range visible {
		if err := e.initialErrors[def.Name]; err != nil {
			return nil, err
		}
	}
	if err := Validate(visible, context); err != nil {
		return nil, err
	}
	return e.Values(), nil
}
func (e *Editor) definition(name string) (catalog.Input, error) {
	for _, def := range e.defs {
		if def.Name == name {
			return def, nil
		}
	}
	return catalog.Input{}, fmt.Errorf("undeclared input %s", name)
}
func (e *Editor) collection(name string) (*Collection, error) {
	def, err := e.definition(name)
	if err != nil {
		return nil, err
	}
	if !def.Multiple || (def.Type != "directory" && def.Type != "file") {
		return nil, fmt.Errorf("input %s is not a path collection", name)
	}
	c := NewCollection(def)
	if value, ok := e.values[name]; ok {
		paths, ok := value.([]string)
		if !ok {
			return nil, fmt.Errorf("input %s has invalid collection values", name)
		}
		for _, path := range paths {
			if err = c.Add(path); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}
func (e *Editor) AddPath(name, path string) error {
	if e.cancelled {
		return picker.ErrCancelled
	}
	c, err := e.collection(name)
	if err != nil {
		return err
	}
	if err = c.Add(path); err != nil {
		return err
	}
	return e.Apply(name, c.Values())
}
func (e *Editor) EditPath(name string, index int, path string) error {
	if e.cancelled {
		return picker.ErrCancelled
	}
	c, err := e.collection(name)
	if err != nil {
		return err
	}
	if err = c.Edit(index, path); err != nil {
		return err
	}
	return e.Apply(name, c.Values())
}
func (e *Editor) RemovePath(name string, index int) error {
	if e.cancelled {
		return picker.ErrCancelled
	}
	c, err := e.collection(name)
	if err != nil {
		return err
	}
	if err = c.Remove(index); err != nil {
		return err
	}
	return e.Apply(name, c.Values())
}

// Collection edits use Apply for normalization and validation before replacing any rows.
func (e *Editor) AddValue(name string, value any) error {
	def, err := e.definition(name)
	if err != nil {
		return err
	}
	if def.Type == "file" || def.Type == "directory" {
		path, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected path string")
		}
		return e.AddPath(name, path)
	}
	rows, err := e.valueRows(name)
	if err != nil {
		return err
	}
	return e.Apply(name, append(rows, value))
}
func (e *Editor) EditValue(name string, index int, value any) error {
	def, err := e.definition(name)
	if err != nil {
		return err
	}
	if def.Type == "file" || def.Type == "directory" {
		path, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected path string")
		}
		return e.EditPath(name, index, path)
	}
	rows, err := e.valueRows(name)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(rows) {
		return fmt.Errorf("collection row out of range")
	}
	rows[index] = value
	return e.Apply(name, rows)
}
func (e *Editor) RemoveValue(name string, index int) error {
	def, err := e.definition(name)
	if err != nil {
		return err
	}
	if def.Type == "file" || def.Type == "directory" {
		return e.RemovePath(name, index)
	}
	rows, err := e.valueRows(name)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(rows) {
		return fmt.Errorf("collection row out of range")
	}
	return e.Apply(name, append(rows[:index], rows[index+1:]...))
}
func (e *Editor) valueRows(name string) ([]any, error) {
	def, err := e.definition(name)
	if err != nil {
		return nil, err
	}
	if !scalarDefinition(def).Multiple {
		return nil, fmt.Errorf("input %s is not a collection", name)
	}
	return collectionRows(e.values[name]), nil
}
func collectionRows(value any) []any {
	rows := []any{}
	if value == nil {
		return rows
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return rows
	}
	for i := 0; i < rv.Len(); i++ {
		rows = append(rows, rv.Index(i).Interface())
	}
	return rows
}
func copyAnswers(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for name, value := range values {
		if value != nil {
			rv := reflect.ValueOf(value)
			if rv.Kind() == reflect.Slice {
				if !rv.IsNil() {
					copy := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
					reflect.Copy(copy, rv)
					value = copy.Interface()
				}
			}
		}
		out[name] = value
	}
	return out
}

func answersEqual(left, right map[string]any) bool {
	for name, value := range left {
		if !answerValueEqual(value, right[name]) {
			return false
		}
	}
	for name, value := range right {
		if !answerValueEqual(value, left[name]) {
			return false
		}
	}
	return true
}

func answerValueEqual(left, right any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	isEmptySlice := func(value any) bool {
		if value == nil {
			return true
		}
		rv := reflect.ValueOf(value)
		return rv.Kind() == reflect.Slice && rv.Len() == 0
	}
	if isEmptySlice(left) && isEmptySlice(right) {
		return true
	}
	return false
}
