package build

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

func BuildGoBinary(isDev bool) {
	goHostOS := os.Getenv("PLATFORM")
	if goHostOS == "" {
		goHostOS = runtime.GOOS
	}
	goHostArch := os.Getenv("ARCH")
	if goHostArch == "" {
		goHostArch = runtime.GOARCH
	}
	goVersion := runtime.Version()
	goTags := "release"
	gcflags := ""
	debug_build_flags := " -s -w "
	if isDev {
		goTags = "dev"
		gcflags = "all=-N -l"
		debug_build_flags = ""
	}
	fmt.Printf("building bililive-go (Platform: %s, Arch: %s, GoVersion: %s, Tags: %s)\n", goHostOS, goHostArch, goVersion, goTags)

	constsPath := "github.com/bililive-go/bililive-go/src/consts"
	now := fmt.Sprintf("%d", time.Now().Unix())
	var buf bytes.Buffer

	// 从环境变量获取 Sentry DSN
	sentryDSN := os.Getenv("SENTRY_DSN")

	// 构建 template data map
	data := map[string]string{
		"DebugBuildFlags": debug_build_flags,
		"ConstsPath":      constsPath,
		"Now":             now,
		"AppVersion":      getGitTagString(),
		"GitHash":         getGitHash(),
	}

	ldFlagsTmpl := "{{.DebugBuildFlags}} " +
		"-X {{.ConstsPath}}.BuildTime={{.Now}} " +
		"-X {{.ConstsPath}}.AppVersion={{.AppVersion}} " +
		"-X {{.ConstsPath}}.GitHash={{.GitHash}}"

	// 如果存在 Sentry DSN，则注入
	if sentryDSN != "" {
		ldFlagsTmpl += " -X main.SentryDSN={{.SentryDSN}}"
		data["SentryDSN"] = sentryDSN
	}

	t := template.Must(template.New("ldFlags").Parse(ldFlagsTmpl))

	t.Execute(&buf, data)
	ldflags := buf.String()

	cmd := exec.Command(
		"go", "build",
		"-tags", goTags,
		`-gcflags=`+gcflags,
		"-o", "bin/"+generateBinaryName(goHostOS, goHostArch),
		"-ldflags="+ldflags,
		"./src/cmd/bililive",
	)
	cmd.Env = append(
		os.Environ(),
		"GOOS="+goHostOS,
		"GOARCH="+goHostArch,
		"CGO_ENABLED=0",
		"UPX_ENABLE=0",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	log.Print(cmd.String())
	err := cmd.Run()
	if err != nil {
		fmt.Printf("Command finished with error: %v", err)
	}
}

func generateBinaryName(goHostOS string, goHostArch string) string {
	binaryName := "bililive-" + goHostOS + "-" + goHostArch
	if goHostOS == "windows" {
		binaryName += ".exe"
	}
	return binaryName
}

func getGitHash() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func getGitTagString() string {
	cmd := exec.Command("git", "describe", "--tags", "--always")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
