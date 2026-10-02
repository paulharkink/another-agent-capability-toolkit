package forms

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Collection owns the editable directory/file rows for one multiple input.
type Collection struct {
	input  catalog.Input
	values []string
}

func NewCollection(input catalog.Input) *Collection {
	return &Collection{input: input, values: []string{}}
}
func (c *Collection) Add(path string) error {
	if c.input.MaxItems != nil && len(c.values) >= *c.input.MaxItems {
		return fmt.Errorf("input %s allows at most %d items", c.input.Name, *c.input.MaxItems)
	}
	clean, e := c.check(path, -1)
	if e != nil {
		return e
	}
	c.values = append(c.values, clean)
	return nil
}
func (c *Collection) Edit(index int, path string) error {
	if index < 0 || index >= len(c.values) {
		return fmt.Errorf("invalid collection index %d", index)
	}
	clean, e := c.check(path, index)
	if e != nil {
		return e
	}
	c.values[index] = clean
	return nil
}
func (c *Collection) Remove(index int) error {
	if index < 0 || index >= len(c.values) {
		return fmt.Errorf("invalid collection index %d", index)
	}
	c.values = append(c.values[:index], c.values[index+1:]...)
	return nil
}
func (c *Collection) Values() []string { return append([]string{}, c.values...) }
func (c *Collection) check(path string, ignore int) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	clean, err := config.ResolvePath(path, filepath.Join(cwd, "interactive"))
	if err != nil {
		return "", err
	}
	key := collectionPathKey(clean)
	for i, value := range c.values {
		if i != ignore && collectionPathKey(value) == key {
			return "", fmt.Errorf("duplicate path %s", path)
		}
	}
	return clean, nil
}
func collectionPathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
