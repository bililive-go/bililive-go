package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
)

func TestGetSoopLiveAuthConfigDoesNotExposeSavedPassword(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "secret"
	configs.SetCurrentConfig(cfg)

	recorder := httptest.NewRecorder()
	getSoopLiveAuthConfig(recorder, nil)

	assert.Equal(t, 200, recorder.Code)

	var resp commonResp
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	assert.NoError(t, err)

	data, ok := resp.Data.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "tester", data["username"])
	assert.Equal(t, true, data["has_saved_credentials"])
	_, exists := data["password"]
	assert.False(t, exists)
}

func TestGetPlatformStatsReportsDisabledRateLimit(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.Interval = 1
	cfg.PlatformConfigs[configs.PlatformKeyDouyin] = configs.PlatformConfig{
		Name:                 "抖音",
		MinAccessIntervalSec: 60,
	}
	cfg.LiveRooms = []configs.LiveRoom{{
		Url:         "https://live.douyin.com/123456",
		IsListening: true,
	}}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(configs.NewConfig()) })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/config/platforms", nil)
	request = request.WithContext(context.WithValue(request.Context(), instance.Key, &instance.Instance{}))
	getPlatformStats(recorder, request)

	assert.Equal(t, 200, recorder.Code)
	var response struct {
		PlatformRateLimitEnabled bool `json:"platform_rate_limit_enabled"`
		Platforms                []struct {
			PlatformKey    string `json:"platform_key"`
			WarningMessage string `json:"warning_message"`
		} `json:"platforms"`
	}
	assert.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.PlatformRateLimitEnabled)
	if assert.Len(t, response.Platforms, 1) {
		assert.Equal(t, configs.PlatformKeyDouyin, response.Platforms[0].PlatformKey)
		assert.Empty(t, response.Platforms[0].WarningMessage)
	}
}

func TestPreviewOutputTmplReportsPathTooLong(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	previousValidator := validatePreviewOutputFilePath
	configs.SetCurrentConfig(configs.NewConfig())
	validatePreviewOutputFilePath = func(string) error {
		return errors.New("输出文件完整路径过长")
	}
	t.Cleanup(func() {
		configs.SetCurrentConfig(previousConfig)
		validatePreviewOutputFilePath = previousValidator
	})

	body := bytes.NewBufferString(`{"template":"recording.flv","out_put_path":"."}`)
	request := httptest.NewRequest("POST", "/api/config/preview-template", body)
	recorder := httptest.NewRecorder()

	previewOutputTmpl(recorder, request)

	assert.Equal(t, 200, recorder.Code)
	var response struct {
		Success     bool   `json:"success"`
		Error       string `json:"error"`
		ErrorType   string `json:"error_type"`
		PreviewPath string `json:"preview_path"`
	}
	assert.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "输出文件完整路径过长", response.Error)
	assert.Equal(t, "path_too_long", response.ErrorType)
	assert.NotEmpty(t, response.PreviewPath)
}
