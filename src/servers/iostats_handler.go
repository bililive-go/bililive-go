package servers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/pkg/iostats"
)

// getIOStats 获取 IO 统计数据
func getIOStats(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	if inst == nil || inst.IOStatsModule == nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块未启用",
		})
		return
	}

	module, ok := inst.IOStatsModule.(*iostats.Module)
	if !ok {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块类型错误",
		})
		return
	}

	store := module.GetStore()

	// 解析查询参数
	query := iostats.IOStatsQuery{}

	if startTimeStr := r.URL.Query().Get("start_time"); startTimeStr != "" {
		if v, err := strconv.ParseInt(startTimeStr, 10, 64); err == nil {
			query.StartTime = v
		}
	}

	if endTimeStr := r.URL.Query().Get("end_time"); endTimeStr != "" {
		if v, err := strconv.ParseInt(endTimeStr, 10, 64); err == nil {
			query.EndTime = v
		}
	}

	if statTypesStr := r.URL.Query().Get("stat_types"); statTypesStr != "" {
		for _, st := range strings.Split(statTypesStr, ",") {
			query.StatTypes = append(query.StatTypes, iostats.StatType(strings.TrimSpace(st)))
		}
	}

	query.LiveID = r.URL.Query().Get("live_id")
	query.Platform = r.URL.Query().Get("platform")
	query.Aggregation = r.URL.Query().Get("aggregation")

	// 查询数据
	stats, err := store.QueryIOStats(r.Context(), query)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "查询 IO 统计失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: iostats.IOStatsResponse{
			Stats: stats,
		},
	})
}

// getRequestStatus 获取请求状态统计
func getRequestStatus(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	if inst == nil || inst.IOStatsModule == nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块未启用",
		})
		return
	}

	module, ok := inst.IOStatsModule.(*iostats.Module)
	if !ok {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块类型错误",
		})
		return
	}

	store := module.GetStore()

	// 解析查询参数
	query := iostats.RequestStatusQuery{}

	if startTimeStr := r.URL.Query().Get("start_time"); startTimeStr != "" {
		if v, err := strconv.ParseInt(startTimeStr, 10, 64); err == nil {
			query.StartTime = v
		}
	}

	if endTimeStr := r.URL.Query().Get("end_time"); endTimeStr != "" {
		if v, err := strconv.ParseInt(endTimeStr, 10, 64); err == nil {
			query.EndTime = v
		}
	}

	query.ViewMode = iostats.ViewMode(r.URL.Query().Get("view_mode"))
	if query.ViewMode == "" {
		query.ViewMode = iostats.ViewModeGlobal
	}

	query.LiveID = r.URL.Query().Get("live_id")
	query.Platform = r.URL.Query().Get("platform")

	// 查询数据
	response, err := store.QueryRequestStatusSegments(r.Context(), query)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "查询请求状态失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: response,
	})
}

// getIOStatsFilters 获取 IO 统计筛选器选项
func getIOStatsFilters(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	if inst == nil || inst.IOStatsModule == nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块未启用",
		})
		return
	}

	module, ok := inst.IOStatsModule.(*iostats.Module)
	if !ok {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块类型错误",
		})
		return
	}

	store := module.GetStore()

	// 查询筛选器选项
	filters, err := store.GetFilters(r.Context())
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "获取筛选器失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: filters,
	})
}

// getDiskIOStats 获取系统磁盘 I/O 统计
func getDiskIOStats(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	if inst == nil || inst.IOStatsModule == nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块未启用",
		})
		return
	}

	module, ok := inst.IOStatsModule.(*iostats.Module)
	if !ok {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块类型错误",
		})
		return
	}

	store := module.GetStore()

	// 解析查询参数
	query := iostats.DiskIOQuery{}

	if startTimeStr := r.URL.Query().Get("start_time"); startTimeStr != "" {
		if v, err := strconv.ParseInt(startTimeStr, 10, 64); err == nil {
			query.StartTime = v
		}
	}

	if endTimeStr := r.URL.Query().Get("end_time"); endTimeStr != "" {
		if v, err := strconv.ParseInt(endTimeStr, 10, 64); err == nil {
			query.EndTime = v
		}
	}

	query.DeviceName = r.URL.Query().Get("device")

	// 查询数据
	stats, err := store.QueryDiskIOStats(r.Context(), query)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "查询磁盘 I/O 统计失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: map[string]interface{}{
			"stats": stats,
		},
	})
}

// getDiskDevices 获取可用的磁盘设备列表
func getDiskDevices(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	if inst == nil || inst.IOStatsModule == nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块未启用",
		})
		return
	}

	module, ok := inst.IOStatsModule.(*iostats.Module)
	if !ok {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块类型错误",
		})
		return
	}

	store := module.GetStore()

	devices, err := store.GetDiskDevices(r.Context())
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "获取磁盘设备列表失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: map[string]interface{}{
			"devices": devices,
		},
	})
}

// getMemoryStatsHistory 获取内存统计历史数据
func getMemoryStatsHistory(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	if inst == nil || inst.IOStatsModule == nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块未启用",
		})
		return
	}

	module, ok := inst.IOStatsModule.(*iostats.Module)
	if !ok {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块类型错误",
		})
		return
	}

	store := module.GetStore()

	// 解析查询参数
	query := iostats.MemoryStatsQuery{}

	if startTimeStr := r.URL.Query().Get("start_time"); startTimeStr != "" {
		if v, err := strconv.ParseInt(startTimeStr, 10, 64); err == nil {
			query.StartTime = v
		}
	}

	if endTimeStr := r.URL.Query().Get("end_time"); endTimeStr != "" {
		if v, err := strconv.ParseInt(endTimeStr, 10, 64); err == nil {
			query.EndTime = v
		}
	}

	// 解析类别列表
	if categoriesStr := r.URL.Query().Get("categories"); categoriesStr != "" {
		for _, cat := range strings.Split(categoriesStr, ",") {
			query.Categories = append(query.Categories, strings.TrimSpace(cat))
		}
	}

	query.Aggregation = r.URL.Query().Get("aggregation")

	// 查询数据
	response, err := store.QueryMemoryStats(r.Context(), query)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "查询内存统计失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: response,
	})
}

// getMemoryCategories 获取可用的内存统计类别列表
func getMemoryCategories(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	if inst == nil || inst.IOStatsModule == nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块未启用",
		})
		return
	}

	module, ok := inst.IOStatsModule.(*iostats.Module)
	if !ok {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "IO 统计模块类型错误",
		})
		return
	}

	store := module.GetStore()

	categories, err := store.GetMemoryCategories(r.Context())
	if err != nil {
		writeJSON(writer, commonResp{
			ErrNo:  -1,
			ErrMsg: "获取内存类别列表失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: map[string]interface{}{
			"categories": categories,
		},
	})
}
