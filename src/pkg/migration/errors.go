package migration

import "fmt"

var (
	// ErrDirtyDatabase 数据库迁移元数据处于 dirty 状态
	ErrDirtyDatabase = fmt.Errorf("database is dirty")
)

// DirtyDatabaseError 表示数据库迁移元数据处于 dirty 状态
type DirtyDatabaseError struct {
	DBPath   string
	Version  uint
	Category DatabaseCategory
}

func (e *DirtyDatabaseError) Error() string {
	return fmt.Sprintf("%v: db=%s version=%d category=%d", ErrDirtyDatabase, e.DBPath, e.Version, e.Category)
}

func (e *DirtyDatabaseError) Unwrap() error {
	return ErrDirtyDatabase
}
