import React from 'react';
import { Card, Empty, Tag } from 'antd';
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { DiagnosticMetric } from './types';

interface Props {
  metrics: DiagnosticMetric[];
  configuredIntervalMs: number;
  totalMs: number;
  windowStartMs?: number;
  referenceStartMs?: number;
}

const METRIC_LABELS: Record<string, string> = {
  'tools.ffmpeg.downloaded_bytes': 'FFmpeg 下载进度',
  'record.output_bytes': 'FLV 文件大小',
  'process.cpu_percent': '进程 CPU',
  'disk.write_latency_p95': '磁盘写入延迟 P95',
  'platform.rate_limiter.queue_depth': '平台限流队列深度',
  'platform.rate_limiter.waiting_rooms': '等待中的直播间',
  'platform.rate_limiter.waiter_count': '平台限流竞争等待者',
  'platform.rate_limiter.grants_total': '平台访问机会累计授予数',
  'target.rate_limiter.elapsed': '目标 generation 限流等待进度',
  'monitor.active_rooms': '已启动监听房间',
  'runtime.goroutines': 'Goroutine 数量',
  'stream.upstream_bytes': '上游累计字节',
  'runtime.scheduler_latency_p99_ms': '调度延迟 P99',
  'runtime.gc_pause_ms': 'GC 暂停',
};

const formatBytes = (value: number): string => {
  if (value >= 1024 * 1024 * 1024) return `${(value / (1024 * 1024 * 1024)).toFixed(1)} GiB`;
  if (value >= 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MiB`;
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${value.toFixed(0)} B`;
};

const formatValue = (value: number, unit: string): string => {
  if (unit === 'bytes') return formatBytes(value);
  if (unit === 'percent') return `${value.toFixed(1)}%`;
  if (unit === 'ms') return `${value.toFixed(value < 10 ? 2 : 0)} ms`;
  if (unit === 'count') return value.toFixed(0);
  return `${value.toFixed(1)} ${unit}`;
};

const MetricCard: React.FC<{
  metric: DiagnosticMetric;
  totalMs: number;
  configuredIntervalMs: number;
  windowStartMs: number;
  referenceStartMs: number;
}> = ({
  metric,
  totalMs,
  configuredIntervalMs,
  windowStartMs,
  referenceStartMs,
}) => {
  const data = metric.series.filter((point) => (
    point.ts >= windowStartMs && point.ts <= windowStartMs + totalMs
  )).map((point) => ({
    time: point.ts,
    seconds: (point.ts - windowStartMs) / 1000,
    value: point.value,
  }));
  const last = data[data.length - 1]?.value || 0;

  return (
    <Card
      className="diagnostic-metric-card"
      size="small"
      title={metric.label || METRIC_LABELS[metric.name] || metric.name}
      extra={<Tag bordered={false}>{formatValue(last, metric.unit)}</Tag>}
    >
      <div className="diagnostic-metric-chart" aria-label={`${metric.name} 指标曲线`}>
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data} margin={{ top: 4, right: 10, bottom: 0, left: -12 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="#edf1f7" />
            <XAxis
              dataKey="seconds"
              type="number"
              domain={[0, Math.max(totalMs / 1000, 1)]}
              tickFormatter={(value) => `+${value}s`}
              tick={{ fontSize: 10, fill: '#718096' }}
            />
            <YAxis
              tickFormatter={(value) => formatValue(Number(value), metric.unit)}
              tick={{ fontSize: 10, fill: '#718096' }}
              width={70}
            />
            <Tooltip
              labelFormatter={(value) => `+${Number(value).toFixed(1)} 秒`}
              formatter={(value) => [formatValue(Number(value), metric.unit), metric.label || METRIC_LABELS[metric.name] || metric.name]}
            />
            {configuredIntervalMs > 0
              && referenceStartMs - windowStartMs + configuredIntervalMs <= totalMs && (
              <ReferenceLine
                x={(referenceStartMs - windowStartMs + configuredIntervalMs) / 1000}
                stroke="#4f8cff"
                strokeDasharray="4 4"
              />
            )}
            <Line
              type="monotone"
              dataKey="value"
              stroke="#4f8cff"
              strokeWidth={2.2}
              dot={{ r: 2 }}
              activeDot={{ r: 5 }}
              isAnimationActive={false}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </Card>
  );
};

const Metrics: React.FC<Props> = ({
  metrics,
  configuredIntervalMs,
  totalMs,
  windowStartMs = 0,
  referenceStartMs = windowStartMs,
}) => {
  if (metrics.length === 0) {
    return <Empty description="数据包没有包含指标切片" />;
  }

  return (
    <div className="diagnostic-metric-grid">
      {metrics.slice(0, 12).map((metric, index) => (
        <MetricCard
          key={`${metric.name}-${index}`}
          metric={metric}
          configuredIntervalMs={configuredIntervalMs}
          totalMs={totalMs}
          windowStartMs={windowStartMs}
          referenceStartMs={referenceStartMs}
        />
      ))}
    </div>
  );
};

export default Metrics;
