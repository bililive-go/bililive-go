package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/sirupsen/logrus"
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

func AsyncInit() {
	go func() {
		err := Init()
		if err != nil {
			logrus.Errorln("Failed to initialize RemoteTools:", err)
		}
	}()
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

	tools.SetToolFolder(filepath.Join(appConfig.AppDataPath, "external_tools"))

	err = api.StartWebUI(0)
	if err != nil {
		return
	}
	logrus.Infoln("RemoteTools Web UI started")

	for _, toolName := range []string{
		"ffmpeg",
		"dotnet",
		"bililive-recorder",
	} {
		AsyncDownloadIfNecessary(toolName)
	}

	return nil
}

func AsyncDownloadIfNecessary(toolName string) {
	go func() {
		err := DownloadIfNecessary(toolName)
		if err != nil {
			logrus.Errorln("Failed to download", toolName, "tool:", err)
		}
	}()
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
	logrus.Infoln(toolName, "tool is ready to use, version:", tool.GetVersion())
	return nil
}

func GetWebUIPort() int {
	return tools.Get().GetWebUIPort()
}

func Get() *tools.API {
	return tools.Get()
}

func FixFlvByBililiveRecorder(ctx context.Context, fileName string) (outputFiles []string, err error) {
	defer func() {
		if err != nil {
			logrus.WithError(err).Warn("failed to fix flv file by bililive-recorder")
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
	var out []byte
	out, err = cmd.Output()
	if err != nil {
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
			logrus.WithError(remErr).Warnf("failed to remove original file: %s", fileName)
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
