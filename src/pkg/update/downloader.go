package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DownloadProgress 下载进度信息
type DownloadProgress struct {
	TotalBytes      int64   `json:"total_bytes"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	Percentage      float64 `json:"percentage"`
	Speed           float64 `json:"speed_bytes_per_second"`
	ETA             int     `json:"eta_seconds"`
}

// DownloadStatus 下载状态
type DownloadStatus string

const (
	DownloadStatusIdle        DownloadStatus = "idle"
	DownloadStatusDownloading DownloadStatus = "downloading"
	DownloadStatusVerifying   DownloadStatus = "verifying"
	DownloadStatusCompleted   DownloadStatus = "completed"
	DownloadStatusFailed      DownloadStatus = "failed"
	DownloadStatusCancelled   DownloadStatus = "cancelled"
)

// DownloadResult 下载结果
type DownloadResult struct {
	Status      DownloadStatus `json:"status"`
	FilePath    string         `json:"file_path,omitempty"`
	SHA256      string         `json:"sha256,omitempty"`
	Error       string         `json:"error,omitempty"`
	TotalBytes  int64          `json:"total_bytes"`
	ElapsedTime float64        `json:"elapsed_time_seconds"`
}

// Downloader 下载器
type Downloader struct {
	httpClient  *http.Client
	downloadDir string

	mu           sync.RWMutex
	status       DownloadStatus
	progress     DownloadProgress
	cancelFunc   context.CancelFunc
	progressChan chan DownloadProgress
}

// NewDownloader 创建新的下载器
func NewDownloader(downloadDir string) *Downloader {
	return &Downloader{
		httpClient: &http.Client{
			Timeout: 0, // 下载不设置超时
		},
		downloadDir: downloadDir,
		status:      DownloadStatusIdle,
	}
}

// GetStatus 获取当前下载状态
func (d *Downloader) GetStatus() DownloadStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.status
}

// GetProgress 获取当前下载进度
func (d *Downloader) GetProgress() DownloadProgress {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.progress
}

// SetProgressCallback 设置进度回调通道
func (d *Downloader) SetProgressCallback(ch chan DownloadProgress) {
	d.mu.Lock()
	d.progressChan = ch
	d.mu.Unlock()
}

// Download 下载更新文件
func (d *Downloader) Download(ctx context.Context, url string, expectedSHA256 string) (*DownloadResult, error) {
	d.mu.Lock()
	if d.status == DownloadStatusDownloading {
		d.mu.Unlock()
		return nil, fmt.Errorf("已有下载任务正在进行中")
	}

	ctx, d.cancelFunc = context.WithCancel(ctx)
	d.status = DownloadStatusDownloading
	d.progress = DownloadProgress{}
	d.mu.Unlock()

	startTime := time.Now()
	result := &DownloadResult{
		Status: DownloadStatusDownloading,
	}

	defer func() {
		d.mu.Lock()
		if result.Status == DownloadStatusDownloading {
			result.Status = DownloadStatusFailed
		}
		d.status = result.Status
		d.cancelFunc = nil
		d.mu.Unlock()
	}()

	// 确保下载目录存在
	if err := os.MkdirAll(d.downloadDir, 0755); err != nil {
		result.Error = fmt.Sprintf("创建下载目录失败: %v", err)
		result.Status = DownloadStatusFailed
		return result, errors.New(result.Error)
	}

	// 创建 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		result.Error = fmt.Sprintf("创建请求失败: %v", err)
		result.Status = DownloadStatusFailed
		return result, errors.New(result.Error)
	}

	req.Header.Set("User-Agent", "bililive-go-updater")

	// 发起请求
	resp, err := d.httpClient.Do(req)
	if err != nil {
		if ctx.Err() == context.Canceled {
			result.Status = DownloadStatusCancelled
			result.Error = "下载已取消"
			return result, nil
		}
		result.Error = fmt.Sprintf("下载请求失败: %v", err)
		result.Status = DownloadStatusFailed
		return result, errors.New(result.Error)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("下载失败，状态码: %d", resp.StatusCode)
		result.Status = DownloadStatusFailed
		return result, errors.New(result.Error)
	}

	totalSize := resp.ContentLength
	result.TotalBytes = totalSize

	// 生成临时文件名
	tempFile := filepath.Join(d.downloadDir, fmt.Sprintf("bililive-update-%d.tmp", time.Now().UnixNano()))
	file, err := os.Create(tempFile)
	if err != nil {
		result.Error = fmt.Sprintf("创建临时文件失败: %v", err)
		result.Status = DownloadStatusFailed
		return result, errors.New(result.Error)
	}
	defer file.Close()

	// 创建 SHA256 哈希器
	hasher := sha256.New()

	// 使用 TeeReader 同时写入文件和计算哈希
	multiWriter := io.MultiWriter(file, hasher)

	// 带进度的下载
	var downloaded int64
	buf := make([]byte, 32*1024) // 32KB buffer
	lastUpdate := time.Now()
	lastDownloaded := int64(0)

	for {
		select {
		case <-ctx.Done():
			os.Remove(tempFile)
			result.Status = DownloadStatusCancelled
			result.Error = "下载已取消"
			return result, nil
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, writeErr := multiWriter.Write(buf[:n])
			if writeErr != nil {
				os.Remove(tempFile)
				result.Error = fmt.Sprintf("写入文件失败: %v", writeErr)
				result.Status = DownloadStatusFailed
				return result, errors.New(result.Error)
			}
			downloaded += int64(n)

			// 更新进度（每 200ms 更新一次）
			now := time.Now()
			if now.Sub(lastUpdate) >= 200*time.Millisecond {
				elapsed := now.Sub(lastUpdate).Seconds()
				speed := float64(downloaded-lastDownloaded) / elapsed

				progress := DownloadProgress{
					TotalBytes:      totalSize,
					DownloadedBytes: downloaded,
					Percentage:      float64(downloaded) / float64(totalSize) * 100,
					Speed:           speed,
				}
				if speed > 0 {
					progress.ETA = int(float64(totalSize-downloaded) / speed)
				}

				d.mu.Lock()
				d.progress = progress
				progressChan := d.progressChan
				d.mu.Unlock()

				// 发送进度到回调通道
				if progressChan != nil {
					select {
					case progressChan <- progress:
					default:
					}
				}

				lastUpdate = now
				lastDownloaded = downloaded
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			os.Remove(tempFile)
			result.Error = fmt.Sprintf("读取响应失败: %v", err)
			result.Status = DownloadStatusFailed
			return result, errors.New(result.Error)
		}
	}

	// 验证阶段
	d.mu.Lock()
	d.status = DownloadStatusVerifying
	d.mu.Unlock()

	// 计算并验证 SHA256
	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	result.SHA256 = actualSHA256

	if expectedSHA256 != "" && actualSHA256 != expectedSHA256 {
		os.Remove(tempFile)
		result.Error = fmt.Sprintf("SHA256 校验失败: 期望 %s, 实际 %s", expectedSHA256, actualSHA256)
		result.Status = DownloadStatusFailed
		return result, errors.New(result.Error)
	}

	// 下载成功
	result.Status = DownloadStatusCompleted
	result.FilePath = tempFile
	result.ElapsedTime = time.Since(startTime).Seconds()

	return result, nil
}

// Cancel 取消当前下载
func (d *Downloader) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancelFunc != nil {
		d.cancelFunc()
	}
}

// CalculateFileSHA256 计算文件的 SHA256 哈希值
func CalculateFileSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("计算哈希失败: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
