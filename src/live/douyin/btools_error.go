package douyin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const maxBToolsErrorBody = 16 * 1024

// btoolsRequestError 只保留可验证的诊断字段，不能把上游的 Cookie、签名 URL
// 或 HTML 错误页带入日志、LastError 和前端。旧版工具仍可通过已知错误特征分类。
type btoolsRequestError struct {
	StatusCode     int
	Code           string
	Kind           string
	API            string
	UpstreamStatus int
	RetryAt        time.Time
}

func (e *btoolsRequestError) Error() string {
	message := fmt.Sprintf("请求失败: %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	if e.Code != "" {
		message += "; code=" + e.Code
	}
	if e.Kind != "" {
		message += "; kind=" + e.Kind
	}
	if e.API != "" {
		message += "; api=" + e.API
	}
	if e.UpstreamStatus != 0 {
		message += fmt.Sprintf("; upstream_status=%d", e.UpstreamStatus)
	}
	if !e.RetryAt.IsZero() {
		message += "; retry_at=" + e.RetryAt.UTC().Format(time.RFC3339)
	}
	return message
}

var btoolsLegacyHTTPStatus = regexp.MustCompile(`Request failed with status code ([45][0-9]{2})(?:\b|$)`)

func readBToolsError(resp *http.Response) error {
	result := &btoolsRequestError{StatusCode: resp.StatusCode}
	// 小响应读到 EOF 后仍可复用连接；超大或持续输出的错误页不再无界排空。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBToolsErrorBody+1))
	if err != nil || len(body) > maxBToolsErrorBody {
		return result
	}
	var payload struct {
		Error          string `json:"error"`
		Code           string `json:"code"`
		Kind           string `json:"kind"`
		API            string `json:"api"`
		UpstreamStatus int    `json:"upstreamStatus"`
		RetryAt        string `json:"retryAt"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return result
	}
	// code/kind/api 均使用白名单，未经处理的 message 不参与输出。
	switch payload.Code {
	case "BGO_BALANCE_BUSY", "BGO_BALANCE_COOLDOWN", "BGO_BALANCE_UPSTREAM", "BGO_UPSTREAM", "BGO_REQUEST_TIMEOUT", "BGO_REQUEST_CANCELED":
		result.Code = payload.Code
	}
	switch payload.Kind {
	case "http", "timeout", "parse", "network", "unknown":
		result.Kind = payload.Kind
	}
	switch payload.API {
	case "web", "webHTML", "mobile", "userHTML":
		result.API = payload.API
	}
	if payload.UpstreamStatus >= 400 && payload.UpstreamStatus <= 599 {
		result.UpstreamStatus = payload.UpstreamStatus
	}
	if retryAt, err := time.Parse(time.RFC3339, payload.RetryAt); err == nil {
		result.RetryAt = retryAt
	}
	if result.Code == "" {
		switch {
		case strings.Contains(payload.Error, "BGO_BALANCE_COOLDOWN"), strings.Contains(payload.Error, "所有 API 端点都不可用"):
			result.Code = "BGO_BALANCE_COOLDOWN"
		case strings.Contains(payload.Error, "BGO_BALANCE_BUSY"):
			result.Code = "BGO_BALANCE_BUSY"
		case strings.Contains(payload.Error, "No match found in HTML"), strings.Contains(payload.Error, "No room match found in HTML"):
			result.Code = "BGO_UPSTREAM"
			result.Kind = "parse"
		case btoolsLegacyHTTPStatus.MatchString(payload.Error):
			result.Code = "BGO_UPSTREAM"
			result.Kind = "http"
			match := btoolsLegacyHTTPStatus.FindStringSubmatch(payload.Error)[1]
			// 正则限定为三位十进制数字，无需接受任意上游内容。
			result.UpstreamStatus = int(match[0]-'0')*100 + int(match[1]-'0')*10 + int(match[2]-'0')
		case strings.Contains(payload.Error, "BGO_BALANCE_UPSTREAM"):
			result.Code = "BGO_BALANCE_UPSTREAM"
		}
	}
	return result
}
