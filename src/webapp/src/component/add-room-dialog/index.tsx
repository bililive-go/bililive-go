import { Modal, Input, Select } from 'antd';
import React from 'react';
import API from '../../utils/api';

const api = new API();
const { Option } = Select;

interface Props {
    refresh?: any
}

class AddRoomDialog extends React.Component<Props> {
    state = {
        ModalText: '请选择类型并输入URL地址',
        visible: false,
        confirmLoading: false,
        textView: '',
        selectedType: 'live_room'
    };

    showModal = () => {
        this.setState({
            ModalText: '请选择类型并输入URL地址',
            visible: true,
            confirmLoading: false,
        });
    };

    handleOk = () => {
        this.setState({
            ModalText: '正在添加......',
            confirmLoading: true,
        });

        api.addNewRoom(this.state.textView, this.state.selectedType)
            .then((rsp) => {
                // 保存设置
                api.saveSettingsInBackground();
                this.setState({
                    visible: false,
                    confirmLoading: false,
                    textView: '',
                    selectedType: 'live_room'
                });
                this.props.refresh();
            })
            .catch(err => {
                alert(`添加失败:\n${err}`);
                this.setState({
                    visible: false,
                    confirmLoading: false,
                    textView: '',
                    selectedType: 'live_room'
                });
            })
    };

    handleCancel = () => {
        this.setState({
            visible: false,
            textView: '',
            selectedType: 'live_room'
        });
    };

    textChange = (e: any) => {
        this.setState({
            textView: e.target.value
        })
    }

    typeChange = (value: string) => {
        this.setState({
            selectedType: value,
            ModalText: value === 'live_room' ? '请输入直播间的URL地址' : '请输入M3U8链接地址'
        });
    }

    render() {
        const { visible, confirmLoading, ModalText, textView, selectedType } = this.state;
        return (
            <div>
                <Modal
                    title="添加直播间"
                    visible={visible}
                    onOk={this.handleOk}
                    confirmLoading={confirmLoading}
                    onCancel={this.handleCancel}>
                    <p>{ModalText}</p>
                    <div style={{ marginBottom: 16 }}>
                        <Select 
                            value={selectedType} 
                            style={{ width: '100%' }}
                            onChange={this.typeChange}
                        >
                            <Option value="live_room">直播间</Option>
                            <Option value="m3u8">M3U8链接</Option>
                        </Select>
                    </div>
                    <Input 
                        size="large" 
                        value={textView} 
                        placeholder={selectedType === 'live_room' ? 'https://live.example.com/room123' : 'https://example.com/stream.m3u8'} 
                        onChange={this.textChange} 
                    />
                </Modal>
            </div>
        );
    }
}

export default AddRoomDialog;
