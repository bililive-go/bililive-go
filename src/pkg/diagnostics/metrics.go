package diagnostics

import "sort"

type viewerMetric struct {
	Name      string       `json:"name"`
	Label     string       `json:"label,omitempty"`
	Unit      string       `json:"unit"`
	Component string       `json:"component,omitempty"`
	Points    [][2]float64 `json:"points"`
}

type metricDefinition struct {
	label     string
	unit      string
	component string
}

var runtimeMetricDefinitions = map[string]metricDefinition{
	"goroutines":          {label: "Goroutine 数", unit: "goroutines", component: "runtime"},
	"gomaxprocs":          {label: "GOMAXPROCS", unit: "processors", component: "runtime"},
	"heap_alloc_bytes":    {label: "堆已分配", unit: "bytes", component: "runtime"},
	"heap_inuse_bytes":    {label: "堆使用中", unit: "bytes", component: "runtime"},
	"heap_objects":        {label: "堆对象数", unit: "objects", component: "runtime"},
	"gc_cycles_total":     {label: "GC 次数", unit: "cycles", component: "runtime"},
	"gc_pause_total_ms":   {label: "GC 累计暂停", unit: "ms", component: "runtime"},
	"gc_last_pause_ms":    {label: "最近一次 GC 暂停", unit: "ms", component: "runtime"},
	"process_sys_bytes":   {label: "Runtime 向系统申请", unit: "bytes", component: "runtime"},
	"process_total_alloc": {label: "进程累计分配", unit: "bytes", component: "runtime"},
}

// deriveViewerMetrics 从已冻结的结构化事件生成适合手机 WebUI 的低成本曲线。
// 这些曲线是观察值，不冒充 Flight Recorder 的 goroutine 调度片段。
func deriveViewerMetrics(events []Event) []any {
	metrics := map[string]*viewerMetric{}
	add := func(name string, definition metricDefinition, ts, value float64) {
		metric := metrics[name]
		if metric == nil {
			metric = &viewerMetric{
				Name:      name,
				Label:     definition.label,
				Unit:      definition.unit,
				Component: definition.component,
				Points:    make([][2]float64, 0),
			}
			metrics[name] = metric
		}
		metric.Points = append(metric.Points, [2]float64{ts, value})
	}

	counters := map[string]float64{}
	counterDefinitions := map[string]metricDefinition{
		"platform.rate_limiter.waiting_rooms": {
			label: "平台限流等待者", unit: "goroutines", component: "ratelimit",
		},
		"platform.rate_limiter.in_flight_waiting_rooms": {
			label: "平台单在途槽位等待者", unit: "goroutines", component: "ratelimit",
		},
		"scheduler.poll.in_flight": {
			label: "正在执行的直播检测", unit: "polls", component: "listener",
		},
		"events.dispatch.in_flight": {
			label: "正在派发的事件", unit: "dispatches", component: "events",
		},
		"recorder.sessions.active": {
			label: "活跃 Recorder 会话", unit: "recorders", component: "recorder",
		},
	}
	updateCounter := func(name string, delta float64, event Event) {
		value := counters[name] + delta
		if value < 0 {
			// 事件分段轮换后可能只剩 end；不能把未知的历史起点画成负数。
			value = 0
		}
		counters[name] = value
		add(name, counterDefinitions[name], event.TS, value)
	}

	for _, event := range events {
		switch event.Name {
		case "scheduler.rate_limit.in_flight.wait.start":
			// 这是等待“同平台最多一个请求在途”的队列，与取得槽位后才会
			// 进入的 token/min-interval 等待不同，必须分开计数，不能把同一
			// 房间的两个串行阶段误画成两个同时等待者。
			updateCounter("platform.rate_limiter.in_flight_waiting_rooms", 1, event)
		case "scheduler.rate_limit.in_flight.wait.end":
			updateCounter("platform.rate_limiter.in_flight_waiting_rooms", -1, event)
		case "scheduler.rate_limit.wait.start":
			updateCounter("platform.rate_limiter.waiting_rooms", 1, event)
		case "scheduler.rate_limit.wait.end":
			updateCounter("platform.rate_limiter.waiting_rooms", -1, event)
		case "listener.poll.start":
			updateCounter("scheduler.poll.in_flight", 1, event)
		case "listener.poll.end":
			updateCounter("scheduler.poll.in_flight", -1, event)
		case "event.dispatch.run.start":
			updateCounter("events.dispatch.in_flight", 1, event)
		case "event.dispatch.run.end":
			updateCounter("events.dispatch.in_flight", -1, event)
		case "recorder.session.start":
			updateCounter("recorder.sessions.active", 1, event)
		case "recorder.session.end":
			updateCounter("recorder.sessions.active", -1, event)
		case "scheduler.rate_limit.granted":
			if value, ok := numericEventAttr(event, "grant_seq"); ok {
				add("platform.rate_limiter.grants_total", metricDefinition{
					label: "平台限流累计放行", unit: "grants", component: "ratelimit",
				}, event.TS, value)
			}
		case "runtime.sample":
			for attr, definition := range runtimeMetricDefinitions {
				if value, ok := numericEventAttr(event, attr); ok {
					add("runtime."+attr, definition, event.TS, value)
				}
			}
		}
	}

	names := make([]string, 0, len(metrics))
	for name, metric := range metrics {
		if len(metric.Points) > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := make([]any, 0, len(names))
	for _, name := range names {
		result = append(result, metrics[name])
	}
	return result
}

func numericEventAttr(event Event, key string) (float64, bool) {
	value, exists := event.Attrs[key]
	if !exists {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}
