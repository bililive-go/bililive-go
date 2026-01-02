//go:build !dev && !release

package webapp

import (
	"net/http"
	"os"
	"path/filepath"
)

// FS 返回webapp的文件系统，默认从文件系统加载
func FS() (http.FileSystem, error) {
	if exePath, err := os.Executable(); err != nil {
		return nil, err
	} else {
		buildPath := filepath.Join(filepath.Dir(exePath), "../src/webapp/build")
		return http.FS(os.DirFS(buildPath)), nil
	}
}
