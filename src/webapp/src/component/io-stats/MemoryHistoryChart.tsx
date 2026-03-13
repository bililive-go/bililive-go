import React, { useMemo, useState, useEffect, useCallback } from 'react';
import {
  ComposedChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip as RechartsTooltip,
  Legend,
  ResponsiveContainer,
  Line,
} from 'recharts';
import { Card, Checkbox, Space, Select, Empty, Spin, Tooltip, Switch, Typography } from 'antd';
import { InfoCircleOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';

const { Text } = Typography;

interface MemoryStat {
  id: number;
  timestamp: number;
  category: string;
  rss: number;
  vms?: number;
  alloc?: number;
  sys?: number;
  num_gc?: number;
  num_goroutine?: number;
}

interface MemoryStatsResponse {
  stats: MemoryStat[];
  grouped_stats: Record<string, MemoryStat[]>;
}

interface Props {
  startTime: number;
  endTime: number;
}

// 类别配置：名称、颜色、说明
const CATEGORY_CONFIG: Record<string, { name: string; color: string; description: string }> = {
  self: {
    name: '主进程',
    color: '#1890ff',
    description: '主进程 (bililive-go) 的物理内存 (RSS)。RSS 是实际占用的物理内存，不包含未映射到物理页的虚拟内存。',
  },
  ffmpeg: {
    name: 'FFmpeg',
    color: '#52c41a',
    description: 'FFmpeg 子进程的物理内存总和。每个正在录制的直播间会启动一个 FFmpeg 进程。',
  },
  'bililive-tools': {
    name: 'bililive-tools',
    color: '#722ed1',
    description: 'bililive-tools (FLV 修复工具) 子进程的物理内存。',
  },
  klive: {
    name: 'klive',
    color: '#eb2f96',
    description: 'klive 子进程的物理内存。klive 是远程访问代理。',
  },
  'bililive-recorder': {
    name: '录播姬',
    color: '#fa8c16',
    description: 'BililiveRecorder (录播姬) 子进程的物理内存。',
  },
  launcher: {
    name: '启动器',
    color: '#2f54eb',
    description: '启动器进程的物理内存。',
  },
  container: {
    name: '容器总内存',
    color: '#fa541c',
    description: '容器 (Docker/cgroup) 报告的总内存使用。这是 cgroup memory.current 的值，包含了容器内所有进程的内存、内核缓存、页缓存等。数值通常远大于各进程 RSS 之和，因为它也算入了操作系统用于缓存文件的内存。',
  },
  total: {
    name: '总内存',
    color: '#13c2c2',
    description: '所有进程物理内存的总和 (主进程 + 子进程)。在容器环境中，此值取 cgroup 报告的内存。注意：总内存可能远大于堆内存，因为它包含了代码段、共享库、文件缓存等。',
  },
  other: {
    name: '其他',
    color: '#8c8c8c',
    description: '未归类的其他进程。',
  },
};

// 格式化内存大小
const formatBytes = (bytes: number): string => {
  if (!bytes || bytes <= 0) return '0 B';
  if (bytes >= 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  } else if (bytes >= 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  } else if (bytes >= 1024) {
    return `${(bytes / 1024).toFixed(2)} KB`;
  }
  return `${bytes} B`;
};

// 格式化时间
const formatTime = (timestamp: number): string => {
  return dayjs(timestamp).format('HH:mm:ss');
};

const MemoryHistoryChart: React.FC<Props> = ({ startTime, endTime }) => {
  const [data, setData] = useState<MemoryStatsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [visibleCategories, setVisibleCategories] = useState<Set<string>>(
    new Set(['self', 'total', 'ffmpeg', 'bililive-tools'])
  );
  const [aggregation, setAggregation] = useState<string>('none');
  const [showGoroutines, setShowGoroutines] = useState(false);

  // 获取数据
  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const params = new URLSearchParams({
        start_time: startTime.toString(),
        end_time: endTime.toString(),
      });
      if (aggregation && aggregation !== 'none') {
        params.append('aggregation', aggregation);
      }

      const response = await fetch(`/api/iostats/memory?${params}`);
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const jsonData = await response.json();
      if (jsonData.data) {
        setData(jsonData.data);
      }
    } catch (error) {
      console.error('Failed to fetch memory history:', error);
    } finally {
      setLoading(false);
    }
  }, [startTime, endTime, aggregation]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // 获取可用的类别
  const availableCategories = useMemo(() => {
    if (!data?.grouped_stats) return [];
    return Object.keys(data.grouped_stats);
  }, [data]);

  // 转换数据为图表格式
  const chartData = useMemo(() => {
    if (!data?.grouped_stats) return [];

    // 收集所有时间点
    const timeMap = new Map<number, Record<string, number>>();

    for (const [category, stats] of Object.entries(data.grouped_stats)) {
      if (!visibleCategories.has(category)) continue;

      for (const stat of stats) {
        const key = stat.timestamp;
        if (!timeMap.has(key)) {
          timeMap.set(key, { timestamp: stat.timestamp });
        }
        const record = timeMap.get(key)!;
        record[category] = stat.rss;
        // 对 self 类别，额外记录 VMS 和 Goroutine 数据
        if (category === 'self') {
          if (stat.vms) record['self_vms'] = stat.vms;
          if (stat.num_goroutine) record['goroutines'] = stat.num_goroutine;
        }
      }
    }

    // 转换为数组并排序
    return Array.from(timeMap.values())
      .sort((a, b) => a.timestamp - b.timestamp)
      .map(item => ({
        ...item,
        time: formatTime(item.timestamp),
      }));
  }, [data, visibleCategories]);

  // 切换类别可见性
  const toggleCategory = (category: string) => {
    const newSet = new Set(visibleCategories);
    if (newSet.has(category)) {
      newSet.delete(category);
    } else {
      newSet.add(category);
    }
    setVisibleCategories(newSet);
  };

  if (loading) {
    return (
      <Card>
        <div style={{ textAlign: 'center', padding: 40 }}>
          <Spin tip="加载中..." />
        </div>
      </Card>
    );
  }

  if (!data || chartData.length === 0) {
    return (
      <Card title="内存使用历史">
        <Empty description="暂无历史数据，请稍等片刻让系统收集数据" />
      </Card>
    );
  }

  return (
    <Card
      title={
        <span>
          内存使用历史{' '}
          <Tooltip
            title={
              <div>
                <p><strong>关于「总内存」和「容器总内存」为什么数值很大：</strong></p>
                <p>• <strong>总内存</strong>：所有进程实际物理内存 (RSS) 之和。在容器中取 cgroup 报告值。</p>
                <p>• <strong>容器总内存</strong>：cgroup memory.current，包含所有进程内存 + 内核页缓存 + tmpfs 等。操作系统会利用空闲内存缓存文件，所以这个值通常远大于进程实际使用的内存。</p>
                <p>• <strong>主进程 RSS</strong> 才是最准确的「bgo 实际使用了多少物理内存」的指标。</p>
                <p style={{ marginTop: 8 }}>鼠标悬浮在类别复选框上可查看各项含义。</p>
              </div>
            }
            overlayStyle={{ maxWidth: 400 }}
          >
            <InfoCircleOutlined style={{ color: '#999', fontSize: 14, marginLeft: 4 }} />
          </Tooltip>
        </span>
      }
    >
      {/* 控制栏 */}
      <Space style={{ marginBottom: 16 }} wrap>
        <span>聚合:</span>
        <Select
          value={aggregation}
          onChange={setAggregation}
          style={{ width: 100 }}
          options={[
            { label: '无', value: 'none' },
            { label: '分钟', value: 'minute' },
            { label: '小时', value: 'hour' },
          ]}
        />
        <span style={{ marginLeft: 16 }}>显示类别:</span>
        {availableCategories.map(category => {
          const config = CATEGORY_CONFIG[category] || { name: category, color: '#8c8c8c', description: '' };
          return (
            <Tooltip key={category} title={config.description} overlayStyle={{ maxWidth: 350 }}>
              <Checkbox
                checked={visibleCategories.has(category)}
                onChange={() => toggleCategory(category)}
              >
                <span style={{ color: config.color }}>{config.name}</span>
              </Checkbox>
            </Tooltip>
          );
        })}
        {visibleCategories.has('self') && (
          <>
            <span style={{ marginLeft: 16 }}>|</span>
            <Tooltip title="在图表中显示 Goroutine 数量的折线（使用右侧 Y 轴）">
              <span style={{ marginLeft: 8 }}>
                <Switch
                  size="small"
                  checked={showGoroutines}
                  onChange={setShowGoroutines}
                />{' '}
                <Text type="secondary" style={{ fontSize: 12 }}>Goroutine</Text>
              </span>
            </Tooltip>
          </>
        )}
      </Space>

      {/* 内存使用图表 */}
      <ResponsiveContainer width="100%" height={400}>
        <ComposedChart
          data={chartData}
          margin={{ top: 10, right: showGoroutines ? 60 : 30, left: 0, bottom: 0 }}
        >
          <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
          <XAxis
            dataKey="time"
            tick={{ fontSize: 12 }}
            stroke="#999"
          />
          <YAxis
            yAxisId="memory"
            tickFormatter={(value) => {
              if (value >= 1024 * 1024 * 1024) {
                return `${(value / (1024 * 1024 * 1024)).toFixed(0)} GB`;
              } else if (value >= 1024 * 1024) {
                return `${(value / (1024 * 1024)).toFixed(0)} MB`;
              } else if (value >= 1024) {
                return `${(value / 1024).toFixed(0)} KB`;
              }
              return `${value} B`;
            }}
            tick={{ fontSize: 12 }}
            stroke="#999"
          />
          {showGoroutines && (
            <YAxis
              yAxisId="goroutine"
              orientation="right"
              tick={{ fontSize: 12 }}
              stroke="#ff7a45"
              label={{ value: 'Goroutine', angle: -90, position: 'insideRight', style: { fontSize: 11, fill: '#ff7a45' } }}
            />
          )}
          <RechartsTooltip
            formatter={(value, name) => {
              const numValue = typeof value === 'number' ? value : parseFloat(String(value));
              if (name === 'goroutines') {
                return [numValue.toLocaleString(), 'Goroutine 数'];
              }
              if (name === 'self_vms') {
                return [formatBytes(numValue), '主进程虚拟内存 (VMS)'];
              }
              const categoryName = CATEGORY_CONFIG[name as string]?.name || name;
              return [formatBytes(numValue), categoryName];
            }}
            labelFormatter={(label) => `时间: ${label}`}
            contentStyle={{
              backgroundColor: 'rgba(255, 255, 255, 0.95)',
              border: '1px solid #d9d9d9',
              borderRadius: 4,
            }}
          />
          <Legend
            formatter={(value) => {
              if (value === 'goroutines') return 'Goroutine 数';
              if (value === 'self_vms') return '主进程 VMS (虚拟内存)';
              return CATEGORY_CONFIG[value]?.name || value;
            }}
          />
          {availableCategories.map(category => {
            if (!visibleCategories.has(category)) return null;
            const config = CATEGORY_CONFIG[category] || { name: category, color: '#8c8c8c' };
            return (
              <Area
                key={category}
                type="monotone"
                dataKey={category}
                name={category}
                yAxisId="memory"
                stroke={config.color}
                fill={config.color}
                fillOpacity={0.3}
                strokeWidth={2}
                dot={false}
                activeDot={{ r: 6 }}
                connectNulls
              />
            );
          })}
          {/* 主进程虚拟内存 (VMS) 虚线 - 仅在显示 self 时 */}
          {visibleCategories.has('self') && (
            <Line
              type="monotone"
              dataKey="self_vms"
              name="self_vms"
              yAxisId="memory"
              stroke="#1890ff"
              strokeWidth={1.5}
              strokeDasharray="5 3"
              dot={false}
              connectNulls
            />
          )}
          {/* Goroutine 折线 */}
          {showGoroutines && (
            <Line
              type="monotone"
              dataKey="goroutines"
              name="goroutines"
              yAxisId="goroutine"
              stroke="#ff7a45"
              strokeWidth={2}
              dot={false}
              activeDot={{ r: 4 }}
              connectNulls
            />
          )}
        </ComposedChart>
      </ResponsiveContainer>
    </Card>
  );
};

export default MemoryHistoryChart;
