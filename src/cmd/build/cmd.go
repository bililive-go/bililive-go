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
	".gemini/MEMORY.md",
}

const sourceAgentFile = "AGENTS.md"

func RunCmd() int {
	app := kingpin.New("Build tool", "bililive-go Build tool.")
	app.Command("dev", "Build for development.").Action(devBuild)
	app.Command("release", "Build for release.").Action(releaseBuild)
	app.Command("release-docker", "Build for release docker.").Action(releaseDocker)
	app.Command("test", "Run tests.").Action(goTest)
	app.Command("generate", "go generate ./...").Action(goGenerate)
	app.Command("build-web", "Build webapp.").Action(buildWeb)
	app.Command("sync-agents", "同步 AGENTS.md 到其他 AI 指示文件").Action(syncAgents)
	app.Command("check-agents", "检查 AI 指示文件是否一致").Action(checkAgents)

	kingpin.MustParse(app.Parse(os.Args[1:]))
	return 0
}

func devBuild(c *kingpin.ParseContext) error {
	BuildGoBinary(true)
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
