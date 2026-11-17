package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultStartupTimeout = 30 * time.Second
	PollInterval          = 100 * time.Millisecond
	LogTailBytes          = 16 << 10
)

type ProcessSpec struct {
	Executable     string
	Args           []string
	StateRoot      string
	InfoPath       string
	LogPath        string
	Bootstrap      any
	Environment    []string
	StartupTimeout time.Duration
}

func StartProcess(spec ProcessSpec) (Info, error) {
	if err := spec.validate(); err != nil {
		return Info{}, err
	}
	if spec.Executable == "" || len(spec.Args) == 0 {
		return Info{}, errors.New("AACT daemon executable and arguments are required")
	}
	root, err := filepath.Abs(spec.StateRoot)
	if err != nil {
		return Info{}, fmt.Errorf("resolve AACT daemon state directory: %w", err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return Info{}, fmt.Errorf("create AACT daemon state directory: %w", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		return Info{}, fmt.Errorf("secure AACT daemon state directory: %w", err)
	}
	if spec.InfoPath == "" {
		spec.InfoPath = filepath.Join(root, InfoFilename)
	}
	if spec.LogPath == "" {
		spec.LogPath = filepath.Join(root, LogFilename)
	}
	payload, err := json.Marshal(spec.Bootstrap)
	if err != nil {
		return Info{}, fmt.Errorf("encode AACT daemon bootstrap: %w", err)
	}
	defer clear(payload)
	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return Info{}, fmt.Errorf("open AACT daemon log: %w", err)
	}
	if err := logFile.Chmod(0600); err != nil {
		_ = logFile.Close()
		return Info{}, fmt.Errorf("secure AACT daemon log: %w", err)
	}
	cmd := exec.Command(spec.Executable, spec.Args...)
	configureDetachedProcess(cmd)
	if spec.Environment != nil {
		cmd.Env = append([]string(nil), spec.Environment...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = logFile.Close()
		return Info{}, fmt.Errorf("open AACT daemon bootstrap pipe: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		return Info{}, fmt.Errorf("start AACT daemon: %w", err)
	}
	if n, err := stdin.Write(payload); err != nil || n != len(payload) {
		_ = stdin.Close()
		clear(payload)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		if err == nil {
			err = io.ErrShortWrite
		}
		return Info{}, fmt.Errorf("send AACT daemon bootstrap: %w", err)
	}
	clear(payload)
	if err := stdin.Close(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		return Info{}, fmt.Errorf("close AACT daemon bootstrap pipe: %w", err)
	}
	_ = logFile.Close()
	info, err := WaitReady(spec.InfoPath, spec.LogPath, spec.StartupTimeout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Info{}, err
	}
	_ = cmd.Process.Release()
	return info, nil
}

func WaitReady(infoPath, logPath string, timeout time.Duration) (Info, error) {
	if timeout <= 0 {
		timeout = DefaultStartupTimeout
	}
	deadline := time.Now().Add(timeout)
	lastError := errors.New("daemon metadata is not available")
	for {
		info, err := ReadInfo(infoPath)
		if err != nil {
			lastError = fmt.Errorf("read daemon metadata: %w", err)
		} else if err := Health(info); err != nil {
			lastError = fmt.Errorf("daemon health check: %w", err)
		} else {
			return info, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		delay := PollInterval
		if remaining < delay {
			delay = remaining
		}
		time.Sleep(delay)
	}
	logText, err := ReadLogTail(logPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logText = fmt.Sprintf("could not read daemon log: %v", err)
	} else if strings.TrimSpace(logText) == "" {
		logText = "daemon log is empty"
	}
	return Info{}, fmt.Errorf("AACT daemon did not become ready within %s (%v): %s", timeout, lastError, logText)
}

func ReadLogTail(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	truncated := info.Size() > LogTailBytes
	if truncated {
		if _, err := file.Seek(-LogTailBytes, io.SeekEnd); err != nil {
			return "", err
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, LogTailBytes))
	if err != nil {
		return "", err
	}
	if truncated {
		return "[earlier log lines truncated]\n" + string(data), nil
	}
	return string(data), nil
}

var controlHTTPClient = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
var healthHTTPClient = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type ControlStatusError struct {
	StatusCode int
	Message    string
}

func (e *ControlStatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("daemon control request failed with HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("daemon control request failed with HTTP %d: %s", e.StatusCode, e.Message)
}

func Health(info Info) error {
	if err := ValidateInfo(info); err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodGet, info.ControlAddress+"/health", nil)
	if err != nil {
		return err
	}
	response, err := healthHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func Running(info Info) bool { return Health(info) == nil }

func Stop(info Info, token []byte) error {
	if !Running(info) {
		return nil
	}
	if err := ControlRequest(info, token, http.MethodPost, "/shutdown", nil, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for Running(info) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if Running(info) {
		return errors.New("AACT daemon did not stop before timeout")
	}
	return nil
}

func ControlRequest(info Info, token []byte, method, path string, body any, result any) error {
	if err := ValidateInfo(info); err != nil {
		return err
	}
	var requestBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestBody = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, info.ControlAddress+path, requestBody)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := controlHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return &ControlStatusError{StatusCode: response.StatusCode, Message: strings.TrimSpace(string(message))}
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result)
	}
	return nil
}

func (s ProcessSpec) validate() error {
	if strings.TrimSpace(s.StateRoot) == "" {
		return errors.New("AACT daemon state directory is required")
	}
	if s.Bootstrap == nil {
		return errors.New("AACT daemon bootstrap is required")
	}
	return nil
}
