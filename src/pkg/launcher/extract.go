package launcher

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// extractUpdate 解压或复制更新文件
// 支持：直接可执行文件、.tar.gz、.tgz、.zip 格式
func extractUpdate(srcPath, dstPath string) error {
	ext := strings.ToLower(filepath.Ext(srcPath))

	switch ext {
	case ".gz":
		// 可能是 .tar.gz
		if strings.HasSuffix(strings.ToLower(srcPath), ".tar.gz") {
			return extractTarGz(srcPath, dstPath)
		}
		// 单独的 .gz 文件
		return extractGzip(srcPath, dstPath)
	case ".tgz":
		return extractTarGz(srcPath, dstPath)
	case ".zip":
		return extractZip(srcPath, dstPath)
	default:
		// 直接复制可执行文件
		return copyFile(srcPath, dstPath)
	}
}

// extractTarGz 解压 .tar.gz 文件
func extractTarGz(srcPath, dstPath string) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("打开压缩包失败: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("创建 gzip reader 失败: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	// 查找可执行文件
	dstDir := filepath.Dir(dstPath)
	dstBase := filepath.Base(dstPath)
	var extractedPath string

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar 头失败: %w", err)
		}

		// 跳过目录
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// 查找可执行文件（通常是与目标文件名相同或类似的文件）
		name := filepath.Base(header.Name)
		if name == dstBase || name == dstBase+".exe" ||
			strings.HasPrefix(name, "bililive-go") || strings.HasPrefix(name, "bililive-") {
			extractedPath = filepath.Join(dstDir, name)
			outFile, err := os.OpenFile(extractedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("创建目标文件失败: %w", err)
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return fmt.Errorf("解压文件失败: %w", err)
			}
			outFile.Close()
		}
	}

	if extractedPath == "" {
		return fmt.Errorf("压缩包中未找到可执行文件")
	}

	// 如果解压的文件名与目标不同，重命名
	if extractedPath != dstPath {
		if err := os.Rename(extractedPath, dstPath); err != nil {
			return fmt.Errorf("重命名文件失败: %w", err)
		}
	}

	return nil
}

// extractGzip 解压单独的 .gz 文件
func extractGzip(srcPath, dstPath string) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("创建 gzip reader 失败: %w", err)
	}
	defer gzReader.Close()

	outFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, gzReader); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	// 设置执行权限
	return os.Chmod(dstPath, 0755)
}

// extractZip 解压 .zip 文件
func extractZip(srcPath, dstPath string) error {
	zipReader, err := zip.OpenReader(srcPath)
	if err != nil {
		return fmt.Errorf("打开 zip 文件失败: %w", err)
	}
	defer zipReader.Close()

	dstDir := filepath.Dir(dstPath)
	dstBase := filepath.Base(dstPath)
	var extractedPath string

	for _, file := range zipReader.File {
		// 跳过目录
		if file.FileInfo().IsDir() {
			continue
		}

		// 查找可执行文件
		name := filepath.Base(file.Name)
		if name == dstBase || name == dstBase+".exe" ||
			strings.HasPrefix(name, "bililive-go") || strings.HasPrefix(name, "bililive-") {
			rc, err := file.Open()
			if err != nil {
				return fmt.Errorf("打开压缩文件失败: %w", err)
			}

			extractedPath = filepath.Join(dstDir, name)
			outFile, err := os.OpenFile(extractedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
			if err != nil {
				rc.Close()
				return fmt.Errorf("创建目标文件失败: %w", err)
			}

			if _, err := io.Copy(outFile, rc); err != nil {
				outFile.Close()
				rc.Close()
				return fmt.Errorf("解压文件失败: %w", err)
			}
			outFile.Close()
			rc.Close()
		}
	}

	if extractedPath == "" {
		return fmt.Errorf("压缩包中未找到可执行文件")
	}

	// 如果解压的文件名与目标不同，重命名
	if extractedPath != dstPath {
		if err := os.Rename(extractedPath, dstPath); err != nil {
			return fmt.Errorf("重命名文件失败: %w", err)
		}
	}

	return nil
}

// copyFile 复制文件
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// 确保目标目录存在
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := destFile.ReadFrom(sourceFile); err != nil {
		return err
	}

	// 复制权限
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}

// GetBinaryName 获取当前平台的二进制文件名
func GetBinaryName() string {
	name := "bililive-go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}
