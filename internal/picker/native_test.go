package picker

import (
	"context"
	"testing"
)

func TestNativePickerIncludesShowHiddenForFilesAndDirectories(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want int
	}{
		{kind: "file", want: 4},
		{kind: "directory", want: 5},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			options := nativeOptions(context.Background(), tc.kind, "")
			if len(options) != tc.want {
				t.Fatalf("got %d dialog options; want %d including ShowHidden", len(options), tc.want)
			}
		})
	}
}
