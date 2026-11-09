package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	remotetools "github.com/kira1928/remotetools/pkg/tools"
)

func TestBToolsNodeVersion(t *testing.T) {
	for _, tt := range []struct {
		name, tool, pin, want string
		wantErr               bool
	}{
		{"旧版本一", "3.1.2-bgo.1", "", "v20.10.0", false},
		{"旧版本二", "3.1.2-bgo.2", "", "v20.10.0", false},
		{"新版本", "4.1.0-bgo.1", "", "v22.23.4", false},
		{"匹配固定版本", "4.1.0-bgo.1", "v22.23.4", "v22.23.4", false},
		{"拒绝旧运行时", "4.1.0-bgo.1", "v20.10.0", "", true},
		{"拒绝旧包配新运行时", "3.1.2-bgo.2", "v22.23.4", "", true},
		{"未知版本保持选择", "custom", "", "", false},
		{"开发版本保持选择", "dev", "v24.0.0", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := btoolsNodeVersion(tt.tool, tt.pin)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("运行时=%q, 错误=%v; 期望=%q, 错误=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// 节点缓存选择与精确版本选择必须分开，避免旧安装或较新缓存抢占匹配的 ABI。
type fakeBToolsNodeAPI struct {
	pinned, selected, selectedVersion string
}

func (a *fakeBToolsNodeAPI) GetTool(name string) (remotetools.Tool, error) {
	a.selected = name
	return nil, nil
}
func (a *fakeBToolsNodeAPI) GetToolWithVersion(name, version string) (remotetools.Tool, error) {
	a.selected, a.selectedVersion = name, version
	return nil, nil
}
func (a *fakeBToolsNodeAPI) GetToolGroupPinnedVersion(string) string { return a.pinned }

func TestGetBToolsNodeSelection(t *testing.T) {
	for _, tt := range []struct {
		tool, pin, expected string
		wantErr             bool
	}{
		{"4.1.0-bgo.1", "", "v22.23.4", false},
		{"3.1.2-bgo.2", "", "v20.10.0", false},
		{"4.1.0-bgo.1", "v20.10.0", "", true},
		{"3.1.2-bgo.2", "v22.23.4", "", true},
		{"dev", "", "", false},
	} {
		api := &fakeBToolsNodeAPI{pinned: tt.pin}
		_, err := getBToolsNode(api, tt.tool)
		if (err != nil) != tt.wantErr {
			t.Fatalf("%s: %v", tt.tool, err)
		}
		if tt.wantErr {
			if api.selected != "" {
				t.Fatal("固定版本冲突时不应选择其他 Node")
			}
		} else if api.selected != "node" || api.selectedVersion != tt.expected {
			t.Fatalf("%s 选择了 %s@%s，期望 node@%s", tt.tool, api.selected, api.selectedVersion, tt.expected)
		}
	}
}

func TestGetBToolsNodeRejectsDevelopmentOverride(t *testing.T) {
	previous := remotetools.GetDevToolOverride("node")
	remotetools.SetDevToolOverride("node", "/custom/node")
	t.Cleanup(func() {
		remotetools.ClearDevToolOverride("node")
		if previous != "" {
			remotetools.SetDevToolOverride("node", previous)
		}
	})
	api := &fakeBToolsNodeAPI{}
	if _, err := getBToolsNode(api, "4.1.0-bgo.1"); err == nil || api.selected != "" {
		t.Fatal("发布包不应静默忽略显式 Node 开发覆盖")
	}
	if _, err := getBToolsNode(api, "dev"); err != nil || api.selected != "node" {
		t.Fatalf("自定义工具应保留 Node 开发覆盖选择：%v", err)
	}
}

func TestBToolsReleaseConfiguration(t *testing.T) {
	data, err := os.ReadFile("remote-tools-config.json")
	if err != nil {
		t.Fatal(err)
	}
	type toolConfig struct {
		DownloadURL map[string]map[string]json.RawMessage `json:"downloadUrl"`
		PathToEntry json.RawMessage                       `json:"pathToEntry"`
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	config := make(map[string]map[string]toolConfig)
	for _, name := range []string{"node", "biliLive-tools"} {
		var versions map[string]toolConfig
		if err := json.Unmarshal(raw[name], &versions); err != nil {
			t.Fatal(err)
		}
		config[name] = versions
	}
	assets := map[string]map[string]string{
		"darwin":  {"amd64": "macos-x64", "arm64": "macos-arm64"},
		"linux":   {"amd64": "ubuntu-x64", "arm64": "ubuntu-arm64", "arm": "ubuntu-armv7"},
		"windows": {"amd64": "windows-x64"},
	}
	btools := config["biliLive-tools"]["4.1.0-bgo.1"]
	if string(btools.PathToEntry) != `"index.cjs"` {
		t.Fatal("工具包入口应保持 index.cjs")
	}
	for platform, arches := range assets {
		for arch, asset := range arches {
			var urls []string
			if err := json.Unmarshal(btools.DownloadURL[platform][arch], &urls); err != nil {
				t.Fatal(err)
			}
			want := "https://github.com/kira1928/biliLive-tools/releases/download/4.1.0-bgo.1/bililive-cli-lib-" + asset + ".zip"
			if len(urls) != 2 || urls[0] != want || urls[1] != "https://bililive-go.com/remotetools/download?downloadurl="+want {
				t.Fatalf("%s/%s 下载地址不匹配：%v", platform, arch, urls)
			}
			for _, nodeVersion := range []string{"v20.10.0", "v22.23.4"} {
				var nodeURL string
				if err := json.Unmarshal(config["node"][nodeVersion].DownloadURL[platform][arch], &nodeURL); err != nil || !strings.Contains(nodeURL, nodeVersion) {
					t.Fatalf("%s/%s 缺少 Node %s：%v", platform, arch, nodeVersion, err)
				}
			}
		}
	}
}

// 使用真实 remotetools 选择器，验证已安装的 Node20 不会抢占新版工具需要的 Node22。
func TestGetBToolsNodeWithInstalledNode20(t *testing.T) {
	api := remotetools.Get()
	previousRoot := remotetools.GetRootFolder()
	remotetools.SetRootFolder(t.TempDir())
	t.Cleanup(func() { remotetools.SetRootFolder(previousRoot) })
	data, err := os.ReadFile("remote-tools-config.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.LoadConfigFromBytes(data); err != nil {
		t.Fatal(err)
	}
	oldNode, err := api.GetToolWithVersion("node", "v20.10.0")
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(oldNode.(*remotetools.DownloadedTool).GetWritableToolFolder(), api.GetConfig().ToolConfigs["node@v20.10.0"].PathToEntry.Primary())
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("测试用已安装工具"), 0o755); err != nil {
		t.Fatal(err)
	}
	selected, err := api.GetTool("node")
	if err != nil || selected.GetVersion() != "v20.10.0" {
		t.Fatalf("测试前置条件应选中已有 Node20：%v", err)
	}
	for _, version := range []string{"4.1.0-bgo.1", "3.1.2-bgo.2"} {
		node, err := getBToolsNode(api, version)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := btoolsNodeVersion(version, "")
		if node.GetVersion() != want {
			t.Fatalf("%s 应选择 %s，实际 %s", version, want, node.GetVersion())
		}
		if version == "4.1.0-bgo.1" && node.DoesToolExist() {
			t.Fatal("新 Node 应进入下载分支，不应误用已安装 Node20")
		}
	}
}
