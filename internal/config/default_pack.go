package config

import (
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
)

const defaultPackConfig = "manager/default-pack"

func savedDefaultPack(stateRoot string) (string, error) {
    if stateRoot == "" {
        return "", nil
    }
    settingPath := filepath.Join(stateRoot, defaultPackConfig)
    data, err := os.ReadFile(settingPath)
    if os.IsNotExist(err) {
        return "", nil
    }
    if err != nil {
        return "", err
    }
    manifest := strings.TrimSpace(string(data))
    if manifest == "" {
        return "", fmt.Errorf("saved default Capability Pack config is empty: %s", settingPath)
    }
    if !filepath.IsAbs(manifest) {
        manifest, err = resolvePath(manifest, settingPath)
        if err != nil {
            return "", fmt.Errorf("saved default Capability Pack config %s: %w", settingPath, err)
        }
    }
    info, err := os.Stat(manifest)
    if err != nil {
        return "", fmt.Errorf("saved default Capability Pack config unavailable at %s: %w", manifest, err)
    }
    if !info.Mode().IsRegular() {
        return "", fmt.Errorf("saved default Capability Pack config is not a file: %s", manifest)
    }
    return manifest, nil
}

func rememberDefaultPack(stateRoot, manifest string) error {
    if stateRoot == "" || manifest == "" {
        return nil
    }
    path := filepath.Join(stateRoot, defaultPackConfig)
    if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
        return err
    }
    file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
    if os.IsExist(err) {
        return nil
    }
    if err != nil {
        return err
    }
    canonical, err := filepath.Abs(manifest)
    if err == nil {
        _, err = io.WriteString(file, canonical+"\n")
    }
    if closeErr := file.Close(); err == nil {
        err = closeErr
    }
    if err != nil {
        _ = os.Remove(path)
        return err
    }
    return nil
}
