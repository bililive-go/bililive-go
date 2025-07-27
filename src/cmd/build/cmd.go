package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kingpin"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

func RunCmd() int {
	app := kingpin.New("Build tool", "bililive-go Build tool.")
	app.Command("dev", "Build for development.").Action(devBuild)
	app.Command("release", "Build for release.").Action(releaseBuild)
	app.Command("release-docker", "Build for release docker.").Action(releaseDocker)
	app.Command("test", "Run tests.").Action(goTest)
	app.Command("generate", "go generate ./...").Action(goGenerate)
	app.Command("build-web", "Build webapp.").Action(buildWeb)

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