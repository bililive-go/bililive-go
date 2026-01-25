//go:build dev

package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TestScenario 测试场景定义
type TestScenario struct {
	Name        string        `yaml:"name" json:"name"`
	Description string        `yaml:"description" json:"description"`
	Stream      StreamConfig  `yaml:"stream" json:"stream"`
	Faults      []FaultConfig `yaml:"faults" json:"faults"`
	Expected    Expected      `yaml:"expected" json:"expected"`
}

// StreamConfig 流配置
type StreamConfig struct {
	Format   string        `yaml:"format" json:"format"`     // flv, hls
	Codec    string        `yaml:"codec" json:"codec"`       // avc, hevc, hevc-annexb
	Duration time.Duration `yaml:"duration" json:"duration"` // 测试时长
	Quality  string        `yaml:"quality" json:"quality"`   // 1080p, 720p
}

// FaultConfig 故障注入配置
type FaultConfig struct {
	Type     string                 `yaml:"type" json:"type"`         // disconnect, delay, slowdown, resolution_change, timestamp_jump
	At       time.Duration          `yaml:"at" json:"at"`             // 何时触发
	Duration time.Duration          `yaml:"duration" json:"duration"` // 持续多久
	Params   map[string]interface{} `yaml:"params" json:"params"`     // 额外参数
}

// Expected 预期结果
type Expected struct {
	OutputPlayable       bool                `yaml:"output_playable" json:"output_playable"`
	MinDuration          time.Duration       `yaml:"min_duration" json:"min_duration"`
	DownloaderReconnects bool                `yaml:"downloader_reconnects" json:"downloader_reconnects"`
	MaxFileSizeDiff      float64             `yaml:"max_file_size_diff" json:"max_file_size_diff"` // 允许的文件大小差异百分比
	ComplianceWarnings   []ComplianceWarning `yaml:"compliance_warnings" json:"compliance_warnings"`
}

// ComplianceWarning 合规性警告
type ComplianceWarning struct {
	Type        string `yaml:"type" json:"type"`
	Description string `yaml:"description" json:"description"`
}

// TestResult 测试结果
type TestResult struct {
	ScenarioName   string        `json:"scenario_name"`
	Success        bool          `json:"success"`
	Duration       time.Duration `json:"duration"`
	OutputPath     string        `json:"output_path,omitempty"`
	OutputPlayable bool          `json:"output_playable"`
	OutputDuration time.Duration `json:"output_duration,omitempty"`
	OutputFileSize int64         `json:"output_file_size,omitempty"`
	ErrorMessage   string        `json:"error_message,omitempty"`
	Warnings       []string      `json:"warnings,omitempty"`
	ReconnectCount int           `json:"reconnect_count,omitempty"`
}

// TestServer 测试服务器客户端
type TestServer struct {
	BaseURL string
	client  *http.Client
}

// NewTestServer 创建测试服务器客户端
func NewTestServer(baseURL string) *TestServer {
	return &TestServer{
		BaseURL: baseURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// HealthCheck 检查测试服务器是否在线
func (ts *TestServer) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", ts.BaseURL+"/health", nil)
	if err != nil {
		return err
	}

	resp, err := ts.client.Do(req)
	if err != nil {
		return fmt.Errorf("测试服务器不可达: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("测试服务器返回 %d", resp.StatusCode)
	}

	return nil
}

// StartStream 启动测试流
func (ts *TestServer) StartStream(ctx context.Context, cfg StreamConfig) (string, error) {
	reqBody, _ := json.Marshal(cfg)

	req, err := http.NewRequestWithContext(ctx, "POST", ts.BaseURL+"/api/streams",
		strings.NewReader(string(reqBody)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		StreamURL string `json:"stream_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.StreamURL, nil
}

// InjectFault 注入故障
func (ts *TestServer) InjectFault(ctx context.Context, streamID string, fault FaultConfig) error {
	reqBody, _ := json.Marshal(fault)

	req, err := http.NewRequestWithContext(ctx, "POST",
		ts.BaseURL+"/api/streams/"+streamID+"/faults",
		strings.NewReader(string(reqBody)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("注入故障失败: %d", resp.StatusCode)
	}

	return nil
}

// StopStream 停止测试流
func (ts *TestServer) StopStream(ctx context.Context, streamID string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		ts.BaseURL+"/api/streams/"+streamID, nil)
	if err != nil {
		return err
	}

	resp, err := ts.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// GetAvailableScenarios 获取可用的测试场景
func GetAvailableScenarios() []TestScenario {
	return []TestScenario{
		// 基础功能测试
		{
			Name:        "basic_flv_h264",
			Description: "基础FLV H.264流录制测试",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "avc",
				Duration: 30 * time.Second,
				Quality:  "1080p",
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    25 * time.Second,
			},
		},

		// HEVC测试
		{
			Name:        "basic_flv_hevc",
			Description: "FLV H.265 (HEVC) 流录制测试",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "hevc",
				Duration: 30 * time.Second,
				Quality:  "1080p",
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    25 * time.Second,
			},
		},

		// Annex B HEVC测试（非标准格式）
		{
			Name:        "annexb_hevc",
			Description: "非标准 Annex B 格式 HEVC 流录制测试",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "hevc-annexb",
				Duration: 30 * time.Second,
				Quality:  "1080p",
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    25 * time.Second,
				ComplianceWarnings: []ComplianceWarning{
					{
						Type:        "annexb_sequence_header",
						Description: "序列头使用了非标准的 Annex B 格式",
					},
				},
			},
		},

		// HLS测试
		{
			Name:        "basic_hls_h264",
			Description: "HLS H.264流录制测试",
			Stream: StreamConfig{
				Format:   "hls",
				Codec:    "avc",
				Duration: 30 * time.Second,
				Quality:  "1080p",
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    25 * time.Second,
			},
		},

		// 网络断连恢复测试
		{
			Name:        "network_disconnect_5s",
			Description: "测试下载器在网络断开5秒后的恢复能力",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "avc",
				Duration: 60 * time.Second,
				Quality:  "1080p",
			},
			Faults: []FaultConfig{
				{
					Type:     "disconnect",
					At:       10 * time.Second,
					Duration: 5 * time.Second,
				},
			},
			Expected: Expected{
				OutputPlayable:       true,
				MinDuration:          50 * time.Second,
				DownloaderReconnects: true,
			},
		},

		// 网络慢速测试
		{
			Name:        "slow_network",
			Description: "测试下载器在网络限速情况下的表现",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "avc",
				Duration: 30 * time.Second,
				Quality:  "720p", // 使用较低分辨率以适应限速
			},
			Faults: []FaultConfig{
				{
					Type:     "slowdown",
					At:       5 * time.Second,
					Duration: 20 * time.Second,
					Params: map[string]interface{}{
						"bandwidth_kbps": 1000, // 限速到1Mbps
					},
				},
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    25 * time.Second,
			},
		},

		// 分辨率变化测试
		{
			Name:        "resolution_change",
			Description: "测试下载器处理分辨率变化的能力",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "avc",
				Duration: 60 * time.Second,
				Quality:  "1080p",
			},
			Faults: []FaultConfig{
				{
					Type: "resolution_change",
					At:   20 * time.Second,
					Params: map[string]interface{}{
						"new_width":  1280,
						"new_height": 720,
					},
				},
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    55 * time.Second,
			},
		},

		// 时间戳跳跃测试
		{
			Name:        "timestamp_jump",
			Description: "测试下载器处理时间戳跳跃的能力",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "avc",
				Duration: 60 * time.Second,
				Quality:  "1080p",
			},
			Faults: []FaultConfig{
				{
					Type: "timestamp_jump",
					At:   20 * time.Second,
					Params: map[string]interface{}{
						"jump_ms": 10000, // 跳跃10秒
					},
				},
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    55 * time.Second,
			},
		},

		// 时间戳回退测试（更严重的问题）
		{
			Name:        "timestamp_reset",
			Description: "测试下载器处理时间戳归零的能力",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "avc",
				Duration: 60 * time.Second,
				Quality:  "1080p",
			},
			Faults: []FaultConfig{
				{
					Type: "timestamp_reset",
					At:   30 * time.Second,
				},
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    55 * time.Second,
			},
		},

		// 丢帧测试
		{
			Name:        "drop_frames",
			Description: "测试下载器在丢帧情况下的容错能力",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "avc",
				Duration: 30 * time.Second,
				Quality:  "1080p",
			},
			Faults: []FaultConfig{
				{
					Type:     "drop_frame",
					At:       10 * time.Second,
					Duration: 5 * time.Second,
					Params: map[string]interface{}{
						"drop_rate": 0.3, // 30%丢帧率
					},
				},
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    25 * time.Second,
			},
		},

		// 多流选择测试
		{
			Name:        "multi_stream_selection",
			Description: "测试多流选择功能",
			Stream: StreamConfig{
				Format:   "flv",
				Codec:    "avc",
				Duration: 20 * time.Second,
				Quality:  "1080p",
			},
			Expected: Expected{
				OutputPlayable: true,
				MinDuration:    15 * time.Second,
			},
		},
	}
}

// SaveScenarios 保存场景到文件
func SaveScenarios(dir string, scenarios []TestScenario) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	for _, scenario := range scenarios {
		filename := filepath.Join(dir, scenario.Name+".json")
		data, err := json.MarshalIndent(scenario, "", "  ")
		if err != nil {
			return err
		}

		if err := os.WriteFile(filename, data, 0644); err != nil {
			return err
		}
	}

	return nil
}
