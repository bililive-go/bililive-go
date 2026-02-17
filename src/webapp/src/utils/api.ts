/*
 * @Author: Jmeow
 * @Date: 2020-01-28 15:30:50
 * @Description: common API
 */

import Utils from './common';
import { StreamPreferenceV2 } from '../types/stream';

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
     * 重命名文件或文件夹
     * @param path 原路径
     * @param newName 新名称（不含后缀）
     */
    renameFile(path: string, newName: string) {
        return utils.requestPut(`${BASE_URL}/file/${path}`, { new_name: newName });
    }

    /**
     * 删除文件或文件夹
     * @param path 路径
     */
    deleteFile(path: string) {
        return utils.requestDelete(`${BASE_URL}/file/${path}`);
    }

    batchRenameFiles(paths: string[], find: string, replace: string) {
        return utils.requestPut(`${BASE_URL}/batch/file/rename`, { paths, find, replace });
    }

    batchDeleteFiles(paths: string[]) {
        return utils.requestPost(`${BASE_URL}/batch/file/delete`, { paths });
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

    /**
     * 获取远程 WebUI 状态
     * 返回当前程序版本对应的云端 WebUI 信息
     */
    getRemoteWebuiStatus() {
        return utils.requestGet(`${BASE_URL}/webui/remote/status`);
    }

    /**
     * 检查是否有更新的云端 WebUI 可用
     * 比较本地 WebUI 版本和云端可用版本
     */
    checkWebuiUpdate() {
        return utils.requestGet(`${BASE_URL}/webui/remote/check`);
    }

    /**
     * 获取哔哩哔哩登录二维码
     */
    getBilibiliQRCode() {
        return utils.requestGet(`${BASE_URL}/bilibili/qrcode`);
    }

    /**
     * 轮询哔哩哔哩登录状态
     * @param key qrcode_key
     */
    pollBilibiliQRCode(key: string) {
        return utils.requestGet(`${BASE_URL}/bilibili/qrcode/poll?key=${key}`);
    }

    /**
     * 验证哔哩哔哩 Cookie
     * @param cookie cookie 字符串
     */
    verifyBilibiliCookie(cookie: string) {
        return utils.requestPost(`${BASE_URL}/bilibili/cookie/verify`, { cookie });
    }

    /**
     * 切换直播间的流设置
     * 更新直播间的流配置并重启录制（如果正在录制中）
     * @param liveId 直播间ID
     * @param streamConfig 流设置，支持新格式 { quality?: string, attributes?: {...} } 或旧格式 { format?: string, quality?: string }
     */
    switchStream(liveId: string, streamConfig: StreamPreferenceV2 | { format?: string; quality?: string }) {
        return utils.requestPost(`${BASE_URL}/lives/${liveId}/switchStream`, streamConfig);
    }

    // ==================== 程序更新 API ====================

    /**
     * 检查程序更新
     * @param includePrerelease 是否包含预发布版本
     */
    checkProgramUpdate(includePrerelease: boolean = false) {
        return utils.requestGet(`${BASE_URL}/update/check?prerelease=${includePrerelease}`);
    }

    /**
     * 获取最新版本信息
     * @param includePrerelease 是否包含预发布版本
     */
    getLatestRelease(includePrerelease: boolean = false) {
        return utils.requestGet(`${BASE_URL}/update/latest?prerelease=${includePrerelease}`);
    }

    /**
     * 开始下载程序更新
     */
    downloadProgramUpdate() {
        return utils.requestPost(`${BASE_URL}/update/download`, {});
    }

    /**
     * 获取更新状态和下载进度
     */
    getUpdateStatus() {
        return utils.requestGet(`${BASE_URL}/update/status`);
    }

    /**
     * 应用更新
     * @param options 更新选项
     * @param options.gracefulWait 是否等待所有录制结束后再更新
     * @param options.forceNow 是否立即强制更新（会中断录制）
     */
    applyUpdate(options: { gracefulWait?: boolean; forceNow?: boolean } = {}) {
        return utils.requestPost(`${BASE_URL}/update/apply`, {
            graceful_wait: options.gracefulWait || false,
            force_now: options.forceNow || false
        });
    }

    /**
     * 取消更新下载或优雅更新等待
     */
    cancelUpdate() {
        return utils.requestPost(`${BASE_URL}/update/cancel`, {});
    }

    /**
     * 获取启动器状态
     */
    getLauncherStatus() {
        return utils.requestGet(`${BASE_URL}/update/launcher`);
    }

    /**
     * 获取回滚信息
     * 检查是否有可用的备份版本可以回滚
     */
    getRollbackInfo() {
        return utils.requestGet(`${BASE_URL}/update/rollback`);
    }

    /**
     * 执行版本回滚
     * 将当前版本切换为备份版本并重启
     */
    doRollback() {
        return utils.requestPost(`${BASE_URL}/update/rollback`, {});
    }

    /**
     * 设置更新通道
     * @param channel 更新通道：stable 或 prerelease
     */
    setUpdateChannel(channel: 'stable' | 'prerelease') {
        return utils.requestPut(`${BASE_URL}/update/channel`, { channel });
    }
}

export default API;

