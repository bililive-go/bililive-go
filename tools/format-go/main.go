package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

func main() {
	write := flag.Bool("write", false, "直接写入 gofmt 格式化结果")
	flag.Parse()

	files, err := goFiles()
	if err != nil {
		fail(err)
	}

	var unformatted []string
	for _, file := range files {
		args := []string{"-l"}
		if *write {
			args = []string{"-w"}
		}
		output, err := exec.Command("gofmt", append(args, file)...).CombinedOutput()
		if err != nil {
			fail(fmt.Errorf("gofmt %s: %s", file, strings.TrimSpace(string(output))))
		}
		if len(bytes.TrimSpace(output)) != 0 {
			unformatted = append(unformatted, file)
		}
	}

	if len(unformatted) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "以下 Go 文件不符合 gofmt：")
	for _, file := range unformatted {
		fmt.Fprintln(os.Stderr, file)
	}
	fmt.Fprintln(os.Stderr, "Run make format to fix formatting issues.")
	os.Exit(1)
}

func goFiles() ([]string, error) {
	output, err := exec.Command(
		"git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("列出 Go 文件：%w", err)
	}

	parts := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) != 0 {
			files = append(files, string(part))
		}
	}
	sort.Strings(files)
	return files, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
