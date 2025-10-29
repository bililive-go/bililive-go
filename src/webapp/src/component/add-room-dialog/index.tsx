import { Modal, Input } from 'antd';
import React from 'react';
import API from '../../utils/api';

const { TextArea } = Input;
const api = new API();

interface Props {
    refresh?: any
}

class AddRoomDialog extends React.Component<Props> {
    state = {
        ModalText: '请输入直播间的URL地址（支持批量添加，每行一个URL）',
        visible: false,
        confirmLoading: false,
        textView: ''
    };

    showModal = () => {
        this.setState({
            ModalText: '请输入直播间的URL地址（支持批量添加，每行一个URL）',
            visible: true,
            confirmLoading: false,
        });
    };

    handleOk = () => {
        this.setState({
            ModalText: '正在添加直播间......',
            confirmLoading: true,
        });

        // 分割输入的文本，支持批量添加
        const urls = this.state.textView
            .split('\n')
            .map(url => url.trim())
            .filter(url => url.length > 0);

        if (urls.length === 0) {
            alert('请输入至少一个URL');
            this.setState({
                confirmLoading: false,
            });
            return;
        }

        api.addNewRoomBatch(urls)
            .then((rsp) => {
                // 保存设置
                api.saveSettingsInBackground();
                this.setState({
                    visible: false,
                    confirmLoading: false,
                    textView:''
                });
                this.props.refresh();
            })
            .catch(err => {
                alert(`添加直播间失败:\n${err}`);
                this.setState({
                    visible: false,
                    confirmLoading: false,
                    textView:''
                });
            })
    };

    handleCancel = () => {
        this.setState({
            visible: false,
            textView:''
        });
    };

    textChange = (e: any) => {
        this.setState({
            textView: e.target.value
        })
    }

    render() {
        const { visible, confirmLoading, ModalText,textView } = this.state;
        return (
            <div>
                <Modal
                    title="添加直播间"
                    visible={visible}
                    onOk={this.handleOk}
                    confirmLoading={confirmLoading}
                    onCancel={this.handleCancel}>
                    <p>{ModalText}</p>
                    <TextArea 
                        rows={6} 
                        value={textView} 
                        placeholder={"https://live.bilibili.com/123\nhttps://www.douyu.com/456\nhttps://www.huya.com/789"} 
                        onChange={this.textChange} 
                    />
                </Modal>
            </div>
        );
    }
}

export default AddRoomDialog;
