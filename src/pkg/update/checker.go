// Package update 提供 bililive-go 的自动更新功能
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// API 地址常量
const (
	// GitHubReleasesAPI GitHub Releases API 地址
	GitHubReleasesAPI = "https://api.github.com/repos/bililive-go/bililive-go/releases"
	// DefaultVersionAPIURL 默认的版本检测 API 地址
	DefaultVersionAPIURL = "https://bililive-go.com/api/versions"
)

// ReleaseInfo 包含版本发布信息
type ReleaseInfo struct {
	Version     string    `json:"version"`
	TagName     string    `json:"tag_name"`
	ReleaseDate time.Time `json:"release_date"`
	DownloadURL string    `json:"download_url"`
	SHA256      string    `json:"sha256,omitempty"`
	Changelog   string    `json:"changelog"`
	Prerelease  bool      `json:"prerelease"`
	AssetName   string    `json:"asset_name"`
	AssetSize   int64     `json:"asset_size"`
}

// githubRelease GitHub API 返回的 Release 结构
type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	Prerelease  bool          `json:"prerelease"`
	Draft       bool          `json:"draft"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
	HTMLURL     string        `json:"html_url"`
}

// githubAsset GitHub Release Asset 结构
type githubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

// Checker 版本检查器
type Checker struct {
	httpClient     *http.Client
	currentVersion string
	releaseURL     string
	versionAPIURL  string // 版本检测 API URL（可自定义测试）
}

// NewChecker 创建新的版本检查器
func NewChecker(currentVersion string) *Checker {
	return &Checker{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		currentVersion: currentVersion,
		releaseURL:     GitHubReleasesAPI,
		versionAPIURL:  DefaultVersionAPIURL,
	}
}

// SetReleaseURL 设置自定义 Release API URL（用于测试或自托管）
func (c *Checker) SetReleaseURL(url string) {
	c.releaseURL = url
}

// SetVersionAPIURL 设置自定义版本检测 API URL（用于测试本地自动升级逻辑）
func (c *Checker) SetVersionAPIURL(url string) {
	c.versionAPIURL = url
}

// CheckForUpdate 检查是否有新版本
// 返回最新版本信息，如果当前已是最新版本则返回 nil
func (c *Checker) CheckForUpdate(includePrerelease bool) (*ReleaseInfo, error) {
	releases, err := c.fetchReleases()
	if err != nil {
		return nil, err
	}

	if len(releases) == 0 {
		return nil, nil
	}

	// 查找最新的适用版本
	var latestRelease *githubRelease
	for i := range releases {
		release := &releases[i]
		if release.Draft {
			continue
		}
		if !includePrerelease && release.Prerelease {
			continue
		}
		latestRelease = release
		break
	}

	if latestRelease == nil {
		return nil, nil
	}

	// 比较版本
	isNewer, err := c.isNewerVersion(latestRelease.TagName)
	if err != nil {
		// 如果版本比较失败，使用字符串比较
		if latestRelease.TagName == c.currentVersion {
			return nil, nil
		}
	} else if !isNewer {
		return nil, nil
	}

	// 查找适合当前平台的下载资源
	assetName := c.getExpectedAssetName()
	var matchedAsset *githubAsset
	for i := range latestRelease.Assets {
		asset := &latestRelease.Assets[i]
		if strings.Contains(asset.Name, assetName) || asset.Name == assetName {
			matchedAsset = asset
			break
		}
	}

	if matchedAsset == nil {
		return nil, fmt.Errorf("未找到适合当前平台的下载资源 (expected: %s)", assetName)
	}

	return &ReleaseInfo{
		Version:     strings.TrimPrefix(latestRelease.TagName, "v"),
		TagName:     latestRelease.TagName,
		ReleaseDate: latestRelease.PublishedAt,
		DownloadURL: matchedAsset.BrowserDownloadURL,
		Changelog:   latestRelease.Body,
		Prerelease:  latestRelease.Prerelease,
		AssetName:   matchedAsset.Name,
		AssetSize:   matchedAsset.Size,
	}, nil
}

// GetLatestRelease 获取最新版本信息（不进行版本比较）
func (c *Checker) GetLatestRelease(includePrerelease bool) (*ReleaseInfo, error) {
	releases, err := c.fetchReleases()
	if err != nil {
		return nil, err
	}

	if len(releases) == 0 {
		return nil, fmt.Errorf("没有找到任何发布版本")
	}

	// 查找最新的适用版本
	var latestRelease *githubRelease
	for i := range releases {
		release := &releases[i]
		if release.Draft {
			continue
		}
		if !includePrerelease && release.Prerelease {
			continue
		}
		latestRelease = release
		break
	}

	if latestRelease == nil {
		return nil, fmt.Errorf("没有找到适用的发布版本")
	}

	// 查找适合当前平台的下载资源
	assetName := c.getExpectedAssetName()
	var matchedAsset *githubAsset
	for i := range latestRelease.Assets {
		asset := &latestRelease.Assets[i]
		if strings.Contains(asset.Name, assetName) || asset.Name == assetName {
			matchedAsset = asset
			break
		}
	}

	if matchedAsset == nil {
		return nil, fmt.Errorf("未找到适合当前平台的下载资源")
	}

	return &ReleaseInfo{
		Version:     strings.TrimPrefix(latestRelease.TagName, "v"),
		TagName:     latestRelease.TagName,
		ReleaseDate: latestRelease.PublishedAt,
		DownloadURL: matchedAsset.BrowserDownloadURL,
		Changelog:   latestRelease.Body,
		Prerelease:  latestRelease.Prerelease,
		AssetName:   matchedAsset.Name,
		AssetSize:   matchedAsset.Size,
	}, nil
}

// fetchReleases 从 GitHub API 获取发布列表
func (c *Checker) fetchReleases() ([]githubRelease, error) {
	req, err := http.NewRequest("GET", c.releaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "bililive-go-updater")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回错误状态码: %d", resp.StatusCode)
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("解析 GitHub API 响应失败: %w", err)
	}

	return releases, nil
}

// isNewerVersion 检查指定版本是否比当前版本新
func (c *Checker) isNewerVersion(tagName string) (bool, error) {
	// 移除 'v' 前缀
	currentVer := strings.TrimPrefix(c.currentVersion, "v")
	newVer := strings.TrimPrefix(tagName, "v")

	current, err := semver.NewVersion(currentVer)
	if err != nil {
		return false, fmt.Errorf("解析当前版本失败: %w", err)
	}

	latest, err := semver.NewVersion(newVer)
	if err != nil {
		return false, fmt.Errorf("解析新版本失败: %w", err)
	}

	return latest.GreaterThan(current), nil
}

// getExpectedAssetName 获取当前平台期望的资源名称
func (c *Checker) getExpectedAssetName() string {
	os := runtime.GOOS
	arch := runtime.GOARCH

	// 生成期望的文件名模式
	// 例如: bililive-windows-amd64, bililive-linux-amd64
	return fmt.Sprintf("bililive-%s-%s", os, arch)
}

// CompareVersions 比较两个版本号
// 返回: -1 (v1 < v2), 0 (v1 == v2), 1 (v1 > v2)
func CompareVersions(v1, v2 string) (int, error) {
	ver1, err := semver.NewVersion(strings.TrimPrefix(v1, "v"))
	if err != nil {
		return 0, fmt.Errorf("解析版本 %s 失败: %w", v1, err)
	}

	ver2, err := semver.NewVersion(strings.TrimPrefix(v2, "v"))
	if err != nil {
		return 0, fmt.Errorf("解析版本 %s 失败: %w", v2, err)
	}

	return ver1.Compare(ver2), nil
}

// =============================================================================
// bililive-go.com API 相关
// =============================================================================

// BililiveGoComResponse bililive-go.com 版本 API 响应
type BililiveGoComResponse struct {
	LatestVersion   string `json:"latest_version"`
	ReleaseDate     string `json:"release_date"`
	Changelog       string `json:"changelog"`
	Prerelease      bool   `json:"prerelease"`
	CurrentVersion  string `json:"current_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	UpdateRequired  bool   `json:"update_required"`
	Download        *struct {
		GitHub   string `json:"github"`
		Proxy    string `json:"proxy"`
		Filename string `json:"filename"`
		SHA256   string `json:"sha256"`
	} `json:"download,omitempty"`
	AllAssets map[string]struct {
		GitHub   string `json:"github"`
		Proxy    string `json:"proxy"`
		Filename string `json:"filename"`
		SHA256   string `json:"sha256"`
	} `json:"all_assets,omitempty"`
	ReleasePage string `json:"release_page"`
}

// CheckForUpdateViaBililiveGoCom 通过版本检测 API 检查更新
// 提供 GitHub 直连和备用的下载中转服务
func (c *Checker) CheckForUpdateViaBililiveGoCom(includePrerelease bool) (*ReleaseInfo, error) {
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)

	// 构建请求 URL
	apiURL, err := url.Parse(c.versionAPIURL)
	if err != nil {
		return nil, fmt.Errorf("解析 API URL 失败: %w", err)
	}

	query := apiURL.Query()
	query.Set("current", c.currentVersion)
	query.Set("platform", platform)
	if includePrerelease {
		query.Set("prerelease", "true")
	}
	apiURL.RawQuery = query.Encode()

	// 发送请求
	req, err := http.NewRequest("GET", apiURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("bililive-go/%s", c.currentVersion))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 bililive-go.com API 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bililive-go.com API 返回错误状态码: %d", resp.StatusCode)
	}

	var response BililiveGoComResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	// 如果没有可用更新，返回 nil
	if !response.UpdateAvailable {
		return nil, nil
	}

	// 构建 ReleaseInfo
	releaseDate, _ := time.Parse("2006-01-02", response.ReleaseDate)

	info := &ReleaseInfo{
		Version:     response.LatestVersion,
		TagName:     "v" + response.LatestVersion,
		ReleaseDate: releaseDate,
		Changelog:   response.Changelog,
		Prerelease:  response.Prerelease,
	}

	// 优先使用 GitHub 直连下载链接（中转链接作为备用，由下载器在失败时自动回退）
	if response.Download != nil {
		if response.Download.GitHub != "" {
			info.DownloadURL = response.Download.GitHub
		} else {
			info.DownloadURL = response.Download.Proxy
		}
		info.SHA256 = response.Download.SHA256
		info.AssetName = response.Download.Filename
	}

	return info, nil
}

// CheckForUpdateWithFallback 检查更新，带回退逻辑
// 优先使用 bililive-go.com API，失败时回退到 GitHub API
func (c *Checker) CheckForUpdateWithFallback(includePrerelease bool) (*ReleaseInfo, error) {
	// 先尝试 bililive-go.com
	info, err := c.CheckForUpdateViaBililiveGoCom(includePrerelease)
	if err == nil {
		return info, nil
	}

	// 回退到 GitHub API
	return c.CheckForUpdate(includePrerelease)
}

// GetProxyDownloadURL 获取中转下载 URL
// 将 GitHub 下载链接转换为通过 bililive-go.com 中转的链接
func GetProxyDownloadURL(githubURL string) string {
	return fmt.Sprintf("https://bililive-go.com/remotetools/download?downloadurl=%s",
		url.QueryEscape(githubURL))
}
