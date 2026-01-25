//go:build dev

package webapp

import (
	"net/http"
	"os"
	"path/filepath"
)

func FS() (http.FileSystem, error) {
	// 开发模式下，优先使用环境变量指定的路径
	if webappPath := os.Getenv("BILILIVE_WEBAPP_PATH"); webappPath != "" {
		return http.FS(os.DirFS(webappPath)), nil
	}

	// 回退：尝试从当前工作目录查找
	if cwd, err := os.Getwd(); err == nil {
		buildPath := filepath.Join(cwd, "src/webapp/build")
		if stat, err := os.Stat(buildPath); err == nil && stat.IsDir() {
			return http.FS(os.DirFS(buildPath)), nil
		}
	}

	// 最后回退：从可执行文件位置查找
	if exePath, err := os.Executable(); err != nil {
		return nil, err
	} else {
		buildPath := filepath.Join(filepath.Dir(exePath), "../src/webapp/build")
		return http.FS(os.DirFS(buildPath)), nil
	}
}
