package recorders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/diagnostics"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/types"
)

var errRecorderOwnerChanged = errors.New("recorder listener owner changed")

// listenerLifecycle 是 Recorder Manager 对一个房间最后已线性化事件的游标。
// stopped 是同 generation 的永久 tombstone：即使旧 listener 在 Close 后又
// 产出了更大 Sequence 的 LiveStart，也不允许它重新创建 recorder。
type listenerLifecycle struct {
	producerID   string
	generation   uint64
	lastSequence uint64
	stopped      bool
}

// recorderOwner 记录当前 recorder 是由哪个 listener generation 创建或接管的。
// 旧 LiveEnd/ListenStop 只有 owner 完全匹配时才有权删除它。
type recorderOwner struct {
	scopeKey   string
	producerID string
	generation uint64
}

func recorderOwnerFromCausality(causality events.Causality) recorderOwner {
	return recorderOwner{
		scopeKey:   causality.ScopeKey,
		producerID: causality.ProducerID,
		generation: causality.Generation,
	}
}

func (o recorderOwner) matches(causality events.Causality) bool {
	return o.scopeKey == causality.ScopeKey &&
		o.producerID == causality.ProducerID &&
		o.generation == causality.Generation
}

type listenerEventResult struct {
	accepted          bool
	reason            string
	action            string
	currentGeneration uint64
	currentSequence   uint64
	retiredCount      int
	lockWait          time.Duration
	err               error
	restartOwner      *recorderOwner
}

func (m *manager) registryListener(ctx context.Context, dispatcher events.Dispatcher) {
	dispatcher.AddEventListener(
		listeners.ListenStart,
		events.NewNamedEventListener("recorder_manager.listen_start", func(event *events.Event) {
			m.handleListenerEvent(ctx, event)
		}),
	)
	dispatcher.AddEventListener(
		listeners.LiveStart,
		events.NewNamedEventListener("recorder_manager.live_start", func(event *events.Event) {
			m.handleListenerEvent(ctx, event)
		}),
	)
	dispatcher.AddEventListener(
		listeners.RoomNameChanged,
		events.NewNamedEventListener("recorder_manager.room_name_changed", func(event *events.Event) {
			m.handleListenerEvent(ctx, event)
		}),
	)
	dispatcher.AddEventListener(
		listeners.LiveEnd,
		events.NewNamedEventListener("recorder_manager.live_end", func(event *events.Event) {
			m.handleListenerEvent(ctx, event)
		}),
	)
	dispatcher.AddEventListener(
		listeners.ListenStop,
		events.NewNamedEventListener("recorder_manager.listen_stop", func(event *events.Event) {
			m.handleListenerEvent(ctx, event)
		}),
	)
}

func (m *manager) handleListenerEvent(fallback context.Context, event *events.Event) {
	// EventDispatcher 异步执行 handler。Close 关闭闸门后，迟到的 LiveStart
	// 必须在产生任何业务轨迹、读取配置或创建 recorder 之前静默退出。
	if !m.beginOperation() {
		return
	}
	defer m.endOperation()

	if event == nil {
		return
	}
	eventCtx := event.Context(fallback)
	liveObject, ok := event.Object.(live.Live)
	if !ok || liveObject == nil {
		diagnostics.Record(eventCtx, "recorder.listener_event.dropped", diagnostics.Fields{
			"component":   "recorder_manager",
			"lane":        "recorder",
			"severity":    "warn",
			"status":      "dropped",
			"event_type":  string(event.Type),
			"drop_reason": "invalid_payload",
		})
		return
	}

	causality, hasCausality := event.Causality()
	if !hasCausality {
		m.handleLegacyListenerEvent(eventCtx, event.Type, liveObject)
		return
	}

	roomScopeID := diagnostics.ScopeID(liveObject.GetRawUrl())
	if !causality.Valid() {
		m.recordListenerEventResult(eventCtx, event, roomScopeID, causality, listenerEventResult{
			reason: "invalid_causality",
		})
		return
	}

	notifyOnly := false
	if event.Type == listeners.LiveStart {
		notifyOnly = roomIsNotifyOnly(liveObject)
	}
	result := m.applyCausalListenerEvent(eventCtx, event.Type, liveObject, causality, notifyOnly)

	if result.restartOwner != nil {
		restartErr := m.restartRecorder(eventCtx, liveObject, result.restartOwner)
		switch {
		case errors.Is(restartErr, errRecorderOwnerChanged), errors.Is(restartErr, ErrRecorderNotExist):
			result.accepted = false
			result.reason = "owner_changed_before_restart"
			result.action = "drop_restart"
			result.err = nil
		case restartErr != nil:
			result.action = "restart_error"
			result.err = restartErr
		default:
			result.action = "restart_recorder"
		}
	}

	m.recordListenerEventResult(eventCtx, event, roomScopeID, causality, result)

	if result.err != nil {
		switch event.Type {
		case listeners.LiveStart:
			liveObject.GetLogger().Errorf("failed to add recorder, err: %v", result.err)
		case listeners.RoomNameChanged:
			liveObject.GetLogger().Errorf("failed to restart recorder, err: %v", result.err)
		case listeners.LiveEnd, listeners.ListenStop:
			liveObject.GetLogger().Errorf("failed to remove recorder, err: %v", result.err)
		}
	}
	if result.accepted && event.Type == listeners.LiveStart && result.action == "skip_notify_only" {
		liveObject.GetLogger().Info("Room is notify-only, skipping auto-recording")
	}
}

func roomIsNotifyOnly(liveObject live.Live) bool {
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return false
	}
	room, err := cfg.GetLiveRoomByUrl(liveObject.GetRawUrl())
	return err == nil && room.NotifyOnly
}

func (m *manager) applyCausalListenerEvent(
	ctx context.Context,
	eventType events.EventType,
	liveObject live.Live,
	causality events.Causality,
	notifyOnly bool,
) listenerEventResult {
	lockStarted := time.Now()
	m.lock.Lock()
	result := listenerEventResult{lockWait: time.Since(lockStarted)}
	defer m.lock.Unlock()
	m.ensureLifecycleMapsLocked()

	result = m.advanceListenerLifecycleLocked(eventType, causality, result)
	if !result.accepted {
		return result
	}
	if result.currentGeneration == causality.Generation {
		result.retiredCount = m.retireOlderRecordersLocked(ctx, causality)
	}

	liveID := liveObject.GetLiveId()
	switch eventType {
	case listeners.ListenStart:
		result.action = "generation_opened"
	case listeners.LiveStart:
		if notifyOnly {
			result.action = "skip_notify_only"
			return result
		}
		if _, exists := m.savers[liveID]; exists {
			owner, ownerKnown := m.recorderOwners[liveID]
			switch {
			case !ownerKnown:
				// 兼容 WebUI/旧代码直接创建的 recorder：当前 generation 接管后，
				// 后续旧 generation 便无法误删它。
				m.recorderOwners[liveID] = recorderOwnerFromCausality(causality)
				result.action = "adopt_existing_recorder"
			case owner.matches(causality):
				result.action = "recorder_already_owned"
			default:
				result.action = "protect_foreign_recorder"
				result.reason = "recorder_owner_conflict"
			}
			return result
		}
		if err := m.addRecorderLocked(ctx, liveObject); err != nil {
			result.action = "add_error"
			result.err = err
			return result
		}
		m.recorderOwners[liveID] = recorderOwnerFromCausality(causality)
		result.action = "add_recorder"
	case listeners.LiveEnd, listeners.ListenStop:
		if _, exists := m.savers[liveID]; !exists {
			result.action = "no_recorder"
			return result
		}
		owner, ownerKnown := m.recorderOwners[liveID]
		if ownerKnown && !owner.matches(causality) {
			result.action = "protect_newer_recorder"
			result.reason = "recorder_owner_mismatch"
			return result
		}
		if err := m.removeRecorderLocked(ctx, liveID); err != nil {
			result.action = "remove_error"
			result.err = err
			return result
		}
		if ownerKnown {
			result.action = "remove_owned_recorder"
		} else {
			result.action = "remove_unowned_recorder"
		}
	case listeners.RoomNameChanged:
		if _, exists := m.savers[liveID]; !exists {
			result.action = "no_recorder"
			return result
		}
		owner, ownerKnown := m.recorderOwners[liveID]
		if !ownerKnown {
			owner = recorderOwnerFromCausality(causality)
			m.recorderOwners[liveID] = owner
		} else if !owner.matches(causality) {
			result.action = "protect_newer_recorder"
			result.reason = "recorder_owner_mismatch"
			return result
		}
		result.action = "restart_pending"
		result.restartOwner = &owner
	default:
		result.accepted = false
		result.reason = "unsupported_event_type"
	}
	return result
}

func (m *manager) ensureLifecycleMapsLocked() {
	if m.listenerLifecycles == nil {
		m.listenerLifecycles = make(map[string]listenerLifecycle)
	}
	if m.recorderOwners == nil {
		m.recorderOwners = make(map[types.LiveID]recorderOwner)
	}
}

func (m *manager) advanceListenerLifecycleLocked(
	eventType events.EventType,
	causality events.Causality,
	result listenerEventResult,
) listenerEventResult {
	current, exists := m.listenerLifecycles[causality.ScopeKey]
	result.currentGeneration = current.generation
	result.currentSequence = current.lastSequence

	switch {
	case !exists || causality.Generation > current.generation:
		current = listenerLifecycle{
			producerID:   causality.ProducerID,
			generation:   causality.Generation,
			lastSequence: causality.Sequence,
			stopped:      eventType == listeners.ListenStop,
		}
	case causality.Generation < current.generation:
		result.reason = "stale_generation"
		return result
	case current.producerID != causality.ProducerID:
		result.reason = "producer_mismatch"
		return result
	case current.stopped:
		result.reason = "generation_stopped"
		return result
	case causality.Sequence <= current.lastSequence:
		result.reason = "stale_sequence"
		return result
	default:
		current.lastSequence = causality.Sequence
		if eventType == listeners.ListenStop {
			current.stopped = true
		}
	}

	m.listenerLifecycles[causality.ScopeKey] = current
	result.accepted = true
	result.reason = ""
	result.currentGeneration = current.generation
	result.currentSequence = current.lastSequence
	return result
}

func (m *manager) retireOlderRecordersLocked(
	ctx context.Context,
	causality events.Causality,
) int {
	var staleIDs []types.LiveID
	for liveID, owner := range m.recorderOwners {
		if owner.scopeKey == causality.ScopeKey && owner.generation < causality.Generation {
			staleIDs = append(staleIDs, liveID)
		}
	}
	retired := 0
	for _, liveID := range staleIDs {
		if _, exists := m.savers[liveID]; !exists {
			delete(m.recorderOwners, liveID)
			continue
		}
		if err := m.removeRecorderLocked(ctx, liveID); err == nil {
			retired++
		}
	}
	return retired
}

func (m *manager) recordListenerEventResult(
	ctx context.Context,
	event *events.Event,
	roomScopeID string,
	causality events.Causality,
	result listenerEventResult,
) {
	fields := diagnostics.Fields{
		"component":              "recorder_manager",
		"lane":                   "recorder",
		"room_scope_id":          roomScopeID,
		"event_id":               event.ID,
		"event_type":             string(event.Type),
		"listener_instance_id":   causality.ProducerID,
		"generation":             causality.Generation,
		"event_sequence":         causality.Sequence,
		"current_generation":     result.currentGeneration,
		"current_event_sequence": result.currentSequence,
		"action":                 result.action,
		"retired_recorder_count": result.retiredCount,
		"lock_wait_ms":           float64(result.lockWait) / float64(time.Millisecond),
	}
	if result.err != nil {
		fields["error_type"] = fmt.Sprintf("%T", result.err)
	}
	if !result.accepted {
		fields["severity"] = "warn"
		fields["status"] = "dropped"
		fields["decision"] = "drop_stale_event"
		fields["drop_reason"] = result.reason
		diagnostics.Record(ctx, "recorder.listener_event.dropped", fields)
		return
	}

	fields["status"] = "accepted"
	fields["decision"] = "apply_event"
	if result.reason != "" {
		fields["action_reason"] = result.reason
	}
	diagnostics.Record(ctx, "recorder.listener_event.accepted", fields)

	if event.Type == listeners.LiveStart {
		decisionFields := diagnostics.Fields{
			"component":      "recorder_manager",
			"lane":           "recorder",
			"room_scope_id":  roomScopeID,
			"generation":     causality.Generation,
			"event_sequence": causality.Sequence,
			"status":         "record",
			"decision":       "start_recording",
			"action":         result.action,
		}
		if result.action == "skip_notify_only" {
			decisionFields["status"] = "notify_only"
			decisionFields["decision"] = "skip_recording"
		}
		diagnostics.Record(ctx, "recorder.decision", decisionFields)
	}
}

func (m *manager) handleLegacyListenerEvent(
	ctx context.Context,
	eventType events.EventType,
	liveObject live.Live,
) {
	diagnostics.Record(ctx, "recorder.listener_event.legacy", diagnostics.Fields{
		"component":  "recorder_manager",
		"lane":       "recorder",
		"severity":   "warn",
		"status":     "accepted_without_order_guard",
		"event_type": string(eventType),
	})

	switch eventType {
	case listeners.ListenStart:
		return
	case listeners.LiveStart:
		if roomIsNotifyOnly(liveObject) {
			diagnostics.Record(ctx, "recorder.decision", diagnostics.Fields{
				"component":     "recorder_manager",
				"lane":          "recorder",
				"room_scope_id": diagnostics.ScopeID(liveObject.GetRawUrl()),
				"status":        "notify_only",
				"decision":      "skip_recording",
			})
			liveObject.GetLogger().Info("Room is notify-only, skipping auto-recording")
			return
		}
		diagnostics.Record(ctx, "recorder.decision", diagnostics.Fields{
			"component":     "recorder_manager",
			"lane":          "recorder",
			"room_scope_id": diagnostics.ScopeID(liveObject.GetRawUrl()),
			"status":        "record",
			"decision":      "start_recording",
		})
		if err := m.AddRecorder(ctx, liveObject); err != nil {
			liveObject.GetLogger().Errorf("failed to add recorder, err: %v", err)
		}
	case listeners.RoomNameChanged:
		if !m.HasRecorder(ctx, liveObject.GetLiveId()) {
			return
		}
		if err := m.RestartRecorder(ctx, liveObject); err != nil {
			liveObject.GetLogger().Errorf("failed to restart recorder, err: %v", err)
		}
	case listeners.LiveEnd, listeners.ListenStop:
		if !m.HasRecorder(ctx, liveObject.GetLiveId()) {
			return
		}
		if err := m.RemoveRecorder(ctx, liveObject.GetLiveId()); err != nil {
			liveObject.GetLogger().Errorf("failed to remove recorder, err: %v", err)
		}
	}
}
