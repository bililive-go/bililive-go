import React from "react";
import { Button, Divider, Table, Tag, Tabs, Row, Col, Tooltip, message, List, Typography, Switch, Space } from 'antd';
import { EditOutlined, SyncOutlined, CloudSyncOutlined, ReloadOutlined } from '@ant-design/icons';
import PopDialog from '../pop-dialog/index';
import AddRoomDialog from '../add-room-dialog/index';
import LogPanel from '../log-panel/index';
import HistoryPanel from '../history-panel/index';
import API from '../../utils/api';
import { subscribeSSE, unsubscribeSSE, SSEMessage } from '../../utils/sse';
import { isListSSEEnabled, setListSSEEnabled, getPollIntervalMs } from '../../utils/settings';
import './live-list.css';
import type { ColumnsType } from 'antd/es/table';
import { useNavigate, NavigateFunction } from "react-router-dom";
import EditCookieDialog from "../edit-cookie/index";
import { RoomConfigForm } from "../config-info";

const api = new API();
const { Text } = Typography;

// 使用动态获取的刷新间隔
const getRefreshTime = () => getPollIntervalMs();

interface Props {
    navigate: NavigateFunction;
    refresh?: () => void;
}

// 刷新状态类型
// idle: 可以立即刷新
// waiting_interval: 等待配置的访问间隔
// waiting_rate_limit: 等待平台访问频率限制
// refreshing: 正在刷新
// no_schedule: 未安排定期刷新（如未监控的直播间）
type RefreshStatus = 'idle' | 'waiting_interval' | 'waiting_rate_limit' | 'refreshing' | 'no_schedule';

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
    countdownTimers: { [key: string]: number }, // 倒计时值缓存（秒）
    lastUpdateTimes: { [key: string]: number }, // 上次更新时间戳（毫秒）
    refreshStatus: { [key: string]: RefreshStatus }, // 刷新状态
    listSSESubscription: string | null, // 列表级别的SSE订阅ID
    enableListSSE: boolean, // 是否启用列表级别SSE（从localStorage读取）
    sortedInfo: { columnKey: string | null; order: 'ascend' | 'descend' | null }, // 表格排序状态
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

    //倒计时定时器
    countdownTimer!: NodeJS.Timeout;

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
        // 从 localStorage 加载排序状态
        let savedSortedInfo = { columnKey: null as string | null, order: null as 'ascend' | 'descend' | null };
        try {
            const saved = localStorage.getItem('liveListSortedInfo');
            if (saved) {
                savedSortedInfo = JSON.parse(saved);
            }
        } catch (e) {
            console.error('加载排序状态失败:', e);
        }
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
            countdownTimers: {},
            lastUpdateTimes: {},
            refreshStatus: {},
            listSSESubscription: null,
            enableListSSE: isListSSEEnabled(),
            sortedInfo: savedSortedInfo,
        }
    }

    pendingRoomId: string | null = null;

    // 监听localStorage设置变化的处理函数
    handleLocalSettingsChange = (event: CustomEvent) => {
        const newSettings = event.detail;
        const oldEnableSSE = this.state.enableListSSE;
        const newEnableSSE = newSettings.enableListSSE;

        if (oldEnableSSE !== newEnableSSE) {
            this.setState({ enableListSSE: newEnableSSE }, () => {
                if (newEnableSSE) {
                    // 启用SSE，设置SSE订阅
                    this.setupListSSE();
                    // 减少轮询频率（使用更长的间隔）
                    clearInterval(this.timer);
                    this.timer = setInterval(() => {
                        this.requestData("livelist");
                    }, getRefreshTime() * 2); // SSE模式下轮询作为备份，间隔翻倍
                } else {
                    // 禁用SSE，取消订阅
                    this.cleanupListSSE();
                    // 恢复正常轮询频率
                    clearInterval(this.timer);
                    this.timer = setInterval(() => {
                        this.requestData("livelist");
                    }, getRefreshTime());
                }
            });
        }
    };

    componentDidMount() {
        // 解析 URL 参数以支持深度链接
        const hash = window.location.hash;
        if (hash.includes('?')) {
            const searchParams = new URLSearchParams(hash.split('?')[1]);
            this.pendingRoomId = searchParams.get('room');
        }

        // 监听localStorage设置变化
        window.addEventListener('localSettingsChanged', this.handleLocalSettingsChange as EventListener);

        this.requestData("livelist"); // Call with a specific targetKey
        this.fetchGlobalConfig().then(() => {
            // 根据用户设置决定是否启用列表级别SSE
            if (this.state.enableListSSE) {
                this.setupListSSE();
            }
        });

        // 设置轮询定时器，SSE模式下使用更长的间隔作为备份
        const refreshInterval = this.state.enableListSSE ? getRefreshTime() * 2 : getRefreshTime();
        this.timer = setInterval(() => {
            this.requestData("livelist"); // Call with a specific targetKey
        }, refreshInterval);

        // 启动倒计时定时器，每秒更新一次
        this.countdownTimer = setInterval(() => {
            this.updateCountdowns();
        }, 1000);
    }

    fetchGlobalConfig = async () => {
        try {
            const config = await api.getEffectiveConfig();
            this.setState({ globalConfig: config });
        } catch (error) {
            console.error('Failed to fetch global config:', error);
        }
    }

    // 设置列表级别的SSE订阅
    setupListSSE = () => {
        // 如果已经有订阅，先清理
        this.cleanupListSSE();

        // 订阅所有房间的 live_update 事件（直播状态变化）
        const liveUpdateSubId = subscribeSSE('*', 'live_update', (message: SSEMessage) => {
            // 刷新列表数据
            this.requestListData();
            // 如果该房间已展开，也刷新详情
            if (this.state.expandedRowKeys.includes(message.room_id)) {
                this.loadRoomDetail(message.room_id);
            }
        });

        // 订阅 list_change 事件（直播间增删、监控开关等）
        const listChangeSubId = subscribeSSE('*', 'list_change', (message: SSEMessage) => {
            console.log('[SSE] List change event:', message);
            const roomId = message.room_id;
            const changeType = message.data?.change_type;

            // 刷新列表数据
            this.requestListData();

            // 如果该房间已展开，且是监控开关变化，重新加载详情（更新调度器状态）
            if (roomId && this.state.expandedRowKeys.includes(roomId)) {
                if (changeType === 'listen_start' || changeType === 'listen_stop') {
                    // 稍微延迟以确保后端状态已更新
                    setTimeout(() => {
                        this.loadRoomDetail(roomId);
                    }, 500);
                }
            }
        });

        // 订阅 rate_limit_update 事件（强制刷新后更新频率限制信息）
        const rateLimitSubId = subscribeSSE('*', 'rate_limit_update', (message: SSEMessage) => {
            console.log('[SSE] Rate limit update event:', message);
            const roomId = message.room_id;
            // 如果该房间已展开，更新频率限制信息
            if (this.state.expandedRowKeys.includes(roomId)) {
                this.handleRateLimitUpdate(roomId, message.data);
            }
        });

        // 保存所有订阅ID（用下划线连接，或者使用新的数据结构）
        this.setState({
            listSSESubscription: `${liveUpdateSubId}|${listChangeSubId}|${rateLimitSubId}`
        });
    }

    // 清理列表级别的SSE订阅
    cleanupListSSE = () => {
        const { listSSESubscription } = this.state;
        if (listSSESubscription) {
            // 取消所有订阅
            const subIds = listSSESubscription.split('|');
            subIds.forEach(subId => {
                if (subId) {
                    unsubscribeSSE(subId);
                }
            });
            this.setState({ listSSESubscription: null });
        }
    }

    // 处理频率限制更新事件（包括调度器刷新完成）
    handleRateLimitUpdate = (roomId: string, updateData: any) => {
        this.setState(prevState => {
            const currentDetail = prevState.expandedDetails[roomId];
            if (!currentDetail) {
                return prevState;
            }

            // 检查是否是调度器刷新完成事件
            const schedulerStatus = updateData?.scheduler_status;
            if (schedulerStatus) {
                // 从调度器状态计算倒计时
                let countdown: number;
                let status: RefreshStatus;

                if (!schedulerStatus.scheduler_running || !schedulerStatus.has_waiters) {
                    // 调度器未运行或没有等待者，无刷新计划
                    countdown = -1;
                    status = 'no_schedule';
                } else if (schedulerStatus.seconds_until_next_request > 0) {
                    // 有下次请求计划
                    countdown = Math.ceil(schedulerStatus.seconds_until_next_request);
                    status = 'waiting_interval';
                } else {
                    // 距离下次请求时间已过
                    countdown = 0;
                    status = 'idle';
                }

                // 更新详情中的调度器状态
                const updatedDetail = {
                    ...currentDetail,
                    scheduler_status: schedulerStatus
                };

                return {
                    ...prevState,
                    expandedDetails: {
                        ...prevState.expandedDetails,
                        [roomId]: updatedDetail
                    },
                    countdownTimers: {
                        ...prevState.countdownTimers,
                        [roomId]: countdown
                    },
                    lastUpdateTimes: {
                        ...prevState.lastUpdateTimes,
                        [roomId]: Date.now()
                    },
                    refreshStatus: {
                        ...prevState.refreshStatus,
                        [roomId]: status
                    }
                };
            }

            // 旧的频率限制信息处理逻辑（兼容性保留）
            const rateLimitInfo = updateData;
            const updatedDetail = {
                ...currentDetail,
                rate_limit_info: rateLimitInfo
            };

            const nextRequestInSec = Math.ceil(rateLimitInfo?.next_request_in_sec || 0);
            const minIntervalSec = rateLimitInfo?.min_interval_sec || currentDetail?.platform_rate_limit || 20;
            const waitedSec = Math.round(rateLimitInfo?.waited_seconds || 0);
            const initialCountdown = nextRequestInSec > 0 ? nextRequestInSec : minIntervalSec - waitedSec;

            return {
                ...prevState,
                expandedDetails: {
                    ...prevState.expandedDetails,
                    [roomId]: updatedDetail
                },
                countdownTimers: {
                    ...prevState.countdownTimers,
                    [roomId]: Math.max(0, initialCountdown)
                },
                lastUpdateTimes: {
                    ...prevState.lastUpdateTimes,
                    [roomId]: Date.now()
                },
                refreshStatus: {
                    ...prevState.refreshStatus,
                    [roomId]: nextRequestInSec > 0 ? 'waiting_interval' : 'idle'
                }
            };
        });
    }

    // 根据列表大小更新SSE订阅策略（保留但简化，因为现在SSE始终订阅）
    updateListSSESubscription = () => {
        // 如果用户启用了SSE但尚未订阅，则设置订阅
        if (this.state.enableListSSE && !this.state.listSSESubscription) {
            this.setupListSSE();
        }
    }

    componentWillUnmount() {
        //clear refresh timer
        clearInterval(this.timer);
        clearInterval(this.countdownTimer);

        // 移除localStorage设置变化监听
        window.removeEventListener('localSettingsChanged', this.handleLocalSettingsChange as EventListener);

        // 取消列表级别的SSE订阅
        this.cleanupListSSE();

        // 取消所有详情页的 SSE 订阅
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
                const oldListLength = this.state.list.length;
                this.setState({
                    list: data
                }, () => {
                    // 如果列表大小发生变化，重新评估SSE订阅策略
                    if (oldListLength !== data.length) {
                        this.updateListSSESubscription();
                    }

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

    // 处理表格排序变化
    handleTableChange = (pagination: any, filters: any, sorter: any) => {
        const sortedInfo = {
            columnKey: sorter.columnKey || null,
            order: sorter.order || null,
        };
        this.setState({ sortedInfo });
        // 保存到 localStorage
        try {
            localStorage.setItem('liveListSortedInfo', JSON.stringify(sortedInfo));
        } catch (e) {
            console.error('保存排序状态失败:', e);
        }
    };

    // 获取带有动态排序状态的列配置
    getColumnsWithSort = (columns: ColumnsType<ItemData>): ColumnsType<ItemData> => {
        const { sortedInfo } = this.state;
        return columns.map(col => {
            // 如果列有 key 且匹配当前排序列，则设置 sortOrder
            if (col.key && col.key === sortedInfo.columnKey) {
                return { ...col, sortOrder: sortedInfo.order };
            }
            // 其他列清除排序状态（如果有 defaultSortOrder，也需要覆盖）
            if ('sortOrder' in col || 'defaultSortOrder' in col) {
                return { ...col, sortOrder: col.key === sortedInfo.columnKey ? sortedInfo.order : undefined };
            }
            return col;
        });
    };

    toggleExpandRow = (roomId: string) => {
        const isCurrentlyExpanded = this.state.expandedRowKeys.includes(roomId);

        if (isCurrentlyExpanded) {
            // 收起 - 取消 SSE 订阅并清理倒计时状态
            const subscriptionId = this.state.sseSubscriptions[roomId];
            if (subscriptionId) {
                unsubscribeSSE(subscriptionId);
            }
            this.setState(prevState => {
                const newSubscriptions = { ...prevState.sseSubscriptions };
                const newCountdowns = { ...prevState.countdownTimers };
                const newLastUpdateTimes = { ...prevState.lastUpdateTimes };
                const newRefreshStatus = { ...prevState.refreshStatus };
                delete newSubscriptions[roomId];
                delete newCountdowns[roomId];
                delete newLastUpdateTimes[roomId];
                delete newRefreshStatus[roomId];
                return {
                    expandedRowKeys: prevState.expandedRowKeys.filter(key => key !== roomId),
                    sseSubscriptions: newSubscriptions,
                    countdownTimers: newCountdowns,
                    lastUpdateTimes: newLastUpdateTimes,
                    refreshStatus: newRefreshStatus
                };
            });
        } else {
            // 展开 - 获取详细信息和日志，并订阅 SSE
            this.setState(prevState => ({
                expandedRowKeys: [...prevState.expandedRowKeys, roomId]
            }), () => {
                // 在状态更新后执行副作用
                this.loadRoomDetail(roomId);
                this.loadRoomLogs(roomId);
                this.subscribeRoomSSE(roomId);
            });
        }
    }

    // 订阅房间的 SSE 事件
    subscribeRoomSSE = (roomId: string) => {
        // 订阅所有该房间的事件
        const subscriptionId = subscribeSSE(roomId, '*', (message: SSEMessage) => {
            this.handleSSEMessage(roomId, message);
        });

        this.setState(prevState => ({
            sseSubscriptions: {
                ...prevState.sseSubscriptions,
                [roomId]: subscriptionId
            }
        }));
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
                this.setState(prevState => {
                    const currentDetail = prevState.expandedDetails[roomId];
                    if (!currentDetail) {
                        return prevState;
                    }
                    return {
                        ...prevState,
                        expandedDetails: {
                            ...prevState.expandedDetails,
                            [roomId]: {
                                ...currentDetail,
                                conn_stats: message.data
                            }
                        }
                    };
                });
                break;

            case 'recorder_status':
                // 更新录制器状态（包含下载速度）
                this.setState(prevState => {
                    const currentDetail = prevState.expandedDetails[roomId];
                    if (!currentDetail) {
                        return prevState;
                    }
                    return {
                        ...prevState,
                        expandedDetails: {
                            ...prevState.expandedDetails,
                            [roomId]: {
                                ...currentDetail,
                                recorder_status: message.data
                            }
                        }
                    };
                });
                break;
        }
    }

    loadRoomDetail = (roomId: string) => {
        api.getLiveDetail(roomId)
            .then((detail: any) => {
                this.setState(prevState => {
                    // 优先使用 scheduler_status 来确定刷新状态
                    const schedulerStatus = detail.scheduler_status;
                    const rateLimitInfo = detail.rate_limit_info;

                    let initialCountdown = 0;
                    let initialStatus: RefreshStatus = 'idle';

                    if (schedulerStatus) {
                        // 有调度器状态信息
                        if (!schedulerStatus.has_waiters) {
                            // 没有等待者，说明没有安排定期刷新
                            initialStatus = 'no_schedule';
                            initialCountdown = -1; // 特殊值表示无计划
                        } else if (schedulerStatus.seconds_until_next_request > 0) {
                            // 有下次请求计划
                            initialCountdown = Math.ceil(schedulerStatus.seconds_until_next_request);
                            // 检查是否在等待平台限制
                            if (rateLimitInfo?.next_request_in_sec > 0) {
                                initialStatus = 'waiting_rate_limit';
                            } else {
                                initialStatus = 'waiting_interval';
                            }
                        } else if (schedulerStatus.seconds_until_next_request === 0) {
                            // 即将发送请求或正在等待平台限制
                            if (rateLimitInfo?.next_request_in_sec > 0) {
                                initialCountdown = Math.ceil(rateLimitInfo.next_request_in_sec);
                                initialStatus = 'waiting_rate_limit';
                            } else {
                                initialCountdown = 0;
                                initialStatus = 'idle';
                            }
                        } else {
                            // seconds_until_next_request < 0，表示没有计划
                            initialStatus = 'no_schedule';
                            initialCountdown = -1;
                        }
                    } else {
                        // 回退到旧逻辑（兼容性）
                        const nextRequestInSec = Math.ceil(rateLimitInfo?.next_request_in_sec || 0);
                        const minIntervalSec = rateLimitInfo?.min_interval_sec || detail.platform_rate_limit || 20;
                        const waitedSec = Math.round(rateLimitInfo?.waited_seconds || 0);

                        if (nextRequestInSec > 0) {
                            initialCountdown = nextRequestInSec;
                            initialStatus = 'waiting_rate_limit';
                        } else if (waitedSec < minIntervalSec) {
                            initialCountdown = minIntervalSec - waitedSec;
                            initialStatus = 'waiting_interval';
                        } else {
                            initialCountdown = 0;
                            initialStatus = 'idle';
                        }
                    }

                    return {
                        expandedDetails: {
                            ...prevState.expandedDetails,
                            [roomId]: detail
                        },
                        countdownTimers: {
                            ...prevState.countdownTimers,
                            [roomId]: initialCountdown
                        },
                        lastUpdateTimes: {
                            ...prevState.lastUpdateTimes,
                            [roomId]: Date.now()
                        },
                        refreshStatus: {
                            ...prevState.refreshStatus,
                            [roomId]: initialStatus
                        }
                    };
                });
            })
            .catch(err => {
                message.error(`获取直播间详情失败: ${err}`);
            });
    }

    // 更新所有展开房间的倒计时
    updateCountdowns = () => {
        this.setState(prevState => {
            const newCountdowns = { ...prevState.countdownTimers };
            const newRefreshStatus = { ...prevState.refreshStatus };
            let hasChanges = false;

            // 只更新展开的房间
            prevState.expandedRowKeys.forEach(roomId => {
                const currentStatus = newRefreshStatus[roomId];
                const currentCountdown = newCountdowns[roomId];

                // 跳过无计划和正在刷新的状态
                if (currentStatus === 'no_schedule' || currentStatus === 'refreshing') {
                    return;
                }

                // 跳过无效的倒计时值
                if (currentCountdown === undefined || currentCountdown < 0) {
                    return;
                }

                if (currentCountdown > 0) {
                    // 递减倒计时
                    newCountdowns[roomId] = currentCountdown - 1;
                    hasChanges = true;

                    // 如果倒计时归零，更新状态为 idle
                    if (newCountdowns[roomId] === 0) {
                        newRefreshStatus[roomId] = 'idle';
                    }
                }
            });

            return hasChanges ? {
                ...prevState,
                countdownTimers: newCountdowns,
                refreshStatus: newRefreshStatus
            } : prevState;
        });
    }

    loadRoomLogs = (roomId: string) => {
        api.getLiveLogs(roomId, 100)
            .then((logs: any) => {
                this.setState(prevState => ({
                    expandedLogs: {
                        ...prevState.expandedLogs,
                        [roomId]: logs.lines || []
                    }
                }));
            })
            .catch(err => {
                message.warning(`获取直播间日志失败: ${err}`);
            });
    }

    // 格式化下载速度：将 ffmpeg 的 speed 值转换为 MB/s 或 KB/s
    formatDownloadSpeed = (recorderStatus: any): string => {
        if (!recorderStatus || !recorderStatus.bitrate) {
            return '';
        }

        // ffmpeg bitrate 格式如 "2345.6kbits/s"
        const bitrateStr = recorderStatus.bitrate;
        const match = bitrateStr.match(/([\d.]+)(k?bits\/s)/i);

        if (!match) {
            return recorderStatus.speed || ''; // 回退到原始 speed 值
        }

        let bitsPerSec = parseFloat(match[1]);
        const unit = match[2].toLowerCase();

        // 转换为 bits/s
        if (unit.startsWith('k')) {
            bitsPerSec *= 1000;
        }

        // 转换为 MB/s 或 KB/s
        const bytesPerSec = bitsPerSec / 8;
        const mbPerSec = bytesPerSec / (1024 * 1024);
        const kbPerSec = bytesPerSec / 1024;

        if (mbPerSec >= 1) {
            return `${mbPerSec.toFixed(2)} MB/s`;
        } else {
            return `${kbPerSec.toFixed(2)} KB/s`;
        }
    }

    // 格式化文件大小：将字节转换为可读格式
    formatFileSize = (sizeStr: string): string => {
        const bytes = parseInt(sizeStr, 10);
        if (isNaN(bytes) || bytes < 0) {
            return '未知';
        }

        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let size = bytes;
        let unitIndex = 0;

        while (size >= 1024 && unitIndex < units.length - 1) {
            size /= 1024;
            unitIndex++;
        }

        return `${size.toFixed(2)} ${units[unitIndex]}`;
    }

    renderExpandedRow = (record: ItemData): JSX.Element => {
        const { expandedDetails, expandedLogs, countdownTimers, refreshStatus } = this.state;
        const detail = expandedDetails[record.roomId];
        const logs = expandedLogs[record.roomId] || [];
        const countdown = countdownTimers[record.roomId] ?? 0;
        const status = refreshStatus[record.roomId] ?? 'idle';
        const liveId = record.roomId;
        // 保存 this 引用供嵌套函数使用
        const component = this;

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

        // 获取刷新状态的显示文本和颜色
        const getRefreshStatusDisplay = () => {
            // 暂无刷新计划状态
            if (status === 'no_schedule') {
                return {
                    text: '未安排刷新',
                    color: 'default' as const,
                    icon: null
                };
            }

            if (countdown > 0) {
                if (status === 'waiting_rate_limit') {
                    return {
                        text: `等待平台限制 ${countdown} 秒`,
                        color: 'red' as const,
                        icon: <SyncOutlined spin />
                    };
                } else {
                    return {
                        text: `${countdown} 秒`,
                        color: 'orange' as const,
                        icon: null
                    };
                }
            } else {
                if (status === 'refreshing') {
                    return {
                        text: '正在刷新',
                        color: 'blue' as const,
                        icon: <SyncOutlined spin />
                    };
                } else {
                    return {
                        text: '立即可用',
                        color: 'green' as const,
                        icon: null
                    };
                }
            }
        };

        // 运行时信息面板
        const renderRuntimePanel = () => {
            const handleForceRefresh = async () => {
                // 设置刷新中状态
                component.setState(prevState => ({
                    refreshStatus: {
                        ...prevState.refreshStatus,
                        [liveId]: 'refreshing'
                    }
                }));

                try {
                    const result = await api.forceRefreshLive(liveId) as { success?: boolean; message?: string };
                    if (result.success) {
                        message.success('强制刷新成功');
                        // 重新加载详细信息（会更新倒计时和状态）
                        component.loadRoomDetail(liveId);
                    } else {
                        message.error(result.message || '强制刷新失败');
                        // 恢复状态
                        component.setState(prevState => ({
                            refreshStatus: {
                                ...prevState.refreshStatus,
                                [liveId]: 'idle'
                            }
                        }));
                    }
                } catch (error) {
                    message.error('强制刷新失败');
                    // 恢复状态
                    component.setState(prevState => ({
                        refreshStatus: {
                            ...prevState.refreshStatus,
                            [liveId]: 'idle'
                        }
                    }));
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
                                {detail.recording && detail.recorder_status?.bitrate && (
                                    <div style={configRowStyle}>
                                        <span style={configLabelStyle}>下载速度</span>
                                        <Tag color="blue">{this.formatDownloadSpeed(detail.recorder_status)}</Tag>
                                    </div>
                                )}
                                {detail.recording && detail.recorder_status?.file_size && (
                                    <div style={configRowStyle}>
                                        <span style={configLabelStyle}>当前文件大小</span>
                                        <Tag color="green">{this.formatFileSize(detail.recorder_status.file_size)}</Tag>
                                    </div>
                                )}
                                {detail.recording && detail.recorder_status?.file_path && (
                                    <div style={configRowStyle}>
                                        <span style={configLabelStyle}>录制文件路径</span>
                                        <Tooltip title={detail.recorder_status.file_path}>
                                            <span style={{
                                                maxWidth: '200px',
                                                overflow: 'hidden',
                                                textOverflow: 'ellipsis',
                                                whiteSpace: 'nowrap',
                                                display: 'inline-block',
                                                verticalAlign: 'middle',
                                                cursor: 'pointer'
                                            }}>
                                                {detail.recorder_status.file_path.split(/[/\\]/).pop() || detail.recorder_status.file_path}
                                            </span>
                                        </Tooltip>
                                    </div>
                                )}
                                <div style={configRowStyle}>
                                    <span style={configLabelStyle}>开播时间</span>
                                    <span>{detail.live_start_time || (detail.status ? '获取中...' : '未开播')}</span>
                                </div>
                                <div style={{ ...configRowStyle, borderBottom: 'none' }}>
                                    <span style={configLabelStyle}>录制开始</span>
                                    <span>{detail.last_record_time || (detail.recording ? '获取中...' : '未在录制')}</span>
                                </div>
                            </div>

                            <Divider style={{ margin: '8px 0' }}>平台访问频率控制</Divider>
                            <div style={{ padding: '0 12px 8px' }}>
                                {detail.rate_limit_info ? (
                                    <div>
                                        <div style={configRowStyle}>
                                            <Tooltip
                                                title={
                                                    <div>
                                                        <p style={{ margin: '4px 0' }}>
                                                            <strong>直播平台级最小访问间隔</strong>
                                                        </p>
                                                        <p style={{ margin: '4px 0' }}>
                                                            为避免触发直播平台的风控机制，对同一平台的所有直播间请求会保持一定的时间间隔。
                                                        </p>
                                                        <p style={{ margin: '4px 0' }}>
                                                            即使同时监控多个{detail.platform}直播间，两次请求之间也会至少间隔该时长。
                                                        </p>
                                                        <p style={{ margin: '4px 0', color: '#faad14' }}>
                                                            可在配置文件的 platform_configs 中自定义各平台的 min_access_interval_sec
                                                        </p>
                                                    </div>
                                                }
                                                placement="right"
                                            >
                                                <span style={{ ...configLabelStyle, cursor: 'help', textDecoration: 'underline dotted' }}>
                                                    平台最小访问间隔
                                                </span>
                                            </Tooltip>
                                            <Tag>{detail.rate_limit_info.min_interval_sec || detail.platform_rate_limit} 秒</Tag>
                                        </div>
                                        <div style={configRowStyle}>
                                            <span style={configLabelStyle}>距上次请求</span>
                                            <span>{Math.round(detail.rate_limit_info.waited_seconds || 0)} 秒</span>
                                        </div>
                                        <div style={configRowStyle}>
                                            <span style={configLabelStyle}>距离下次刷新</span>
                                            {(() => {
                                                const statusDisplay = getRefreshStatusDisplay();
                                                return (
                                                    <Tag color={statusDisplay.color} icon={statusDisplay.icon}>
                                                        {statusDisplay.text}
                                                    </Tag>
                                                );
                                            })()}
                                        </div>
                                        <div style={{ marginTop: 12, borderBottom: 'none' }}>
                                            <Button
                                                type="primary"
                                                size="small"
                                                onClick={handleForceRefresh}
                                                loading={status === 'refreshing'}
                                                icon={<ReloadOutlined />}
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
                this.setState(prevState => ({
                    expandedLogs: {
                        ...prevState.expandedLogs,
                        [record.roomId]: newLogs
                    }
                }));
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
                    <Tabs.TabPane tab="直播历史" key="history">
                        <HistoryPanel roomId={record.roomId} roomName={record.name} />
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
                            <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                                <Tooltip title={this.state.enableListSSE
                                    ? "实时更新已启用：列表变化将自动同步"
                                    : "实时更新已禁用：需手动刷新页面查看变化"}>
                                    <Space size="small">
                                        <CloudSyncOutlined style={{ color: this.state.enableListSSE ? '#1890ff' : '#999' }} />
                                        <Switch
                                            size="small"
                                            checked={this.state.enableListSSE}
                                            onChange={(checked) => {
                                                setListSSEEnabled(checked);
                                                // 状态更新会通过 handleLocalSettingsChange 事件处理
                                            }}
                                        />
                                    </Space>
                                </Tooltip>
                                <Button key="2" type="default" onClick={this.onSettingSave}>保存设置</Button>
                                <Button key="1" type="primary" onClick={this.onAddRoomClick}>
                                    添加房间
                                </Button>
                                <AddRoomDialog key="0" ref={this.onRef} refresh={this.refresh} />
                            </div>
                        </div>
                        <Table
                            className="item-pad"
                            columns={this.getColumnsWithSort((this.state.window.screen.width > 768) ? this.columns : this.smallColumns)}
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
                            onChange={this.handleTableChange}
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
