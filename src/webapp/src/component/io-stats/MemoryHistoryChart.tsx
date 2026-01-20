import React, { useMemo, useState, useEffect, useCallback } from 'react';
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from 'recharts';
import { Card, Checkbox, Space, Select, Empty, Spin } from 'antd';
import dayjs from 'dayjs';

interface MemoryStat {
  id: number;
  timestamp: number;
  category: string;
  rss: number;
  vms?: number;
  alloc?: number;
  sys?: number;
  num_gc?: number;
}

interface MemoryStatsResponse {
  stats: MemoryStat[];
  grouped_stats: Record<string, MemoryStat[]>;
}

interface Props {
  startTime: number;
  endTime: number;
}

// 类别配置
const CATEGORY_CONFIG: Record<string, { name: string; color: string }> = {
  self: { name: '主进程', color: '#1890ff' },
  ffmpeg: { name: 'FFmpeg', color: '#52c41a' },
  'bililive-tools': { name: 'bililive-tools', color: '#722ed1' },
  klive: { name: 'klive', color: '#eb2f96' },
  'bililive-recorder': { name: '录播姬', color: '#fa8c16' },
  container: { name: '容器总内存', color: '#fa541c' },
  total: { name: '总内存', color: '#13c2c2' },
  other: { name: '其他', color: '#8c8c8c' },
};

// 格式化内存大小
const formatBytes = (bytes: number): string => {
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
    <Card title="内存使用历史">
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
          const config = CATEGORY_CONFIG[category] || { name: category, color: '#8c8c8c' };
          return (
            <Checkbox
              key={category}
              checked={visibleCategories.has(category)}
              onChange={() => toggleCategory(category)}
            >
              <span style={{ color: config.color }}>{config.name}</span>
            </Checkbox>
          );
        })}
      </Space>

      {/* 图表 */}
      <ResponsiveContainer width="100%" height={400}>
        <AreaChart
          data={chartData}
          margin={{ top: 10, right: 30, left: 0, bottom: 0 }}
        >
          <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
          <XAxis
            dataKey="time"
            tick={{ fontSize: 12 }}
            stroke="#999"
          />
          <YAxis
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
          <Tooltip
            formatter={(value, name) => {
              const numValue = typeof value === 'number' ? value : parseFloat(String(value));
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
            formatter={(value) => CATEGORY_CONFIG[value]?.name || value}
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
        </AreaChart>
      </ResponsiveContainer>
    </Card>
  );
};

export default MemoryHistoryChart;
