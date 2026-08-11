package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

var runIDPattern = regexp.MustCompile(`^run-[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

type acknowledgeMarker struct {
	Schema         string    `json:"schema"`
	RunID          string    `json:"run_id"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

func validateRunID(runID string) error {
	if !runIDPattern.MatchString(runID) {
		return ErrInvalidRunID
	}
	return nil
}

func (m *Manager) runPath(runID string) (string, error) {
	if err := validateRunID(runID); err != nil {
		return "", err
	}
	path := filepath.Join(m.runsDir, runID)
	rel, err := filepath.Rel(m.runsDir, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return "", ErrInvalidRunID
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrRunNotFound
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return "", ErrRunNotFound
	}
	return path, nil
}

// ListRuns 按开始时间从新到旧返回所有仍保留的 run。
func (m *Manager) ListRuns() ([]RunInfo, error) {
	if m == nil {
		return nil, ErrNotInitialized
	}
	return scanRunInfos(m.runsDir, activeRunID(m))
}

func activeRunID(m *Manager) string {
	if m != nil && !m.closed.Load() {
		return m.runID
	}
	return ""
}

func scanRunInfos(runsDir, activeID string) ([]RunInfo, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []RunInfo{}, nil
		}
		return nil, err
	}
	runs := make([]RunInfo, 0, len(entries))
	now := time.Now().UTC()
	for _, entry := range entries {
		if !entry.IsDir() || validateRunID(entry.Name()) != nil {
			continue
		}
		path := filepath.Join(runsDir, entry.Name())
		runs = append(runs, inspectRunInfo(path, entry, activeID, now))
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].RunID > runs[j].RunID
		}
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})
	return runs, nil
}

func inspectRunInfo(path string, entry fs.DirEntry, activeID string, now time.Time) RunInfo {
	runID := entry.Name()
	dirModifiedAt := time.Time{}
	if entryInfo, err := entry.Info(); err == nil {
		dirModifiedAt = entryInfo.ModTime().UTC()
	}

	manifest, manifestErr := readManifest(filepath.Join(path, "run.json"))
	if manifestErr == nil &&
		(manifest.Schema != RunSchema || manifest.RunID != runID) {
		manifestErr = errors.New("run manifest 与目录不匹配")
	}
	if manifestErr != nil {
		manifest.RunID = runID
		manifest.StartedAt = dirModifiedAt
	}
	heartbeat, heartbeatOK := readHeartbeat(filepath.Join(path, "heartbeat.json"))
	if heartbeatOK && heartbeat.RunID != runID {
		heartbeatOK = false
	}
	lease, leaseOK := readRunLease(filepath.Join(path, runLeaseFileName))
	if leaseOK && lease.RunID != runID {
		leaseOK = false
	}
	hasPanic := fileExists(filepath.Join(path, "panic.json")) ||
		fileExists(filepath.Join(path, "panic.stack"))
	cleanMarker, cleanMarkerOK := readCleanMarker(filepath.Join(path, "clean.json"))
	clean := cleanMarkerOK && cleanMarker.RunID == runID && !hasPanic
	current := runID == activeID
	active, activeReason, ownerPID := detectRunActivity(
		path,
		manifest,
		manifestErr == nil,
		heartbeat,
		heartbeatOK,
		lease,
		leaseOK,
		current,
		dirModifiedAt,
		now,
	)
	info := RunInfo{
		RunID:                   runID,
		Path:                    path,
		StartedAt:               manifest.StartedAt,
		EndedAt:                 manifest.EndedAt,
		Current:                 current,
		Active:                  active,
		ActiveReason:            activeReason,
		OwnerPID:                ownerPID,
		Clean:                   clean,
		Acknowledged:            validAcknowledgeMarker(filepath.Join(path, "ack.json"), runID),
		HasPanic:                hasPanic,
		EventSegments:           countEventSegments(filepath.Join(path, "events")),
		FlightRecorderAvailable: hasFlightSnapshot(filepath.Join(path, "flight")),
		SizeBytes:               directorySize(path),
	}
	info.EventCount = readEventIndex(filepath.Join(path, "events")).LatestSeq
	if heartbeatOK {
		at := heartbeat.At
		info.LastHeartbeat = &at
	}
	if leaseOK {
		renewedAt := lease.RenewedAt
		expiresAt := lease.ExpiresAt
		info.LeaseRenewedAt = &renewedAt
		info.LeaseExpiresAt = &expiresAt
	}
	switch {
	case info.HasPanic:
		// panic 证据永远优先于 clean/current；Active 仍单独保留，以便
		// ACK 和 prune 知道 owner 是否还在收尾。
		info.Status = "panic"
	case info.Active:
		info.Status = "active"
	case info.Clean:
		info.Status = "clean"
	default:
		info.Status = "unclean"
	}
	return info
}

func detectRunActivity(
	path string,
	manifest RunManifest,
	manifestOK bool,
	heartbeat heartbeatFile,
	heartbeatOK bool,
	lease runLease,
	leaseOK bool,
	current bool,
	dirModifiedAt time.Time,
	now time.Time,
) (active bool, reason string, ownerPID int) {
	if leaseOK {
		ownerPID = lease.OwnerPID
	} else {
		ownerPID = manifest.PID
	}
	if current {
		return true, "current", ownerPID
	}

	lockPath := filepath.Join(path, runLeaseLockFileName)
	lockExists := fileExists(lockPath)
	lockProbe := leaseLockProbe{}
	if lockExists {
		lockProbe = probeLeaseFileLock(lockPath)
		if lockProbe.Held {
			return true, "lease_lock_held", ownerPID
		}
		// 新版 owner 明确写下自己已取得锁，而现在又能取得该锁，表示
		// owner 已退出；近期 heartbeat 或 PID 复用都不能推翻这个证据。
		if leaseOK && lease.LockAcquired && lockProbe.Known {
			return false, "", ownerPID
		}
	}

	if leaseOK {
		if !leaseStateIsActive(lease.State) {
			return false, "", ownerPID
		}
		observation, observeErr := observeProcess(ownerPID)
		if observeErr == nil {
			switch {
			case !observation.Exists:
				return false, "", ownerPID
			case lease.OwnerStartedAtMS > 0 &&
				observation.StartedAtMS > 0 &&
				lease.OwnerStartedAtMS != observation.StartedAtMS:
				return false, "", ownerPID
			default:
				return true, "owner_identity_alive", ownerPID
			}
		}
		// 进程状态查询可能被权限策略拒绝。新鲜租约能说明最近仍在
		// 续租；即使租约过期也不能证明被暂停的 owner 已退出。宁可
		// 要求人工处理，也不能让 ACK/prune 删除可能恢复写入的 run。
		if lease.ExpiresAt.After(now) {
			return true, "lease_recent_unverified", ownerPID
		}
		return true, "owner_unverified", ownerPID
	}

	// 兼容升级期间由旧版本进程创建、尚无 lease.json 的 run。近期
	// heartbeat 加上仍存在的 owner PID 才足以认为 active。
	if manifestOK && (manifest.EndedAt != nil ||
		manifest.State == "closed" ||
		manifest.State == "aborted" ||
		manifest.State == "panicked") {
		return false, "", ownerPID
	}
	if heartbeatOK && leaseStateIsActive(heartbeat.State) &&
		!heartbeat.At.IsZero() &&
		now.Sub(heartbeat.At) <= legacyHeartbeatFreshFor {
		observation, observeErr := observeProcess(ownerPID)
		if observeErr != nil {
			return true, "legacy_recent_heartbeat", ownerPID
		}
		if observation.Exists {
			return true, "legacy_owner_alive", ownerPID
		}
		return false, "", ownerPID
	}

	// mkdir 与首次 lease/manifest 发布之间存在极短窗口。只保护刚出现的
	// starting/running 或不完整目录，避免另一个 Init 的 prune 删除它。
	referenceAt := dirModifiedAt
	if manifest.StartedAt.After(referenceAt) {
		referenceAt = manifest.StartedAt
	}
	if !referenceAt.IsZero() && now.Sub(referenceAt) <= initializingRunGrace &&
		(!manifestOK ||
			(!heartbeatOK && (manifest.State == "starting" || manifest.State == "running"))) {
		return true, "initializing", ownerPID
	}
	return false, "", ownerPID
}

func readHeartbeat(path string) (heartbeatFile, bool) {
	var heartbeat heartbeatFile
	data, err := readRegularFileNoSymlink(path)
	if err != nil || json.Unmarshal(data, &heartbeat) != nil ||
		heartbeat.Schema != "bililive.diagnostics-heartbeat/v1" ||
		heartbeat.RunID == "" {
		return heartbeat, false
	}
	return heartbeat, true
}

func fileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil &&
		info.Mode()&fs.ModeSymlink == 0 &&
		info.Mode().IsRegular()
}

func readAcknowledgeMarker(path string) (acknowledgeMarker, bool) {
	var marker acknowledgeMarker
	data, err := readRegularFileNoSymlink(path)
	if err != nil || json.Unmarshal(data, &marker) != nil ||
		marker.Schema != "bililive.diagnostics-ack/v1" ||
		marker.RunID == "" ||
		marker.AcknowledgedAt.IsZero() {
		return marker, false
	}
	return marker, true
}

func validAcknowledgeMarker(path, runID string) bool {
	marker, ok := readAcknowledgeMarker(path)
	return ok && marker.RunID == runID
}

func directorySize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, infoErr := entry.Info(); infoErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func pruneEligibleRuns(runs []RunInfo, maxRuns int) []RunInfo {
	if maxRuns <= 0 || len(runs) < maxRuns {
		return runs
	}
	kept := append([]RunInfo(nil), runs...)
	for len(kept) >= maxRuns {
		index := -1
		for i := len(kept) - 1; i >= 0; i-- {
			if !kept[i].Active && (kept[i].Clean || kept[i].Acknowledged) {
				index = i
				break
			}
		}
		if index < 0 {
			break
		}
		_ = os.RemoveAll(kept[index].Path)
		kept = append(kept[:index], kept[index+1:]...)
	}
	return kept
}

// Acknowledge 以单独 ack.json 确认异常 run；不会改写原 run.json、事件或 panic 证据。
// 重复确认是幂等的。
func (m *Manager) Acknowledge(runID string) error {
	if m == nil {
		return ErrNotInitialized
	}
	path, err := m.runPath(runID)
	if err != nil {
		return err
	}
	runs, err := scanRunInfos(m.runsDir, activeRunID(m))
	if err != nil {
		return err
	}
	for _, info := range runs {
		if info.RunID == runID && info.Active {
			return ErrRunActive
		}
	}
	ackPath := filepath.Join(path, "ack.json")
	if validAcknowledgeMarker(ackPath, runID) {
		m.markStartupAcknowledged(runID)
		return nil
	}
	if _, statErr := os.Lstat(ackPath); statErr == nil {
		return fmt.Errorf("既有 ack marker 无效，拒绝覆盖调查证据")
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}
	err = atomicWriteJSON(ackPath, acknowledgeMarker{
		Schema:         "bililive.diagnostics-ack/v1",
		RunID:          runID,
		AcknowledgedAt: time.Now().UTC(),
	}, false)
	if errors.Is(err, fs.ErrExist) {
		m.markStartupAcknowledged(runID)
		return nil
	}
	if err == nil {
		m.markStartupAcknowledged(runID)
	}
	return err
}

func (m *Manager) markStartupAcknowledged(runID string) {
	m.startupMu.Lock()
	defer m.startupMu.Unlock()
	if m.startup.PreviousRun != nil && m.startup.PreviousRun.RunID == runID {
		m.startup.PreviousRun.Acknowledged = true
	}
	for i := range m.startup.UncleanRuns {
		if m.startup.UncleanRuns[i].RunID == runID {
			m.startup.UncleanRuns[i].Acknowledged = true
		}
	}
}
