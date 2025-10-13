//go:build dev

package tools

import (
	"os"
)

func getConfigData() (data []byte, err error) {
	return os.ReadFile("src/tools/remote-tools-config.json")
}
