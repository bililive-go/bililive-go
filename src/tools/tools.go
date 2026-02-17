package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/bililive-go/bililive-go/src/configs"
	blog "github.com/bililive-go/bililive-go/src/log"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/tidwall/gjson"

	"github.com/kira1928/remotetools/pkg/tools"
)

type toolStatusValue int32

const (
	toolStatusValueNotInitialized toolStatusValue = iota
	toolStatusValueInitializing
	toolStatusValueInitialized
)

var currentToolStatus atomic.Int32

// bililive-tools 状态跟踪
type btoolsStatusValue int32

const (
	BToolsStatusNotStarted btoolsStatusValue = iota
	BToolsStatusStarting
	BToolsStatusReady
	BToolsStatusFailed
)

var currentBToolsStatus atomic.Int32

// IsBToolsReady 检查 bililive-tools 是否已就绪
func IsBToolsReady() bool {
	return btoolsStatusValue(currentBToolsStatus.Load()) == BToolsStatusReady
}

// IsBToolsStarting 检查 bililive-tools 是否正在启动
func IsBToolsStarting() bool {
	status := btoolsStatusValue(currentBToolsStatus.Load())
	return status == BToolsStatusStarting || status == BToolsStatusNotStarted
}

// Cleanup 关闭所有通过 tools 包管理的资源：
// - 终止所有已注册的子进程（btools、klive 等）
// - 关闭 remotetools WebUI 服务器
// 在进入 launcher 模式前调用，确保端口被释放以供新版本使用。
func Cleanup() {
	logger := blog.GetLogger()

	// 1. 终止所有已注册的子进程
	KillAllProcesses()

	// 2. 关闭 remotetools WebUI 服务器
	api := tools.Get()
	if api != nil {
		if err := api.StopWebUI(); err != nil {
			logger.Warnf("关闭 RemoteTools WebUI 失败: %v", err)
		} else {
			logger.Info("RemoteTools WebUI 已关闭")
		}
	}
}

// DownloaderAvailability 包含各下载器的可用状态
type DownloaderAvailability struct {
	FFmpegAvailable           bool   `json:"ffmpeg_available"`
	FFmpegPath                string `json:"ffmpeg_path,omitempty"`
	NativeAvailable           bool   `json:"native_available"` // 内置解析器永远可用
	BililiveRecorderAvailable bool   `json:"bililive_recorder_available"`
	BililiveRecorderPath      string `json:"bililive_recorder_path,omitempty"`
}

// GetDownloaderAvailability 返回所有下载器的可用状态
func GetDownloaderAvailability() DownloaderAvailability {
	result := DownloaderAvailability{
		NativeAvailable: true, // 内置解析器永远可用
	}

	api := tools.Get()
	if api == nil {
		return result
	}

	// 检查 FFmpeg
	ffmpeg, err := api.GetTool("ffmpeg")
	if err == nil && ffmpeg.DoesToolExist() {
		result.FFmpegAvailable = true
		result.FFmpegPath = ffmpeg.GetToolPath()
	}

	// 检查 BililiveRecorder CLI
	dotnet, err := api.GetTool("dotnet")
	if err == nil && dotnet.DoesToolExist() {
		recorder, err := api.GetTool("bililive-recorder-cli")
		if err == nil && recorder.DoesToolExist() {
			result.BililiveRecorderAvailable = true
			result.BililiveRecorderPath = recorder.GetToolPath()
		}
	}

	return result
}

func AsyncInit() {
	bilisentry.Go(func() {
		err := Init()
		if err != nil {
			blog.GetLogger().Errorln("Failed to initialize RemoteTools:", err)
		}
	})
}

func SyncBuiltInTools(targetToolFolder string) (err error) {
	// 初始化 remotetools API 配置，避免未加载配置时获取工具失败
	api := tools.Get()
	if api == nil {
		return errors.New("failed to get remotetools API instance")
	}
	cfgData, cfgErr := getConfigData()
	if cfgErr != nil || cfgData == nil {
		if cfgErr == nil {
			cfgErr = errors.New("failed to get config data")
		}
		return cfgErr
	}
	if err = api.LoadConfigFromBytes(cfgData); err != nil {
		return err
	}

	tools.SetRootFolder(targetToolFolder)
	toolsToKeep := []tools.Tool{}
	for _, toolName := range []string{
		"ffmpeg",
		"dotnet",
		"bililive-recorder",
		"node",
		"biliLive-tools",
	} {
		var tool tools.Tool
		tool, err = api.GetTool(toolName)
		if err != nil {
			blog.GetLogger().WithError(err).Warn("failed to get built-in tool:", toolName)
			continue
		}
		if !tool.DoesToolExist() {
			blog.GetLogger().Infoln("Installing built-in tool:", toolName)
			err = tool.Install()
			if err != nil {
				return err
			}
		}
		blog.GetLogger().Infoln("Built-in tool is ready:", toolName, "version:", tool.GetVersion())
		toolsToKeep = append(toolsToKeep, tool)
	}

	_, err = api.DeleteAllExceptToolsInRoot(toolsToKeep)
	if err != nil {
		blog.GetLogger().WithError(err).Warn("failed to clean up unused built-in tools")
		return
	}
	blog.GetLogger().Infoln("Built-in tools synchronized to", targetToolFolder)

	return err
}

func Init() (err error) {
	// 已初始化直接返回
	if toolStatusValue(currentToolStatus.Load()) == toolStatusValueInitialized {
		return
	}

	// CAS 抢占初始化权；失败表示已在初始化或已初始化，视为无操作
	if !currentToolStatus.CompareAndSwap(int32(toolStatusValueNotInitialized), int32(toolStatusValueInitializing)) {
		return
	}

	defer func() {
		if err != nil {
			currentToolStatus.Store(int32(toolStatusValueNotInitialized))
		} else {
			currentToolStatus.Store(int32(toolStatusValueInitialized))
		}
	}()

	api := tools.Get()
	if api == nil {
		return errors.New("failed to get remotetools API instance")
	}
	configData, err := getConfigData()
	if configData == nil {
		return errors.New("failed to get config data")
	}

	if err = api.LoadConfigFromBytes(configData); err != nil {
		return
	}

	appConfig := configs.GetCurrentConfig()
	if appConfig == nil {
		return errors.New("failed to get app config")
	}

	// 配置只读工具目录（若有），并设置可写工具目录
	if ro := strings.TrimSpace(appConfig.ReadOnlyToolFolder); ro != "" {
		tools.SetReadOnlyRootFolders([]string{ro})
	}

	preferredWritable := strings.TrimSpace(appConfig.ToolRootFolder)
	if preferredWritable == "" {
		preferredWritable = filepath.Join(appConfig.AppDataPath, "external_tools")
	}

	// 始终使用持久化目录作为存储目录（即便其不可执行），运行时由 remotetools 复制到临时目录执行
	if mkErr := os.MkdirAll(preferredWritable, 0o755); mkErr != nil {
		blog.GetLogger().WithError(mkErr).Warnf("无法创建工具目录 %s，外部工具功能可能受限", preferredWritable)
		logDirectoryPermissionDiagnostics(preferredWritable)
	}
	tools.SetRootFolder(preferredWritable)
	// 为不可执行场景指定临时执行目录（容器内目录，具备执行权限）
	execTmp := filepath.Join(string(os.PathSeparator), "opt", "bililive", "tmp_for_exec")
	if mkErr := os.MkdirAll(execTmp, 0o755); mkErr != nil {
		blog.GetLogger().WithError(mkErr).Warnf("无法创建临时执行目录 %s，某些外部工具可能无法运行", execTmp)
		logDirectoryPermissionDiagnostics(execTmp)
	}
	tools.SetTmpRootFolderForExecPermission(execTmp)

	err = api.StartWebUI(0)
	if err != nil {
		return
	}
	blog.GetLogger().Infoln("RemoteTools Web UI started")

	for _, toolName := range []string{
		"ffmpeg",
		"dotnet",
		"bililive-recorder",
	} {
		AsyncDownloadIfNecessary(toolName)
	}
	bilisentry.Go(func() {
		err := startBTools()
		if err != nil {
			blog.GetLogger().WithError(err).Errorln("Failed to start bililive-tools")
		}
	})

	return nil
}

func startBTools() error {
	// 设置状态为正在启动
	currentBToolsStatus.Store(int32(BToolsStatusStarting))

	// bililive-tools 依赖 node 环境
	err := DownloadIfNecessary("node")
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return fmt.Errorf("failed to install node: %w", err)
	}
	api := tools.Get()
	if api == nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return errors.New("failed to get remotetools API instance")
	}

	node, err := api.GetTool("node")
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return err
	}
	if !node.DoesToolExist() {
		err = node.Install()
		if err != nil {
			currentBToolsStatus.Store(int32(BToolsStatusFailed))
			return err
		}
	}

	btools, err := api.GetTool("biliLive-tools")
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return err
	}
	if !btools.DoesToolExist() {
		err = btools.Install()
		if err != nil {
			currentBToolsStatus.Store(int32(BToolsStatusFailed))
			return err
		}
	}

	nodeFolder := filepath.Dir(node.GetToolPath())
	btoolsFolder := filepath.Dir(btools.GetToolPath())
	env := []string{
		"PATH=" + nodeFolder + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	nodePath, err := filepath.Abs(node.GetToolPath())
	if err != nil {
		currentBToolsStatus.Store(int32(BToolsStatusFailed))
		return err
	}
	cmd := exec.Command(
		nodePath,
		"./index.cjs",
		"server",
		"-c",
		"./appConfig.json",
	)
	cmd.Dir = btoolsFolder
	cmd.Env = env
	// 动态决定是否输出，保留错误信息
	cmd.Stdout = utils.NewDebugControlledWriter(os.Stdout)
	cmd.Stderr = utils.NewLogFilterWriter(os.Stderr)

	blog.GetLogger().Infoln("Starting bililive-tools server…")

	// 设置状态为已就绪（服务已启动）
	currentBToolsStatus.Store(int32(BToolsStatusReady))

	// 在 Windows 下使用 Job Object，确保主进程退出时子进程被一并终止
	// 使用 runWithKillOnCloseAndGetPID 来获取进程 PID
	return runWithKillOnCloseAndGetPID(cmd, func(pid int) {
		// 使用通用的进程跟踪器注册子进程
		RegisterProcess("bililive-tools", pid, ProcessCategoryBTools)
		blog.GetLogger().Infof("bililive-tools process started with PID: %d", pid)
	})
}

func AsyncDownloadIfNecessary(toolName string) {
	bilisentry.Go(func() {
		err := DownloadIfNecessary(toolName)
		if err != nil {
			blog.GetLogger().Errorln("Failed to download", toolName, "tool:", err)
		}
	})
}

func DownloadIfNecessary(toolName string) (err error) {
	api := tools.Get()
	if api == nil {
		return errors.New("failed to get remotetools API instance")
	}

	tool, err := api.GetTool(toolName)
	if err != nil {
		return
	}
	if !tool.DoesToolExist() {
		err = tool.Install()
		if err != nil {
			return err
		}
	}
	blog.GetLogger().Infoln(toolName, "tool is ready to use, version:", tool.GetVersion())
	return nil
}

func GetWebUIPort() int {
	return tools.Get().GetWebUIPort()
}

func Get() *tools.API {
	return tools.Get()
}

func FixFlvByBililiveRecorder(ctx context.Context, fileName string) (outputFiles []string, err error) {
	return FixFlvByBililiveRecorderWithPID(ctx, fileName, nil)
}

// FixFlvByBililiveRecorderWithPID 使用 BililiveRecorder 修复 FLV 文件
// onPID 可选回调函数，在子进程启动后立即调用，传递子进程 PID
func FixFlvByBililiveRecorderWithPID(ctx context.Context, fileName string, onPID func(pid int)) (outputFiles []string, err error) {
	defer func() {
		if err != nil {
			blog.GetLogger().WithError(err).Warn("failed to fix flv file by bililive-recorder")
		}
	}()

	outputFiles = []string{fileName}

	// 仅处理 .flv 文件，其他类型直接跳过
	if strings.ToLower(filepath.Ext(fileName)) != ".flv" {
		return
	}

	api := tools.Get()
	if api == nil {
		err = errors.New("failed to get remotetools API instance")
		return
	}

	dotnet, err := api.GetTool("dotnet")
	if err != nil {
		return
	}
	if !dotnet.DoesToolExist() {
		err = errors.New("dotnet tool not exist")
		return
	}

	bililiveRecorder, err := api.GetTool("bililive-recorder")
	if err != nil {
		return
	}
	if !bililiveRecorder.DoesToolExist() {
		return
	}

	var cmd *exec.Cmd
	cmd, err = dotnet.CreateExecuteCmd(
		bililiveRecorder.GetToolPath(),
		"tool",
		"fix",
		fileName,
		fileName,
		"--json-indented",
	)
	if err != nil {
		return
	}

	// 使用 cmd.Start() 非阻塞启动，以便获取 PID
	stdout, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		err = fmt.Errorf("failed to create stdout pipe: %w", pipeErr)
		return
	}

	if err = cmd.Start(); err != nil {
		return
	}

	// 回调通知调用方子进程 PID
	if onPID != nil && cmd.Process != nil {
		onPID(cmd.Process.Pid)
	}

	// 读取 stdout
	out, readErr := io.ReadAll(stdout)
	if readErr != nil {
		// 仍需等待进程结束
		cmd.Wait()
		err = fmt.Errorf("failed to read stdout: %w", readErr)
		return
	}

	// 等待进程结束
	if err = cmd.Wait(); err != nil {
		return
	}

	outJson := gjson.ParseBytes(out)
	if !outJson.Exists() {
		err = fmt.Errorf("bililive-recorder returned no json: %s", string(out))
		return
	}
	if status := outJson.Get("Status").String(); strings.ToUpper(status) != "OK" {
		err = fmt.Errorf("bililive-recorder failed: %s", string(out))
		return
	}

	// 原始文件尺寸
	origStat, statErr := os.Stat(fileName)
	if statErr != nil {
		err = fmt.Errorf("stat original file failed: %w", statErr)
		return
	}
	origSize := origStat.Size()

	dir := filepath.Dir(fileName)
	base := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	ext := filepath.Ext(fileName)

	// 获取输出文件列表：优先使用 JSON 数组 Data.OutputFiles；没有则按命名规则回退
	var outFiles []string
	if of := outJson.Get("Data.OutputFiles"); of.Exists() {
		for _, v := range of.Array() {
			p := v.String()
			if p == "" {
				continue
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			outFiles = append(outFiles, p)
		}
	} else {
		cnt := int(outJson.Get("Data.OutputFileCount").Int())
		for i := 1; i <= cnt; i++ {
			name := fmt.Sprintf("%s.fix_p%03d%s", base, i, ext)
			outFiles = append(outFiles, filepath.Join(dir, name))
		}
	}

	if len(outFiles) == 0 {
		err = fmt.Errorf("no output files were generated for %s", fileName)
		return
	}

	// 计算输出文件总大小；若有任何不存在，则按失败处理
	var total int64
	var missing []string
	for _, f := range outFiles {
		st, e := os.Stat(f)
		if e != nil {
			if os.IsNotExist(e) {
				missing = append(missing, f)
				continue
			}
			// 其他错误也视为失败
			missing = append(missing, f+" ("+e.Error()+")")
			continue
		}
		total += st.Size()
	}

	if len(missing) > 0 {
		// 有缺失的分段，清理已生成的分段并报错
		for _, f := range outFiles {
			_ = os.Remove(f)
		}
		err = fmt.Errorf("some output parts are missing: %v", missing)
		return
	}

	// 判定：分段总和 >= 原始大小的 90%
	if total*10 >= origSize*9 {
		// 成功：删除原始文件
		if remErr := os.Remove(fileName); remErr != nil {
			blog.GetLogger().WithError(remErr).Warnf("failed to remove original file: %s", fileName)
		}
		// 重命名输出文件, 去掉中间的 .fix_p 部分
		// 如果输出文件只有一个，则直接使用原文件名
		if len(outFiles) == 1 {
			os.Rename(outFiles[0], fileName)
		} else {
			outputFiles = []string{}
			for _, f := range outFiles {
				newName := strings.ReplaceAll(f, ".fix_p", "")
				os.Rename(f, newName)
				outputFiles = append(outputFiles, newName)
			}
		}
		return
	}

	// 失败：删除输出分段，并返回错误
	for _, f := range outFiles {
		_ = os.Remove(f)
	}
	err = fmt.Errorf("sum of fixed parts (%d) < 90%% of original (%d)", total, origSize)
	return
}
