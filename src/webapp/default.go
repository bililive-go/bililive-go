//go:build !dev && !release

package webapp

import (
	"net/http"
	"os"
	"path/filepath"
)

func FS() (http.FileSystem, error) {
	if exePath, err := os.Executable(); err != nil {
		return nil, err
	} else {
		buildPath := filepath.Join(filepath.Dir(exePath), "../src/webapp/build")
		return http.FS(os.DirFS(buildPath)), nil
	}
}