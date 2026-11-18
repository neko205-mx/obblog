// Package updater checks GitHub Releases for a newer build.
//
// The design keeps the network surface small: only the public releases
// endpoint is contacted, responses are size limited, and nothing is downloaded
// unless a caller explicitly asks for the asset.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"obblog/internal/version"
)

const (
	// DefaultRepo is where releases are published.
	DefaultRepo = "neko205-mx/obblog"
	// maxMetadata bounds the JSON we are willing to read.
	maxMetadata = 1 << 20
	// checkTimeout keeps a slow network from stalling the panel.
	checkTimeout = 15 * time.Second
)

// Release is the subset of the GitHub payload the panel needs.
type Release struct {
	Tag         string    `json:"tag"`
	Notes       string    `json:"notes"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
	// ChecksumsURL is the release asset holding SHA256SUMS.
	ChecksumsURL string `json:"-"`
	// SignatureURL is the detached signature over SHA256SUMS.
	SignatureURL string `json:"-"`
	// BinaryURL is the asset matching this build's OS and architecture.
	BinaryURL string `json:"-"`
	// BinaryName is the expected asset filename.
	BinaryName string `json:"binaryName"`
	// BinarySize lets the caller reject an implausible download.
	BinarySize int64 `json:"binarySize"`
}

// Status is what the admin panel displays.
type Status struct {
	Current     version.Info `json:"current"`
	Latest      string       `json:"latest,omitempty"`
	Notes       string       `json:"notes,omitempty"`
	URL         string       `json:"url,omitempty"`
	PublishedAt *time.Time   `json:"publishedAt,omitempty"`
	Available   bool         `json:"available"`
	CheckedAt   time.Time    `json:"checkedAt"`
	// Error reports why the check failed without failing the request.
	Error string `json:"error,omitempty"`
}

// Checker queries the GitHub releases API.
type Checker struct {
	Repo   string
	Client *http.Client
	// APIBase is overridable so tests do not reach the network.
	APIBase string
}

func NewChecker(repo string) *Checker {
	if repo == "" {
		repo = DefaultRepo
	}
	base := "https://api.github.com"
	// A custom endpoint lets an installation behind a mirror or a proxy use the
	// same checks without reaching GitHub directly.
	if override := os.Getenv("OBBLOG_UPDATE_API"); override != "" {
		base = override
	}
	return &Checker{
		Repo:    repo,
		Client:  &http.Client{Timeout: checkTimeout},
		APIBase: base,
	}
}

// AssetName is the filename a release publishes for this platform.
func AssetName() string {
	return fmt.Sprintf("obblog-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// Latest fetches the newest release and decides whether it is an upgrade.
func (c *Checker) Latest(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimSuffix(c.APIBase, "/"), c.Repo)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "obblog/"+version.Version)

	response, err := c.Client.Do(request)
	if err != nil {
		return nil, errors.New("无法连接 GitHub，请检查网络")
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, errors.New("仓库还没有发布任何版本")
	case response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests:
		// Unauthenticated requests share a per-IP quota, and a proxy can also
		// produce 403. Both are temporary from the user's point of view, so the
		// message names the likely cause instead of just the status code.
		if response.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, errors.New("GitHub API 访问次数已用尽，请稍后再试")
		}
		return nil, errors.New("GitHub 拒绝了请求（403），通常是 API 访问次数已用尽或代理拦截")
	case response.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub 返回 %d", response.StatusCode)
	}

	var payload struct {
		TagName     string    `json:"tag_name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxMetadata))
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("无法解析 GitHub 响应")
	}
	if payload.Draft || payload.Prerelease {
		return nil, errors.New("最新版本是草稿或预发布，已忽略")
	}
	if _, err := version.Parse(payload.TagName); err != nil {
		return nil, errors.New("最新版本的标签格式无效")
	}

	release := &Release{
		Tag:         payload.TagName,
		Notes:       payload.Body,
		URL:         payload.HTMLURL,
		PublishedAt: payload.PublishedAt,
	}
	want := AssetName()
	for _, asset := range payload.Assets {
		switch asset.Name {
		case want:
			release.BinaryURL, release.BinaryName, release.BinarySize = asset.URL, asset.Name, asset.Size
		case "SHA256SUMS":
			release.ChecksumsURL = asset.URL
		case "SHA256SUMS.sig":
			release.SignatureURL = asset.URL
		}
	}
	return release, nil
}

// Status performs a check and flattens the result for the panel. Network
// failures are reported in Error rather than returned, so a blocked egress
// does not look like a server fault.
func (c *Checker) Status(ctx context.Context) Status {
	status := Status{Current: version.Current(), CheckedAt: time.Now().UTC()}
	release, err := c.Latest(ctx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Latest = release.Tag
	status.Notes = release.Notes
	status.URL = release.URL
	status.PublishedAt = &release.PublishedAt
	status.Available = version.Newer(release.Tag, version.Version)
	if status.Available && (release.BinaryURL == "" || release.ChecksumsURL == "" || release.SignatureURL == "") {
		status.Available = false
		status.Error = "该版本缺少 " + AssetName() + " 或签名文件，无法自动更新"
	}
	return status
}
