package tools

import (
	"fmt"

	remotetools "github.com/kira1928/remotetools/pkg/tools"
)

// btoolsNodeVersion 为已发布的工具包固定构建时使用的 Node ABI。
// 不根据版本大小猜测未知工具包的运行环境；自定义版本仍沿用原有选择行为。
func btoolsNodeVersion(version, pinnedNode string) (string, error) {
	required := ""
	switch version {
	case "3.1.2-bgo.1", "3.1.2-bgo.2":
		required = "v20.10.0"
	case "4.1.0-bgo.1":
		required = "v22.23.4"
	}
	if required != "" && pinnedNode != "" && pinnedNode != required {
		return "", fmt.Errorf("biliLive-tools %s 需要 Node %s，但 Node 已固定为 %s；请在工具管理中取消固定或选择匹配版本", version, required, pinnedNode)
	}
	return required, nil
}

type btoolsNodeAPI interface {
	GetTool(string) (remotetools.Tool, error)
	GetToolWithVersion(string, string) (remotetools.Tool, error)
	GetToolGroupPinnedVersion(string) string
}

func getBToolsNode(api btoolsNodeAPI, version string) (remotetools.Tool, error) {
	required, err := btoolsNodeVersion(version, api.GetToolGroupPinnedVersion("node"))
	if err != nil {
		return nil, err
	}
	if required == "" {
		return api.GetTool("node")
	}
	// 开发覆盖不会经过 GetToolWithVersion，不能静默忽略用户的显式覆盖。
	if remotetools.GetDevToolOverride("node") != "" {
		return nil, fmt.Errorf("biliLive-tools %s 需要 Node %s，请先移除 Node 开发路径覆盖或使用自定义工具版本", version, required)
	}
	return api.GetToolWithVersion("node", required)
}
