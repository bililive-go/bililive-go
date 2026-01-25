package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/sirupsen/logrus"
)

// ExtractCover 从视频文件提取第一帧作为封面图
// 输出文件名与视频文件相同，扩展名改为 .jpg
// 返回封面文件路径，如果提取失败返回空字符串和错误
func ExtractCover(ctx context.Context, videoPath string) (string, error) {
	// 构建输出文件路径
	ext := filepath.Ext(videoPath)
	coverPath := strings.TrimSuffix(videoPath, ext) + ".jpg"

	return ExtractCoverTo(ctx, videoPath, coverPath)
}

// ExtractCoverTo 从视频文件提取第一帧到指定路径
func ExtractCoverTo(ctx context.Context, videoPath, coverPath string) (string, error) {
	// 检查输入文件是否存在
	if _, err := os.Stat(videoPath); os.IsNotExist(err) {
		return "", fmt.Errorf("video file does not exist: %s", videoPath)
	}

	// 获取 ffmpeg 路径
	ffmpegPath, err := utils.GetFFmpegPath(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get ffmpeg path: %w", err)
	}

	// 构建 ffmpeg 命令
	// -ss 0: 从第 0 秒开始
	// -i: 输入文件
	// -frames:v 1: 只提取 1 帧
	// -q:v 2: JPEG 质量 (2-31, 2 为最高质量)
	// -y: 覆盖已存在的文件
	args := []string{
		"-ss", "0",
		"-i", videoPath,
		"-frames:v", "1",
		"-q:v", "2",
		"-y",
		coverPath,
	}

	logrus.WithFields(logrus.Fields{
		"video": videoPath,
		"cover": coverPath,
	}).Debug("extracting cover from video")

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	// 捕获输出用于调试
	output, err := cmd.CombinedOutput()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"video":  videoPath,
			"error":  err,
			"output": string(output),
		}).Warn("failed to extract cover")
		return "", fmt.Errorf("ffmpeg failed: %w", err)
	}

	// 检查封面文件是否生成
	if _, err := os.Stat(coverPath); os.IsNotExist(err) {
		return "", fmt.Errorf("cover file was not created")
	}

	logrus.WithFields(logrus.Fields{
		"video": videoPath,
		"cover": coverPath,
	}).Info("cover extracted successfully")

	return coverPath, nil
}

// RenameCover 重命名封面文件以匹配视频文件名
// oldVideoPath: 原视频文件路径（封面文件应该与此同名）
// newVideoPath: 新视频文件路径（封面文件将改名以匹配此文件）
func RenameCover(oldVideoPath, newVideoPath string) error {
	// 构建旧封面路径
	oldExt := filepath.Ext(oldVideoPath)
	oldCoverPath := strings.TrimSuffix(oldVideoPath, oldExt) + ".jpg"

	// 检查旧封面是否存在
	if _, err := os.Stat(oldCoverPath); os.IsNotExist(err) {
		// 旧封面不存在，无需操作
		return nil
	}

	// 构建新封面路径
	newExt := filepath.Ext(newVideoPath)
	newCoverPath := strings.TrimSuffix(newVideoPath, newExt) + ".jpg"

	// 如果路径相同，无需操作
	if oldCoverPath == newCoverPath {
		return nil
	}

	// 重命名
	if err := os.Rename(oldCoverPath, newCoverPath); err != nil {
		return fmt.Errorf("failed to rename cover: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"old": oldCoverPath,
		"new": newCoverPath,
	}).Debug("cover renamed")

	return nil
}
