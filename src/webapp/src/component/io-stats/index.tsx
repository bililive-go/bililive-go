import React, { useState, useEffect, useCallback } from 'react';
import { Card, Tabs, Select, DatePicker, Space, Spin, Alert, Button, Radio, Tooltip } from 'antd';
import { ReloadOutlined, AreaChartOutlined, BarChartOutlined, DashboardOutlined } from '@ant-design/icons';
import dayjs, { Dayjs } from 'dayjs';
import NetworkChart from './NetworkChart';
import DiskIOChart from './DiskIOChart';
import RequestStatusChart from './RequestStatusChart';
import MemoryStats from './MemoryStats';
import MemoryHistoryChart from './MemoryHistoryChart';
import './index.css';

const { RangePicker } = DatePicker;
const { TabPane } = Tabs;

// 类型定义
interface IOStat {
  id: number;
  timestamp: number;
  stat_type: string;
  live_id: string;
  platform: string;
  speed: number;
  total_bytes: number;
}

interface RequestStatusSegment {
  start_time: number;
  end_time: number;
  success: boolean;
  count: number;
}

interface RequestStatusResponse {
  segments: RequestStatusSegment[];
  grouped_segments?: Record<string, RequestStatusSegment[]>;
}

interface FiltersResponse {
  live_ids: string[];
  platforms: string[];
}

// API 调用函数
const fetchIOStats = async (params: {
  start_time: number;
  end_time: number;
  stat_types?: string[];
  live_id?: string;
  aggregation?: string;
}): Promise<IOStat[]> => {
  const searchParams = new URLSearchParams();
  searchParams.append('start_time', params.start_time.toString());
  searchParams.append('end_time', params.end_time.toString());
  if (params.stat_types && params.stat_types.length > 0) {
    searchParams.append('stat_types', params.stat_types.join(','));
  }
  if (params.live_id) {
    searchParams.append('live_id', params.live_id);
  }
  if (params.aggregation) {
    searchParams.append('aggregation', params.aggregation);
  }

  const response = await fetch(`/api/iostats?${searchParams.toString()}`);
  const data = await response.json();
  if (data.err_no !== 0) {
    throw new Error(data.err_msg || '获取 IO 统计失败');
  }
  return data.data?.stats || [];
};

const fetchRequestStatus = async (params: {
  start_time: number;
  end_time: number;
  view_mode: string;
  live_id?: string;
  platform?: string;
}): Promise<RequestStatusResponse> => {
  const searchParams = new URLSearchParams();
  searchParams.append('start_time', params.start_time.toString());
  searchParams.append('end_time', params.end_time.toString());
  searchParams.append('view_mode', params.view_mode);
  if (params.live_id) {
    searchParams.append('live_id', params.live_id);
  }
  if (params.platform) {
    searchParams.append('platform', params.platform);
  }

  const response = await fetch(`/api/iostats/requests?${searchParams.toString()}`);
  const data = await response.json();
  if (data.err_no !== 0) {
    throw new Error(data.err_msg || '获取请求状态失败');
  }
  return data.data || { segments: [], grouped_segments: {} };
};

const fetchFilters = async (): Promise<FiltersResponse> => {
  const response = await fetch('/api/iostats/filters');
  const data = await response.json();
  if (data.err_no !== 0) {
    throw new Error(data.err_msg || '获取筛选器失败');
  }
  return data.data || { live_ids: [], platforms: [] };
};

const IOStatsPage: React.FC = () => {
  // 状态
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 时间范围（默认最近 1 小时）
  const [timeRange, setTimeRange] = useState<[Dayjs, Dayjs]>([
    dayjs().subtract(1, 'hour'),
    dayjs()
  ]);

  // 筛选器
  const [filters, setFilters] = useState<FiltersResponse>({ live_ids: [], platforms: [] });
  const [selectedLiveId, setSelectedLiveId] = useState<string>('');

  // IO 统计数据
  const [networkStats, setNetworkStats] = useState<IOStat[]>([]);
  const [diskStats, setDiskStats] = useState<IOStat[]>([]);

  // 请求状态数据
  const [viewMode, setViewMode] = useState<'global' | 'by_live' | 'by_platform'>('global');
  const [requestStatusData, setRequestStatusData] = useState<RequestStatusResponse>({ segments: [], grouped_segments: {} });

  // 数据聚合粒度
  const [aggregation, setAggregation] = useState<string>('none');

  // 加载筛选器选项
  useEffect(() => {
    fetchFilters().then(setFilters).catch(console.error);
  }, []);

  // 加载数据
  const loadData = useCallback(async () => {
    if (!timeRange[0] || !timeRange[1]) return;

    setLoading(true);
    setError(null);

    try {
      const startTime = timeRange[0].valueOf();
      const endTime = timeRange[1].valueOf();

      // 并行加载所有数据
      const [networkData, diskData, requestData] = await Promise.all([
        fetchIOStats({
          start_time: startTime,
          end_time: endTime,
          stat_types: ['network_download'],
          live_id: selectedLiveId || undefined,
          aggregation: aggregation === 'none' ? undefined : aggregation,
        }),
        fetchIOStats({
          start_time: startTime,
          end_time: endTime,
          stat_types: ['disk_record_write', 'disk_fix_read', 'disk_fix_write', 'disk_convert_read', 'disk_convert_write'],
          live_id: selectedLiveId || undefined,
          aggregation: aggregation === 'none' ? undefined : aggregation,
        }),
        fetchRequestStatus({
          start_time: startTime,
          end_time: endTime,
          view_mode: viewMode,
          live_id: viewMode === 'by_live' ? selectedLiveId : undefined,
        }),
      ]);

      setNetworkStats(networkData);
      setDiskStats(diskData);
      setRequestStatusData(requestData);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [timeRange, selectedLiveId, aggregation, viewMode]);

  // 初次加载
  useEffect(() => {
    loadData();
  }, [loadData]);

  // 时间范围快捷选项
  const presetRanges: Record<string, [Dayjs, Dayjs]> = {
    '最近1小时': [dayjs().subtract(1, 'hour'), dayjs()],
    '最近6小时': [dayjs().subtract(6, 'hour'), dayjs()],
    '最近24小时': [dayjs().subtract(24, 'hour'), dayjs()],
    '最近3天': [dayjs().subtract(3, 'day'), dayjs()],
    '最近7天': [dayjs().subtract(7, 'day'), dayjs()],
  };

  return (
    <div className="io-stats-page">
      {/* 工具栏 */}
      <Card className="toolbar-card" size="small">
        <Space wrap>
          <RangePicker
            showTime
            value={timeRange}
            onChange={(dates) => {
              if (dates && dates[0] && dates[1]) {
                setTimeRange([dates[0], dates[1]]);
              }
            }}
            presets={Object.entries(presetRanges).map(([label, range]) => ({
              label,
              value: range,
            }))}
          />

          <Select
            style={{ width: 200 }}
            placeholder="选择直播间"
            allowClear
            value={selectedLiveId || undefined}
            onChange={(value) => setSelectedLiveId(value || '')}
            options={[
              { value: '', label: '全部直播间' },
              ...filters.live_ids.map(id => ({ value: id, label: id }))
            ]}
          />

          <Select
            style={{ width: 120 }}
            value={aggregation}
            onChange={setAggregation}
            options={[
              { value: 'none', label: '原始数据' },
              { value: 'minute', label: '按分钟' },
              { value: 'hour', label: '按小时' },
            ]}
          />

          <Tooltip title="刷新数据">
            <Button
              icon={<ReloadOutlined />}
              onClick={loadData}
              loading={loading}
            >
              刷新
            </Button>
          </Tooltip>
        </Space>
      </Card>

      {/* 错误提示 */}
      {error && (
        <Alert
          message="加载失败"
          description={error}
          type="error"
          showIcon
          closable
          onClose={() => setError(null)}
          style={{ marginBottom: 16 }}
        />
      )}

      {/* 图表区域 */}
      <Spin spinning={loading}>
        <Tabs defaultActiveKey="network">
          <TabPane
            tab={
              <span>
                <AreaChartOutlined />
                网络速度
              </span>
            }
            key="network"
          >
            <Card>
              <NetworkChart data={networkStats} />
            </Card>
          </TabPane>

          <TabPane
            tab={
              <span>
                <AreaChartOutlined />
                磁盘 IO
              </span>
            }
            key="disk"
          >
            <Card>
              <DiskIOChart data={diskStats} />
            </Card>
          </TabPane>

          <TabPane
            tab={
              <span>
                <BarChartOutlined />
                请求状态
              </span>
            }
            key="request"
          >
            <Card>
              <Space style={{ marginBottom: 16 }}>
                <span>查看模式：</span>
                <Radio.Group value={viewMode} onChange={(e) => setViewMode(e.target.value)}>
                  <Radio.Button value="global">全局</Radio.Button>
                  <Radio.Button value="by_live">按直播间</Radio.Button>
                  <Radio.Button value="by_platform">按平台</Radio.Button>
                </Radio.Group>
              </Space>
              <RequestStatusChart
                data={requestStatusData}
                viewMode={viewMode}
                liveIds={filters.live_ids}
                platforms={filters.platforms}
              />
            </Card>
          </TabPane>

          <TabPane
            tab={
              <span>
                <DashboardOutlined />
                系统资源
              </span>
            }
            key="system"
          >
            <MemoryStats />
            <div style={{ marginTop: 16 }}>
              <MemoryHistoryChart
                startTime={timeRange[0].valueOf()}
                endTime={timeRange[1].valueOf()}
              />
            </div>
          </TabPane>
        </Tabs>
      </Spin>
    </div >
  );
};

export default IOStatsPage;
