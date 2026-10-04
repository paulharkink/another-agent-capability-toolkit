package picker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// macPickerScript runs as a temporary macOS applet. A plain osascript process
// cannot reliably own a foreground Open panel when AACT runs inside a terminal.
const macPickerScript = `function run(argv) {
  ObjC.import("AppKit");
  var args = ObjC.deepUnwrap($.NSProcessInfo.processInfo.arguments);
  var resultPath = args[1];
  var kind = args[2];
  var directory = args[3];
  var app = $.NSApplication.sharedApplication;
  try {
    app.setActivationPolicy($.NSApplicationActivationPolicyRegular);
    var panel = $.NSOpenPanel.openPanel;
    panel.setCanChooseFiles(kind === "file");
    panel.setCanChooseDirectories(kind === "directory");
    panel.setAllowsMultipleSelection(false);
    panel.setShowsHiddenFiles(true);
    if (directory) panel.setDirectoryURL($.NSURL.fileURLWithPath(directory));
    app.activateIgnoringOtherApps(true);
    var response = panel.runModal;
    var result = Number(response) === 1
      ? {status: "selected", path: ObjC.unwrap(panel.URL.path)}
      : {status: "cancel"};
    $.NSString.stringWithString(JSON.stringify(result)).writeToFileAtomicallyEncodingError(resultPath, true, $.NSUTF8StringEncoding, null);
  } catch (e) {
    $.NSString.stringWithString(JSON.stringify({status: "error", error: String(e)})).writeToFileAtomicallyEncodingError(resultPath, true, $.NSUTF8StringEncoding, null);
  }
  app.terminate(null);
}`

type macPickerResult struct {
	Status string `json:"status"`
	Path   string `json:"path"`
	Error  string `json:"error"`
}

func selectMacNative(ctx context.Context, kind, initial string) (string, error) {
	if kind != "file" && kind != "directory" {
		return "", fmt.Errorf("unsupported picker kind %q", kind)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "aact-picker-")
	if err != nil {
		return "", fmt.Errorf("%w: create temporary picker directory: %v", ErrUnavailable, err)
	}
	defer os.RemoveAll(dir)
	app := filepath.Join(dir, "AACTPicker.app")
	resultFile := filepath.Join(dir, "result.json")
	output, err := exec.CommandContext(ctx, "osacompile", "-l", "JavaScript", "-o", app, "-e", macPickerScript).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: osacompile: %v: %s", ErrUnavailable, err, strings.TrimSpace(string(output)))
	}
	initialDirectory := initial
	if info, err := os.Stat(initial); initial == "" || err != nil || !info.IsDir() {
		initialDirectory = filepath.Dir(initial)
	}
	if initialDirectory == "." {
		initialDirectory, _ = os.Getwd()
	}
	output, err = exec.CommandContext(ctx, "open", "-n", "-a", app, "--args", resultFile, kind, initialDirectory).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: open native dialog: %v: %s", ErrUnavailable, err, strings.TrimSpace(string(output)))
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(15 * time.Minute)
	defer timeout.Stop()
	for {
		data, err := os.ReadFile(resultFile)
		if err == nil {
			var result macPickerResult
			if err := json.Unmarshal(data, &result); err != nil {
				return "", fmt.Errorf("%w: decode native dialog result: %v", ErrUnavailable, err)
			}
			switch result.Status {
			case "selected":
				if result.Path == "" {
					return "", fmt.Errorf("%w: native dialog returned no path", ErrUnavailable)
				}
				return result.Path, nil
			case "cancel":
				return "", ErrCancelled
			case "error":
				return "", fmt.Errorf("%w: native dialog: %s", ErrUnavailable, result.Error)
			default:
				return "", fmt.Errorf("%w: unexpected native dialog status %q", ErrUnavailable, result.Status)
			}
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: read native dialog result: %v", ErrUnavailable, err)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", fmt.Errorf("%w: native dialog did not respond within 15 minutes", ErrUnavailable)
		case <-ticker.C:
		}
	}
}
