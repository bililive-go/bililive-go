/*
 * @Author: Jmeow
 * @Date: 2020-01-28 11:25:44
 * @Description: common utils
 */

function customFetch(arg1: Parameters<typeof fetch>[0], ...args: any[]) {
    return new Promise((resolve, reject) => {
        fetch.call(null, arg1, ...args)
            .then(rsp => {
                if (rsp.ok) {
                    return rsp.json();
                } else {
                    const clonedRsp = rsp.clone();
                    return rsp.json()
                        .catch(err => {
                            return clonedRsp
                                .text()
                                .then((err: any) => {
                                    let message = "";
                                    if (err) {
                                        if (err.err_msg) {
                                            message = err.err_msg;
                                        } else {
                                            message = err;
                                        }
                                    }
                                    return message;
                                })
                                .catch(err => rsp.statusText)
                                .then(data => {
                                    reject(data);
                                    throw (data);
                                });
                        });
                }
            }).then(data => {
                resolve(data);
            }).catch(err => {
                Utils.alertError();
                reject(err);
            });
    });
}

class Utils {
    /**
     * Get request
     * @param url URL
     */
    requestGet(url: string) {
        return customFetch(url);
    }

    /**
     * Post request
     * @param url URL
     * @param body Request body
     */
    requestPost(url: string, body?: object) {
        return customFetch(url, {
            method: 'POST',
            body: JSON.stringify(body),
            headers: new Headers({
                'Content-Type': 'application/json'
            })
        });
    }

    /**
     * Post request
     * @param url URL
     * @param body Request body
     */
    requestPut(url: string, body?: object) {
        return customFetch(url, {
            method: 'PUT',
            body: JSON.stringify(body),
            headers: new Headers({
                'Content-Type': 'application/json'
            })
        })
    }

    /**
     * Delete request
     * @param url URL
     */
    requestDelete(url: string) {
        return customFetch(url, {
            method: 'DELETE'
        });
    }

    /**
     * Show Error 
     * @param err error Object
     */
    static alertError(err?: any) {
        console.error(err ? err : "Server Error!");
    }

    static byteSizeToHumanReadableFileSize(size: number): string {
        if (!size) {
            return "0";
        }
        const i = Math.floor(Math.log(size) / Math.log(1024));
        const ret = Number((size / Math.pow(1024, i)).toFixed(2)) + " " + ['B', 'kB', 'MB', 'GB', 'TB'][i];
        return ret;
    }

    static timestampToHumanReadable(timestamp: number): string {
        const date = new Date(timestamp * 1000);
        const year = date.getFullYear().toString().padStart(4, "0");
        const month = (date.getMonth() + 1).toString().padStart(2, "0");
        const day = date.getDate().toString().padStart(2, "0");
        const hour = date.getHours().toString().padStart(2, "0");
        const min = date.getMinutes().toString().padStart(2, "0");
        const sec = date.getSeconds().toString().padStart(2, "0");
        return `${year}-${month}-${day} ${hour}:${min}:${sec}`;
    }
}

export default Utils;
