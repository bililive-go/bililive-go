package recorders

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
)

func TestResolveRecordedOutputFiles_BililiveRecorderMultipleParts(t *testing.T) {
	dir := t.TempDir()
	expectedFile := filepath.Join(dir, "video.flv")
	partFiles := []string{
		filepath.Join(dir, "video_PART002.flv"),
		filepath.Join(dir, "video_PART000.flv"),
		filepath.Join(dir, "video_PART001.flv"),
	}
	for _, file := range partFiles {
		assert.NoError(t, os.WriteFile(file, []byte("test"), 0o644))
	}

	outputFiles := resolveRecordedOutputFiles(expectedFile, configs.DownloaderBililiveRecorder, true, nil)

	assert.Equal(t, []string{
		filepath.Join(dir, "video_PART000.flv"),
		filepath.Join(dir, "video_PART001.flv"),
		filepath.Join(dir, "video_PART002.flv"),
	}, outputFiles)
}

func TestResolveRecordedOutputFiles_BililiveRecorderSinglePartRename(t *testing.T) {
	dir := t.TempDir()
	expectedFile := filepath.Join(dir, "video.flv")
	partFile := filepath.Join(dir, "video_PART000.flv")
	assert.NoError(t, os.WriteFile(partFile, []byte("test"), 0o644))

	outputFiles := resolveRecordedOutputFiles(expectedFile, configs.DownloaderBililiveRecorder, false, nil)

	assert.Equal(t, []string{expectedFile}, outputFiles)
	_, err := os.Stat(expectedFile)
	assert.NoError(t, err)
	_, err = os.Stat(partFile)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestResolveRecordedOutputFiles_NonBililiveRecorderFallback(t *testing.T) {
	dir := t.TempDir()
	expectedFile := filepath.Join(dir, "video.flv")
	assert.NoError(t, os.WriteFile(expectedFile, []byte("test"), 0o644))

	outputFiles := resolveRecordedOutputFiles(expectedFile, configs.DownloaderFFmpeg, false, nil)

	assert.Equal(t, []string{expectedFile}, outputFiles)
	assert.Nil(t, resolveRecordedOutputFiles(filepath.Join(dir, "missing.flv"), configs.DownloaderFFmpeg, false, nil))
}

func TestRunCustomCommandline_MultipleOutputFiles(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()

	r := &recorder{Live: l}
	info := &live.Info{HostName: "host", RoomName: "room"}
	cfg := &configs.Config{}

	backupExecCommandFunc := execCommandFunc
	defer func() { execCommandFunc = backupExecCommandFunc }()

	var got [][]string
	call := 0
	execCommandFunc = func(name string, args []string, stdout, stderr io.Writer) error {
		call++
		got = append(got, append([]string{name}, args...))
		if call == 1 {
			return assert.AnError
		}
		return nil
	}

	tempDir := t.TempDir()
	outputFiles := []string{
		filepath.Join(tempDir, "segment-1.flv"),
		filepath.Join(tempDir, "segment-2.flv"),
	}
	r.runCustomCommandline(cfg, info, `echo "{{ .FileName }}" "{{ .Ffmpeg }}"`, "/usr/bin/ffmpeg", outputFiles, false)

	if assert.Len(t, got, 2) {
		assert.Contains(t, got[0][2], outputFiles[0])
		assert.Contains(t, got[0][2], "/usr/bin/ffmpeg")
		assert.Contains(t, got[1][2], outputFiles[1])
		assert.Contains(t, got[1][2], "/usr/bin/ffmpeg")
	}
}

func TestRunCustomCommandline_DeletesSourceOnSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()

	r := &recorder{Live: l}
	info := &live.Info{HostName: "host", RoomName: "room"}
	cfg := &configs.Config{}

	backupExecCommandFunc := execCommandFunc
	defer func() { execCommandFunc = backupExecCommandFunc }()
	execCommandFunc = func(name string, args []string, stdout, stderr io.Writer) error {
		return nil
	}

	tempDir := t.TempDir()
	outputFile := filepath.Join(tempDir, "segment-1.flv")
	assert.NoError(t, os.WriteFile(outputFile, []byte("test"), 0o644))

	r.runCustomCommandline(cfg, info, `echo "{{ .FileName }}"`, "/usr/bin/ffmpeg", []string{outputFile}, true)

	_, err := os.Stat(outputFile)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
