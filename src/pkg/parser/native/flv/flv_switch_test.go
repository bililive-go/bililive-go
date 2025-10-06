package flv

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFileSwitching(t *testing.T) {
	// Create a test parser
	builder := &builder{}
	parser, err := builder.Build(map[string]string{})
	assert.NoError(t, err)
	
	flvParser := parser.(*Parser)
	
	// Test SwitchOutputFile method
	tempFile1 := "/tmp/test1.flv"
	tempFile2 := "/tmp/test2.flv"
	
	// Clean up test files
	defer func() {
		os.Remove(tempFile1)
		os.Remove(tempFile2)
	}()
	
	// Create initial file
	f1, err := os.Create(tempFile1)
	assert.NoError(t, err)
	flvParser.oFile = f1
	flvParser.o = f1
	
	// Set some metadata to test header generation
	flvParser.Metadata.HasVideo = true
	flvParser.Metadata.HasAudio = true
	
	// Test file switching (in a goroutine to simulate real usage)
	go func() {
		time.Sleep(10 * time.Millisecond)
		err := flvParser.SwitchOutputFile(tempFile2)
		assert.NoError(t, err)
	}()
	
	// Simulate parser listening for switch requests
	select {
	case newFile := <-flvParser.switchCh:
		err := flvParser.doSwitchFile(newFile)
		assert.NoError(t, err)
		
		// Verify new file was created and has FLV header
		stat, err := os.Stat(tempFile2)
		assert.NoError(t, err)
		assert.True(t, stat.Size() > 0, "New file should have content (FLV header)")
		
		// Verify FLV header
		f2, err := os.Open(tempFile2)
		assert.NoError(t, err)
		defer f2.Close()
		
		header := make([]byte, 13)
		n, err := f2.Read(header)
		assert.NoError(t, err)
		assert.Equal(t, 13, n)
		
		// Check FLV signature
		assert.Equal(t, []byte{0x46, 0x4c, 0x56, 0x01}, header[:4], "Should have correct FLV signature")
		
		// Check flags (video + audio)
		assert.Equal(t, uint8(0x05), header[4], "Should have video and audio flags set")
		
	case <-time.After(100 * time.Millisecond):
		t.Fatal("File switch request not received")
	}
}

func TestFileSwitchingAfterStop(t *testing.T) {
	builder := &builder{}
	parser, err := builder.Build(map[string]string{})
	assert.NoError(t, err)
	
	flvParser := parser.(*Parser)
	
	// Stop the parser
	err = flvParser.Stop()
	assert.NoError(t, err)
	
	// Try to switch file after stop - should fail
	err = flvParser.SwitchOutputFile("/tmp/test.flv")
	assert.Error(t, err, "Should fail to switch file after parser is stopped")
	assert.Contains(t, err.Error(), "parser stopped")
}