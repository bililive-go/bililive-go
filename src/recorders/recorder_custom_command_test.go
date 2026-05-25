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

func TestResolveRecordedOutputFilesReturnsAllBililiveRecorderParts(t *testing.T) {
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

func TestCustomCommandlineRunsForEveryOutputFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()

	r := &recorder{Live: l}
	info := &live.Info{HostName: "host", RoomName: "room"}
	cfg := &configs.Config{}

	backupExecCommand := execCommand
	defer func() { execCommand = backupExecCommand }()

	var got [][]string
	call := 0
	execCommand = func(name string, args []string, stdout, stderr io.Writer) error {
		call++
		got = append(got, append([]string{name}, args...))
		if call == 1 {
			return assert.AnError
		}
		return nil
	}

	outputFiles := []string{"/tmp/segment-1.flv", "/tmp/segment-2.flv"}
	r.runCustomCommandline(cfg, info, `echo "{{ .FileName }}" "{{ .Ffmpeg }}"`, "/usr/bin/ffmpeg", outputFiles, false)

	if assert.Len(t, got, 2) {
		assert.Contains(t, got[0][2], outputFiles[0])
		assert.Contains(t, got[0][2], "/usr/bin/ffmpeg")
		assert.Contains(t, got[1][2], outputFiles[1])
		assert.Contains(t, got[1][2], "/usr/bin/ffmpeg")
	}
}
