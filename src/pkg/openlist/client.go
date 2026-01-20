package openlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

// Client OpenList API 客户端
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient 创建 OpenList 客户端
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 0, // 上传可能需要很长时间
		},
	}
}

// SetToken 设置 API Token
func (c *Client) SetToken(token string) {
	c.token = token
}

// Upload 上传文件（使用 PUT /api/fs/put）
func (c *Client) Upload(ctx context.Context, localPath, remotePath string, onProgress func(UploadProgress)) error {
	// 打开本地文件
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("获取文件信息失败: %w", err)
	}

	totalSize := fileInfo.Size()

	// 创建进度追踪 Reader
	progressReader := NewProgressReader(file, totalSize, onProgress)

	// 构建请求
	req, err := http.NewRequestWithContext(ctx, "PUT", c.baseURL+"/api/fs/put", progressReader)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", totalSize))
	req.Header.Set("File-Path", url.PathEscape(remotePath))
	req.ContentLength = totalSize

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("上传请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("上传失败 (HTTP %d): %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		if result.Code != 200 {
			return fmt.Errorf("上传失败: %s", result.Message)
		}
	}

	return nil
}

// StorageInfo 存储信息
type StorageInfo struct {
	ID        int    `json:"id"`
	MountPath string `json:"mount_path"`
	Driver    string `json:"driver"`
	Status    string `json:"status"`
	Disabled  bool   `json:"disabled"`
}

// ListStorages 列出所有存储
func (c *Client) ListStorages(ctx context.Context) ([]StorageInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/admin/storage/list", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Content []StorageInfo `json:"content"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误: %s", result.Message)
	}

	return result.Data.Content, nil
}

// CheckStorageHealth 检查存储健康状态
func (c *Client) CheckStorageHealth(ctx context.Context, storageName string) error {
	body := fmt.Sprintf(`{"path":"/%s","refresh":true}`, storageName)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/fs/list",
		bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("存储连接失败: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	if result.Code != 200 {
		return fmt.Errorf("存储不可用: %s", result.Message)
	}

	return nil
}

// GetToken 获取管理员 Token（通过登录）
func (c *Client) GetToken(ctx context.Context, username, password string) (string, error) {
	body := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/auth/login",
		bytes.NewReader([]byte(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	if result.Code != 200 {
		return "", fmt.Errorf("登录失败: %s", result.Message)
	}

	return result.Data.Token, nil
}

// Mkdir 创建目录
func (c *Client) Mkdir(ctx context.Context, remotePath string) error {
	body := fmt.Sprintf(`{"path":"%s"}`, remotePath)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/fs/mkdir",
		bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 目录已存在也算成功
	if result.Code != 200 && result.Message != "file exists" {
		return fmt.Errorf("创建目录失败: %s", result.Message)
	}

	return nil
}

// IsServiceReady 检查 OpenList 服务是否就绪
func (c *Client) IsServiceReady(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/public/settings", nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200
}
