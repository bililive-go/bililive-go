import React from "react";
import { Button, Divider, Table, Tag, Tabs, Row, Col, Tooltip, message, List, Typography } from 'antd';
import { EditOutlined } from '@ant-design/icons';
import PopDialog from '../pop-dialog/index';
import AddRoomDialog from '../add-room-dialog/index';
import LogPanel from '../log-panel/index';
import API from '../../utils/api';
import { subscribeSSE, unsubscribeSSE, SSEMessage } from '../../utils/sse';
import './live-list.css';
import type { ColumnsType } from 'antd/es/table';
import { useNavigate, NavigateFunction } from "react-router-dom";
import EditCookieDialog from "../edit-cookie/index";
import { RoomConfigForm } from "../config-info";

const api = new API();
const { Text } = Typography;

const REFRESH_TIME = 3 * 60 * 1000;

interface Props {
    navigate: NavigateFunction;
    refresh?: () => void;
}

interface IState {
    list: ItemData[],
    cookieList: CookieItemData[],
    addRoomDialogVisible: boolean,
    window: any,
    expandedRowKeys: string[],  // 展开的行
    expandedDetails: { [key: string]: any }, // 直播间详细信息缓存
    expandedLogs: { [key: string]: string[] }, // 直播间日志缓存
    sseSubscriptions: { [key: string]: string }, // roomId -> subscriptionId 映射
    globalConfig: any, // 全局配置缓存
}

interface ItemData {
    key: string,
    name: string,
    room: Room,
    address: string,
    tags: string[],
    listening: boolean
    roomId: string
}
interface CookieItemData {
    Platform_cn_name: string,
    Host: string,
    Cookie: string
}

interface Room {
    roomName: string;
    url: string;
}

class LiveList extends React.Component<Props, IState> {
    //子控件
    child!: AddRoomDialog;

    //cookie开窗
    cookieChild!: EditCookieDialog;

    //定时器
    timer!: NodeJS.Timeout;

    runStatus: ColumnsType<ItemData>[number] = {
        title: '运行状态',
        key: 'tags',
        dataIndex: 'tags',
        render: (tags: string[]) => (
            <span>
                {tags.map(tag => {
                    let color = 'green';
                    if (tag === '已停止') {
                        color = 'grey';
                    }
                    if (tag === '监控中') {
                        color = 'green';
                    }
                    if (tag === '录制中') {
                        color = 'red';
                    }
                    if (tag === '初始化') {
                        color = 'orange';
                    }

                    return (
                        <Tag color={color} key={tag}>
                            {tag.toUpperCase()}
                        </Tag>
                    );
                })}
            </span>
        ),
        sorter: (a: ItemData, b: ItemData) => {
            const isRecordingA = a.tags.includes('录制中');
            const isRecordingB = b.tags.includes('录制中');
            if (isRecordingA === isRecordingB) {
                return 0;
            } else if (isRecordingA) {
                return 1;
            } else {
                return -1;
            }
        },
        defaultSortOrder: 'descend',
    };

    runAction: ColumnsType<ItemData>[number] = {
        title: '操作',
        key: 'action',
        dataIndex: 'listening',
        render: (listening: boolean, data: ItemData) => (
            <span onClick={(e) => e.stopPropagation()}>
                <PopDialog
                    title={listening ? "确定停止监控？" : "确定开启监控？"}
                    onConfirm={(e) => {
                        if (listening) {
                            //停止监控
                            api.stopRecord(data.roomId)
                                .then(rsp => {
                                    api.saveSettingsInBackground();
                                    this.refresh();
                                })
                                .catch(err => {
                                    alert(`停止监控失败:\n${err}`);
                                });
                        } else {
                            //开启监控
                            api.startRecord(data.roomId)
                                .then(rsp => {
                                    api.saveSettingsInBackground();
                                    this.refresh();
                                })
                                .catch(err => {
                                    alert(`开启监控失败:\n${err}`);
                                });
                        }
                    }}>
                    <Button type="link" size="small">{listening ? "停止监控" : "开启监控"}</Button>
                </PopDialog>
                <Divider type="vertical" />
                <PopDialog title="确定删除当前直播间？"
                    onConfirm={(e) => {
                        api.deleteRoom(data.roomId)
                            .then(rsp => {
                                api.saveSettingsInBackground();
                                this.refresh();
                            })
                            .catch(err => {
                                alert(`删除直播间失败:\n${err}`);
                            });
                    }}>
                    <Button type="link" size="small">删除</Button>
                </PopDialog>
                <Divider type="vertical" />
                <Button type="link" size="small" onClick={(e) => {
                    this.props.navigate(`/fileList/${data.address}/${data.name}`);
                }}>文件</Button>
                <Divider type="vertical" />
                <a
                    href={`/#/configInfo#rooms-live-${data.roomId}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    style={{ fontSize: 12 }}
                >
                    配置
                </a>
            </span>
        ),
    };

    columns: ColumnsType<ItemData> = [
        {
            title: '主播名称',
            dataIndex: 'name',
            key: 'name',
            sorter: (a: ItemData, b: ItemData) => {
                return a.name.localeCompare(b.name);
            },
            render: (name: string) => <span>{name}</span>
        },
        {
            title: '直播间名称',
            dataIndex: 'room',
            key: 'room',
            render: (room: Room) => <a href={room.url} rel="noopener noreferrer" target="_blank" onClick={(e) => e.stopPropagation()}>{room.roomName}</a>
        },
        {
            title: '直播平台',
            dataIndex: 'address',
            key: 'address',
            sorter: (a: ItemData, b: ItemData) => {
                return a.address.localeCompare(b.address);
            },
            render: (address: string) => <span>{address}</span>
        },
        this.runStatus,
        this.runAction
    ];

    smallColumns: ColumnsType<ItemData> = [
        {
            title: '主播名称',
            dataIndex: 'name',
            key: 'name',
            render: (name: string, data: ItemData) => <a href={data.room.url} rel="noopener noreferrer" target="_blank" onClick={(e) => e.stopPropagation()}>{name}</a>
        },
        this.runStatus,
        this.runAction
    ];
    cookieColumns: ColumnsType<CookieItemData> = [
        {
            title: '直播平台',
            dataIndex: 'livename',
            key: 'livename',
            render: (name: string, data: CookieItemData) => data.Platform_cn_name + '(' + data.Host + ')'
        }, {
            title: 'Cookie',
            dataIndex: 'Cookie',
            key: 'Cookie',
            ellipsis: true,
            render: (name: String, data: CookieItemData) => {
                return <Row gutter={16}>
                    <Col className="gutter-row" span={12}>
                        <Tooltip title={data.Cookie}>
                            <div className="gutter-box cookieString" title={data.Cookie}>{data.Cookie}</div>
                        </Tooltip>
                    </Col>
                    <Col className="gutter-row" span={4}>
                        <div className="gutter-box">
                            <Button type="primary" shape="circle" icon={<EditOutlined />} onClick={() => {
                                this.onEditCookitClick(data)
                            }} />
                        </div>
                    </Col>
                </Row>
            }
        }
    ]

    constructor(props: Props) {
        super(props);
        this.state = {
            list: [],
            cookieList: [],
            addRoomDialogVisible: false,
            window: window,
            expandedRowKeys: [],
            expandedDetails: {},
            expandedLogs: {},
            sseSubscriptions: {},
            globalConfig: null,
        }
    }

    pendingRoomId: string | null = null;

    componentDidMount() {
        // 解析 URL 参数以支持深度链接
        const hash = window.location.hash;
        if (hash.includes('?')) {
            const searchParams = new URLSearchParams(hash.split('?')[1]);
            this.pendingRoomId = searchParams.get('room');
        }

        this.requestData("livelist"); // Call with a specific targetKey
        this.fetchGlobalConfig();
        this.timer = setInterval(() => {
            this.requestData("livelist"); // Call with a specific targetKey
        }, REFRESH_TIME);
    }

    fetchGlobalConfig = async () => {
        try {
            const config = await api.getEffectiveConfig();
            this.setState({ globalConfig: config });
        } catch (error) {
            console.error('Failed to fetch global config:', error);
        }
    }

    componentWillUnmount() {
        //clear refresh timer
        clearInterval(this.timer);
        // 取消所有 SSE 订阅
        const { sseSubscriptions } = this.state;
        Object.values(sseSubscriptions).forEach(subId => {
            unsubscribeSSE(subId);
        });
    }

    onRef = (ref: AddRoomDialog) => {
        this.child = ref
    }

    onCookieRef = (ref: EditCookieDialog) => {
        this.cookieChild = ref
    }

    /**
     * 当添加房间按钮点击，弹出Dialog
     */
    onAddRoomClick = () => {
        this.child.showModal()
    }

    onEditCookitClick = (data: any) => {
        this.cookieChild.showModal(data)
    }

    /**
     * 保存设置至config文件
     */
    onSettingSave = () => {
        api.saveSettings()
            .then((rsp: any) => {
                if (rsp.err_no === 0) {
                    alert("设置保存成功");
                } else {
                    alert("Server Error!");
                }
            }).catch(err => {
                alert(`Server Error!:\n${err}`);
            })
    }

    /**
     * 刷新页面数据
     */
    refresh = () => {
        this.requestListData();
    }

    refreshCookie = () => {
        this.requestCookieData();
    }

    /**
     * 加载列表数据
     */
    requestListData() {
        api.getRoomList()
            .then(function (rsp: any) {
                if (rsp.length === 0) {
                    return [];
                }
                return rsp.map((item: any, index: number) => {
                    //判断标签状态
                    let tags;
                    if (item.listening === true) {
                        tags = ['监控中'];
                    } else {
                        tags = ['已停止'];
                    }

                    if (item.recording === true) {
                        tags = ['录制中'];
                    }

                    if (item.initializing === true) {
                        tags.push('初始化')
                    }

                    return {
                        key: index + 1,
                        name: item.nick_name || item.host_name,
                        room: {
                            roomName: item.room_name,
                            url: item.live_url
                        },
                        address: item.platform_cn_name,
                        tags,
                        listening: item.listening,
                        roomId: item.id
                    };
                });
            })
            .then((data: ItemData[]) => {
                this.setState({
                    list: data
                }, () => {
                    // 处理深度链接自动展开
                    if (this.pendingRoomId) {
                        const targetRoom = data.find(item => item.roomId === this.pendingRoomId);
                        if (targetRoom) {
                            if (!this.state.expandedRowKeys.includes(this.pendingRoomId)) {
                                this.toggleExpandRow(this.pendingRoomId);
                            }
                            // 滚动到该行
                            setTimeout(() => {
                                const element = document.getElementById(`row-live-${this.pendingRoomId}`);
                                if (element) {
                                    element.scrollIntoView({ behavior: 'smooth', block: 'center' });
                                    element.classList.add('highlight-row'); // 可以添加 CSS 动画
                                }
                            }, 500);
                        }
                        // 清除 pending，避免后续刷新重复操作
                        this.pendingRoomId = null;
                    }
                });
            })
            .catch(err => {
                alert(`加载列表数据失败:\n${err}`);
            });
    }

    requestCookieData() {
        api.getCookieList()
            .then(function (rsp: any) {
                return rsp
            }).then((data: CookieItemData[]) => {
                this.setState({
                    cookieList: data
                });
            })
    }

    requestData = (targetKey: string) => {
        switch (targetKey) {
            case "livelist":
                this.requestListData()
                break
            case "cookielist":
                this.requestCookieData()
                break
        }
    }

    toggleExpandRow = (roomId: string) => {
        const { expandedRowKeys, sseSubscriptions } = this.state;
        const isExpanded = expandedRowKeys.includes(roomId);

        if (isExpanded) {
            // 收起 - 取消 SSE 订阅
            if (sseSubscriptions[roomId]) {
                unsubscribeSSE(sseSubscriptions[roomId]);
                const newSubscriptions = { ...sseSubscriptions };
                delete newSubscriptions[roomId];
                this.setState({
                    expandedRowKeys: expandedRowKeys.filter(key => key !== roomId),
                    sseSubscriptions: newSubscriptions
                });
            } else {
                this.setState({
                    expandedRowKeys: expandedRowKeys.filter(key => key !== roomId)
                });
            }
        } else {
            // 展开 - 获取详细信息和日志，并订阅 SSE
            this.setState({
                expandedRowKeys: [...expandedRowKeys, roomId]
            });
            this.loadRoomDetail(roomId);
            this.loadRoomLogs(roomId);
            this.subscribeRoomSSE(roomId);
        }
    }

    // 订阅房间的 SSE 事件
    subscribeRoomSSE = (roomId: string) => {
        // 订阅所有该房间的事件
        const subscriptionId = subscribeSSE(roomId, '*', (message: SSEMessage) => {
            this.handleSSEMessage(roomId, message);
        });

        this.setState({
            sseSubscriptions: {
                ...this.state.sseSubscriptions,
                [roomId]: subscriptionId
            }
        });
    }

    // 处理 SSE 消息
    handleSSEMessage = (roomId: string, message: SSEMessage) => {
        switch (message.type) {
            case 'log':
                // 追加新日志
                this.setState(prevState => {
                    const currentLogs = prevState.expandedLogs[roomId] || [];
                    // 限制日志数量，保留最新的 500 条（与 LogPanel 的 MAX_LOG_LINES 保持一致）
                    const newLogs = [...currentLogs, message.data].slice(-500);
                    return {
                        expandedLogs: {
                            ...prevState.expandedLogs,
                            [roomId]: newLogs
                        }
                    };
                });
                break;

            case 'live_update':
                // 刷新房间详情
                this.loadRoomDetail(roomId);
                // 同时刷新列表数据
                this.requestListData();
                break;

            case 'conn_stats':
                // 更新连接统计
                const currentDetail = this.state.expandedDetails[roomId];
                if (currentDetail) {
                    this.setState({
                        expandedDetails: {
                            ...this.state.expandedDetails,
                            [roomId]: {
                                ...currentDetail,
                                conn_stats: message.data
                            }
                        }
                    });
                }
                break;
        }
    }

    loadRoomDetail = (roomId: string) => {
        api.getLiveDetail(roomId)
            .then((detail: any) => {
                this.setState({
                    expandedDetails: {
                        ...this.state.expandedDetails,
                        [roomId]: detail
                    }
                });
            })
            .catch(err => {
                message.error(`获取直播间详情失败: ${err}`);
            });
    }

    loadRoomLogs = (roomId: string) => {
        api.getLiveLogs(roomId, 100)
            .then((logs: any) => {
                this.setState({
                    expandedLogs: {
                        ...this.state.expandedLogs,
                        [roomId]: logs.lines || []
                    }
                });
            })
            .catch(err => {
                message.warning(`获取直播间日志失败: ${err}`);
            });
    }

    renderExpandedRow = (record: ItemData): JSX.Element => {
        const { expandedDetails, expandedLogs } = this.state;
        const detail = expandedDetails[record.roomId];
        const logs = expandedLogs[record.roomId] || [];
        const liveId = record.roomId;
        // 保存 this 引用供嵌套函数使用
        const component = this;

        // 配置来源对应的颜色
        const sourceColors: { [key: string]: string } = {
            room: 'blue',
            platform: 'orange',
            global: 'green',
            default: 'default',
        };

        // 获取配置项的颜色
        const getSourceColor = (configKey: string): string => {
            if (!detail || !detail.config_sources) return sourceColors.default;
            const source = detail.config_sources[configKey];
            return sourceColors[source] || sourceColors.default;
        };

        // 配置项行样式
        const configRowStyle: React.CSSProperties = {
            display: 'flex',
            alignItems: 'center',
            padding: '6px 12px',
            borderBottom: '1px solid #f0f0f0',
        };

        const configLabelStyle: React.CSSProperties = {
            width: '120px',
            flexShrink: 0,
            fontWeight: 500,
            color: '#666',
        };

        // 配置信息面板
        // 配置面板 - 保留供后续配置标签页使用
        const _renderConfigPanel = () => (
            <div>
                {/* 配置来源图例 */}
                <div style={{
                    padding: '8px 12px',
                    backgroundColor: '#fafafa',
                    borderBottom: '1px solid #e8e8e8',
                    fontSize: 12
                }}>
                    <Text type="secondary">配置来源图例: </Text>
                    <Tag color={sourceColors.room} style={{ marginLeft: 8 }}>房间级</Tag>
                    <Tag color={sourceColors.platform}>平台级</Tag>
                    <Tag color={sourceColors.global}>全局</Tag>
                    <Tag>默认</Tag>
                </div>
                {detail ? (
                    <div style={{ padding: '4px 0' }}>
                        <div style={configRowStyle}>
                            <span style={configLabelStyle}>检测间隔</span>
                            <Tag color={getSourceColor('interval')}>
                                {`${detail.effective_interval || '30'}秒`}
                            </Tag>
                        </div>
                        <div style={configRowStyle}>
                            <span style={configLabelStyle}>输出路径</span>
                            <Tag color={getSourceColor('out_put_path')}>
                                {detail.effective_out_path || './'}
                            </Tag>
                        </div>
                        <div style={configRowStyle}>
                            <span style={configLabelStyle}>FFmpeg路径</span>
                            <Tag color={getSourceColor('ffmpeg_path')}>
                                {detail.effective_ffmpeg_path || '默认内置ffmpeg'}
                            </Tag>
                        </div>
                        <div style={configRowStyle}>
                            <span style={configLabelStyle}>录制质量</span>
                            <span>{detail.quality === 0 ? '原画' : `画质${detail.quality}`}</span>
                        </div>
                        <div style={configRowStyle}>
                            <span style={configLabelStyle}>仅录音频</span>
                            <Tag color={detail.audio_only ? 'blue' : undefined}>
                                {detail.audio_only ? '是' : '否'}
                            </Tag>
                        </div>
                        <div style={{ ...configRowStyle, borderBottom: 'none' }}>
                            <span style={configLabelStyle}>平台访问限制</span>
                            <span>{detail.platform_rate_limit ? `${detail.platform_rate_limit}秒` : '无限制'}</span>
                        </div>
                    </div>
                ) : (
                    <div style={{ padding: '20px', textAlign: 'center', color: '#999' }}>
                        加载配置信息中...
                    </div>
                )}
            </div>
        );

        // 运行时信息面板
        const renderRuntimePanel = () => {
            const handleForceRefresh = async () => {
                try {
                    const result = await api.forceRefreshLive(liveId) as { success?: boolean; message?: string };
                    if (result.success) {
                        message.success('强制刷新成功');
                        // 重新加载详细信息
                        component.loadRoomDetail(liveId);
                    } else {
                        message.error(result.message || '强制刷新失败');
                    }
                } catch (error) {
                    message.error('强制刷新失败');
                }
            };

            return (
                <div>
                    {detail ? (
                        <div>
                            <div style={{ padding: '4px 0' }}>
                                <div style={configRowStyle}>
                                    <span style={configLabelStyle}>监控状态</span>
                                    <Tag color={detail.listening ? 'green' : undefined}>
                                        {detail.listening ? '监控中' : '已停止'}
                                    </Tag>
                                </div>
                                <div style={configRowStyle}>
                                    <span style={configLabelStyle}>录制状态</span>
                                    <Tag color={detail.recording ? 'red' : undefined}>
                                        {detail.recording ? '录制中' : '未录制'}
                                    </Tag>
                                </div>
                                <div style={configRowStyle}>
                                    <span style={configLabelStyle}>开播时间</span>
                                    <span>{detail.live_start_time || '未知'}</span>
                                </div>
                                <div style={{ ...configRowStyle, borderBottom: 'none' }}>
                                    <span style={configLabelStyle}>上次录制</span>
                                    <span>{detail.last_record_time || '无'}</span>
                                </div>
                            </div>

                            <Divider style={{ margin: '8px 0' }}>平台访问频率控制</Divider>
                            <div style={{ padding: '0 12px 8px' }}>
                                {detail.rate_limit_info ? (
                                    <div>
                                        <div style={configRowStyle}>
                                            <span style={configLabelStyle}>最小访问间隔</span>
                                            <Tag>{detail.rate_limit_info.min_interval_sec || detail.platform_rate_limit} 秒</Tag>
                                        </div>
                                        <div style={configRowStyle}>
                                            <span style={configLabelStyle}>距上次请求</span>
                                            <span>{Math.round(detail.rate_limit_info.waited_seconds || 0)} 秒</span>
                                        </div>
                                        <div style={configRowStyle}>
                                            <span style={configLabelStyle}>预计下次请求</span>
                                            <Tag color={(detail.rate_limit_info.next_request_in_sec || 0) > 0 ? 'orange' : 'green'}>
                                                {(detail.rate_limit_info.next_request_in_sec || 0) > 0
                                                    ? `${Math.round(detail.rate_limit_info.next_request_in_sec)} 秒后`
                                                    : '立即可用'}
                                            </Tag>
                                        </div>
                                        <div style={{ marginTop: 12, borderBottom: 'none' }}>
                                            <Button
                                                type="primary"
                                                size="small"
                                                onClick={handleForceRefresh}
                                            >
                                                强制刷新（突破频率限制）
                                            </Button>
                                        </div>
                                    </div>
                                ) : (
                                    <div style={{ padding: '8px 0', textAlign: 'center', color: '#999' }}>
                                        暂无访问频率信息
                                    </div>
                                )}
                            </div>

                            <Divider style={{ margin: '8px 0' }}>网络连接统计</Divider>
                            <div style={{ padding: '0 12px 8px' }}>
                                {detail.conn_stats && detail.conn_stats.length > 0 ? (
                                    <List
                                        size="small"
                                        dataSource={detail.conn_stats}
                                        split={false}
                                        renderItem={(item: any) => (
                                            <List.Item style={{ padding: '6px 0', borderBottom: '1px dashed #f0f0f0' }}>
                                                <div style={{ width: '100%' }}>
                                                    <Text strong style={{ fontSize: 13 }}>{item.host}</Text>
                                                    <div style={{ marginTop: 4 }}>
                                                        <Text type="secondary">↓ 接收: </Text>
                                                        <Tag color="blue" style={{ marginRight: 16 }}>{item.received_format}</Tag>
                                                        <Text type="secondary">↑ 发送: </Text>
                                                        <Tag color="green">{item.sent_format}</Tag>
                                                    </div>
                                                </div>
                                            </List.Item>
                                        )}
                                    />
                                ) : (
                                    <div style={{ padding: '12px 0', textAlign: 'center', color: '#999' }}>
                                        暂无网络连接统计数据
                                    </div>
                                )}
                            </div>
                        </div>
                    ) : (
                        <div style={{ padding: '20px', textAlign: 'center', color: '#999' }}>
                            加载运行时信息中...
                        </div>
                    )}
                </div>
            );
        };

        // 日志面板
        const renderLogsPanel = () => {
            const handleLogsChange = (newLogs: string[]) => {
                this.setState({
                    expandedLogs: {
                        ...this.state.expandedLogs,
                        [record.roomId]: newLogs
                    }
                });
            };

            return (
                <LogPanel
                    logs={logs}
                    onLogsChange={handleLogsChange}
                    roomName={record.name}
                />
            );
        };

        return (
            <div style={{
                margin: '8px 16px 16px',
                border: '1px solid #d9d9d9',
                borderRadius: '6px',
                backgroundColor: '#fff',
                boxShadow: '0 2px 8px rgba(0,0,0,0.06)'
            }}>
                <Tabs
                    defaultActiveKey="runtime"
                    size="small"
                    animated={false}
                    style={{ margin: 0 }}
                    tabBarStyle={{
                        margin: 0,
                        padding: '0 12px',
                        backgroundColor: '#fafafa',
                        borderBottom: '1px solid #e8e8e8',
                        borderRadius: '6px 6px 0 0'
                    }}
                >
                    <Tabs.TabPane tab="运行时信息" key="runtime">
                        {renderRuntimePanel()}
                    </Tabs.TabPane>
                    <Tabs.TabPane tab="设置" key="settings">
                        <div style={{ padding: '16px 20px' }}>
                            {this.state.globalConfig && detail && detail.room_config ? (
                                <RoomConfigForm
                                    room={detail.room_config}
                                    globalConfig={this.state.globalConfig}
                                    platformId={detail.platform_key}
                                    onSave={async (updates) => {
                                        await api.updateRoomConfigById(detail.live_id, updates);
                                        // 更新后重新加载详情以获取最新配置状态
                                        await this.loadRoomDetail(record.roomId);
                                    }}
                                    loading={false}
                                    onRefresh={() => this.loadRoomDetail(record.roomId)}
                                />
                            ) : (
                                <div style={{ textAlign: 'center', padding: '20px' }}>正在加载配置...</div>
                            )}
                        </div>
                    </Tabs.TabPane>
                    <Tabs.TabPane tab="最近日志" key="logs">
                        {renderLogsPanel()}
                    </Tabs.TabPane>
                </Tabs>
            </div>
        );
    }

    render() {
        const { list } = this.state;
        this.columns.forEach((column: ColumnsType<ItemData>[number]) => {
            if (column.key === 'address') {
                // 直播平台去重数组
                const addressList = Array.from(new Set(list.map(item => item.address)));
                column.filters = addressList.map(text => ({ text, value: text }));
                column.onFilter = (value: string | number | boolean, record: ItemData) => record.address === value;
            }
            if (column.key === 'tags') {
                column.filters = ['初始化', '监控中', '录制中', '已停止'].map(text => ({ text, value: text }));
                column.onFilter = (value: string | number | boolean, record: ItemData) => record.tags.includes(value as string);
            }
        })
        return (
            <div>
                <Tabs defaultActiveKey="livelist" type="card" onChange={this.requestData}>
                    <Tabs.TabPane tab="直播间列表" key="livelist">
                        <div style={{
                            padding: '16px 24px',
                            backgroundColor: '#fff',
                            borderBottom: '1px solid #e8e8e8',
                            marginBottom: 16,
                            display: 'flex',
                            justifyContent: 'space-between',
                            alignItems: 'center'
                        }}>
                            {/* ... content ... */}
                            <div>
                                <span style={{ fontSize: '20px', fontWeight: 600, color: 'rgba(0,0,0,0.85)', marginRight: 12 }}>直播间列表</span>
                                <span style={{ fontSize: '14px', color: 'rgba(0,0,0,0.45)' }}>Room List</span>
                            </div>
                            <div style={{ display: 'flex', gap: '8px' }}>
                                <Button key="2" type="default" onClick={this.onSettingSave}>保存设置</Button>
                                <Button key="1" type="primary" onClick={this.onAddRoomClick}>
                                    添加房间
                                </Button>
                                <AddRoomDialog key="0" ref={this.onRef} refresh={this.refresh} />
                            </div>
                        </div>
                        <Table
                            className="item-pad"
                            columns={(this.state.window.screen.width > 768) ? this.columns : this.smallColumns}
                            dataSource={this.state.list}
                            size={(this.state.window.screen.width > 768) ? "large" : "middle"}
                            pagination={false}
                            expandedRowKeys={this.state.expandedRowKeys}
                            expandedRowRender={this.renderExpandedRow}
                            rowKey={record => record.roomId}
                            onExpand={(expanded, record) => this.toggleExpandRow(record.roomId)}
                            onRow={(record) => ({
                                id: `row-live-${record.roomId}`,
                                style: { transition: 'background-color 1s' },
                                onClick: (e) => {
                                    // 只有点击 td 单元格本身（空白处）才触发展开
                                    // 如果点击的是 td 内的内容元素，则不触发
                                    const target = e.target as HTMLElement;
                                    if (target.tagName === 'TD') {
                                        this.toggleExpandRow(record.roomId);
                                    }
                                }
                            })}
                        />
                    </Tabs.TabPane>
                    <Tabs.TabPane tab="Cookie管理" key="cookielist">
                        <div style={{
                            padding: '16px 24px',
                            backgroundColor: '#fff',
                            borderBottom: '1px solid #e8e8e8',
                            marginBottom: 16,
                            display: 'flex',
                            justifyContent: 'space-between',
                            alignItems: 'center'
                        }}>
                            <div>
                                <span style={{ fontSize: '20px', fontWeight: 600, color: 'rgba(0,0,0,0.85)', marginRight: 12 }}>Cookie管理</span>
                                <span style={{ fontSize: '14px', color: 'rgba(0,0,0,0.45)' }}>Cookie List</span>
                            </div>
                            <div>
                                <EditCookieDialog key="1" ref={this.onCookieRef} refresh={this.refreshCookie} />
                            </div>
                        </div>
                        <Table
                            className="item-pad"
                            columns={(this.state.window.screen.width > 768) ? this.cookieColumns : this.cookieColumns}
                            dataSource={this.state.cookieList}
                            size={(this.state.window.screen.width > 768) ? "large" : "middle"}
                            pagination={false}
                        />
                    </Tabs.TabPane>
                </Tabs>
            </div>
        );
    };
}

// HOC to inject navigate hook into class component
function LiveListWithRouter(props: Omit<Props, 'navigate'>) {
    const navigate = useNavigate();
    return <LiveList {...props} navigate={navigate} />;
}

export default LiveListWithRouter;
