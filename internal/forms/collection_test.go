package forms

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestCollectionAddsOnePath(t *testing.T) {
	root := t.TempDir()
	one, two, three := filepath.Join(root, "one"), filepath.Join(root, "two"), filepath.Join(root, "three")
	c := NewCollection(catalog.Input{Type: "directory", Multiple: true})
	if e := c.Add(one); e != nil {
		t.Fatal(e)
	}
	if e := c.Add(two); e != nil {
		t.Fatal(e)
	}
	if e := c.Add(one + string(filepath.Separator) + ".." + string(filepath.Separator) + "one"); e == nil {
		t.Fatal("accepted duplicate")
	}
	if e := c.Edit(1, three); e != nil {
		t.Fatal(e)
	}
	got := c.Values()
	got[0] = "changed"
	if !reflect.DeepEqual(c.Values(), []string{one, three}) {
		t.Fatalf("%v", c.Values())
	}
	if e := c.Remove(0); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(c.Values(), []string{three}) {
		t.Fatalf("%v", c.Values())
	}
	if e := c.Edit(-1, "bad"); e == nil {
		t.Fatal("accepted invalid index")
	}
}
func TestWindowsPathCaseDeduplication(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows case rules")
	}
	c := NewCollection(catalog.Input{Type: "directory", Multiple: true})
	if e := c.Add(`C:\Repos\One`); e != nil {
		t.Fatal(e)
	}
	if e := c.Add(`c:\repos\one`); e == nil {
		t.Fatal("accepted Windows duplicate")
	}
}
func TestCollectionMaximumAndEmptyPath(t *testing.T) {
	max := 1
	c := NewCollection(catalog.Input{Type: "directory", Multiple: true, MaxItems: &max})
	if e := c.Add(""); e == nil {
		t.Fatal("accepted empty")
	}
	if e := c.Add("/one"); e != nil {
		t.Fatal(e)
	}
	if e := c.Add("/two"); e == nil {
		t.Fatal("exceeded max")
	}
}
