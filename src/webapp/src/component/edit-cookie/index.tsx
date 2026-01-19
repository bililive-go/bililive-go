import { Modal, Input, notification, Divider } from 'antd';
import React from 'react';
import API from '../../utils/api';
import BiliLoginPanel from './bili-login-panel';
import './edit-cookie.css'

const { TextArea } = Input;

interface IProps {
    refresh: () => void;
}

class EditCookieDialog extends React.Component<IProps> {
    state = {
        visible: false,
        confirmLoading: false,
        textView: '',
        Host: '',
        Platform_cn_name: '',
        modalKey: 0
    };

    api = new API();

    showModal = (data: any) => {
        this.setState({
            visible: true,
            confirmLoading: false,
            textView: data.Cookie || '',
            Host: data.Host,
            Platform_cn_name: data.Platform_cn_name,
            modalKey: Date.now()
        });
    };

    handleCookieChange = (val: string) => {
        this.setState({ textView: val });
    };

    handleOk = () => {
        this.setState({ confirmLoading: true });

        this.api.saveCookie({ Host: this.state.Host, Cookie: this.state.textView })
            .then(() => {
                this.api.saveSettingsInBackground();
                this.setState({ visible: false, confirmLoading: false });
                this.props.refresh();
                notification.success({ message: '保存成功' });
            })
            .catch(err => {
                this.setState({ confirmLoading: false });
                notification.error({ message: '保存失败', description: String(err) });
            });
    };

    handleCancel = () => {
        this.setState({ visible: false, textView: '' });
    };

    render() {
        const { visible, confirmLoading, textView, Host, Platform_cn_name } = this.state;
        const isBili = Host === 'live.bilibili.com';

        return (
            <div>
                {/* @ts-ignore */}
                <Modal
                    title={`修改 ${Platform_cn_name} (${Host}) Cookie`}
                    open={visible}
                    onOk={this.handleOk}
                    confirmLoading={confirmLoading}
                    onCancel={this.handleCancel}
                    width={isBili ? 720 : 520}
                    okText="保存并生效"
                    cancelText="取消"
                    destroyOnClose // Important to reset BiliLoginPanel state
                >
                    {isBili ? (
                        <BiliLoginPanel
                            key={this.state.modalKey}
                            initialCookie={textView}
                            api={this.api}
                            onCookieChange={this.handleCookieChange}
                        />
                    ) : (
                        <div style={{ marginTop: 10, padding: '8px' }}>
                            <div style={{ marginBottom: 12, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                                <span style={{ fontSize: '15px', fontWeight: 600, color: '#262626' }}>
                                    请输入有效的 Cookie 字符串：
                                </span>
                            </div>
                            <TextArea
                                className="cookie-textarea"
                                autoSize={{ minRows: 6, maxRows: 10 }}
                                value={textView}
                                placeholder="格式: KEY1=VALUE1; KEY2=VALUE2; ..."
                                onChange={(e) => this.handleCookieChange(e.target.value)}
                            />

                            {/* @ts-ignore */}
                            <Divider orientation="left" className="divider-text">
                                获取方式指南
                            </Divider>
                            <div style={{ fontSize: '14px', color: '#595959', lineHeight: '2' }}>
                                <ol style={{ paddingLeft: '20px' }}>
                                    <li>在浏览器登录 <b>{Platform_cn_name}</b> 官网。</li>
                                    <li>按 <b>F12</b> 查看控制台 - <b>网络 (Network)</b>。</li>
                                    <li>复制请求标头中的 <b>Cookie</b> 字段并粘贴到上方。</li>
                                </ol>
                            </div>
                        </div>
                    )}
                </Modal>
            </div>
        );
    }
}

export default EditCookieDialog;
