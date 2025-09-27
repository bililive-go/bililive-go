package media

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/sirupsen/logrus"
)

// GenerateNFO creates an NFO file for media servers like Emby/Jellyfin
func GenerateNFO(filePath string, info *live.Info, logger *logrus.Entry) error {
	nfoPath := strings.TrimSuffix(filePath, filepath.Ext(filePath)) + ".nfo"
	
	// Get file info for duration estimation
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	// Calculate estimated runtime (use file modification time delta or default to 2 hours)
	currentTime := time.Now()
	estimatedDuration := currentTime.Sub(fileInfo.ModTime())
	if estimatedDuration < 0 || estimatedDuration > 12*time.Hour {
		estimatedDuration = 2 * time.Hour // Default fallback
	}
	runtimeMinutes := int(estimatedDuration.Minutes())

	// Create NFO content based on Jellyfin/Emby format
	nfoContent := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<movie>
    <title>%s</title>
    <sorttitle>%s</sorttitle>
    <plot>直播录制：%s - %s

主播：%s
平台：%s
录制时间：%s</plot>
    <tagline>直播录制</tagline>
    <year>%d</year>
    <premiered>%s</premiered>
    <dateadded>%s</dateadded>
    <runtime>%d</runtime>
    <director>%s</director>
    <studio>%s</studio>
    <genre>直播</genre>
    <genre>录播</genre>
    <tag>直播</tag>
    <tag>录播</tag>
    <tag>%s</tag>
    <country>CN</country>
    <language>zh</language>
    <fileinfo>
        <streamdetails>
            <video>
                <aspect>16:9</aspect>
                <codec>h264</codec>
                <width>1920</width>
                <height>1080</height>
            </video>
            <audio>
                <codec>aac</codec>
                <language>zh</language>
                <channels>2</channels>
            </audio>
        </streamdetails>
    </fileinfo>
</movie>`,
		escapeXML(info.RoomName),
		escapeXML(info.RoomName),
		escapeXML(info.HostName),
		escapeXML(info.RoomName),
		escapeXML(info.HostName),
		escapeXML(info.Live.GetPlatformCNName()),
		fileInfo.ModTime().Format("2006-01-02 15:04:05"),
		fileInfo.ModTime().Year(),
		fileInfo.ModTime().Format("2006-01-02"),
		currentTime.Format("2006-01-02 15:04:05"),
		runtimeMinutes,
		escapeXML(info.HostName),
		escapeXML(info.Live.GetPlatformCNName()),
		escapeXML(info.Live.GetPlatformCNName()),
	)

	err = os.WriteFile(nfoPath, []byte(nfoContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write NFO file: %w", err)
	}

	logger.Infof("Generated NFO file: %s", nfoPath)
	return nil
}

// GeneratePoster creates a poster image from video using ffmpeg
func GeneratePoster(filePath, ffmpegPath, timeOffset string, logger *logrus.Entry) error {
	if timeOffset == "" {
		timeOffset = "00:00:30"
	}
	
	posterPath := strings.TrimSuffix(filePath, filepath.Ext(filePath)) + ".jpg"
	
	// Use ffmpeg to extract a frame as poster
	cmd := exec.Command(
		ffmpegPath,
		"-hide_banner",
		"-loglevel", "error",
		"-ss", timeOffset,
		"-i", filePath,
		"-vframes", "1",
		"-q:v", "2", // High quality
		"-y", // Overwrite existing file
		posterPath,
	)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to generate poster with ffmpeg: %w, output: %s", err, string(output))
	}

	logger.Infof("Generated poster image: %s", posterPath)
	return nil
}

// escapeXML escapes special XML characters
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}