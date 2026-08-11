import React from 'react';
import { DiagnosticEvent, TimelineItem } from './types';
import { formatDuration, timelineLanes } from './analysis';

interface Props {
  items: TimelineItem[];
  totalMs: number;
  windowStartMs?: number;
  referenceStartMs?: number;
  configuredIntervalMs: number;
  selectedEventId?: string;
  onSelect: (event: DiagnosticEvent) => void;
}

const clampPercent = (value: number): number => Math.max(0, Math.min(100, value));

const Timeline: React.FC<Props> = ({
  items,
  totalMs,
  windowStartMs = 0,
  referenceStartMs = windowStartMs,
  configuredIntervalMs,
  selectedEventId,
  onSelect,
}) => {
  const safeTotal = Math.max(totalMs, 1);
  const ticks = Array.from({ length: 6 }, (_, index) => (safeTotal / 5) * index);
  const intervalPercent = clampPercent((
    (referenceStartMs - windowStartMs + configuredIntervalMs) / safeTotal
  ) * 100);

  return (
    <div className="diagnostic-timeline-shell">
      <div className="diagnostic-timeline-scroll">
        <div className="diagnostic-timeline" role="group" aria-label="业务和运行时泳道时间线">
          <div className="diagnostic-timeline-axis-row">
            <div className="diagnostic-timeline-corner">关键路径泳道</div>
            <div className="diagnostic-timeline-axis">
              {ticks.map((tick) => (
                <span
                  key={tick}
                  className="diagnostic-axis-tick"
                  style={{ left: `${clampPercent((tick / safeTotal) * 100)}%` }}
                >
                  +{(tick / 1000).toFixed(0)}s
                </span>
              ))}
            </div>
          </div>

          {timelineLanes.map((lane) => {
            const laneItems = items.filter((item) => item.lane === lane.key);
            return (
              <div className="diagnostic-timeline-lane" key={lane.key}>
                <div className="diagnostic-lane-label">
                  <strong>{lane.label}</strong>
                  <span>{laneItems.length} 个事件</span>
                </div>
                <div className="diagnostic-lane-track">
                  <div
                    className="diagnostic-interval-marker"
                    style={{ left: `${intervalPercent}%` }}
                    aria-label={`配置检测间隔 ${(configuredIntervalMs / 1000).toFixed(1)} 秒`}
                  >
                    <span>{(configuredIntervalMs / 1000).toFixed(0)}s 配置参考</span>
                  </div>
                  <div className="diagnostic-first-byte-marker" style={{ left: 'calc(100% - 2px)' }}>
                    <span>FLV 首字节</span>
                  </div>
                  {laneItems.map((item, index) => {
                    const relativeStart = item.startMs - windowStartMs;
                    const left = clampPercent((relativeStart / safeTotal) * 100);
                    const width = clampPercent(((item.endMs - item.startMs) / safeTotal) * 100);
                    const title = `${item.label} · +${formatDuration(relativeStart)}${
                      item.milestone ? '' : ` · 持续 ${formatDuration(item.endMs - item.startMs)}`
                    }${item.event.generation !== undefined ? ` · gen${item.event.generation}` : ''}`;
                    return (
                      <button
                        type="button"
                        key={item.id}
                        title={title}
                        aria-label={title}
                        className={[
                          'diagnostic-timeline-item',
                          `diagnostic-timeline-item-${item.status}`,
                          item.milestone ? 'diagnostic-timeline-milestone' : '',
                          selectedEventId === item.event.id ? 'diagnostic-timeline-item-selected' : '',
                        ].filter(Boolean).join(' ')}
                        style={{
                          left: `${left}%`,
                          width: item.milestone ? undefined : `${Math.max(width, 0.45)}%`,
                          top: `${8 + (index % 3) * 20}px`,
                        }}
                        onClick={() => onSelect(item.event)}
                      >
                        {!item.milestone && <span>{item.label}</span>}
                      </button>
                    );
                  })}
                </div>
              </div>
            );
          })}
        </div>
      </div>
      <div className="diagnostic-timeline-legend" aria-label="时间线图例">
        <span><i className="legend-dot legend-normal" />正常事实</span>
        <span><i className="legend-dot legend-warning" />警告</span>
        <span><i className="legend-dot legend-critical" />主要异常</span>
        <span><i className="legend-dot legend-runtime" />运行时佐证</span>
        <span className="diagnostic-scroll-hint">手机可左右滑动时间线</span>
      </div>
    </div>
  );
};

export default Timeline;
