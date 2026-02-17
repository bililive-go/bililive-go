package build

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kingpin"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

// AI 指示文件路径
var agentFiles = []string{
	".github/copilot-instructions.md",
	".agent/rules/gemini-guide.md",
	".gemini/GEMINI.md",
}

const sourceAgentFile = "AGENTS.md"

// 全局变量，用于存储命令行参数
var customVersion string

func RunCmd() int {
	app := kingpin.New("Build tool", "bililive-go Build tool.")

	// dev 命令支持 --version 参数
	devCmd := app.Command("dev", "Build for development.")
	devCmd.Flag("version", "自定义版本号（用于测试升级功能）").StringVar(&customVersion)
	devCmd.Action(devBuild)

	app.Command("dev-incremental", "增量构建：只在源码变化时重新编译（用于调试）").Action(devIncrementalBuild)
	app.Command("release", "Build for release.").Action(releaseBuild)
	app.Command("release-docker", "Build for release docker.").Action(releaseDocker)
	app.Command("test", "Run tests.").Action(goTest)
	app.Command("generate", "go generate ./...").Action(goGenerate)
	app.Command("build-web", "Build webapp.").Action(buildWeb)
	app.Command("sync-agents", "同步 AGENTS.md 到其他 AI 指示文件").Action(syncAgents)
	app.Command("check-agents", "检查 AI 指示文件是否一致").Action(checkAgents)
	app.Command("clean", "清理构建产物").Action(cleanBuild)

	kingpin.MustParse(app.Parse(os.Args[1:]))
	return 0
}

func devBuild(c *kingpin.ParseContext) error {
	// 如果指定了自定义版本号，设置环境变量供 GetBuildFlags 使用
	if customVersion != "" {
		os.Setenv("APP_VERSION", customVersion)
	}
	BuildGoBinary(true)
	return nil
}

func devIncrementalBuild(c *kingpin.ParseContext) error {
	BuildDevIncremental()
	return nil
}

func releaseBuild(c *kingpin.ParseContext) error {
	BuildGoBinary(false)
	return nil
}

func releaseDocker(c *kingpin.ParseContext) error {
	fmt.Printf("release-docker command\n")
	return nil
}

func goTest(c *kingpin.ParseContext) error {
	return utils.ExecCommand([]string{
		"go", "test",
		"-tags", "release",
		"--cover",
		"-coverprofile=coverage.txt",
		"./src/...",
	})
}

func goGenerate(c *kingpin.ParseContext) error {
	return utils.ExecCommand([]string{"go", "generate", "./..."})
}

func buildWeb(c *kingpin.ParseContext) error {
	webappDir := filepath.Join("src", "webapp")
	err := utils.ExecCommandsInDir(
		[][]string{
			{"yarn", "install"},
			{"yarn", "build"},
		},
		webappDir,
	)
	if err != nil {
		return err
	}
	return nil
}

// syncAgents 将 AGENTS.md 同步到其他 AI 指示文件
func syncAgents(c *kingpin.ParseContext) error {
	content, err := os.ReadFile(sourceAgentFile)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", sourceAgentFile, err)
	}

	for _, target := range agentFiles {
		if err := os.WriteFile(target, content, 0644); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", target, err)
		}
		fmt.Printf("已同步: %s -> %s\n", sourceAgentFile, target)
	}

	fmt.Println("AI 指示文件已同步")
	return nil
}

// checkAgents 检查 AI 指示文件是否一致
func checkAgents(c *kingpin.ParseContext) error {
	source, err := os.ReadFile(sourceAgentFile)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", sourceAgentFile, err)
	}

	allMatch := true
	for _, target := range agentFiles {
		targetContent, err := os.ReadFile(target)
		if err != nil {
			fmt.Printf("错误：无法读取 %s: %v\n", target, err)
			allMatch = false
			continue
		}

		if !bytes.Equal(source, targetContent) {
			fmt.Printf("错误：%s 与 %s 不一致，请运行 make sync-agents\n", target, sourceAgentFile)
			allMatch = false
		}
	}

	if !allMatch {
		return fmt.Errorf("AI 指示文件不一致")
	}

	fmt.Println("AI 指示文件一致性检查通过")
	return nil
}

// cleanBuild 清理构建产物（跨平台）
func cleanBuild(c *kingpin.ParseContext) error {
	dirsToClean := []string{
		"bin",
		filepath.Join("src", "webapp", "build"),
	}

	for _, dir := range dirsToClean {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("删除 %s 失败: %w", dir, err)
		}
		fmt.Printf("已删除: %s\n", dir)
	}

	fmt.Println("清理完成")
	return nil
}
