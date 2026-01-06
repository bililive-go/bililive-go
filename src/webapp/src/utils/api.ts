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
     * 删除文件或文件夹
     * @param path 文件相对于输出目录的路径
     */
    deleteFile(path: string) {
        return utils.requestDelete(`${BASE_URL}/file/${path}`);
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
}

export default API;
