package openlist

import (
	"io"
	"sync"
	"time"
)

// UploadProgress 上传进度
type UploadProgress struct {
	BytesUploaded       int64   // 已上传字节数
	TotalBytes          int64   // 总字节数
	SpeedBytesPerSec    int64   // 实时速度 (bytes/sec)
	AvgSpeedBytesPerSec int64   // 平均速度 (bytes/sec)
	EtaSeconds          int64   // 预计剩余时间 (秒)
	Percentage          float64 // 百分比 (0-100)
}

// speedSample 速度采样
type speedSample struct {
	bytes int64
	time  time.Time
}

// ProgressReader 带进度追踪的 Reader
type ProgressReader struct {
	reader    io.Reader
	total     int64
	uploaded  int64
	startTime time.Time

	// 用于计算瞬时速度的滑动窗口
	samples   []speedSample
	samplesMu sync.Mutex

	onProgress     func(UploadProgress)
	lastReport     time.Time
	reportInterval time.Duration
}

// NewProgressReader 创建进度追踪 Reader
func NewProgressReader(reader io.Reader, total int64, onProgress func(UploadProgress)) *ProgressReader {
	return &ProgressReader{
		reader:         reader,
		total:          total,
		startTime:      time.Now(),
		samples:        make([]speedSample, 0, 100),
		onProgress:     onProgress,
		reportInterval: 500 * time.Millisecond,
	}
}

func (pr *ProgressReader) Read(p []byte) (n int, err error) {
	n, err = pr.reader.Read(p)
	if n > 0 {
		pr.uploaded += int64(n)
		pr.addSample(int64(n))
		pr.maybeReport()
	}
	return n, err
}

func (pr *ProgressReader) addSample(bytes int64) {
	pr.samplesMu.Lock()
	defer pr.samplesMu.Unlock()

	now := time.Now()
	pr.samples = append(pr.samples, speedSample{bytes: bytes, time: now})

	// 保留最近 5 秒的样本
	cutoff := now.Add(-5 * time.Second)
	for len(pr.samples) > 0 && pr.samples[0].time.Before(cutoff) {
		pr.samples = pr.samples[1:]
	}
}

func (pr *ProgressReader) calculateInstantSpeed() int64 {
	pr.samplesMu.Lock()
	defer pr.samplesMu.Unlock()

	if len(pr.samples) < 2 {
		return 0
	}

	// 计算最近样本的平均速度
	var totalBytes int64
	for _, s := range pr.samples {
		totalBytes += s.bytes
	}

	duration := pr.samples[len(pr.samples)-1].time.Sub(pr.samples[0].time).Seconds()
	if duration <= 0 {
		return 0
	}

	return int64(float64(totalBytes) / duration)
}

func (pr *ProgressReader) maybeReport() {
	if pr.onProgress == nil {
		return
	}

	now := time.Now()
	if now.Sub(pr.lastReport) < pr.reportInterval && pr.uploaded < pr.total {
		return
	}
	pr.lastReport = now

	elapsed := now.Sub(pr.startTime).Seconds()
	var avgSpeed int64
	if elapsed > 0 {
		avgSpeed = int64(float64(pr.uploaded) / elapsed)
	}

	instantSpeed := pr.calculateInstantSpeed()

	remaining := pr.total - pr.uploaded
	var eta int64
	if avgSpeed > 0 {
		eta = remaining / avgSpeed
	}

	var percentage float64
	if pr.total > 0 {
		percentage = float64(pr.uploaded) / float64(pr.total) * 100
	}

	pr.onProgress(UploadProgress{
		BytesUploaded:       pr.uploaded,
		TotalBytes:          pr.total,
		SpeedBytesPerSec:    instantSpeed,
		AvgSpeedBytesPerSec: avgSpeed,
		EtaSeconds:          eta,
		Percentage:          percentage,
	})
}
