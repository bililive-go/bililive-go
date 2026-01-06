import { Modal, Input, notification, Button, Spin, Alert } from 'antd';
import React from 'react';
import API from '../../utils/api';
import './edit-cookie.css'

const api = new API();

interface Props {
    refresh?: any
}

const { TextArea } = Input

class EditCookieDialog extends React.Component<Props> {
    state = {
        ModalText: '请输入Cookie',
        visible: false,
        confirmLoading: false,
        textView: '',
        alertVisible: false,
        errorInfo: '',
        Host: '',
        Platform_cn_name: '',
        // 扫码登录相关
        qrCodeVisible: false,
        qrCodeUrl: '',
        qrcodeKey: '',
        qrStatus: '', // 'idle', 'loading', 'waiting', 'scanned', 'confirmed', 'expired', 'error'
        qrMessage: '',
        polling: false,
        // SoopLive 登录相关
        soopUsername: '',
        soopPassword: '',
        soopLoginLoading: false
    };

    pollTimer: any = null;

    componentWillUnmount() {
        this.stopPolling();
    }

    showModal = (data: any) => {
        var tmpcookie = data.Cookie
        if (!tmpcookie) {
            tmpcookie = ""
        }
        this.setState({
            ModalText: '请输入Cookie',
            visible: true,
            confirmLoading: false,
            textView: tmpcookie,
            alertVisible: false,
            errorInfo: '',
            Host: data.Host,
            Platform_cn_name: data.Platform_cn_name,
            qrCodeVisible: false,
            qrStatus: 'idle',
            qrCodeUrl: '',
            qrcodeKey: '',
            soopUsername: data.Username || '',
            soopPassword: data.Password || '',
            soopLoginLoading: false
        });
    };

    handleSoopLogin = () => {
        const { soopUsername, soopPassword } = this.state;
        if (!soopUsername || !soopPassword) {
            notification.error({ message: '请输入用户名和密码' });
            return;
        }

        this.setState({ soopLoginLoading: true });
        api.soopliveLogin({ username: soopUsername, password: soopPassword })
            .then((res: any) => {
                this.setState({ soopLoginLoading: false });
                if (res.code === 0) {
                    this.setState({ textView: res.data.cookie });
                    notification.success({ message: 'SoopLive 登录及 Cookie 获取成功' });
                } else {
                    notification.error({ message: res.message || '登录失败' });
                }
            })
            .catch(err => {
                this.setState({ soopLoginLoading: false });
                notification.error({ message: '登录请求失败: ' + err.message });
            });
    }

    handleOk = () => {
        this.stopPolling();
        this.setState({
            ModalText: '正在保存Cookie......',
            confirmLoading: true,
        });

        api.saveCookie({
            Host: this.state.Host,
            Cookie: this.state.textView,
            Username: this.state.soopUsername,
            Password: this.state.soopPassword
        })
            .then((rsp) => {
                // 保存设置
                api.saveSettingsInBackground();
                this.setState({
                    visible: false,
                    confirmLoading: false,
                    textView: '',
                    Host: '',
                    Platform_cn_name: ''
                });
                this.props.refresh();
                notification.open({
                    message: '保存成功',
                });
            })
            .catch(err => {
                alert(`保存Cookie失败:\n${err}`);
                this.setState({
                    visible: false,
                    confirmLoading: false,
                    textView: ''
                });
            })
    };

    handleCancel = () => {
        this.stopPolling();
        this.setState({
            visible: false,
            textView: '',
            alertVisible: false,
            errorInfo: '',
            Host: '',
            Platform_cn_name: '',
            qrCodeVisible: false
        });
    };

    startQrLogin = () => {
        this.setState({ qrCodeVisible: true, qrStatus: 'loading', qrMessage: '正在获取二维码...' });
        api.getBilibiliQrcode()
            .then((res: any) => {
                if (res.code === 0) {
                    this.setState({
                        qrCodeUrl: res.data.url,
                        qrcodeKey: res.data.qrcode_key,
                        qrStatus: 'waiting',
                        qrMessage: '请使用 Bilibili 手机客户端扫码'
                    });
                    this.startPolling(res.data.qrcode_key);
                } else {
                    this.setState({ qrStatus: 'error', qrMessage: '获取二维码失败: ' + res.message });
                }
            })
            .catch(err => {
                this.setState({ qrStatus: 'error', qrMessage: '网络错误' });
            });
    }

    startPolling = (key: string) => {
        this.stopPolling();
        this.setState({ polling: true });
        this.pollTimer = setInterval(() => {
            api.pollBilibiliLogin(key)
                .then((res: any) => {
                    const data = res.data;
                    if (data.code === 0) {
                        // 登录成功
                        this.stopPolling();
                        this.setState({
                            textView: data.cookie,
                            qrStatus: 'confirmed',
                            qrMessage: '登录成功！',
                            qrCodeVisible: false
                        });
                        notification.success({ message: '扫码登录成功' });
                    } else if (data.code === 86090) {
                        this.setState({ qrStatus: 'scanned', qrMessage: '已扫码，请在手机上确认' });
                    } else if (data.code === 86038) {
                        this.stopPolling();
                        this.setState({ qrStatus: 'expired', qrMessage: '二维码已失效，请重新获取' });
                    } else if (data.code === 86101) {
                        this.setState({ qrStatus: 'waiting', qrMessage: '请使用 Bilibili 手机客户端扫码' });
                    }
                })
                .catch(err => {
                    console.error('Polling error', err);
                });
        }, 2000);
    }

    stopPolling = () => {
        if (this.pollTimer) {
            clearInterval(this.pollTimer);
            this.pollTimer = null;
        }
        this.setState({ polling: false });
    }

    checkCookie = () => {
        const { textView } = this.state;
        if (!textView) return;
        this.setState({ qrStatus: 'checking', qrMessage: '正在验证 Cookie...' });
        api.checkBilibiliCookie(textView)
            .then((res: any) => {
                if (res.code === 0) {
                    notification.success({
                        message: 'Cookie 有效',
                        description: `当前登录用户：${res.data.uname}`
                    });
                } else {
                    notification.error({
                        message: 'Cookie 无效',
                        description: res.message
                    });
                }
            })
            .catch(err => {
                notification.error({
                    message: '验证失败',
                    description: '网络错误或后端服务异常'
                });
            });
    }

    textChange = (e: any) => {
        const val = e.target.value;
        this.setState({
            textView: val,
            alertVisible: false,
            errorInfo: ''
        })
        let cookiearr = val.split(";")
        cookiearr.forEach((cookie: string) => {
            if (cookie.trim() === "") return;
            if (cookie.indexOf("=") === -1) {
                this.setState({ alertVisible: true, errorInfo: 'cookie格式错误' })
                return
            }
            if (cookie.indexOf("expire") > -1) {
                //可能是cookie过期时间
                let parts = cookie.split("=");
                if (parts.length < 2) return;
                let value = parts[1].trim();
                let tmpdate
                if (value.indexOf("-") > -1) {
                    //可能是日期格式
                    tmpdate = new Date(value)
                } else if (value.length === 10) {
                    tmpdate = new Date(parseInt(value) * 1000)
                } else if (value.length === 13) {
                    tmpdate = new Date(parseInt(value))
                }
                if (tmpdate) {
                    if (tmpdate < new Date()) {
                        this.setState({ alertVisible: true, errorInfo: 'cookie可能已经过期' })
                    }
                }
            }
        })
    }

    render() {
        const { visible, confirmLoading, ModalText, textView, alertVisible, errorInfo,
            Host, Platform_cn_name, qrCodeVisible, qrCodeUrl, qrMessage, qrStatus } = this.state;

        const isBilibili = Host === 'live.bilibili.com';
        const isSoop = Host === 'sooplive.co.kr' || Host === 'play.sooplive.co.kr' || Host === 'www.sooplive.co.kr' || Host === 'play.afreecatv.com' || Host === 'www.afreecatv.com';

        return (
            <div>
                <Modal
                    title={"修改" + Platform_cn_name + "(" + Host + ")Cookie"}
                    visible={visible}
                    onOk={this.handleOk}
                    confirmLoading={confirmLoading}
                    onCancel={this.handleCancel}
                    width={isBilibili || isSoop ? 600 : 520}
                >
                    <p>{ModalText}</p>
                    {isBilibili && (
                        <div style={{ marginBottom: 16 }}>
                            <Button type="primary" onClick={this.startQrLogin} disabled={qrStatus === 'loading' || this.state.polling}>
                                {this.state.polling ? '正在等待扫码...' : 'B 站扫码登录'}
                            </Button>
                            <Button style={{ marginLeft: 8 }} onClick={this.checkCookie} disabled={!textView}>
                                验证 Cookie
                            </Button>
                            <div style={{ marginTop: 12 }}>
                                <Alert
                                    message="获取方式建议"
                                    description={
                                        <div style={{ fontSize: '12px' }}>
                                            <p style={{ marginBottom: 4 }}>通常推荐 <b>扫码登录</b>，快速且稳定。</p>
                                            <p style={{ marginBottom: 4 }}>但在以下情况建议 <b>手动从浏览器 F12 获取</b>：</p>
                                            <ul style={{ paddingLeft: 16, marginBottom: 4 }}>
                                                <li><b>画质受限</b>：如无法开启 4K/高帧率（需缺失的 buvid 字段）</li>
                                                <li><b>触发风控</b>：报错 412 或频繁验证（需同步浏览器环境）</li>
                                                <li><b>解析失败</b>：弹幕获取失败或 WBI 签名校验不通过</li>
                                            </ul>
                                            <p style={{ marginBottom: 0, color: '#888' }}>方法：浏览器 B 站 → F12 → 网络 → 刷新 → 复制请求头中的 Cookie。</p>
                                        </div>
                                    }
                                    type="info"
                                    showIcon
                                />
                            </div>
                            {qrCodeVisible && (
                                <div style={{ marginTop: 16, textAlign: 'center', border: '1px solid #eee', padding: 16, borderRadius: 8 }}>
                                    {qrStatus === 'loading' ? (
                                        <Spin tip="加载二维码..." />
                                    ) : (
                                        <div>
                                            {qrCodeUrl && (
                                                <div style={{ marginBottom: 8 }}>
                                                    <img
                                                        src={`https://api.qrserver.com/v1/create-qr-code/?size=180x180&data=${encodeURIComponent(qrCodeUrl)}`}
                                                        alt="QR Code"
                                                        style={{ width: 180, height: 180 }}
                                                    />
                                                </div>
                                            )}
                                            <Alert message={qrMessage} type={qrStatus === 'expired' ? 'error' : qrStatus === 'error' ? 'error' : 'info'} showIcon />
                                            {(qrStatus === 'expired' || qrStatus === 'error') && (
                                                <Button size="small" style={{ marginTop: 8 }} onClick={this.startQrLogin}>重试</Button>
                                            )}
                                        </div>
                                    )}
                                </div>
                            )}
                        </div>
                    )}
                    {isSoop && (
                        <div style={{ marginBottom: 16, border: '1px solid #f0f0f0', padding: '12px', borderRadius: '4px', backgroundColor: '#fafafa' }}>
                            <div style={{ display: 'flex', gap: '8px', marginBottom: '8px' }}>
                                <Input
                                    placeholder="Soop 用户名"
                                    value={this.state.soopUsername}
                                    onChange={(e) => this.setState({ soopUsername: e.target.value })}
                                />
                                <Input.Password
                                    placeholder="密码"
                                    value={this.state.soopPassword}
                                    onChange={(e) => this.setState({ soopPassword: e.target.value })}
                                />
                                <Button type="primary" onClick={this.handleSoopLogin} loading={this.state.soopLoginLoading}>
                                    登录并获取 Cookie
                                </Button>
                            </div>
                            <Alert
                                type="info"
                                showIcon
                                message="自动获取说明"
                                description="输入账号密码后点击登录获取 Cookie 。适用于 19+ 房间录制。"
                            />
                        </div>
                    )}
                    <TextArea
                        autoSize={{ minRows: 2, maxRows: 6 }}
                        value={textView}
                        placeholder="请输入Cookie"
                        onChange={this.textChange}
                        allowClear
                    />
                    <div id="errorinfo" className={alertVisible ? 'word-style' : 'word-style:hide'}>{errorInfo}</div>
                </Modal>
            </div>
        );
    }
}
export default EditCookieDialog;
