// fake-ffmpeg 是用于 e2e 测试的极简 FFmpeg 替身。
// 它响应 -version 参数，其余情况返回非零退出码。
package main

import (
	"fmt"
	"os"
)

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "-version" {
			fmt.Println("ffmpeg version fake-test-1.0.0-e2e")
			fmt.Println("built with bililive-go test suite")
			os.Exit(0)
		}
	}
	fmt.Fprintln(os.Stderr, "fake-ffmpeg: this is a stub used only for e2e testing")
	os.Exit(1)
}
