import React, { useMemo } from 'react';
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

const NetworkChart: React.FC<Props> = ({ data }) => {
  // 转换数据为图表格式
  const chartData = useMemo(() => {
    // 按时间排序
    const sorted = [...data].sort((a, b) => a.timestamp - b.timestamp);

    // 转换为图表数据点
    return sorted.map(item => ({
      timestamp: item.timestamp,
      time: formatTime(item.timestamp),
      speed: item.speed,
      totalBytes: item.total_bytes,
      liveId: item.live_id || '全局',
    }));
  }, [data]);

  if (chartData.length === 0) {
    return (
      <div className="chart-empty">
        <p>暂无网络速度数据</p>
        <p style={{ color: '#999', fontSize: 12 }}>开始录制后将显示下载速度统计</p>
      </div>
    );
  }

  return (
    <div className="chart-container">
      <h4 style={{ marginBottom: 16, color: '#666' }}>网络下载速度</h4>
      <ResponsiveContainer width="100%" height={400}>
        <AreaChart
          data={chartData}
          margin={{ top: 10, right: 30, left: 0, bottom: 0 }}
        >
          <defs>
            <linearGradient id="colorSpeed" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#1890ff" stopOpacity={0.8} />
              <stop offset="95%" stopColor="#1890ff" stopOpacity={0.1} />
            </linearGradient>
          </defs>
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
            formatter={(value) => {
              const numValue = typeof value === 'number' ? value : parseFloat(String(value));
              return [formatSpeed(numValue), '下载速度'];
            }}
            labelFormatter={(label) => `时间: ${label}`}
            contentStyle={{
              backgroundColor: 'rgba(255, 255, 255, 0.95)',
              border: '1px solid #d9d9d9',
              borderRadius: 4,
            }}
          />
          <Legend />
          <Area
            type="monotone"
            dataKey="speed"
            name="下载速度"
            stroke="#1890ff"
            fillOpacity={1}
            fill="url(#colorSpeed)"
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 6 }}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
};

export default NetworkChart;
