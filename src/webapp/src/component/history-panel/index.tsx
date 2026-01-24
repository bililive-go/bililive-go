import React, { useState, useEffect, useCallback } from 'react';
import { Tag, Select, DatePicker, Pagination, Collapse, Empty, Spin, Space, Typography } from 'antd';
import { ClockCircleOutlined, EditOutlined } from '@ant-design/icons';
import API from '../../utils/api';
import dayjs, { Dayjs } from 'dayjs';

const { RangePicker } = DatePicker;
const { Text } = Typography;
const { Panel } = Collapse;

const api = new API();

// 事件类型
type EventType = 'session' | 'name_change';

// 时间范围预设
type TimePreset = 'day' | 'week' | 'month' | 'custom';

interface HistoryEvent {
  id: number;
  type: EventType;
  timestamp: string;
  data: any;
}

// HistoryResponse 接口用于 API 响应类型参考（当前使用 any）
// interface HistoryResponse {
//   live_id: string;
//   events: HistoryEvent[];
//   total: number;
//   page: number;
//   page_size: number;
//   total_pages: number;
// }

interface Props {
  roomId: string;
  roomName?: string;
}

// 格式化时间
const formatTime = (timeStr: string): string => {
  if (!timeStr) return '-';
  const date = new Date(timeStr);
  if (isNaN(date.getTime())) return '-';
  return dayjs(date).format('YYYY-MM-DD HH:mm:ss');
};

// 计算时长
const formatDuration = (startStr: string, endStr: string): string => {
  if (!startStr || !endStr) return '-';
  const start = new Date(startStr);
  const end = new Date(endStr);
  if (isNaN(start.getTime()) || isNaN(end.getTime())) return '-';

  const durationMs = end.getTime() - start.getTime();
  if (durationMs <= 0) return '-';

  const hours = Math.floor(durationMs / (1000 * 60 * 60));
  const minutes = Math.floor((durationMs % (1000 * 60 * 60)) / (1000 * 60));
  const seconds = Math.floor((durationMs % (1000 * 60)) / 1000);

  if (hours > 0) {
    return `${hours}小时${minutes}分钟`;
  } else if (minutes > 0) {
    return `${minutes}分钟${seconds}秒`;
  } else {
    return `${seconds}秒`;
  }
};

// 会话结束原因
const getEndReasonText = (reason: string): { text: string; color: string } => {
  switch (reason) {
    case 'normal':
      return { text: '主播下播', color: 'green' };
    case 'user_stop':
      return { text: '用户停止监控', color: 'blue' };
    case 'crash':
      return { text: '程序崩溃', color: 'red' };
    case 'error':
      return { text: '录制异常', color: 'orange' };
    default:
      return { text: '未知', color: 'default' };
  }
};

// 名称变更类型
const getNameTypeText = (nameType: string): string => {
  switch (nameType) {
    case 'host_name':
      return '主播名';
    case 'room_name':
      return '直播间名';
    default:
      return nameType;
  }
};

const HistoryPanel: React.FC<Props> = ({ roomId }) => {
  // 状态
  const [loading, setLoading] = useState(false);
  const [events, setEvents] = useState<HistoryEvent[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  // totalPages 暂未使用，但保留以备后续分页信息显示
  const [, setTotalPages] = useState(0);

  // 筛选条件
  const [selectedTypes, setSelectedTypes] = useState<EventType[]>(['session', 'name_change']);
  const [timePreset, setTimePreset] = useState<TimePreset>('week');
  const [customRange, setCustomRange] = useState<[Dayjs | null, Dayjs | null]>([null, null]);

  // 计算时间范围
  const getTimeRange = useCallback((): { start?: number; end?: number } => {
    const now = dayjs();
    switch (timePreset) {
      case 'day':
        return { start: now.subtract(1, 'day').unix() };
      case 'week':
        return { start: now.subtract(1, 'week').unix() };
      case 'month':
        return { start: now.subtract(1, 'month').unix() };
      case 'custom':
        if (customRange[0] && customRange[1]) {
          return {
            start: customRange[0].startOf('day').unix(),
            end: customRange[1].endOf('day').unix()
          };
        }
        return {};
      default:
        return {};
    }
  }, [timePreset, customRange]);

  // 加载数据
  const loadData = useCallback(() => {
    setLoading(true);
    const timeRange = getTimeRange();
    api.getLiveHistory(roomId, {
      page,
      pageSize,
      startTime: timeRange.start,
      endTime: timeRange.end,
      types: selectedTypes.length === 2 ? undefined : selectedTypes
    })
      .then((response: any) => {
        setEvents(response.events || []);
        setTotal(response.total || 0);
        setTotalPages(response.total_pages || 0);
      })
      .catch((error: any) => {
        console.error('加载历史事件失败:', error);
        setEvents([]);
        setTotal(0);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [roomId, page, pageSize, selectedTypes, getTimeRange]);

  // 初始加载和筛选条件变化时重新加载
  useEffect(() => {
    loadData();
  }, [loadData]);

  // 筛选条件变化时重置页码
  useEffect(() => {
    setPage(1);
  }, [selectedTypes, timePreset, customRange]);

  // 渲染会话事件
  const renderSessionEvent = (event: HistoryEvent) => {
    const session = event.data;
    const endReason = getEndReasonText(session.end_reason);
    const hasEnded = session.end_time && new Date(session.end_time).getTime() > 0;

    return (
      <div style={{ padding: '12px 16px' }}>
        <Space direction="vertical" style={{ width: '100%' }}>
          {/* 显示主播名和直播间名 */}
          {(session.host_name || session.room_name) && (
            <>
              {session.host_name && (
                <div>
                  <Text strong>主播名：</Text>
                  <Text>{session.host_name}</Text>
                </div>
              )}
              {session.room_name && (
                <div>
                  <Text strong>直播间名：</Text>
                  <Text>{session.room_name}</Text>
                </div>
              )}
            </>
          )}
          <div>
            <Text strong>开播时间：</Text>
            <Text>{formatTime(session.start_time)}</Text>
          </div>
          {hasEnded && (
            <>
              <div>
                <Text strong>下播时间：</Text>
                <Text>{formatTime(session.end_time)}</Text>
              </div>
              <div>
                <Text strong>直播时长：</Text>
                <Text>{formatDuration(session.start_time, session.end_time)}</Text>
              </div>
              <div>
                <Text strong>结束原因：</Text>
                <Tag color={endReason.color}>{endReason.text}</Tag>
              </div>
            </>
          )}
          {!hasEnded && (
            <div>
              <Text strong>下播时间：</Text>
              <Text type="secondary">未记录（可能正在直播或程序异常退出）</Text>
            </div>
          )}
        </Space>
      </div>
    );
  };

  // 渲染名称变更事件
  const renderNameChangeEvent = (event: HistoryEvent) => {
    const change = event.data;
    return (
      <div style={{ padding: '12px 16px' }}>
        <Space direction="vertical" style={{ width: '100%' }}>
          <div>
            <Text strong>变更类型：</Text>
            <Tag color="blue">{getNameTypeText(change.name_type)}</Tag>
          </div>
          <div>
            <Text strong>变更前：</Text>
            <Text delete type="secondary">{change.old_value || '(空)'}</Text>
          </div>
          <div>
            <Text strong>变更后：</Text>
            <Text type="success">{change.new_value || '(空)'}</Text>
          </div>
        </Space>
      </div>
    );
  };

  // 渲染事件列表项
  const renderEventItem = (event: HistoryEvent) => {
    const isSession = event.type === 'session';
    const icon = isSession ? <ClockCircleOutlined /> : <EditOutlined />;
    const tagColor = isSession ? 'blue' : 'orange';
    const tagText = isSession ? '直播会话' : '名称变更';
    const header = (
      <Space>
        {icon}
        <Tag color={tagColor}>{tagText}</Tag>
        <Text type="secondary">{formatTime(event.timestamp)}</Text>
      </Space>
    );

    return (
      <Panel header={header} key={`${event.type}-${event.id}`}>
        {isSession ? renderSessionEvent(event) : renderNameChangeEvent(event)}
      </Panel>
    );
  };

  return (
    <div style={{ padding: '16px 20px' }}>
      {/* 筛选条件 */}
      <div style={{ marginBottom: 16, display: 'flex', flexWrap: 'wrap', gap: 12 }}>
        {/* 事件类型筛选 */}
        <div>
          <Text strong style={{ marginRight: 8 }}>事件类型：</Text>
          <Select
            mode="multiple"
            value={selectedTypes}
            onChange={setSelectedTypes}
            style={{ minWidth: 200 }}
            placeholder="选择事件类型"
            options={[
              { value: 'session', label: '直播会话' },
              { value: 'name_change', label: '名称变更' }
            ]}
          />
        </div>

        {/* 时间范围筛选 */}
        <div>
          <Text strong style={{ marginRight: 8 }}>时间范围：</Text>
          <Select
            value={timePreset}
            onChange={setTimePreset}
            style={{ width: 120 }}
            options={[
              { value: 'day', label: '最近一天' },
              { value: 'week', label: '最近一周' },
              { value: 'month', label: '最近一月' },
              { value: 'custom', label: '自定义' }
            ]}
          />
        </div>

        {/* 自定义时间范围 */}
        {timePreset === 'custom' && (
          <div>
            <RangePicker
              value={customRange}
              onChange={(dates) => setCustomRange(dates as [Dayjs | null, Dayjs | null])}
              format="YYYY-MM-DD"
            />
          </div>
        )}

        {/* 每页条数 */}
        <div>
          <Text strong style={{ marginRight: 8 }}>每页：</Text>
          <Select
            value={pageSize}
            onChange={(v) => { setPageSize(v); setPage(1); }}
            style={{ width: 80 }}
            options={[
              { value: 10, label: '10' },
              { value: 20, label: '20' },
              { value: 50, label: '50' }
            ]}
          />
        </div>
      </div>

      {/* 事件列表 */}
      <Spin spinning={loading}>
        {events.length === 0 ? (
          <Empty description="暂无历史事件" />
        ) : (
          <>
            <Collapse accordion>
              {events.map(renderEventItem)}
            </Collapse>

            {/* 分页 */}
            <div style={{ marginTop: 16, textAlign: 'right' }}>
              <Pagination
                current={page}
                pageSize={pageSize}
                total={total}
                onChange={setPage}
                showSizeChanger={false}
                showTotal={(t) => `共 ${t} 条记录`}
                size="small"
              />
            </div>
          </>
        )}
      </Spin>
    </div>
  );
};

export default HistoryPanel;
