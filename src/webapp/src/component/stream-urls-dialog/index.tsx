import React from "react";
import { Modal, Table, Button, message, Tag, Typography } from 'antd';
import copy from 'copy-to-clipboard';
import API from '../../utils/api';

const { Text } = Typography;
const api = new API();

interface Props {
    visible: boolean;
    onCancel: () => void;
    roomId: string;
    roomName: string;
    platform: string;
}

interface StreamUrl {
    name: string;
    description: string;
    url: string;
    resolution: number;
    vbitrate: number;
}

interface StreamUrlsResponse {
    stream_urls: StreamUrl[];
    platform: string;
}

interface State {
    loading: boolean;
    streamUrls: StreamUrl[];
    platform: string;
}

class StreamUrlsDialog extends React.Component<Props, State> {
    constructor(props: Props) {
        super(props);
        this.state = {
            loading: false,
            streamUrls: [],
            platform: ''
        };
    }

    componentDidUpdate(prevProps: Props) {
        if (this.props.visible && !prevProps.visible && this.props.roomId) {
            this.fetchStreamUrls();
        }
    }

    fetchStreamUrls = async () => {
        this.setState({ loading: true });
        try {
            const response = await api.getStreamUrls(this.props.roomId) as StreamUrlsResponse;
            this.setState({
                streamUrls: response.stream_urls || [],
                platform: response.platform || this.props.platform
            });
        } catch (error) {
            message.error(`获取直播源失败: ${error}`);
            this.setState({
                streamUrls: [],
                platform: this.props.platform
            });
        } finally {
            this.setState({ loading: false });
        }
    };

    handleCopy = (url: string, name: string) => {
        const result = copy(url);
        if (result) {
            message.success(`${name} 直播源已复制到剪贴板`);
        } else {
            message.error('复制失败，请手动复制');
        }
    };

    columns = [
        {
            title: '名称',
            dataIndex: 'name',
            key: 'name',
            render: (name: string, record: StreamUrl) => (
                <>
                    <Text strong>{name}</Text>
                    {record.description && (
                        <div style={{ color: '#666', fontSize: '12px' }}>
                            {record.description}
                        </div>
                    )}
                </>
            )
        },
        {
            title: '质量',
            key: 'quality',
            render: (record: StreamUrl) => (
                <>
                    {record.resolution > 0 && (
                        <Tag color="blue">{record.resolution}p</Tag>
                    )}
                    {record.vbitrate > 0 && (
                        <Tag color="green">{Math.round(record.vbitrate / 1000)}kbps</Tag>
                    )}
                </>
            )
        },
        {
            title: '直播源URL',
            dataIndex: 'url',
            key: 'url',
            ellipsis: true,
            render: (url: string) => (
                <Text code copyable style={{ fontSize: '11px' }}>
                    {url}
                </Text>
            )
        },
        {
            title: '操作',
            key: 'action',
            width: 120,
            render: (record: StreamUrl) => (
                <Button
                    type="primary"
                    size="small"
                    onClick={() => this.handleCopy(record.url, record.name)}
                >
                    复制
                </Button>
            )
        }
    ];

    render() {
        const { visible, onCancel, roomName } = this.props;
        const { loading, streamUrls, platform } = this.state;

        return (
            <Modal
                title={
                    <div>
                        <span>直播源URL - {roomName}</span>
                        {platform && <Tag color="orange" style={{ marginLeft: 8 }}>{platform}</Tag>}
                    </div>
                }
                visible={visible}
                onCancel={onCancel}
                footer={[
                    <Button key="close" onClick={onCancel}>
                        关闭
                    </Button>
                ]}
                width={800}
                destroyOnClose
            >
                <div style={{ marginBottom: 16 }}>
                    <Text type="secondary">
                        <span role="img" aria-label="info">ℹ️</span> 以下URL可在VLC、PotPlayer等播放器中使用。注意：直播源URL会定期更新，请在需要时重新获取。
                    </Text>
                </div>
                
                <Table
                    columns={this.columns}
                    dataSource={streamUrls}
                    loading={loading}
                    rowKey="url"
                    size="small"
                    pagination={false}
                    locale={{
                        emptyText: loading ? '正在获取直播源...' : '暂无可用的直播源URL'
                    }}
                />
            </Modal>
        );
    }
}

export default StreamUrlsDialog;