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
        polling: false
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
            qrcodeKey: ''
        });
    };

    handleOk = () => {
        this.stopPolling();
        this.setState({
            ModalText: '正在保存Cookie......',
            confirmLoading: true,
        });

        api.saveCookie({ Host: this.state.Host, Cookie: this.state.textView })
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

        return (
            <div>
                <Modal
                    title={"修改" + Platform_cn_name + "(" + Host + ")Cookie"}
                    visible={visible}
                    onOk={this.handleOk}
                    confirmLoading={confirmLoading}
                    onCancel={this.handleCancel}
                    width={isBilibili ? 600 : 520}
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
