package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hr3lxphr6j/requests"

	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v3"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	applog "github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/types"
)

// FIXME: remove this
func parseInfo(ctx context.Context, l live.Live) *live.Info {
	inst := instance.GetInstance(ctx)
	obj, _ := inst.Cache.Get(l)
	info := obj.(*live.Info)
	info.Listening = inst.ListenerManager.(listeners.Manager).HasListener(ctx, l.GetLiveId())
	info.Recording = inst.RecorderManager.(recorders.Manager).HasRecorder(ctx, l.GetLiveId())
	if info.HostName == "" {
		info.HostName = "获取失败"
	}
	if info.RoomName == "" {
		info.RoomName = l.GetRawUrl()
	}
	return info
}

func getAllLives(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	lives := liveSlice(make([]*live.Info, 0, 4))
	for _, v := range inst.Lives {
		lives = append(lives, parseInfo(r.Context(), v))
	}
	sort.Sort(lives)
	writeJSON(writer, lives)
}

func getLive(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	live, ok := inst.Lives[types.LiveID(vars["id"])]
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", vars["id"]),
		})
		return
	}
	writeJSON(writer, parseInfo(r.Context(), live))
}

func parseLiveAction(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	resp := commonResp{}
	live, ok := inst.Lives[types.LiveID(vars["id"])]
	if !ok {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("live id: %s can not find", vars["id"])
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}
	cfg := configs.GetCurrentConfig()
	_, err := cfg.GetLiveRoomByUrl(live.GetRawUrl())
	if err != nil {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("room : %s can not find", live.GetRawUrl())
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}
	switch vars["action"] {
	case "start":
		if err := startListening(r.Context(), live); err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = err.Error()
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		}
		if _, err := configs.SetLiveRoomListening(live.GetRawUrl(), true); err != nil {
			applog.GetLogger().Error("failed to set live room listening: " + err.Error())
		}
	case "stop":
		if err := stopListening(r.Context(), live.GetLiveId()); err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = err.Error()
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		}
		if _, err := configs.SetLiveRoomListening(live.GetRawUrl(), false); err != nil {
			applog.GetLogger().Error("failed to set live room listening: " + err.Error())
		}
	default:
		resp.ErrNo = http.StatusBadRequest
		resp.ErrMsg = fmt.Sprintf("invalid Action: %s", vars["action"])
		writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		return
	}
	writeJSON(writer, parseInfo(r.Context(), live))
}

func startListening(ctx context.Context, live live.Live) error {
	inst := instance.GetInstance(ctx)
	return inst.ListenerManager.(listeners.Manager).AddListener(ctx, live)
}

func stopListening(ctx context.Context, liveId types.LiveID) error {
	inst := instance.GetInstance(ctx)
	return inst.ListenerManager.(listeners.Manager).RemoveListener(ctx, liveId)
}

/*
	Post data example

[

	{
		"url": "http://live.bilibili.com/1030",
		"listen": true
	},
	{
		"url": "https://live.bilibili.com/493",
		"listen": true
	}

]
*/
func addLives(writer http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	info := liveSlice(make([]*live.Info, 0))
	errorMessages := make([]string, 0, 4)
	gjson.ParseBytes(b).ForEach(func(key, value gjson.Result) bool {
		isListen := value.Get("listen").Bool()
		urlStr := strings.Trim(value.Get("url").String(), " ")
		if retInfo, err := addLiveImpl(r.Context(), urlStr, isListen); err != nil {
			msg := urlStr + ": " + err.Error()
			applog.GetLogger().Error(msg)
			errorMessages = append(errorMessages, msg)
			return true
		} else {
			info = append(info, retInfo)
		}
		return true
	})
	sort.Sort(info)
	// TODO return error messages too
	writeJSON(writer, info)
}

func addLiveImpl(ctx context.Context, urlStr string, isListen bool) (info *live.Info, err error) {
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		urlStr = "https://" + urlStr
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, errors.New("can't parse url: " + urlStr)
	}
	inst := instance.GetInstance(ctx)
	needAppend := false
	liveRoom, err := configs.GetCurrentConfig().GetLiveRoomByUrl(u.String())
	if err != nil {
		liveRoom = &configs.LiveRoom{
			Url:         u.String(),
			IsListening: isListen,
		}
		needAppend = true
	}
	newLive, err := live.New(ctx, liveRoom, inst.Cache)
	if err != nil {
		return nil, err
	}
	// 记录 LiveId 到全局配置（并发安全）
	configs.SetLiveRoomId(u.String(), newLive.GetLiveId())
	if _, ok := inst.Lives[newLive.GetLiveId()]; !ok {
		inst.Lives[newLive.GetLiveId()] = newLive
		if isListen {
			inst.ListenerManager.(listeners.Manager).AddListener(ctx, newLive)
		}
		info = parseInfo(ctx, newLive)

		if needAppend {
			if liveRoom == nil {
				return nil, errors.New("liveRoom is nil, cannot append to LiveRooms")
			}
			// 使用统一的 Update 接口做 COW 并原子替换
			if _, err := configs.AppendLiveRoom(*liveRoom); err != nil {
				return nil, err
			}
		}
	}
	return info, nil
}

func removeLive(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	live, ok := inst.Lives[types.LiveID(vars["id"])]
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", vars["id"]),
		})
		return
	}
	if err := removeLiveImpl(r.Context(), live); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

func removeLiveImpl(ctx context.Context, live live.Live) error {
	inst := instance.GetInstance(ctx)
	lm := inst.ListenerManager.(listeners.Manager)
	if lm.HasListener(ctx, live.GetLiveId()) {
		if err := lm.RemoveListener(ctx, live.GetLiveId()); err != nil {
			return err
		}
	}
	delete(inst.Lives, live.GetLiveId())
	if _, err := configs.RemoveLiveRoomByUrl(live.GetRawUrl()); err != nil {
		return err
	}
	return nil
}

func getConfig(writer http.ResponseWriter, r *http.Request) {
	writeJSON(writer, configs.GetCurrentConfig())
}

func putConfig(writer http.ResponseWriter, r *http.Request) {
	config := configs.GetCurrentConfig()
	config.RefreshLiveRoomIndexCache()
	if err := config.Marshal(); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJsonWithStatusCode(writer, http.StatusOK, commonResp{
		Data: "OK",
	})
}

func getRawConfig(writer http.ResponseWriter, r *http.Request) {
	b, err := yaml.Marshal(configs.GetCurrentConfig())
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJSON(writer, map[string]string{
		"config": string(b),
	})
}

func putRawConfig(writer http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	ctx := r.Context()
	var jsonBody map[string]any
	json.Unmarshal(b, &jsonBody)
	newConfig, err := configs.NewConfigWithBytes([]byte(jsonBody["config"].(string)))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	if err := newConfig.Verify(); err != nil {
		writeJsonWithStatusCode(writer, http.StatusOK, commonResp{
			ErrNo:  1,
			ErrMsg: err.Error(),
		})
		return
	}
	oldConfig := configs.GetCurrentConfig()
	oldConfig.RefreshLiveRoomIndexCache()
	// 继承原配置的文件路径
	newConfig.File = oldConfig.File
	// 预先将旧配置中的 LiveId 迁移到新配置（相同 URL）
	oldMap := make(map[string]configs.LiveRoom, len(oldConfig.LiveRooms))
	for _, room := range oldConfig.LiveRooms {
		oldMap[room.Url] = room
	}
	for i := range newConfig.LiveRooms {
		if rOld, ok := oldMap[newConfig.LiveRooms[i].Url]; ok {
			newConfig.LiveRooms[i].LiveId = rOld.LiveId
		}
	}
	// 先设置为当前全局配置，再驱动运行态差异变更
	configs.SetCurrentConfig(newConfig)
	if err := applyLiveRoomsByConfig(ctx, oldConfig, newConfig); err != nil {
		writeJsonWithStatusCode(writer, http.StatusOK, commonResp{
			ErrNo:  1,
			ErrMsg: err.Error(),
		})
		return
	}
	if err := newConfig.Marshal(); err != nil {
		applog.GetLogger().Error("failed to save config: " + err.Error())
	}
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

func applyLiveRoomsByConfig(ctx context.Context, oldConfig *configs.Config, newConfig *configs.Config) error {
	inst := instance.GetInstance(ctx)
	newLiveRooms := newConfig.LiveRooms
	newUrlMap := make(map[string]*configs.LiveRoom)
	for index := range newLiveRooms {
		newRoom := &newLiveRooms[index]
		newUrlMap[newRoom.Url] = newRoom
		if room, err := oldConfig.GetLiveRoomByUrl(newRoom.Url); err != nil {
			// add live
			if _, err := addLiveImpl(ctx, newRoom.Url, newRoom.IsListening); err != nil {
				applog.GetLogger().Errorf("failed to add live room %s: %v", newRoom.Url, err)
			}
		} else {
			if room.LiveId == "" {
				// 如果旧配置里 LiveId 为空，尝试重新初始化
				if _, err := addLiveImpl(ctx, newRoom.Url, newRoom.IsListening); err != nil {
					applog.GetLogger().Errorf("failed to re-init live room %s: %v", newRoom.Url, err)
				}
				continue
			}
			liveInstance, ok := inst.Lives[types.LiveID(room.LiveId)]
			if !ok {
				// 如果实例不存在，也尝试重新添加，而不是直接报错返回
				if _, err := addLiveImpl(ctx, newRoom.Url, newRoom.IsListening); err != nil {
					applog.GetLogger().Errorf("failed to restore live room %s: %v", newRoom.Url, err)
				}
				continue
			}
			liveInstance.UpdateLiveOptionsbyConfig(ctx, newRoom)
			if room.IsListening != newRoom.IsListening {
				if newRoom.IsListening {
					// start listening
					if err := startListening(ctx, liveInstance); err != nil {
						applog.GetLogger().Errorf("failed to start listening for %s: %v", newRoom.Url, err)
					}
				} else {
					// stop listening
					if err := stopListening(ctx, liveInstance.GetLiveId()); err != nil {
						applog.GetLogger().Errorf("failed to stop listening for %s: %v", newRoom.Url, err)
					}
				}
			}
		}
	}
	loopRooms := oldConfig.LiveRooms
	for _, room := range loopRooms {
		if _, ok := newUrlMap[room.Url]; !ok {
			// remove live
			if room.LiveId == "" {
				continue
			}
			liveInstance, ok := inst.Lives[types.LiveID(room.LiveId)]
			if !ok {
				continue
			}
			removeLiveImpl(ctx, liveInstance)
		}
	}
	return nil
}

func getInfo(writer http.ResponseWriter, r *http.Request) {
	writeJSON(writer, consts.AppInfo)
}

func getFileInfo(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrMsg: "无效输出目录",
		})
		return
	}

	absPath, err := filepath.Abs(filepath.Join(base, path))
	if err != nil {
		writeJSON(writer, commonResp{
			ErrMsg: "无效路径",
		})
		return
	}
	if !strings.HasPrefix(absPath, base) {
		writeJSON(writer, commonResp{
			ErrMsg: "异常路径",
		})
		return
	}

	files, err := os.ReadDir(absPath)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrMsg: "获取目录失败",
		})
		return
	}

	type jsonFile struct {
		IsFolder     bool   `json:"is_folder"`
		Name         string `json:"name"`
		LastModified int64  `json:"last_modified"`
		Size         int64  `json:"size"`
	}
	jsonFiles := make([]jsonFile, len(files))
	json := struct {
		Files []jsonFile `json:"files"`
		Path  string     `json:"path"`
	}{
		Path: path,
	}
	for i, file := range files {
		info, err := file.Info()
		if err != nil {
			continue
		}
		jsonFiles[i].IsFolder = file.IsDir()
		jsonFiles[i].Name = file.Name()
		jsonFiles[i].LastModified = info.ModTime().Unix()
		if !file.IsDir() {
			jsonFiles[i].Size = info.Size()
		}
	}
	json.Files = jsonFiles

	writeJSON(writer, json)
}

func deleteFile(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path, err := url.PathUnescape(vars["path"])
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  1,
			ErrMsg: "无效的路径格式: " + err.Error(),
		})
		return
	}

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  1,
			ErrMsg: "无效输出目录",
		})
		return
	}

	absPath, err := filepath.Abs(filepath.Join(base, path))
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  1,
			ErrMsg: "无效路径",
		})
		return
	}
	if !strings.HasPrefix(absPath, base) || absPath == base {
		writeJSON(writer, commonResp{
			ErrNo:  1,
			ErrMsg: "异常路径或禁止删除根目录",
		})
		return
	}

	err = os.RemoveAll(absPath)
	if err != nil {
		var errMsg string
		if errors.Is(err, os.ErrPermission) {
			errMsg = "权限不足，无法删除该文件"
		} else if strings.Contains(err.Error(), "being used by another process") {
			errMsg = "文件正在被其他程序占用（可能正在录制中），无法删除"
		} else {
			errMsg = "删除失败: " + err.Error()
		}
		writeJSON(writer, commonResp{
			ErrNo:  1,
			ErrMsg: errMsg,
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

type renameAction struct {
	OldPath string `json:"old_path"` // 相对路径，例如 "room1/record.flv"
	NewName string `json:"new_name"` // 新文件名，例如 "new_record.flv"
}

func renameFiles(writer http.ResponseWriter, r *http.Request) {
	var req struct {
		Actions []renameAction `json:"actions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(writer, commonResp{ErrNo: 1, ErrMsg: "无效的请求格式"})
		return
	}

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 1, ErrMsg: "无效输出目录"})
		return
	}

	results := make([]string, 0)
	for _, action := range req.Actions {
		if strings.ContainsAny(action.NewName, "/\\") {
			results = append(results, fmt.Sprintf("%s: 新文件名不能包含路径分隔符", action.OldPath))
			continue
		}

		// 校验旧路径
		oldAbs, err := filepath.Abs(filepath.Join(base, action.OldPath))
		if err != nil || !strings.HasPrefix(oldAbs, base) || oldAbs == base {
			results = append(results, fmt.Sprintf("%s: 路径无效", action.OldPath))
			continue
		}

		// 构造新路径 (保持在同一目录下)
		dir := filepath.Dir(oldAbs)
		newAbs := filepath.Join(dir, action.NewName)

		// 再次校验新路径安全性 (防止通过新文件名跳出目录)
		newAbsClean, err := filepath.Abs(newAbs)
		if err != nil {
			results = append(results, fmt.Sprintf("%s: 新路径生成失败", action.OldPath))
			continue
		}
		if !strings.HasPrefix(newAbsClean, base) {
			results = append(results, fmt.Sprintf("%s: 新文件名含有非法字符", action.OldPath))
			continue
		}

		// 执行重命名
		if err := os.Rename(oldAbs, newAbs); err != nil {
			var errMsg string
			if errors.Is(err, os.ErrPermission) {
				errMsg = "权限不足"
			} else if strings.Contains(err.Error(), "being used by another process") {
				errMsg = "文件被占用"
			} else {
				errMsg = err.Error()
			}
			results = append(results, fmt.Sprintf("%s -> %s 失败: %s", action.OldPath, action.NewName, errMsg))
		}
	}

	if len(results) > 0 {
		writeJSON(writer, commonResp{
			ErrNo:  1,
			ErrMsg: strings.Join(results, "; "),
		})
	} else {
		writeJSON(writer, commonResp{Data: "OK"})
	}
}

func deleteFilesBatch(writer http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(writer, commonResp{ErrNo: 1, ErrMsg: "无效的请求格式"})
		return
	}

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 1, ErrMsg: "无效输出目录"})
		return
	}

	results := make([]string, 0)
	for _, path := range req.Paths {
		absPath, err := filepath.Abs(filepath.Join(base, path))
		if err != nil {
			results = append(results, fmt.Sprintf("%s: 无效路径", path))
			continue
		}

		// 安全检查：防止越权删除或删除根目录
		if !strings.HasPrefix(absPath, base) || absPath == base {
			results = append(results, fmt.Sprintf("%s: 禁止删除该路径", path))
			continue
		}

		err = os.RemoveAll(absPath)
		if err != nil {
			var errMsg string
			if errors.Is(err, os.ErrPermission) {
				errMsg = "权限不足"
			} else if strings.Contains(err.Error(), "being used by another process") {
				errMsg = "文件正在被录制或占用中"
			} else {
				errMsg = err.Error()
			}
			results = append(results, fmt.Sprintf("%s 失败: %s", path, errMsg))
		}
	}

	if len(results) > 0 {
		writeJSON(writer, commonResp{
			ErrNo:  1,
			ErrMsg: strings.Join(results, "; "),
		})
	} else {
		writeJSON(writer, commonResp{Data: "OK"})
	}
}

func getLiveHostCookie(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	hostCookieMap := make(map[string]*live.InfoCookie)
	keys := make([]string, 0)
	for _, v := range inst.Lives {
		urltmp, _ := url.Parse(v.GetRawUrl())
		if urltmp == nil || urltmp.Host == "" {
			continue
		}
		host := urltmp.Host
		// 统一 SoopLive 域名，避免重复存储
		if strings.Contains(host, "sooplive.co.kr") || strings.Contains(host, "afreecatv.com") {
			host = "sooplive.co.kr"
		}
		if _, ok := hostCookieMap[host]; ok {
			continue
		}
		v1, err := v.GetInfo()
		if err != nil || v1 == nil {
			continue
		}
		cfg := configs.GetCurrentConfig()
		tmp := &live.InfoCookie{Platform_cn_name: v1.Live.GetPlatformCNName(), Host: host}
		if cookie, ok := cfg.Cookies[host]; ok {
			tmp.Cookie = cookie
		}
		if acc, ok := cfg.Accounts[host]; ok {
			tmp.Username = acc.Username
			tmp.Password = acc.Password
		}
		hostCookieMap[host] = tmp
		keys = append(keys, host)
	}
	sort.Strings(keys)
	result := make([]*live.InfoCookie, 0)
	for _, v := range keys {
		result = append(result, hostCookieMap[v])
	}
	writeJSON(writer, result)
}

func putLiveHostCookie(writer http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	ctx := r.Context()
	inst := instance.GetInstance(ctx)
	data := gjson.ParseBytes(b)

	host := data.Get("Host").Str
	// 规范化 SoopLive 域名
	if strings.Contains(host, "sooplive.co.kr") || strings.Contains(host, "afreecatv.com") {
		host = "sooplive.co.kr"
	}

	cookie := data.Get("Cookie").Str
	if cookie == "" {

	} else {
		reg, _ := regexp.Compile(".*=.*")
		if !reg.MatchString(cookie) {
			writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
				ErrNo:  http.StatusBadRequest,
				ErrMsg: "cookie格式错误",
			})
			return
		}
	}
	// 使用统一 Update 接口更新 Cookies
	newCfg, err := configs.SetCookie(host, cookie)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}

	// 更新账号密码（如果有提供）
	username := data.Get("Username").Str
	password := data.Get("Password").Str
	if username != "" || password != "" {
		newCfg, _ = configs.SetAccount(host, username, password)
	}
	for _, v := range newCfg.LiveRooms {
		tmpurl, err := url.Parse(v.Url)
		if err != nil {
			applog.GetLogger().Errorf("failed to parse url %s: %v", v.Url, err)
			continue
		}
		targetHost := tmpurl.Host
		if strings.Contains(targetHost, "sooplive.co.kr") || strings.Contains(targetHost, "afreecatv.com") {
			targetHost = "sooplive.co.kr"
		}
		if targetHost != host {
			continue
		}
		live := inst.Lives[v.LiveId]
		if live == nil {
			writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
				ErrNo:  http.StatusBadRequest,
				ErrMsg: "can't find live by url: " + v.Url,
			})
			return
		}
		live.UpdateLiveOptionsbyConfig(ctx, &v)
	}
	if err := newCfg.Marshal(); err != nil {
		applog.GetLogger().Error("failed to persistence config: " + err.Error())
	}
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

func getBilibiliQrcode(writer http.ResponseWriter, r *http.Request) {
	resp, err := requests.Get("https://passport.bilibili.com/x/passport-login/web/qrcode/generate")
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJSON(writer, json.RawMessage(body))
}

func pollBilibiliLogin(writer http.ResponseWriter, r *http.Request) {
	qrcodeKey := r.URL.Query().Get("qrcode_key")
	if qrcodeKey == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "qrcode_key is required",
		})
		return
	}

	resp, err := requests.Get("https://passport.bilibili.com/x/passport-login/web/qrcode/poll", requests.Query("qrcode_key", qrcodeKey))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}

	res := gjson.ParseBytes(body)
	if res.Get("data.code").Int() == 0 {
		// 登录成功，从 Header 获取 Cookie
		cookies := resp.Header["Set-Cookie"]
		var cookieList []string
		for _, c := range cookies {
			// 精准提取 key=value 部分，避开 Expires, Path, HttpOnly 等干扰
			parts := strings.Split(c, ";")
			if len(parts) > 0 {
				kv := strings.TrimSpace(parts[0])
				if kv != "" && strings.Contains(kv, "=") {
					cookieList = append(cookieList, kv)
				}
			}
		}
		cookieStr := strings.Join(cookieList, "; ")

		// 构造返回数据
		writeJSON(writer, map[string]any{
			"code":    0,
			"message": "0",
			"data": map[string]any{
				"code":    0,
				"message": "登录成功",
				"cookie":  cookieStr,
			},
		})
		return
	}

	writeJSON(writer, json.RawMessage(body))
}

func checkBilibiliCookie(writer http.ResponseWriter, r *http.Request) {
	cookie := r.URL.Query().Get("cookie")
	if cookie == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "cookie is required",
		})
		return
	}

	cookieKVs := make(map[string]string)
	parts := strings.Split(cookie, ";")
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) == 2 {
			cookieKVs[kv[0]] = kv[1]
		}
	}

	resp, err := requests.Get("https://api.bilibili.com/x/member/web/account", requests.Cookies(cookieKVs))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}

	res := gjson.ParseBytes(body)
	code := res.Get("code").Int()
	if code == 0 {
		uname := res.Get("data.uname").String()
		writeJSON(writer, map[string]any{
			"code":    0,
			"message": "Cookie 有效",
			"data": map[string]any{
				"uname": uname,
			},
		})
	} else {
		message := res.Get("message").String()
		writeJSON(writer, map[string]any{
			"code":    code,
			"message": "Cookie 无效: " + message,
		})
	}
}

func soopliveLogin(writer http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	username := gjson.ParseBytes(b).Get("username").String()
	password := gjson.ParseBytes(b).Get("password").String()

	if username == "" || password == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "用户名和密码不能为空",
		})
		return
	}

	resp, err := requests.Post("https://login.sooplive.co.kr/app/LoginAction.php",
		requests.Form(map[string]string{
			"szWork":        "login",
			"szType":        "json",
			"szUid":         username,
			"szPassword":    password,
			"isSaveId":      "true",
			"isSavePw":      "false",
			"isSaveJoin":    "false",
			"isLoginRetain": "Y",
		}),
		requests.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		requests.Header("Origin", "https://play.sooplive.co.kr"),
		requests.Referer("https://play.sooplive.co.kr/"),
	)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "请求 SoopLive 登录失败: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "读取响应失败: " + err.Error(),
		})
		return
	}

	res := gjson.ParseBytes(body)
	if res.Get("RESULT").Int() == 1 {
		// 登录成功，从 Header 获取 Cookie
		cookies := resp.Header["Set-Cookie"]
		var cookieList []string
		for _, c := range cookies {
			parts := strings.Split(c, ";")
			if len(parts) > 0 {
				kv := strings.TrimSpace(parts[0])
				if kv != "" && strings.Contains(kv, "=") {
					cookieList = append(cookieList, kv)
				}
			}
		}
		cookieStr := strings.Join(cookieList, "; ")

		// 自动更新到内存中以便立即生效
		// 统一存储到 sooplive.co.kr，实现一份存储多处生效
		configs.SetCookie("sooplive.co.kr", cookieStr)
		configs.SetAccount("sooplive.co.kr", username, password)

		writeJSON(writer, map[string]any{
			"code":    0,
			"message": "登录成功",
			"data": map[string]any{
				"cookie": cookieStr,
			},
		})
	} else {
		writeJSON(writer, map[string]any{
			"code":    -1,
			"message": "登录失败，请检查用户名和密码",
		})
	}
}
