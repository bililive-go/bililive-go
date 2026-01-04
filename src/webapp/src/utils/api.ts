/*
 * @Author: Jmeow
 * @Date: 2020-01-28 15:30:50
 * @Description: common API
 */

import Utils from './common';

const utils = new Utils();

const BASE_URL = "api";

class API {
    /**
     * 获取录播机状态
     */
    getLiveInfo() {
        return utils.requestGet(`${BASE_URL}/info`);
    }

    /**
     * 获取直播间列表
     */
    getRoomList() {
        return utils.requestGet(`${BASE_URL}/lives`);
    }

    /**
     * 添加新的直播间
     * @param url URL
     */
    addNewRoom(url: string) {
        const reqBody = [
            {
                "url": url,
                "listen": true
            }
        ];
        return utils.requestPost(`${BASE_URL}/lives`, reqBody);
    }

    /**
     * 删除直播间
     * @param id 直播间id
     */
    deleteRoom(id: string) {
        return utils.requestDelete(`${BASE_URL}/lives/${id}`);
    }

    /**
     * 开始监听直播间
     * @param id 直播间id
     */
    startRecord(id: string) {
        return utils.requestGet(`${BASE_URL}/lives/${id}/start`);
    }

    /**
     * 停止监听直播间
     * @param id 直播间id
     */
    stopRecord(id: string) {
        return utils.requestGet(`${BASE_URL}/lives/${id}/stop`);
    }

    /**
     * 保存设置至config文件
     */
    saveSettings() {
        return utils.requestPut(`${BASE_URL}/config`);
    }

    /**
     * 保存设置至config文件，且不处理返回结果
     */
    saveSettingsInBackground() {
        this.saveSettings()
            .then((rsp: any) => {
                if (rsp.err_no === 0) {
                    console.log('Save Settings success !!');
                } else {
                    console.log('Server Error !!');
                }
            })
            .catch(err => {
                alert(`保存设置失败:\n${err}`);
            })
    }

    /**
     * 获取设置明文
     */
    getConfigInfo() {
        return utils.requestGet(`${BASE_URL}/raw-config`);
    }

    /**
     * 保存设置明文
     * @param json \{config: "yaml格式的设置原文"\}
     */
    saveRawConfig(json: any) {
        return utils.requestPut(`${BASE_URL}/raw-config`, json);
    }

    /**
     *
     * @param path 获取文件目录
     */
    getFileList(path: string = "") {
        return utils.requestGet(`${BASE_URL}/file/${path}`);
    }

    /**
     * 获取Cookie列表
     */
    getCookieList() {
        return utils.requestGet(`${BASE_URL}/cookies`);
    }

    /**
     * 保存Cookie
     * @param json {"Host":"","Cookie":""}
     */
    saveCookie(json: any) {
        return utils.requestPut(`${BASE_URL}/cookies`, json);
    }

    /**
     * 获取直播间详细信息和有效配置
     * @param id 直播间id
     */
    getLiveDetail(id: string) {
        return utils.requestGet(`${BASE_URL}/lives/${id}`);
    }

    /**
     * 获取直播间最近日志
     * @param id 直播间id
     * @param lines 日志行数，默认100行
     */
    getLiveLogs(id: string, lines: number = 100) {
        return utils.requestGet(`${BASE_URL}/lives/${id}/logs?lines=${lines}`);
    }

    /**
     * 获取实际生效的配置值（用于GUI模式显示）
     */
    getEffectiveConfig() {
        return utils.requestGet(`${BASE_URL}/config/effective`);
    }

    /**
     * 获取平台统计信息
     */
    getPlatformStats() {
        return utils.requestGet(`${BASE_URL}/config/platforms`);
    }

    /**
     * 更新全局配置（部分更新）
     * @param updates 要更新的配置项
     */
    updateConfig(updates: any) {
        return utils.requestPatch(`${BASE_URL}/config`, updates);
    }

    /**
     * 更新平台配置
     * @param platformKey 平台标识
     * @param updates 要更新的配置项
     */
    updatePlatformConfig(platformKey: string, updates: any) {
        return utils.requestPatch(`${BASE_URL}/config/platforms/${platformKey}`, updates);
    }

    /**
     * 删除平台配置
     * @param platformKey 平台标识
     */
    deletePlatformConfig(platformKey: string) {
        return utils.requestDelete(`${BASE_URL}/config/platforms/${platformKey}`);
    }

    /**
     * 更新直播间配置
     * @param roomUrl 直播间URL
     * @param updates 要更新的配置项
     */
    updateRoomConfig(roomUrl: string, updates: any) {
        return utils.requestPatch(`${BASE_URL}/config/rooms/${encodeURIComponent(roomUrl)}`, updates);
    }

    /**
     * 通过 ID 更新直播间配置
     * @param liveId 直播间ID
     * @param updates 要更新的配置项
     */
    updateRoomConfigById(liveId: string, updates: any) {
        return utils.requestPatch(`${BASE_URL}/config/rooms/id/${liveId}`, updates);
    }

    /**
     * 预览输出模板生成的路径
     * @param template 模板字符串
     * @param outPutPath 输出路径
     */
    previewOutputTemplate(template: string, outPutPath: string) {
        return utils.requestPost(`${BASE_URL}/config/preview-template`, {
            template,
            out_put_path: outPutPath
        });
    }

    /**
     * 强制刷新直播间信息
     * 忽略平台访问频率限制，立即获取最新信息
     * @param liveId 直播间ID
     */
    forceRefreshLive(liveId: string) {
        return utils.requestGet(`${BASE_URL}/lives/${liveId}/forceRefresh`);
    }

    /**
     * 获取直播间历史事件（统一接口，支持分页和筛选）
     * @param liveId 直播间ID
     * @param options 查询选项
     */
    getLiveHistory(liveId: string, options?: {
        page?: number;
        pageSize?: number;
        startTime?: number; // Unix timestamp
        endTime?: number;   // Unix timestamp
        types?: string[];   // 事件类型: 'session', 'name_change'
    }) {
        const params = new URLSearchParams();
        if (options?.page) params.append('page', String(options.page));
        if (options?.pageSize) params.append('page_size', String(options.pageSize));
        if (options?.startTime) params.append('start_time', String(options.startTime));
        if (options?.endTime) params.append('end_time', String(options.endTime));
        if (options?.types) {
            options.types.forEach(t => params.append('type', t));
        }
        const queryString = params.toString();
        const url = `${BASE_URL}/lives/${liveId}/history${queryString ? '?' + queryString : ''}`;
        return utils.requestGet(url);
    }
}

export default API;
