package diagnostics

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"runtime/trace"
	"strconv"
	"strings"
	"sync"
	"time"
)

type contextFieldsKey struct{}

var (
	idPrefixPattern = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	urlPattern      = regexp.MustCompile(`(?i)(?:https?|wss?|rtmps?)://[^\s"'<>]+`)
)

var topLevelFieldNames = map[string]struct{}{
	"component": {}, "lane": {}, "severity": {}, "room_scope_id": {},
	"generation": {}, "flow_id": {}, "task_id": {}, "span_id": {},
	"parent_span_id": {}, "status": {}, "duration_ms": {}, "kind": {},
	"category": {},
}

// WithFields 将关联字段放入 context；后续 Record、StartSpan、NewTask 会继承它们。
func WithFields(ctx context.Context, fields Fields) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	merged := fieldsFromContext(ctx)
	mergeFields(merged, fields)
	return context.WithValue(ctx, contextFieldsKey{}, merged)
}

func fieldsFromContext(ctx context.Context) Fields {
	result := Fields{}
	if ctx == nil {
		return result
	}
	if fields, ok := ctx.Value(contextFieldsKey{}).(Fields); ok {
		mergeFields(result, fields)
	}
	return result
}

func mergeFields(target Fields, source Fields) {
	for key, value := range source {
		if key == "attrs" {
			targetAttrs, _ := target[key].(map[string]any)
			if targetAttrs == nil {
				targetAttrs = map[string]any{}
			}
			switch attrs := value.(type) {
			case Fields:
				for attrKey, attrValue := range attrs {
					targetAttrs[attrKey] = attrValue
				}
			case map[string]any:
				for attrKey, attrValue := range attrs {
					targetAttrs[attrKey] = attrValue
				}
			}
			target[key] = targetAttrs
			continue
		}
		target[key] = value
	}
}

func stringField(fields Fields, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func int64Field(fields Fields, key string) int64 {
	switch value := fields[key].(type) {
	case int:
		return int64(value)
	case int8:
		return int64(value)
	case int16:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case uint:
		return int64(value)
	case uint8:
		return int64(value)
	case uint16:
		return int64(value)
	case uint32:
		return int64(value)
	case uint64:
		if value <= uint64(^uint64(0)>>1) {
			return int64(value)
		}
	case float32:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		result, _ := value.Int64()
		return result
	case string:
		result, _ := strconv.ParseInt(value, 10, 64)
		return result
	}
	return 0
}

func float64Field(fields Fields, key string) float64 {
	switch value := fields[key].(type) {
	case time.Duration:
		return float64(value) / float64(time.Millisecond)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case uint64:
		return float64(value)
	case float32:
		return float64(value)
	case float64:
		return value
	case json.Number:
		result, _ := value.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(value, 64)
		return result
	}
	return 0
}

func buildAttrs(manager *Manager, fields Fields) map[string]any {
	attrs := map[string]any{}
	if explicit, ok := fields["attrs"].(map[string]any); ok {
		for key, value := range explicit {
			attrs[key] = sanitizeValue(manager, key, value, 0)
		}
	} else if explicit, ok := fields["attrs"].(Fields); ok {
		for key, value := range explicit {
			attrs[key] = sanitizeValue(manager, key, value, 0)
		}
	}
	for key, value := range fields {
		if key == "attrs" {
			continue
		}
		if _, topLevel := topLevelFieldNames[key]; topLevel {
			continue
		}
		attrs[key] = sanitizeValue(manager, key, value, 0)
	}
	return attrs
}

// Record 写一条结构化业务事件。未初始化或已停止时是安全 no-op。
func Record(ctx context.Context, name string, fields Fields) {
	if manager := Default(); manager != nil {
		manager.Record(ctx, name, fields)
	}
}

// Record 写一条结构化业务事件。
func (m *Manager) Record(ctx context.Context, name string, fields Fields) {
	if m == nil || m.stopping.Load() || m.closed.Load() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all := fieldsFromContext(ctx)
	mergeFields(all, fields)
	if name == "" {
		name = "unknown.event"
	}
	component := stringField(all, "component")
	if component == "" {
		component = "unknown"
	}
	lane := stringField(all, "lane")
	if lane == "" {
		lane = component
	}
	severity := strings.ToLower(stringField(all, "severity"))
	switch severity {
	case "trace", "debug", "info", "warn", "error":
	default:
		severity = "info"
	}
	kind := stringField(all, "kind")
	if kind == "" {
		kind = "instant"
	}
	category := stringField(all, "category")
	if category == "" {
		if component == "runtime" {
			category = "runtime"
		} else {
			category = "business"
		}
	}
	now := time.Now()
	event := &Event{
		ID:           m.NewID("evt"),
		TS:           float64(now.Sub(m.startedAt)) / float64(time.Millisecond),
		WallTime:     now.UTC(),
		Name:         name,
		Kind:         kind,
		Category:     category,
		Component:    component,
		Lane:         lane,
		Severity:     severity,
		RoomScopeID:  normalizedRoomScopeID(m, stringField(all, "room_scope_id")),
		Generation:   int64Field(all, "generation"),
		FlowID:       stringField(all, "flow_id"),
		TaskID:       stringField(all, "task_id"),
		SpanID:       stringField(all, "span_id"),
		ParentSpanID: stringField(all, "parent_span_id"),
		Status:       stringField(all, "status"),
		DurationMS:   float64Field(all, "duration_ms"),
		Attrs:        buildAttrs(m, all),
	}
	trace.Log(ctx, "bililive.diagnostics", name)
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	if m.stopping.Load() || m.writer == nil {
		return
	}
	if err := m.writer.Write(event); err != nil {
		m.setError(err)
		return
	}
	m.lastSeq.Store(event.Seq)
	m.dropped.Store(m.writer.dropped)
}

func normalizedRoomScopeID(manager *Manager, value string) string {
	if value == "" || strings.HasPrefix(value, "scope_") {
		return value
	}
	return scopeFor(manager, value)
}

// StartSpan 记录成对的 <name>.start / <name>.end 业务事件。end 可传 nil，且只生效一次。
//
// 这里刻意不创建 runtime/trace.Region：业务 span 的结束回调经常会在另一个
// goroutine 中触发，而 Region 要求开始、结束发生在同一 goroutine 且严格嵌套。
// 关联关系由 JSONL 中的 span_id 和 runtime/trace.Log 保留。
func StartSpan(ctx context.Context, name string, fields Fields) (context.Context, func(Fields)) {
	if manager := Default(); manager != nil {
		return manager.StartSpan(ctx, name, fields)
	}
	return startSpan(nil, ctx, name, fields)
}

func (m *Manager) StartSpan(ctx context.Context, name string, fields Fields) (context.Context, func(Fields)) {
	return startSpan(m, ctx, name, fields)
}

func startSpan(m *Manager, ctx context.Context, name string, fields Fields) (context.Context, func(Fields)) {
	if ctx == nil {
		ctx = context.Background()
	}
	baseName := strings.TrimSuffix(strings.TrimSuffix(name, ".start"), ".end")
	if baseName == "" {
		baseName = "unknown.span"
	}
	startName := baseName + ".start"
	endName := baseName + ".end"
	spanID := newIDFor(m, "span")
	parentID := SpanID(ctx)
	spanFields := fieldsFromContext(ctx)
	mergeFields(spanFields, fields)
	spanFields["span_id"] = spanID
	spanFields["parent_span_id"] = parentID
	spanFields["kind"] = "span.start"
	spanCtx := WithFields(ctx, spanFields)
	if m != nil {
		m.Record(spanCtx, startName, nil)
	}
	started := time.Now()
	var once sync.Once
	end := func(endFields Fields) {
		once.Do(func() {
			fields := Fields{}
			mergeFields(fields, spanFields)
			mergeFields(fields, endFields)
			fields["span_id"] = spanID
			fields["parent_span_id"] = parentID
			fields["kind"] = "span.end"
			fields["duration_ms"] = float64(time.Since(started)) / float64(time.Millisecond)
			if stringField(fields, "status") == "" {
				fields["status"] = "ok"
			}
			if m != nil {
				m.Record(spanCtx, endName, fields)
			}
		})
	}
	return spanCtx, end
}

// NewTask 创建业务任务和 runtime/trace Task，记录 task.start / task.end。
func NewTask(ctx context.Context, taskType string, fields Fields) (context.Context, func(Fields)) {
	if manager := Default(); manager != nil {
		return manager.NewTask(ctx, taskType, fields)
	}
	return newTask(nil, ctx, taskType, fields)
}

func (m *Manager) NewTask(ctx context.Context, taskType string, fields Fields) (context.Context, func(Fields)) {
	return newTask(m, ctx, taskType, fields)
}

func newTask(m *Manager, ctx context.Context, taskType string, fields Fields) (context.Context, func(Fields)) {
	if ctx == nil {
		ctx = context.Background()
	}
	if taskType == "" {
		taskType = "unknown"
	}
	traceCtx, runtimeTask := trace.NewTask(ctx, taskType)
	taskFields := fieldsFromContext(traceCtx)
	mergeFields(taskFields, fields)
	taskFields["task_id"] = newIDFor(m, "task")
	if stringField(taskFields, "flow_id") == "" {
		taskFields["flow_id"] = newIDFor(m, "flow")
	}
	taskFields["kind"] = "span.start"
	taskFields["task_type"] = taskType
	taskCtx := WithFields(traceCtx, taskFields)
	if m != nil {
		m.Record(taskCtx, "task.start", nil)
	}
	started := time.Now()
	var once sync.Once
	end := func(endFields Fields) {
		once.Do(func() {
			defer runtimeTask.End()
			fields := Fields{}
			mergeFields(fields, taskFields)
			mergeFields(fields, endFields)
			fields["kind"] = "span.end"
			fields["duration_ms"] = float64(time.Since(started)) / float64(time.Millisecond)
			if stringField(fields, "status") == "" {
				fields["status"] = "ok"
			}
			if m != nil {
				m.Record(taskCtx, "task.end", fields)
			}
		})
	}
	return taskCtx, end
}

// TaskID、SpanID、FlowID 返回 context 中当前关联 ID。
func TaskID(ctx context.Context) string { return stringField(fieldsFromContext(ctx), "task_id") }
func SpanID(ctx context.Context) string { return stringField(fieldsFromContext(ctx), "span_id") }
func FlowID(ctx context.Context) string { return stringField(fieldsFromContext(ctx), "flow_id") }

// NewID 生成只在日志中使用、不含业务原始值的 ID。
func NewID(prefix string) string {
	return newIDFor(Default(), prefix)
}

func (m *Manager) NewID(prefix string) string {
	return newIDFor(m, prefix)
}

func newIDFor(m *Manager, prefix string) string {
	prefix = strings.Trim(idPrefixPattern.ReplaceAllString(prefix, "_"), "_.-")
	if prefix == "" {
		prefix = "id"
	}
	if m != nil {
		seq := m.idSeq.Add(1)
		runSuffix := m.runID
		if len(runSuffix) > 12 {
			runSuffix = runSuffix[len(runSuffix)-12:]
		}
		return fmt.Sprintf("%s_%s_%012d", prefix, runSuffix, seq)
	}
	random, err := randomHex(12)
	if err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + random
}

// ScopeID 用本次运行的随机 HMAC key 为原始房间号或 URL 生成不可逆关联 ID。
// 同一 run 内相同输入稳定，不同 run 不可关联。
func ScopeID(raw string) string {
	return scopeIDWithKey(globalScopeKey(), raw)
}

func (m *Manager) ScopeID(raw string) string {
	if m == nil {
		return scopeIDWithKey(globalScopeKey(), raw)
	}
	return scopeIDWithKey(m.scopeKey, raw)
}

func scopeIDWithKey(key []byte, raw string) string {
	if raw == "" {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(raw))
	sum := mac.Sum(nil)
	return "scope_" + base64.RawURLEncoding.EncodeToString(sum[:18])
}

func sanitizeMap(manager *Manager, input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = sanitizeValue(manager, key, value, 0)
	}
	return result
}

func sanitizeValue(manager *Manager, key string, value any, depth int) any {
	if value == nil {
		return nil
	}
	if depth > 8 {
		return "[过深字段已省略]"
	}
	lowerKey := strings.ToLower(key)
	if isSecretKey(lowerKey) {
		return "[redacted]"
	}
	switch typed := value.(type) {
	case string:
		return sanitizeString(manager, lowerKey, typed)
	case []byte:
		return fmt.Sprintf("[bytes:%d]", len(typed))
	case error:
		return sanitizeString(manager, lowerKey, typed.Error())
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return float64(typed) / float64(time.Millisecond)
	case Fields:
		return sanitizeMap(manager, map[string]any(typed))
	case map[string]any:
		return sanitizeMapDepth(manager, typed, depth+1)
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, sanitizeValue(manager, key, item, depth+1))
		}
		return result
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return typed
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		result := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result = append(result, sanitizeValue(manager, key, rv.Index(i).Interface(), depth+1))
		}
		return result
	case reflect.Map:
		result := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			mapKey := fmt.Sprint(iter.Key().Interface())
			result[mapKey] = sanitizeValue(manager, mapKey, iter.Value().Interface(), depth+1)
		}
		return result
	}
	return sanitizeString(manager, lowerKey, fmt.Sprint(value))
}

func sanitizeMapDepth(manager *Manager, input map[string]any, depth int) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = sanitizeValue(manager, key, value, depth)
	}
	return result
}

func isSecretKey(key string) bool {
	for _, fragment := range []string{"cookie", "authorization", "password", "passwd", "token", "secret"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func sanitizeString(manager *Manager, key, value string) string {
	lowerKey := strings.ToLower(key)
	if isLiveIDKey(lowerKey) {
		if value == "" || strings.HasPrefix(value, "scope_") {
			return value
		}
		return scopeFor(manager, value)
	}
	if strings.Contains(lowerKey, "url") || strings.Contains(lowerKey, "uri") {
		if strings.Contains(lowerKey, "scope") || strings.Contains(lowerKey, "fingerprint") {
			return value
		}
		return scopeFor(manager, value)
	}
	return urlPattern.ReplaceAllStringFunc(value, func(rawURL string) string {
		return scopeFor(manager, rawURL)
	})
}

func isLiveIDKey(key string) bool {
	if strings.Contains(key, "scope") || strings.Contains(key, "fingerprint") {
		return false
	}
	return key == "live_id" ||
		strings.HasSuffix(key, "_live_id") ||
		strings.HasPrefix(key, "live_id_")
}

func scopeFor(manager *Manager, raw string) string {
	if manager != nil {
		return manager.ScopeID(raw)
	}
	return scopeIDWithKey(globalScopeKey(), raw)
}
