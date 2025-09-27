package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/sirupsen/logrus"
	"go.uber.org/mock/gomock"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
)

func TestGenerateNFO(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := "/tmp/test_nfo"
	err := os.MkdirAll(tmpDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.flv")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock live info with gomock
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	
	mockLive := livemock.NewMockLive(ctrl)
	mockLive.EXPECT().GetPlatformCNName().Return("测试平台").AnyTimes()
	
	info := &live.Info{
		Live:     mockLive,
		HostName: "测试主播",
		RoomName: "测试房间",
		Status:   true,
	}

	// Create logger
	logger := logrus.NewEntry(logrus.New())

	// Generate NFO
	err = GenerateNFO(testFile, info, logger)
	if err != nil {
		t.Fatalf("Failed to generate NFO: %v", err)
	}

	// Check if NFO file was created
	nfoFile := strings.TrimSuffix(testFile, filepath.Ext(testFile)) + ".nfo"
	if _, err := os.Stat(nfoFile); os.IsNotExist(err) {
		t.Fatalf("NFO file was not created: %s", nfoFile)
	}

	// Read and check NFO content
	content, err := os.ReadFile(nfoFile)
	if err != nil {
		t.Fatalf("Failed to read NFO file: %v", err)
	}

	nfoContent := string(content)
	if !strings.Contains(nfoContent, "测试房间") {
		t.Errorf("NFO content should contain room name")
	}
	if !strings.Contains(nfoContent, "测试主播") {
		t.Errorf("NFO content should contain host name")
	}
	if !strings.Contains(nfoContent, "测试平台") {
		t.Errorf("NFO content should contain platform name")
	}
	if !strings.Contains(nfoContent, `<?xml version="1.0"`) {
		t.Errorf("NFO should be valid XML")
	}
}

func TestGeneratePoster(t *testing.T) {
	// Check if test video exists
	testVideo := "/tmp/test_video.mp4"
	if _, err := os.Stat(testVideo); os.IsNotExist(err) {
		t.Skip("Test video not found, skipping poster generation test")
	}

	// Create logger
	logger := logrus.NewEntry(logrus.New())

	// Generate poster
	err := GeneratePoster(testVideo, "ffmpeg", "00:00:01", logger)
	if err != nil {
		t.Fatalf("Failed to generate poster: %v", err)
	}

	// Check if poster file was created
	posterFile := "/tmp/test_video.jpg"
	if _, err := os.Stat(posterFile); os.IsNotExist(err) {
		t.Fatalf("Poster file was not created: %s", posterFile)
	}

	// Check file size (should be > 0)
	info, err := os.Stat(posterFile)
	if err != nil {
		t.Fatalf("Failed to stat poster file: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("Poster file is empty")
	}

	// Clean up
	os.Remove(posterFile)
}

func TestEscapeXML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple text", "simple text"},
		{"text & more", "text &amp; more"},
		{"<tag>content</tag>", "&lt;tag&gt;content&lt;/tag&gt;"},
		{`"quoted" & 'single'`, "&quot;quoted&quot; &amp; &apos;single&apos;"},
	}

	for _, test := range tests {
		result := escapeXML(test.input)
		if result != test.expected {
			t.Errorf("escapeXML(%q) = %q; want %q", test.input, result, test.expected)
		}
	}
}