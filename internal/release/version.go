package release

import (
	"fmt"
	"strconv"
	"strings"
)

const readmeInstallPrefix = "curl -fsSL https://raw.githubusercontent.com/paulharkink/another-agent-capability-toolkit/main/install.sh | sh -s -- -v "

// NextVersion increments one numeric semantic-version component.
func NextVersion(current, bump string) (string, error) {
	current = strings.TrimPrefix(current, "v")
	parts := strings.Split(current, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid semantic version %q", current)
	}
	var values [3]int
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || strconv.Itoa(value) != part {
			return "", fmt.Errorf("invalid semantic version %q", current)
		}
		values[i] = value
	}
	switch bump {
	case "patch":
		values[2]++
	case "minor":
		values[1]++
		values[2] = 0
	case "major":
		values[0]++
		values[1], values[2] = 0, 0
	default:
		return "", fmt.Errorf("invalid release bump %q (want major, minor, or patch)", bump)
	}
	return fmt.Sprintf("v%d.%d.%d", values[0], values[1], values[2]), nil
}

// ReadmeInstallVersion returns the numeric version from the single documented
// installer command in README.md.
func ReadmeInstallVersion(readme string) (string, error) {
	var version string
	count := 0
	for _, line := range strings.Split(readme, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, readmeInstallPrefix) {
			count++
			version = strings.TrimPrefix(line, readmeInstallPrefix)
		}
	}
	if count != 1 {
		return "", fmt.Errorf("README.md must contain exactly one versioned AACT install command; found %d", count)
	}
	if _, err := NextVersion(version, "patch"); err != nil {
		return "", fmt.Errorf("invalid README install version: %w", err)
	}
	return version, nil
}

// UpdateReadmeInstallVersion changes only the version in the one-line installer
// command, preserving all other README content.
func UpdateReadmeInstallVersion(readme, version string) (string, error) {
	if _, err := NextVersion(version, "patch"); err != nil {
		return "", fmt.Errorf("invalid release version: %w", err)
	}
	oldVersion, err := ReadmeInstallVersion(readme)
	if err != nil {
		return "", err
	}
	oldCommand := readmeInstallPrefix + oldVersion
	newCommand := readmeInstallPrefix + strings.TrimPrefix(version, "v")
	if oldCommand == newCommand {
		return "", fmt.Errorf("README already names release %s", newCommand[len(readmeInstallPrefix):])
	}
	return strings.Replace(readme, oldCommand, newCommand, 1), nil
}
