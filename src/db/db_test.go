package db

import (
	"os"
	"testing"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/stretchr/testify/assert"
)

func TestNotifications(t *testing.T) {
	// Setup temporary config and DB path
	tmpDir, err := os.MkdirTemp("", "bililive_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	mockConfig := &configs.Config{
		AppDataPath: tmpDir,
	}
	configs.SetCurrentConfig(mockConfig)

	// Reset db instance for testing (dirty hack as it is a singleton)
	// In a real scenario, we might want dependency injection or a Reset() method.
	// For now, we just rely on it initializing if not already.
    // NOTE: If dbInstance is already initialized by other tests or init, this might fail or reuse DB.
    // But since this is a fresh test run in a fresh process usually, it's fine.
    // However, `once` is package level.

    // Force re-init schema for the new path if needed.
    // Since `once` is private, we can't easily reset it.
    // We will assume this is the first time GetDB is called in this process execution context.

	db, err := GetDB()
	assert.NoError(t, err)
	assert.NotNil(t, db)

    // Clear table
    _, err = db.Exec("DELETE FROM notifications")
    assert.NoError(t, err)

	n := &Notification{
		Type:    "test_type",
		Message: "test message",
	}

	err = CreateNotification(n)
	assert.NoError(t, err)
	assert.NotZero(t, n.ID)

	pending, err := GetPendingNotifications()
	assert.NoError(t, err)
	assert.Len(t, pending, 1)
	assert.Equal(t, "test_type", pending[0].Type)
	assert.Equal(t, "test message", pending[0].Message)

	err = ResolveNotification(n.ID)
	assert.NoError(t, err)

	pending, err = GetPendingNotifications()
	assert.NoError(t, err)
	assert.Len(t, pending, 0)
}
