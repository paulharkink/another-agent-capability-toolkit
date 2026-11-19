package releasecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const defaultAPI = "https://api.github.com/repos/paulharkink/another-agent-capability-toolkit/releases"

var stableTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

func APIURL() string {
	if value := strings.TrimSpace(os.Getenv("AACT_RELEASE_API_URL")); value != "" {
		return value
	}
	return defaultAPI
}

func List(ctx context.Context) ([]Release, error) {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := APIURL()
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, errors.New("invalid release catalog URL")
	}
	var releases []Release
	for page := 1; page <= 10; page++ {
		u := *parsed
		q := u.Query()
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprint(page))
		u.RawQuery = q.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("create release catalog request: %w", err)
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("query release catalog: %w", err)
		}
		if response.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
			return nil, fmt.Errorf("release catalog returned HTTP %d", response.StatusCode)
		}
		var batch []Release
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&batch)
		response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode release catalog: %w", decodeErr)
		}
		for _, release := range batch {
			if !release.Draft && !release.Prerelease && stableTag.MatchString(release.Tag) {
				releases = append(releases, release)
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	sort.Slice(releases, func(i, j int) bool { return greater(releases[i].Tag, releases[j].Tag) })
	return releases, nil
}

func StableVersion(tag string) bool { return stableTag.MatchString(tag) }

func NewerThan(candidate, current string) bool {
	if !StableVersion("v"+candidate) || !StableVersion("v"+strings.TrimPrefix(current, "v")) {
		return false
	}
	return greater("v"+candidate, "v"+strings.TrimPrefix(current, "v"))
}

func ValidateAssetURL(value string) (string, error) {
	asset, err := url.Parse(value)
	if err != nil || asset.Host == "" || (asset.Scheme != "https" && asset.Scheme != "http") {
		return "", errors.New("asset URL must use HTTP or HTTPS")
	}
	api, err := url.Parse(APIURL())
	if err != nil {
		return "", errors.New("invalid release catalog URL")
	}
	allowed := asset.Host == api.Host || asset.Host == "github.com" || asset.Host == "objects.githubusercontent.com"
	if !allowed {
		return "", errors.New("asset URL host does not match the release catalog")
	}
	if api.Scheme == "https" && asset.Scheme != "https" {
		return "", errors.New("asset URL must use HTTPS")
	}
	return asset.String(), nil
}

func IsAllowedAssetRedirect(value string) bool {
	asset, err := url.Parse(value)
	if err != nil || asset.Host == "" {
		return false
	}
	api, err := url.Parse(APIURL())
	if err != nil {
		return false
	}
	if asset.Host == api.Host {
		return asset.Scheme == api.Scheme
	}
	return asset.Scheme == "https" && (asset.Host == "github.com" || asset.Host == "release-assets.githubusercontent.com" || asset.Host == "objects.githubusercontent.com")
}

func greater(a, b string) bool {
	var amaj, amin, apat, bmaj, bmin, bpat int
	_, _ = fmt.Sscanf(a, "v%d.%d.%d", &amaj, &amin, &apat)
	_, _ = fmt.Sscanf(b, "v%d.%d.%d", &bmaj, &bmin, &bpat)
	if amaj != bmaj {
		return amaj > bmaj
	}
	if amin != bmin {
		return amin > bmin
	}
	return apat > bpat
}
