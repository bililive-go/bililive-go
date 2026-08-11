package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type segmentInfo struct {
	Name   string `json:"name"`
	Events uint64 `json:"events"`
	Bytes  int64  `json:"bytes"`
}

type eventIndex struct {
	Schema        string        `json:"schema"`
	UpdatedAt     time.Time     `json:"updated_at"`
	NextSegment   uint64        `json:"next_segment"`
	LatestSeq     uint64        `json:"latest_seq"`
	DroppedEvents uint64        `json:"dropped_events"`
	Segments      []segmentInfo `json:"segments"`
}

type eventWriter struct {
	dir         string
	maxBytes    int64
	maxSegments int
	file        *os.File
	current     *segmentInfo
	segments    []segmentInfo
	nextSegment uint64
	latestSeq   uint64
	dropped     uint64
	lastSync    time.Time
	syncEvery   time.Duration
}

func newEventWriter(dir string, maxBytes int64, maxSegments int, syncEvery time.Duration) (*eventWriter, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	w := &eventWriter{
		dir:         dir,
		maxBytes:    maxBytes,
		maxSegments: maxSegments,
		nextSegment: 1,
		syncEvery:   syncEvery,
	}
	if err := w.openNext(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *eventWriter) openNext() error {
	name := fmt.Sprintf("events-%06d.jsonl", w.nextSegment)
	w.nextSegment++
	path := filepath.Join(w.dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err = syncDir(w.dir); err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.segments = append(w.segments, segmentInfo{Name: name})
	w.current = &w.segments[len(w.segments)-1]
	return w.persistIndex()
}

func (w *eventWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Sync(); err != nil {
			return err
		}
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	if err := w.openNext(); err != nil {
		return err
	}
	for len(w.segments) > w.maxSegments {
		oldest := w.segments[0]
		if err := os.Remove(filepath.Join(w.dir, oldest.Name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		w.dropped += oldest.Events
		w.segments = w.segments[1:]
		w.current = &w.segments[len(w.segments)-1]
	}
	if err := syncDir(w.dir); err != nil {
		return err
	}
	return w.persistIndex()
}

func (w *eventWriter) Write(event *Event) error {
	event.Seq = w.latestSeq + 1
	event.GlobalSeq = event.Seq
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if w.current.Events > 0 && w.current.Bytes+int64(len(data)) > w.maxBytes {
		if err = w.rotate(); err != nil {
			return err
		}
	}
	if err = writeAll(w.file, data); err != nil {
		return err
	}
	w.latestSeq = event.Seq
	w.current.Events++
	w.current.Bytes += int64(len(data))
	if w.syncEvery <= 0 || time.Since(w.lastSync) >= w.syncEvery {
		if err = w.file.Sync(); err != nil {
			return err
		}
		w.lastSync = time.Now()
	}
	return nil
}

func (w *eventWriter) Sync() error {
	if w.file == nil {
		return nil
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	w.lastSync = time.Now()
	return w.persistIndex()
}

func (w *eventWriter) Close() error {
	if w.file == nil {
		return nil
	}
	if err := w.Sync(); err != nil {
		return err
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *eventWriter) persistIndex() error {
	index := eventIndex{
		Schema:        "bililive.diagnostics-events/v1",
		UpdatedAt:     time.Now().UTC(),
		NextSegment:   w.nextSegment,
		LatestSeq:     w.latestSeq,
		DroppedEvents: w.dropped,
		Segments:      append([]segmentInfo(nil), w.segments...),
	}
	return atomicWriteJSON(filepath.Join(w.dir, "index.json"), index, true)
}

func eventSegmentPaths(dir string) ([]string, error) {
	if err := ensureDirectoryNoSymlink(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "events-") &&
			strings.HasSuffix(entry.Name(), ".jsonl") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func readEventIndex(dir string) eventIndex {
	var index eventIndex
	data, err := readRegularFileNoSymlink(filepath.Join(dir, "index.json"))
	if err == nil {
		_ = json.Unmarshal(data, &index)
	}
	return index
}

func countEventSegments(dir string) int {
	paths, err := eventSegmentPaths(dir)
	if err != nil {
		return 0
	}
	return len(paths)
}
