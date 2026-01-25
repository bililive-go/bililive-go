import React, { useMemo, useState } from 'react';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from 'recharts';
import { Checkbox, Space } from 'antd';
import dayjs from 'dayjs';

interface IOStat {
  id: number;
  timestamp: number;
  stat_type: string;
  live_id: string;
  platform: string;
  speed: number;
  total_bytes: number;
}

interface Props {
  data: IOStat[];
}

// 统计类型配置
const STAT_TYPES = {
  disk_record_write: { name: '录制写入', color: '#52c41a' },
  disk_fix_read: { name: 'FLV修复读取', color: '#1890ff' },
  disk_fix_write: { name: 'FLV修复写入', color: '#13c2c2' },
  disk_convert_read: { name: 'MP4转换读取', color: '#722ed1' },
  disk_convert_write: { name: 'MP4转换写入', color: '#eb2f96' },
} as const;

type StatTypeKey = keyof typeof STAT_TYPES;

// 格式化速度为可读字符串
const formatSpeed = (value: number): string => {
  if (value >= 1024 * 1024 * 1024) {
    return `${(value / (1024 * 1024 * 1024)).toFixed(2)} GB/s`;
  } else if (value >= 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(2)} MB/s`;
  } else if (value >= 1024) {
    return `${(value / 1024).toFixed(2)} KB/s`;
  }
  return `${value} B/s`;
};

// 格式化时间
const formatTime = (timestamp: number): string => {
  return dayjs(timestamp).format('HH:mm:ss');
};

const DiskIOChart: React.FC<Props> = ({ data }) => {
  // 控制显示的数据类型
  const [visibleTypes, setVisibleTypes] = useState<Set<StatTypeKey>>(
    new Set(Object.keys(STAT_TYPES) as StatTypeKey[])
  );

  // 转换数据为图表格式
  const chartData = useMemo(() => {
    // 按时间分组
    const timeMap = new Map<number, Record<string, number>>();

    for (const item of data) {
      const key = item.timestamp;
      if (!timeMap.has(key)) {
        timeMap.set(key, { timestamp: item.timestamp });
      }
      const record = timeMap.get(key)!;
      record[item.stat_type] = item.speed;
    }

    // 转换为数组并排序
    return Array.from(timeMap.values())
      .sort((a, b) => a.timestamp - b.timestamp)
      .map(item => ({
        ...item,
        time: formatTime(item.timestamp),
      }));
  }, [data]);

  // 切换类型可见性
  const toggleType = (type: StatTypeKey) => {
    const newSet = new Set(visibleTypes);
    if (newSet.has(type)) {
      newSet.delete(type);
    } else {
      newSet.add(type);
    }
    setVisibleTypes(newSet);
  };

  if (chartData.length === 0) {
    return (
      <div className="chart-empty">
        <p>暂无磁盘 IO 数据</p>
        <p style={{ color: '#999', fontSize: 12 }}>开始录制或执行 FLV 修复/MP4 转换任务后将显示 IO 统计</p>
      </div>
    );
  }

  return (
    <div className="chart-container">
      <h4 style={{ marginBottom: 16, color: '#666' }}>磁盘读写速度</h4>

      {/* 数据类型筛选器 */}
      <Space style={{ marginBottom: 16 }} wrap>
        {Object.entries(STAT_TYPES).map(([key, config]) => (
          <Checkbox
            key={key}
            checked={visibleTypes.has(key as StatTypeKey)}
            onChange={() => toggleType(key as StatTypeKey)}
            style={{
              '--checkbox-color': config.color,
            } as React.CSSProperties}
          >
            <span style={{ color: config.color }}>{config.name}</span>
          </Checkbox>
        ))}
      </Space>

      <ResponsiveContainer width="100%" height={400}>
        <LineChart
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
              if (value >= 1024 * 1024) {
                return `${(value / (1024 * 1024)).toFixed(0)} MB/s`;
              } else if (value >= 1024) {
                return `${(value / 1024).toFixed(0)} KB/s`;
              }
              return `${value} B/s`;
            }}
            tick={{ fontSize: 12 }}
            stroke="#999"
          />
          <Tooltip
            formatter={(value, name) => {
              const numValue = typeof value === 'number' ? value : parseFloat(String(value));
              return [formatSpeed(numValue), String(name)];
            }}
            labelFormatter={(label) => `时间: ${label}`}
            contentStyle={{
              backgroundColor: 'rgba(255, 255, 255, 0.95)',
              border: '1px solid #d9d9d9',
              borderRadius: 4,
            }}
          />
          <Legend />
          {Object.entries(STAT_TYPES).map(([key, config]) =>
            visibleTypes.has(key as StatTypeKey) && (
              <Line
                key={key}
                type="monotone"
                dataKey={key}
                name={config.name}
                stroke={config.color}
                strokeWidth={2}
                dot={false}
                activeDot={{ r: 6 }}
                connectNulls
              />
            )
          )}
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
};

export default DiskIOChart;
