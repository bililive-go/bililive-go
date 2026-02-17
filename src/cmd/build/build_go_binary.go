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

// BuildFlags 包含构建所需的参数
type BuildFlags struct {
	Tags         string
	GcFlags      string
	LdFlags      string
	DebugLdFlags string // -s -w（release 模式）或空（dev 模式）
}

// constsPath 是注入版本信息的包路径
const constsPath = "github.com/bililive-go/bililive-go/src/consts"

// GetBuildFlags 返回构建参数
func GetBuildFlags(isDev bool) BuildFlags {
	now := fmt.Sprintf("%d", time.Now().Unix())
	var buf bytes.Buffer

	// 版本号优先级：环境变量 APP_VERSION > git tag
	appVersion := os.Getenv("APP_VERSION")
	if appVersion == "" {
		appVersion = getGitTagString()
	}

	data := map[string]string{
		"ConstsPath": constsPath,
		"Now":        now,
		"AppVersion": appVersion,
		"GitHash":    getGitHash(),
	}

	ldFlagsTmpl := "-X {{.ConstsPath}}.BuildTime={{.Now}} " +
		"-X {{.ConstsPath}}.AppVersion={{.AppVersion}} " +
		"-X {{.ConstsPath}}.GitHash={{.GitHash}}"

	// 从环境变量获取 Sentry DSN
	sentryDSN := os.Getenv("SENTRY_DSN")
	if sentryDSN != "" {
		ldFlagsTmpl += " -X main.SentryDSN={{.SentryDSN}}"
		data["SentryDSN"] = sentryDSN
	}

	t := template.Must(template.New("ldFlags").Parse(ldFlagsTmpl))
	t.Execute(&buf, data)

	if isDev {
		return BuildFlags{
			Tags:         "dev",
			GcFlags:      "all=-N -l", // 禁用优化以便调试
			LdFlags:      strings.TrimSpace(buf.String()),
			DebugLdFlags: "", // dev 模式保留调试符号
		}
	}

	return BuildFlags{
		Tags:         "release",
		GcFlags:      "",
		LdFlags:      strings.TrimSpace(buf.String()),
		DebugLdFlags: "-s -w", // release 模式去除调试符号
	}
}

// BuildGoBinary 构建 Go 二进制文件到默认路径（bin/bililive-{平台}-{架构}）
func BuildGoBinary(isDev bool) {
	goHostOS := os.Getenv("PLATFORM")
	if goHostOS == "" {
		goHostOS = runtime.GOOS
	}
	goHostArch := os.Getenv("ARCH")
	if goHostArch == "" {
		goHostArch = runtime.GOARCH
	}

	outputPath := "bin/" + generateBinaryName(goHostOS, goHostArch)
	BuildGoBinaryWithOutput(isDev, outputPath)
}

// BuildGoBinaryWithOutput 构建 Go 二进制文件到指定路径
func BuildGoBinaryWithOutput(isDev bool, outputPath string) {
	goHostOS := os.Getenv("PLATFORM")
	if goHostOS == "" {
		goHostOS = runtime.GOOS
	}
	goHostArch := os.Getenv("ARCH")
	if goHostArch == "" {
		goHostArch = runtime.GOARCH
	}
	goVersion := runtime.Version()

	flags := GetBuildFlags(isDev)

	fmt.Printf("building bililive-go (Platform: %s, Arch: %s, GoVersion: %s, Tags: %s)\n", goHostOS, goHostArch, goVersion, flags.Tags)

	// 组合完整的 ldflags
	ldflags := flags.LdFlags
	if flags.DebugLdFlags != "" {
		ldflags = flags.DebugLdFlags + " " + ldflags
	}

	// 确保输出目录存在
	os.MkdirAll("bin", 0755)

	cmd := exec.Command(
		"go", "build",
		"-tags", flags.Tags,
		`-gcflags=`+flags.GcFlags,
		"-o", outputPath,
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
