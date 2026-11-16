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

// getBTools 升级未固定的已知旧包，避免“优先已安装版本”一直选择旧版本。
// 保留显式固定版本，以及自定义工具包和开发覆盖的原有选择行为。
func getBTools(api btoolsNodeAPI) (remotetools.Tool, error) {
	tool, err := api.GetTool("biliLive-tools")
	if err != nil {
		return nil, err
	}
	if tool == nil {
		return nil, fmt.Errorf("未获取到 biliLive-tools 实例")
	}
	if api.GetToolGroupPinnedVersion("biliLive-tools") == "" {
		switch tool.GetVersion() {
		case "3.1.2-bgo.1", "3.1.2-bgo.2":
			tool, err = api.GetToolWithVersion("biliLive-tools", "4.1.0-bgo.1")
			if err != nil {
				return nil, err
			}
			if tool == nil {
				return nil, fmt.Errorf("未获取到 biliLive-tools 4.1.0-bgo.1 实例")
			}
		}
	}
	return tool, nil
}

func getBToolsNode(api btoolsNodeAPI, version string) (remotetools.Tool, error) {
	required, err := btoolsNodeVersion(version, api.GetToolGroupPinnedVersion("node"))
	if err != nil {
		return nil, err
	}
	var tool remotetools.Tool
	if required == "" {
		tool, err = api.GetTool("node")
	} else {
		// 开发覆盖不会经过 GetToolWithVersion，不能静默忽略用户的显式覆盖。
		if remotetools.GetDevToolOverride("node") != "" {
			return nil, fmt.Errorf("biliLive-tools %s 需要 Node %s，请先移除 Node 开发路径覆盖或使用自定义工具版本", version, required)
		}
		tool, err = api.GetToolWithVersion("node", required)
	}
	if err != nil {
		return nil, err
	}
	if tool == nil {
		return nil, fmt.Errorf("未获取到 node 实例")
	}
	return tool, nil
}
