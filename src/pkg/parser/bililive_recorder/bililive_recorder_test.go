package bililive_recorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestBililiveRecorderOutputFile_PicksHighestPart(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "room_20260101.flv")

	for _, name := range []string{"room_20260101_PART000.flv", "room_20260101_PART002.flv", "room_20260101_PART001.flv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("failed to write fixture file: %v", err)
		}
	}

	got := latestBililiveRecorderOutputFile(expected)
	want := filepath.Join(dir, "room_20260101_PART002.flv")
	if got != want {
		t.Errorf("latestBililiveRecorderOutputFile() = %q, want %q", got, want)
	}
}

func TestLatestBililiveRecorderOutputFile_FallsBackToExpectedFile(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "room_20260101.flv")
	if err := os.WriteFile(expected, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	got := latestBililiveRecorderOutputFile(expected)
	if got != expected {
		t.Errorf("latestBililiveRecorderOutputFile() = %q, want %q", got, expected)
	}
}

func TestLatestBililiveRecorderOutputFile_NoMatchReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "room_20260101.flv")

	if got := latestBililiveRecorderOutputFile(expected); got != "" {
		t.Errorf("latestBililiveRecorderOutputFile() = %q, want empty string", got)
	}
}

func TestLatestBililiveRecorderOutputFile_IgnoresMismatchedExtAndBadSuffix(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "room_20260101.flv")

	for _, name := range []string{"room_20260101_PART000.mp4", "room_20260101_PARTabc.flv", "other_PART000.flv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("failed to write fixture file: %v", err)
		}
	}

	if got := latestBililiveRecorderOutputFile(expected); got != "" {
		t.Errorf("latestBililiveRecorderOutputFile() = %q, want empty string", got)
	}
}

func TestGuardOutputFile_LocksAndReleasesFile(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "room_20260101.flv")
	partFile := filepath.Join(dir, "room_20260101_PART000.flv")
	if err := os.WriteFile(partFile, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	p := &Parser{}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		p.guardOutputFile(done, expected)
		close(finished)
	}()

	// 等待守护 goroutine 至少完成一次探测并持有句柄
	locked := false
	for i := 0; i < 200; i++ {
		p.guardedFileMu.Lock()
		locked = p.guardedFile != nil
		p.guardedFileMu.Unlock()
		if locked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !locked {
		t.Fatal("expected guardOutputFile to lock the part file within timeout")
	}

	close(done)
	<-finished

	p.guardedFileMu.Lock()
	defer p.guardedFileMu.Unlock()
	if p.guardedFile != nil {
		t.Errorf("expected guarded file handle to be released after guard stops")
	}
}
