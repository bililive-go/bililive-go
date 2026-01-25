import React, { useMemo } from 'react';
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Cell } from 'recharts';
import { Empty, Tag } from 'antd';
import dayjs from 'dayjs';

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

interface Props {
  data: RequestStatusResponse;
  viewMode: 'global' | 'by_live' | 'by_platform';
  liveIds: string[];
  platforms: string[];
}

// 颜色配置
const COLORS = {
  success: '#52c41a',
  failure: '#ff4d4f',
};

// 格式化时间范围
const formatTimeRange = (start: number, end: number): string => {
  const startStr = dayjs(start).format('MM-DD HH:mm');
  const endStr = dayjs(end).format('HH:mm');
  return `${startStr} - ${endStr}`;
};

// 全局视图的横条图
const GlobalTimelineChart: React.FC<{ segments: RequestStatusSegment[] }> = ({ segments }) => {
  if (segments.length === 0) {
    return (
      <div className="chart-empty">
        <Empty description="暂无请求状态数据" />
      </div>
    );
  }

  // 转换数据为横条图格式
  const chartData = segments.map((seg, index) => ({
    id: index,
    name: formatTimeRange(seg.start_time, seg.end_time),
    duration: seg.end_time - seg.start_time,
    count: seg.count,
    success: seg.success,
    startTime: seg.start_time,
    endTime: seg.end_time,
  }));

  return (
    <div className="timeline-chart">
      <div className="timeline-legend" style={{ marginBottom: 16, display: 'flex', gap: 16 }}>
        <Tag color={COLORS.success}>成功</Tag>
        <Tag color={COLORS.failure}>失败</Tag>
      </div>
      <ResponsiveContainer width="100%" height={Math.max(200, segments.length * 40 + 60)}>
        <BarChart
          layout="vertical"
          data={chartData}
          margin={{ top: 20, right: 30, left: 100, bottom: 5 }}
        >
          <CartesianGrid strokeDasharray="3 3" horizontal={false} />
          <XAxis
            type="number"
            tickFormatter={(value) => {
              // 将持续时间转换为分钟
              const minutes = value / (1000 * 60);
              if (minutes >= 60) {
                return `${(minutes / 60).toFixed(1)}h`;
              }
              return `${minutes.toFixed(0)}m`;
            }}
            label={{ value: '持续时间', position: 'insideBottomRight', offset: -10 }}
          />
          <YAxis
            type="category"
            dataKey="name"
            width={90}
            tick={{ fontSize: 11 }}
          />
          <Tooltip
            content={({ active, payload }) => {
              if (active && payload && payload.length > 0) {
                const item = payload[0].payload;
                return (
                  <div style={{
                    backgroundColor: 'rgba(255, 255, 255, 0.95)',
                    border: '1px solid #d9d9d9',
                    borderRadius: 4,
                    padding: '8px 12px',
                    fontSize: 12,
                  }}>
                    <div>状态: {item.success ? '成功' : '失败'}</div>
                    <div>请求次数: {item.count}</div>
                    <div>持续时间: {Math.round((item.endTime - item.startTime) / 1000 / 60)} 分钟</div>
                  </div>
                );
              }
              return null;
            }}
          />
          <Bar dataKey="duration" name="持续时间">
            {chartData.map((entry, index) => (
              <Cell key={`cell-${index}`} fill={entry.success ? COLORS.success : COLORS.failure} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
};

// 分组视图（按直播间或按平台）
const GroupedTimelineChart: React.FC<{
  groupedSegments: Record<string, RequestStatusSegment[]>;
  groupType: 'live' | 'platform';
}> = ({ groupedSegments, groupType }) => {
  const groups = Object.entries(groupedSegments);

  if (groups.length === 0) {
    return (
      <div className="chart-empty">
        <Empty description={`暂无${groupType === 'live' ? '直播间' : '平台'}请求状态数据`} />
      </div>
    );
  }

  return (
    <div className="grouped-timeline">
      {groups.map(([groupName, segments]) => (
        <div key={groupName} style={{ marginBottom: 24 }}>
          <h5 style={{ marginBottom: 8, color: '#666' }}>
            {groupType === 'live' ? '直播间' : '平台'}: {groupName}
          </h5>
          <div className="segment-row" style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
            {segments.map((seg, index) => {
              // 计算每个时段的宽度（基于持续时间）
              const minWidth = 60;
              const duration = seg.end_time - seg.start_time;
              const width = Math.max(minWidth, Math.min(300, duration / (60 * 1000) * 5)); // 每分钟 5px

              return (
                <div
                  key={index}
                  className="segment-block"
                  style={{
                    width: width,
                    height: 32,
                    backgroundColor: seg.success ? COLORS.success : COLORS.failure,
                    borderRadius: 4,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    color: '#fff',
                    fontSize: 11,
                    cursor: 'pointer',
                    transition: 'transform 0.2s',
                  }}
                  title={`${formatTimeRange(seg.start_time, seg.end_time)}\n请求次数: ${seg.count}\n状态: ${seg.success ? '成功' : '失败'}`}
                  onMouseEnter={(e) => {
                    (e.target as HTMLElement).style.transform = 'scale(1.05)';
                  }}
                  onMouseLeave={(e) => {
                    (e.target as HTMLElement).style.transform = 'scale(1)';
                  }}
                >
                  {seg.count}
                </div>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
};

const RequestStatusChart: React.FC<Props> = ({ data, viewMode, liveIds: _liveIds, platforms: _platforms }) => {
  // 根据视图模式渲染不同的图表
  const content = useMemo(() => {
    switch (viewMode) {
      case 'global':
        return <GlobalTimelineChart segments={data.segments || []} />;
      case 'by_live':
        return (
          <GroupedTimelineChart
            groupedSegments={data.grouped_segments || {}}
            groupType="live"
          />
        );
      case 'by_platform':
        return (
          <GroupedTimelineChart
            groupedSegments={data.grouped_segments || {}}
            groupType="platform"
          />
        );
      default:
        return <GlobalTimelineChart segments={data.segments || []} />;
    }
  }, [data, viewMode]);

  return (
    <div className="request-status-chart">
      <h4 style={{ marginBottom: 16, color: '#666' }}>
        请求状态时间线
        <span style={{ fontSize: 12, color: '#999', marginLeft: 8 }}>
          (绿色=成功, 红色=失败)
        </span>
      </h4>
      {content}
    </div>
  );
};

export default RequestStatusChart;
