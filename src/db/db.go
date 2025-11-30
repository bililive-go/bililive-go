package db

import (
    "database/sql"
    "path/filepath"
    "os"
    "sync"
    "time"

    _ "github.com/mattn/go-sqlite3"
    "github.com/bililive-go/bililive-go/src/configs"
)

var (
    dbInstance *sql.DB
    once       sync.Once
)

type Notification struct {
    ID          int64     `json:"id"`
    Type        string    `json:"type"`
    Message     string    `json:"message"`
    Status      string    `json:"status"` // "pending", "resolved"
    CreatedAt   time.Time `json:"created_at"`
    ResolvedAt  time.Time `json:"resolved_at,omitempty"`
    Metadata    string    `json:"metadata,omitempty"` // JSON string for extra data
}

func GetDB() (*sql.DB, error) {
    var err error
    once.Do(func() {
        cfg := configs.GetCurrentConfig()
        if cfg == nil {
            err = os.ErrNotExist // Or appropriate error
            return
        }

        dbPath := filepath.Join(cfg.AppDataPath, "bililive.db")
        // Ensure directory exists
        if err = os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
            return
        }

        dbInstance, err = sql.Open("sqlite3", dbPath)
        if err != nil {
            return
        }

        err = initSchema(dbInstance)
    })
    return dbInstance, err
}

func initSchema(db *sql.DB) error {
    query := `
    CREATE TABLE IF NOT EXISTS notifications (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        type TEXT NOT NULL,
        message TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'pending',
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        resolved_at DATETIME,
        metadata TEXT
    );
    `
    _, err := db.Exec(query)
    return err
}

func CreateNotification(n *Notification) error {
    db, err := GetDB()
    if err != nil {
        return err
    }
    query := `INSERT INTO notifications (type, message, status, created_at, metadata) VALUES (?, ?, ?, ?, ?)`
    res, err := db.Exec(query, n.Type, n.Message, "pending", time.Now(), n.Metadata)
    if err != nil {
        return err
    }
    id, err := res.LastInsertId()
    if err == nil {
        n.ID = id
    }
    return err
}

func GetPendingNotifications() ([]Notification, error) {
    db, err := GetDB()
    if err != nil {
        return nil, err
    }
    rows, err := db.Query("SELECT id, type, message, status, created_at, metadata FROM notifications WHERE status = 'pending' ORDER BY created_at DESC")
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var notifications []Notification
    for rows.Next() {
        var n Notification
        if err := rows.Scan(&n.ID, &n.Type, &n.Message, &n.Status, &n.CreatedAt, &n.Metadata); err != nil {
            continue
        }
        notifications = append(notifications, n)
    }
    return notifications, nil
}

func ResolveNotification(id int64) error {
    db, err := GetDB()
    if err != nil {
        return err
    }
    _, err = db.Exec("UPDATE notifications SET status = 'resolved', resolved_at = ? WHERE id = ?", time.Now(), id)
    return err
}
