// Package update 提供 bililive-go 的自动更新功能
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// GitHubReleasesAPI GitHub Releases API 地址
const GitHubReleasesAPI = "https://api.github.com/repos/bililive-go/bililive-go/releases"

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
}

// NewChecker 创建新的版本检查器
func NewChecker(currentVersion string) *Checker {
	return &Checker{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		currentVersion: currentVersion,
		releaseURL:     GitHubReleasesAPI,
	}
}

// SetReleaseURL 设置自定义 Release API URL（用于测试或自托管）
func (c *Checker) SetReleaseURL(url string) {
	c.releaseURL = url
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
