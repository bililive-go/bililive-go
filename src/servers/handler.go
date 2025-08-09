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

	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v2"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
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
	room, err := inst.Config.GetLiveRoomByUrl(live.GetRawUrl())
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
			return
		} else {
			room.IsListening = true
		}
	case "stop":
		if err := stopListening(r.Context(), live.GetLiveId()); err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = err.Error()
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		} else {
			room.IsListening = false
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
		writeJSON(writer, map[string]any{
			"error": err.Error(),
		})
		return
	}
	inst := instance.GetInstance(r.Context())
	info := liveSlice(make([]*live.Info, 0))
	errorMessages := make([]string, 0, 4)
	gjson.ParseBytes(b).ForEach(func(key, value gjson.Result) bool {
		isListen := value.Get("listen").Bool()
		urlStr := strings.Trim(value.Get("url").String(), " ")
		if retInfo, err := addLiveImpl(r.Context(), urlStr, isListen); err != nil {
			msg := urlStr + ": " + err.Error()
			inst.Logger.Error(msg)
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
	liveRoom := configs.LiveRoom{
		Url:         u.String(),
		IsListening: isListen,
	}
	newLive, err := live.New(ctx, &liveRoom, inst.Cache)
	liveRoom.LiveId = newLive.GetLiveId()
	if err != nil {
		return nil, err
	}
	if _, ok := inst.Lives[newLive.GetLiveId()]; !ok {
		inst.Lives[newLive.GetLiveId()] = newLive
		if isListen {
			inst.ListenerManager.(listeners.Manager).AddListener(ctx, newLive)
		}
		info = parseInfo(ctx, newLive)

		inst.Config.LiveRooms = append(inst.Config.LiveRooms, liveRoom)
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
	inst.Config.RemoveLiveRoomByUrl(live.GetRawUrl())
	return nil
}

func getConfig(writer http.ResponseWriter, r *http.Request) {
	writeJSON(writer, instance.GetInstance(r.Context()).Config)
}

func putConfig(writer http.ResponseWriter, r *http.Request) {
	config := instance.GetInstance(r.Context()).Config
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
	b, err := yaml.Marshal(instance.GetInstance(r.Context()).Config)
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
	inst := instance.GetInstance(ctx)
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
	oldConfig := inst.Config
	oldConfig.RefreshLiveRoomIndexCache()
	inst.Config = newConfig
	newConfig.File = oldConfig.File
	if err := applyLiveRoomsByConfig(ctx, oldConfig); err != nil {
		writeJSON(writer, map[string]any{
			"error": err.Error(),
		})
		return
	}
	newConfig.Marshal()
	newConfig.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(newConfig)
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

func applyLiveRoomsByConfig(ctx context.Context, oldConfig *configs.Config) error {
	inst := instance.GetInstance(ctx)
	newConfig := inst.Config
	newLiveRooms := newConfig.LiveRooms
	newUrlMap := make(map[string]*configs.LiveRoom)
	for index := range newLiveRooms {
		newRoom := &newLiveRooms[index]
		newUrlMap[newRoom.Url] = newRoom
		if room, err := oldConfig.GetLiveRoomByUrl(newRoom.Url); err != nil {
			// add live
			if _, err := addLiveImpl(ctx, newRoom.Url, newRoom.IsListening); err != nil {
				return err
			}
		} else {
			live, ok := inst.Lives[types.LiveID(room.LiveId)]
			if !ok {
				return fmt.Errorf("live id: %s can not find", room.LiveId)
			}
			live.UpdateLiveOptionsbyConfig(ctx, newRoom)
			if room.IsListening != newRoom.IsListening {
				if newRoom.IsListening {
					// start listening
					if err := startListening(ctx, live); err != nil {
						return err
					}
				} else {
					// stop listening
					if err := stopListening(ctx, live.GetLiveId()); err != nil {
						return err
					}
				}
			}
			newRoom.LiveId = room.LiveId
		}
	}
	loopRooms := oldConfig.LiveRooms
	for _, room := range loopRooms {
		if _, ok := newUrlMap[room.Url]; !ok {
			// remove live
			live, ok := inst.Lives[types.LiveID(room.LiveId)]
			if !ok {
				return fmt.Errorf("live id: %s can not find", room.LiveId)
			}
			removeLiveImpl(ctx, live)
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

	inst := instance.GetInstance(r.Context())
	base, err := filepath.Abs(inst.Config.OutPutPath)
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

func getLiveHostCookie(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	hostCookieMap := make(map[string]*live.InfoCookie)
	keys := make([]string, 0)
	for _, v := range inst.Lives {
		urltmp, _ := url.Parse(v.GetRawUrl())
		if _, ok := hostCookieMap[urltmp.Host]; ok {
			continue
		}
		v1, _ := v.GetInfo()
		host := urltmp.Host
		if cookie, ok := inst.Config.Cookies[host]; ok {
			tmp := &live.InfoCookie{Platform_cn_name: v1.Live.GetPlatformCNName(), Host: host, Cookie: cookie}
			hostCookieMap[host] = tmp
		} else {
			tmp := &live.InfoCookie{Platform_cn_name: v1.Live.GetPlatformCNName(), Host: host}
			hostCookieMap[host] = tmp
		}
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
	if inst.Config.Cookies == nil {
		inst.Config.Cookies = make(map[string]string)
	}
	inst.Config.Cookies[host] = cookie
	for _, v := range inst.Config.LiveRooms {
		tmpurl, _ := url.Parse(v.Url)
		if tmpurl.Host != host {
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
	inst.Config.Marshal()
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}
