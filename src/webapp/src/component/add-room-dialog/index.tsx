import { Modal, Input } from 'antd';
import React from 'react';
import API from '../../utils/api';

const api = new API();

interface Props {
    refresh?: any;
    children?: React.ReactNode;
}

class AddRoomDialog extends React.Component<Props> {
    state = {
        ModalText: '请输入直播间的URL地址',
        visible: false,
        confirmLoading: false,
        textView: ''
    };

    showModal = () => {
        this.setState({
            ModalText: '请输入直播间的URL地址',
            visible: true,
            confirmLoading: false,
        });
    };

    handleOk = () => {
        this.setState({
            ModalText: '正在添加直播间......',
            confirmLoading: true,
        });

        api.addNewRoom(this.state.textView)
            .then((rsp) => {
                // 保存设置
                api.saveSettingsInBackground();
                this.setState({
                    visible: false,
                    confirmLoading: false,
                    textView: ''
                });
                this.props.refresh();
            })
            .catch(err => {
                alert(`添加直播间失败:\n${err}`);
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
            textView: ''
        });
    };

    textChange = (e: any) => {
        this.setState({
            textView: e.target.value
        })
    }

    render() {
        const { visible, confirmLoading, ModalText, textView } = this.state;
        return (
            <div>
                <Modal
                    title="添加直播间"
                    open={visible}
                    onOk={this.handleOk}
                    confirmLoading={confirmLoading}
                    onCancel={this.handleCancel}>
                    <p>{ModalText}</p>
                    <Input size="large" value={textView} placeholder="https://" onChange={this.textChange} />
                </Modal>
            </div>
        );
    }
}

export default AddRoomDialog;
