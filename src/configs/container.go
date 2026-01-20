package configs

import (
	"os"
	"strings"
)

// isInContainer 仅依据自家镜像在 Dockerfile 中设置的环境变量进行判断。
// 我们的 Dockerfile 会设置 `ENV IS_DOCKER=true`，只要该变量为 true 即视为在容器内。
func isInContainer() bool {
	return strings.ToLower(strings.TrimSpace(os.Getenv("IS_DOCKER"))) == "true"
}
