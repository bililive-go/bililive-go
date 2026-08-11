package diagnostics

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

const (
	runLeaseSchema          = "bililive.diagnostics-lease/v1"
	runLeaseFileName        = "lease.json"
	runLeaseLockFileName    = "lease.lock"
	minRunLeaseDuration     = 15 * time.Second
	legacyHeartbeatFreshFor = 30 * time.Second
	initializingRunGrace    = 30 * time.Second
)

// runLease 是可读的租约元数据；lease.lock 才是同一台机器上判断 owner 是否
// 仍存活的强证据。ExpiresAt 用于文件系统不支持 advisory lock、进程状态也
// 无法查询时的保守降级。
type runLease struct {
	Schema           string    `json:"schema"`
	RunID            string    `json:"run_id"`
	OwnerID          string    `json:"owner_id"`
	OwnerPID         int       `json:"owner_pid"`
	OwnerStartedAtMS int64     `json:"owner_started_at_ms,omitempty"`
	State            string    `json:"state"`
	RenewedAt        time.Time `json:"renewed_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	LockAcquired     bool      `json:"lock_acquired"`
}

type leaseFileLock struct {
	file *os.File
}

type processObservation struct {
	Exists      bool
	StartedAtMS int64
}

var observeProcess = observeProcessIdentity

func observeProcessIdentity(pid int) (processObservation, error) {
	if pid <= 0 {
		return processObservation{}, nil
	}
	exists, err := process.PidExists(int32(pid))
	if err != nil || !exists {
		return processObservation{Exists: exists}, err
	}
	owner, err := process.NewProcess(int32(pid))
	if err != nil {
		return processObservation{Exists: true}, err
	}
	startedAt, err := owner.CreateTime()
	return processObservation{Exists: true, StartedAtMS: startedAt}, err
}

func acquireLeaseFileLock(path string) (*leaseFileLock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	acquired, err := tryLockLeaseFile(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !acquired {
		_ = file.Close()
		return nil, ErrRunActive
	}
	return &leaseFileLock{file: file}, nil
}

func (lock *leaseFileLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unlockLeaseFile(lock.file)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

type leaseLockProbe struct {
	Held  bool
	Known bool
}

func probeLeaseFileLock(path string) leaseLockProbe {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return leaseLockProbe{Known: true}
	}
	if err != nil {
		return leaseLockProbe{}
	}
	defer file.Close()
	acquired, err := tryLockLeaseFile(file)
	if err != nil {
		return leaseLockProbe{}
	}
	if !acquired {
		return leaseLockProbe{Held: true, Known: true}
	}
	if err = unlockLeaseFile(file); err != nil {
		return leaseLockProbe{}
	}
	return leaseLockProbe{Known: true}
}

func readRunLease(path string) (runLease, bool) {
	var lease runLease
	data, err := readRegularFileNoSymlink(path)
	if err != nil || json.Unmarshal(data, &lease) != nil ||
		lease.Schema != runLeaseSchema || lease.RunID == "" {
		return lease, false
	}
	return lease, true
}

func runLeaseDuration(interval time.Duration) time.Duration {
	duration := interval * 3
	if duration < minRunLeaseDuration {
		return minRunLeaseDuration
	}
	return duration
}

func leaseStateIsActive(state string) bool {
	switch state {
	case "starting", "running", "stopping", "panicked":
		return true
	default:
		return false
	}
}

func (m *Manager) writeLease(state string) error {
	m.leaseMu.Lock()
	defer m.leaseMu.Unlock()
	return m.writeLeaseLocked(state)
}

func (m *Manager) writeLeaseLocked(state string) error {
	if leaseStateIsActive(state) && m.closed.Load() {
		return nil
	}
	now := time.Now().UTC()
	return atomicWriteJSON(filepath.Join(m.runDir, runLeaseFileName), runLease{
		Schema:           runLeaseSchema,
		RunID:            m.runID,
		OwnerID:          m.ownerID,
		OwnerPID:         m.ownerPID,
		OwnerStartedAtMS: m.ownerStartedAtMS,
		State:            state,
		RenewedAt:        now,
		ExpiresAt:        now.Add(m.leaseDuration),
		LockAcquired:     m.leaseLock != nil,
	}, true)
}

func (m *Manager) releaseLease(state string) error {
	m.leaseMu.Lock()
	defer m.leaseMu.Unlock()
	var result error
	if err := m.writeLeaseLocked(state); err != nil {
		result = err
	}
	if err := m.leaseLock.close(); err != nil && result == nil {
		result = err
	}
	m.leaseLock = nil
	return result
}
