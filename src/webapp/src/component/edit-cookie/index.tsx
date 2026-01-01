import { Modal, Input, notification } from 'antd';
import React from 'react';
import API from '../../utils/api';
import './edit-cookie.css'

const api = new API();

interface Props {
    refresh?: any;
    children?: React.ReactNode;
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
        Platform_cn_name: ''
    };

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
            Platform_cn_name: data.Platform_cn_name
        });
    };

    handleOk = () => {
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
        this.setState({
            visible: false,
            textView: '',
            alertVisible: false,
            errorInfo: '',
            Host: '',
            Platform_cn_name: ''
        });
    };

    textChange = (e: any) => {
        this.setState({
            textView: e.target.value,
            alertVisible: false,
            errorInfo: ''
        })
        let cookiearr = this.state.textView.split(";")
        cookiearr.forEach((cookie, index) => {
            if (cookie.indexOf("=") === -1) {
                this.setState({ alertVisible: true, errorInfo: 'cookie格式错误' })
                return
            }
            if (cookie.indexOf("expire") > -1) {
                //可能是cookie过期时间
                let value = cookie.split("=")[1]
                let tmpdate
                if (value.indexOf("-") > -1) {
                    //可能是日期格式
                    tmpdate = new Date(value)
                } else if (value.length === 10) {
                    tmpdate = new Date(value + "000")
                } else if (value.length === 13) {
                    tmpdate = new Date(value)
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
            Host, Platform_cn_name } = this.state;
        return (
            <div>
                <Modal
                    title={"修改" + Platform_cn_name + "(" + Host + ")Cookie"}
                    open={visible}
                    onOk={this.handleOk}
                    confirmLoading={confirmLoading}
                    onCancel={this.handleCancel}>
                    <p>{ModalText}</p>
                    <TextArea autoSize={{ minRows: 2, maxRows: 6 }} value={textView} placeholder="请输入Cookie" onChange={this.textChange} allowClear />
                    <div id="errorinfo" className={alertVisible ? 'word-style' : 'word-style:hide'}>{errorInfo}</div>
                </Modal>
            </div>
        );
    }
}
export default EditCookieDialog;