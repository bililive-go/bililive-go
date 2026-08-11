import React, { useMemo } from 'react';
import {
  Alert,
  Card,
  Col,
  Descriptions,
  Row,
  Space,
  Statistic,
  Tag,
  Typography,
} from 'antd';
import {
  ApartmentOutlined,
  BranchesOutlined,
  ClockCircleOutlined,
  DeploymentUnitOutlined,
  NodeIndexOutlined,
  TeamOutlined,
} from '@ant-design/icons';
import {
  buildConcurrencySummary,
  diagnosticLifecycle,
  eventGeneration,
  formatDuration,
  scopedDiagnosticEvents,
} from './analysis';
import {
  DiagnosticAnalysis,
  DiagnosticBundle,
  DiagnosticEvent,
  DiagnosticMetric,
  DiagnosticRuntimeSlice,
} from './types';

const { Text, Title } = Typography;

interface Props {
  bundle: DiagnosticBundle;
  analysis: DiagnosticAnalysis;
  selectedEventId?: string;
  onSelect: (event: DiagnosticEvent) => void;
}

const EVENT_NAME_LABELS: Record<string, string> = {
  'monitor.batch.start': '开始创建 100 个监听任务',
  'monitor.batch.end': '监听任务批量创建完成',
  'monitor.started': '目标房间开始监控',
  'monitor.user_stop.requested': '用户点击停止',
  'monitor.user_stop.accepted': '停止请求已接受',
  'monitor.user_resume.requested': '用户点击恢复',
  'monitor.user_resume.accepted': '恢复请求已接受',
  'listener.generation.created': '新 Listener generation 创建',
  'context.cancel.requested': '请求取消旧 generation',
  'context.cancel.observed': '旧任务观察到取消',
  'scheduler.rate_limit.wait.start': '进入平台共享限流等待',
  'scheduler.rate_limit.wait.end': '取得平台访问机会',
  'scheduler.rate_limit.in_flight.wait.start': '等待同平台请求串行槽位',
  'scheduler.rate_limit.in_flight.wait.end': '取得同平台请求串行槽位',
  'scheduler.rate_limit.in_flight.enter': '进入同平台请求串行等待',
  'scheduler.rate_limit.in_flight.acquired': '同平台请求槽位已取得',
  'scheduler.rate_limit.in_flight.released': '同平台请求槽位已释放',
  'scheduler.poll.enqueued': '检测任务进入竞争等待',
  'scheduler.poll.dequeued': '检测任务获准执行',
  'listener.start.requested': '请求启动 Listener',
  'listener.start.accepted': 'Listener 启动已接受',
  'listener.close.requested': '请求关闭 Listener',
  'listener.close.accepted': 'Listener 关闭已接受',
  'listener.manager.add.start': 'Listener 管理器开始添加实例',
  'listener.manager.add.end': 'Listener 管理器完成添加实例',
  'listener.manager.remove.start': 'Listener 管理器开始移除实例',
  'listener.manager.remove.end': 'Listener 管理器完成移除实例',
  'listener.manager.replace.start': 'Listener 管理器开始切换实例',
  'listener.manager.replace.end': 'Listener 管理器完成切换实例',
  'listener.poll.start': '发起直播状态检测',
  'listener.poll.end': '平台确认直播中',
  'live.refresh.start': '发起直播状态检测',
  'live.refresh.end': '平台确认直播中',
  'event.dispatch': '派发 LiveStart',
  'event.handler.start': 'Recorder handler 开始',
  'event.handler.end': 'Recorder handler 完成',
  'event.stale.drop': '丢弃旧 generation 迟到事件',
  'recorder.session.start': '创建 Recorder 会话',
  'stream.resolve.start': '开始解析直播流',
  'stream.resolve.end': '直播流地址就绪',
  'parser.start': 'Parser 启动',
  'segment.open': '创建 FLV 文件',
  'segment.first_byte': '写入 FLV 首字节',
  'runtime.blocked': '采样：Goroutine 正在等待',
  'runtime.unblocked': '采样：Goroutine 被唤醒',
  'runtime.flow': 'Flight Recorder 调度流',
};

const labelForEvent = (event: DiagnosticEvent): string => (
  EVENT_NAME_LABELS[event.name] || event.name
);

const generationColor = (generation?: number, target?: number): string => {
  if (generation === undefined) return 'default';
  if (generation === target) return 'blue';
  return 'default';
};

const runtimeStateLabel: Record<DiagnosticRuntimeSlice['state'], string> = {
  running: '运行',
  runnable: '可运行',
  waiting: '等待',
  syscall: '系统调用',
  unknown: '未知',
};

const metricDefinitions = [
  {
    names: ['startup.rooms.remaining', 'monitor.rooms.starting'],
    label: '待启动房间',
    color: '#7f8fa6',
  },
  {
    names: [
      'platform.rate_limiter.waiting_rooms',
      'platform.rate_limiter.waiter_count',
      'scheduler.waiter_count',
      'scheduler.queue_depth',
    ],
    label: '平台最小间隔等待者',
    color: '#d94a5b',
  },
  {
    names: ['platform.rate_limiter.in_flight_waiting_rooms'],
    label: '同平台串行槽位等待者',
    color: '#7657d5',
  },
  {
    names: ['scheduler.poll.in_flight', 'live.poll.in_flight'],
    label: '检测执行中',
    color: '#d88316',
  },
  {
    names: ['events.dispatch.in_flight'],
    label: '事件派发中',
    color: '#7657d5',
  },
  {
    names: ['recorder.sessions.active', 'recorder.active'],
    label: 'Recorder 活跃',
    color: '#159b62',
  },
];

const metricForDefinition = (
  metrics: DiagnosticMetric[],
  names: string[],
): DiagnosticMetric | undefined => metrics.find((metric) => names.includes(metric.name));

const GlobalMetricChart: React.FC<{
  bundle: DiagnosticBundle;
  analysis: DiagnosticAnalysis;
  stopAtMs?: number;
  resumeAtMs?: number;
}> = ({ bundle, analysis, stopAtMs, resumeAtMs }) => {
  const metrics = metricDefinitions
    .map((definition) => ({
      ...definition,
      metric: metricForDefinition(bundle.metrics || [], definition.names),
    }))
    .filter((item): item is typeof item & { metric: DiagnosticMetric } => Boolean(item.metric));
  const windowStart = Math.min(
    analysis.processStartMs,
    ...metrics.flatMap((item) => item.metric.series.map((point) => point.ts)),
  );
  const windowEnd = Math.max(
    analysis.firstByteAtMs,
    ...metrics.flatMap((item) => item.metric.series.map((point) => point.ts)),
  );
  const width = 1000;
  const height = 250;
  const padding = { left: 48, right: 18, top: 18, bottom: 34 };
  const innerWidth = width - padding.left - padding.right;
  const innerHeight = height - padding.top - padding.bottom;
  const maxValue = Math.max(
    1,
    ...metrics.flatMap((item) => item.metric.series.map((point) => point.value)),
  );
  const x = (value: number): number => (
    padding.left + ((value - windowStart) / Math.max(1, windowEnd - windowStart)) * innerWidth
  );
  const y = (value: number): number => padding.top + innerHeight - (value / maxValue) * innerHeight;
  const markers = [
    stopAtMs !== undefined ? { at: stopAtMs, label: '旧实例关闭', color: '#7f8fa6' } : undefined,
    resumeAtMs !== undefined ? { at: resumeAtMs, label: '目标实例启动', color: '#3478e5' } : undefined,
    { at: analysis.firstLiveAtMs, label: '确认 live', color: '#d88316' },
    { at: analysis.firstByteAtMs, label: '首字节', color: '#159b62' },
  ].filter((item): item is { at: number; label: string; color: string } => Boolean(item));

  if (metrics.length === 0) {
    return <Alert type="info" showIcon message="数据包没有并发聚合指标；仍可查看下方业务与 Runtime 事件。" />;
  }

  return (
    <div className="diagnostic-concurrency-chart-wrap">
      <svg
        className="diagnostic-concurrency-chart"
        viewBox={`0 0 ${width} ${height}`}
        role="img"
        aria-label="多房间启动时的全局并发指标"
      >
        {[0, 0.25, 0.5, 0.75, 1].map((ratio) => (
          <g key={ratio}>
            <line
              x1={padding.left}
              x2={width - padding.right}
              y1={padding.top + innerHeight * ratio}
              y2={padding.top + innerHeight * ratio}
              className="diagnostic-concurrency-gridline"
            />
            <text x={padding.left - 8} y={padding.top + innerHeight * ratio + 4} textAnchor="end">
              {Math.round(maxValue * (1 - ratio))}
            </text>
          </g>
        ))}
        {metrics.map((item) => (
          <polyline
            key={item.metric.name}
            points={item.metric.series.map((point) => `${x(point.ts)},${y(point.value)}`).join(' ')}
            fill="none"
            stroke={item.color}
            strokeWidth="3"
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        ))}
        {markers.map((marker) => (
          <g key={`${marker.label}-${marker.at}`}>
            <line
              x1={x(marker.at)}
              x2={x(marker.at)}
              y1={padding.top}
              y2={padding.top + innerHeight}
              stroke={marker.color}
              strokeDasharray="5 4"
            />
            <text
              x={x(marker.at)}
              y={height - 8}
              textAnchor={marker.at === windowEnd ? 'end' : 'middle'}
              fill={marker.color}
            >
              {marker.label} +{(marker.at / 1000).toFixed(1)}s
            </text>
          </g>
        ))}
      </svg>
      <div className="diagnostic-concurrency-legend">
        {metrics.map((item) => (
          <span key={item.metric.name}><i style={{ background: item.color }} />{item.label}</span>
        ))}
      </div>
    </div>
  );
};

const RoomPopulation: React.FC<{
  bundle: DiagnosticBundle;
  targetRoomId?: string;
  configuredRooms: number;
}> = ({ bundle, targetRoomId, configuredRooms }) => {
  const entities = (bundle.entities || []).filter((entity) => (
    entity.type === 'room' || entity.type === 'live_room'
  ));
  const rooms = Array.from({ length: configuredRooms }, (_, index) => {
    const entity = entities[index];
    return {
      id: typeof entity?.id === 'string' ? entity.id : `aggregate_room_${index + 1}`,
      label: typeof entity?.label === 'string' ? entity.label : `房间 ${index + 1}`,
      aggregate: !entity,
    };
  });
  const visibleRooms = rooms.slice(0, 200);
  return (
    <div className="diagnostic-room-population" aria-label={`${configuredRooms} 个监控房间概览`}>
      {visibleRooms.map((room, index) => (
        <span
          key={room.id}
          className={[
            'diagnostic-room-cell',
            room.id === targetRoomId ? 'diagnostic-room-cell-target' : '',
            room.aggregate ? 'diagnostic-room-cell-aggregate' : '',
          ].filter(Boolean).join(' ')}
          title={`${index + 1}. ${room.label}${room.id === targetRoomId ? '（Incident 目标）' : ''}`}
        />
      ))}
    </div>
  );
};

const hasInterestingEvent = (event: DiagnosticEvent): boolean => (
  event.name.startsWith('monitor.')
  || event.name.startsWith('listener.generation')
  || event.name.startsWith('listener.start')
  || event.name.startsWith('listener.close')
  || event.name.startsWith('listener.manager')
  || event.name.startsWith('context.cancel')
  || event.name.includes('rate_limit')
  || event.name.startsWith('scheduler.poll')
  || event.name === 'listener.poll.end'
  || event.name === 'live.refresh.end'
  || event.name.startsWith('event.')
  || event.name === 'recorder.session.start'
  || event.name.startsWith('stream.resolve')
  || event.name === 'parser.start'
  || event.name.startsWith('segment.')
  || event.category === 'runtime'
);

const eventDisposition = (event: DiagnosticEvent): string => {
  if (event.disposition) return event.disposition;
  if (
    event.attrs?.stale === true
    || event.attrs?.stale_generation === true
    || event.attrs?.outcome_code === 'stale_generation_ignored'
    || event.name.includes('stale')
  ) return 'dropped';
  return event.status || String(event.attrs?.outcome_status || 'observed');
};

const ExecutionOrder: React.FC<{
  events: DiagnosticEvent[];
  targetGeneration?: number;
  selectedEventId?: string;
  onSelect: (event: DiagnosticEvent) => void;
}> = ({ events, targetGeneration, selectedEventId, onSelect }) => {
  const rows = events.filter(hasInterestingEvent).slice(0, 70);
  return (
    <div className="diagnostic-execution-order">
      <div className="diagnostic-execution-header">
        <span>观察序号 / 时间</span>
        <span>generation · task / goroutine</span>
        <span>观察到的业务或 Runtime 事件</span>
        <span>因果边 / 处置</span>
      </div>
      {rows.map((event) => {
        const generation = eventGeneration(event);
        const task = event.task_id
          || event.trace_task_id
          || event.goroutine_id
          || event.attrs?.task_id
          || event.attrs?.goroutine_id;
        const links = event.links?.filter((link) => link.event_id).slice(0, 2) || [];
        const disposition = eventDisposition(event);
        return (
          <button
            type="button"
            key={event.id}
            className={[
              'diagnostic-execution-row',
              generation !== undefined && generation !== targetGeneration ? 'diagnostic-execution-row-old' : '',
              disposition === 'dropped' || disposition === 'ignored' ? 'diagnostic-execution-row-dropped' : '',
              event.id === selectedEventId ? 'diagnostic-execution-row-selected' : '',
            ].filter(Boolean).join(' ')}
            onClick={() => onSelect(event)}
          >
            <span>
              <b>#{event.global_seq ?? event.seq ?? '—'}</b>
              <small>+{formatDuration(event.ts)}</small>
            </span>
            <span>
              {generation !== undefined
                ? <Tag color={generationColor(generation, targetGeneration)}>gen{generation}</Tag>
                : <Tag>全局</Tag>}
              <code>{task ? String(task) : '—'}</code>
            </span>
            <span>
              <strong>{labelForEvent(event)}</strong>
              <small>{event.component} · {event.name}</small>
            </span>
            <span>
              {links.map((link) => (
                <code key={`${link.rel}-${link.event_id}`}>{link.rel} → {link.event_id}</code>
              ))}
              <Tag color={disposition === 'dropped' ? 'red' : disposition === 'ok' ? 'green' : 'default'}>
                {disposition}
              </Tag>
            </span>
          </button>
        );
      })}
    </div>
  );
};

const CausalChain: React.FC<{
  events: DiagnosticEvent[];
  analysis: DiagnosticAnalysis;
  onSelect: (event: DiagnosticEvent) => void;
}> = ({ events, analysis, onSelect }) => {
  const eventById = new Map(events.map((event) => [event.id, event]));
  const goal = events.find((event) => (
    event.ts === analysis.firstByteAtMs
    && ['segment.first_byte', 'segment.first_nonzero_observed'].includes(event.name)
  )) || events.find((event) => (
    ['segment.first_byte', 'segment.first_nonzero_observed'].includes(event.name)
  ));
  const chain: DiagnosticEvent[] = [];
  let cursor = goal;
  const seen = new Set<string>();
  while (cursor && !seen.has(cursor.id) && chain.length < 16) {
    chain.unshift(cursor);
    seen.add(cursor.id);
    const link = cursor.links?.find((candidate) => (
      ['caused_by', 'created_by', 'handled_by', 'granted_by', 'enqueued_by'].includes(candidate.rel)
      && candidate.event_id
    ));
    cursor = link?.event_id ? eventById.get(link.event_id) : undefined;
  }
  const milestones = chain.length >= 4 ? chain : events.filter((event) => (
    [
      'monitor.user_resume.accepted',
      'monitor.started',
      'listener.start.accepted',
      'scheduler.rate_limit.in_flight.wait.start',
      'scheduler.rate_limit.in_flight.wait.end',
      'scheduler.rate_limit.in_flight.acquired',
      'scheduler.rate_limit.in_flight.released',
      'scheduler.rate_limit.wait.start',
      'scheduler.rate_limit.wait.end',
      'listener.poll.end',
      'live.refresh.end',
      'event.dispatch',
      'recorder.session.start',
      'parser.start',
      'segment.first_byte',
      'segment.first_nonzero_observed',
    ].includes(event.name)
    && (eventGeneration(event) === undefined || eventGeneration(event) === analysis.targetGeneration)
  )).slice(0, 12);
  return (
    <div className="diagnostic-causal-chain">
      {milestones.map((event, index) => (
        <React.Fragment key={event.id}>
          {index > 0 && <span className="diagnostic-causal-arrow">→</span>}
          <button type="button" onClick={() => onSelect(event)}>
            <small>+{formatDuration(event.ts - analysis.windowStartMs)}</small>
            <strong>{labelForEvent(event)}</strong>
            <span>#{event.global_seq ?? event.seq ?? '—'} · gen{eventGeneration(event) ?? '—'}</span>
          </button>
        </React.Fragment>
      ))}
    </div>
  );
};

const RuntimeLanes: React.FC<{
  slices: DiagnosticRuntimeSlice[];
  analysis: DiagnosticAnalysis;
  events: DiagnosticEvent[];
}> = ({ slices, analysis, events }) => {
  const windowStart = analysis.processStartMs;
  const windowEnd = analysis.firstByteAtMs;
  const relevantSlices = slices.filter((slice) => (
    slice.end_ms >= windowStart
    && slice.start_ms <= windowEnd
  ));
  const byGoroutine = new Map<string, DiagnosticRuntimeSlice[]>();
  relevantSlices.forEach((slice) => {
    const items = byGoroutine.get(slice.goroutine_id) || [];
    items.push(slice);
    byGoroutine.set(slice.goroutine_id, items);
  });
  const runtimeSamples = events.filter((event) => event.category === 'runtime');
  if (byGoroutine.size === 0) {
    return (
      <div>
        <Alert
          type="info"
          showIcon
          message={`${runtimeSamples.length} 个 Runtime 采样点（不是连续执行片段）`}
          description="采样点只能证明该时刻观察到 waiting/runnable 等状态。只有 Go Flight Recorder 转换出的 runtime_slices 才能把 running、runnable、waiting 和 syscall 画成连续片段。"
        />
        <div className="diagnostic-runtime-samples">
          {runtimeSamples.slice(0, 12).map((event) => (
            <span key={event.id}>
              <b>+{formatDuration(event.ts)}</b>
              <code>{String(event.goroutine_id || event.attrs?.goroutine_id || 'unknown G')}</code>
              {String(event.attrs?.state || event.attrs?.wait_reason || event.name)}
            </span>
          ))}
        </div>
      </div>
    );
  }
  const total = Math.max(1, windowEnd - windowStart);
  return (
    <div className="diagnostic-runtime-lanes">
      {Array.from(byGoroutine.entries()).slice(0, 14).map(([goroutine, items]) => (
        <div className="diagnostic-runtime-lane" key={goroutine}>
          <div>
            <strong>{goroutine}</strong>
            <small>
              gen{items[0].generation ?? '—'} · {items[0].task_id || items[0].trace_task_id || '未关联业务 task'}
            </small>
          </div>
          <div>
            {items.map((slice) => {
              const left = ((Math.max(windowStart, slice.start_ms) - windowStart) / total) * 100;
              const width = (
                (Math.min(windowEnd, slice.end_ms) - Math.max(windowStart, slice.start_ms)) / total
              ) * 100;
              return (
                <span
                  key={slice.id}
                  className={`diagnostic-runtime-slice diagnostic-runtime-slice-${slice.state}`}
                  style={{ left: `${left}%`, width: `${Math.max(width, 0.45)}%` }}
                  title={`${runtimeStateLabel[slice.state]} ${formatDuration(slice.end_ms - slice.start_ms)}${slice.wait_reason ? ` · ${slice.wait_reason}` : ''}`}
                >
                  {width > 7 ? runtimeStateLabel[slice.state] : ''}
                </span>
              );
            })}
          </div>
        </div>
      ))}
      <div className="diagnostic-runtime-legend">
        {(['running', 'runnable', 'waiting', 'syscall'] as const).map((state) => (
          <span key={state}><i className={`diagnostic-runtime-slice-${state}`} />{runtimeStateLabel[state]}</span>
        ))}
      </div>
    </div>
  );
};

const ConcurrencyOverview: React.FC<Props> = ({
  bundle,
  analysis,
  selectedEventId,
  onSelect,
}) => {
  const summary = useMemo(() => buildConcurrencySummary(bundle), [bundle]);
  const lifecycle = useMemo(() => diagnosticLifecycle(bundle), [bundle]);
  const targetEvents = useMemo(() => (
    scopedDiagnosticEvents(bundle)
      .sort((a, b) => (a.global_seq ?? a.seq ?? 0) - (b.global_seq ?? b.seq ?? 0) || a.ts - b.ts)
  ), [bundle]);

  return (
    <div className="diagnostic-concurrency-view" data-testid="concurrency-view">
      <Alert
        type="warning"
        showIcon
        message="时间用于对齐，因果边用于证明先后"
        description="global_seq 是轨迹 writer 的观察顺序，不是多个 goroutine 的绝对执行顺序。下方只有同一 task/goroutine 的片段，以及 caused_by、wakes、supersedes、cancels 等显式边，才作为强因果证据。"
      />

      <Row gutter={[12, 12]} className="diagnostic-concurrency-kpis">
        <Col xs={12} lg={6}>
          <Card size="small">
            <Statistic title="包内观察到启动" value={summary.startedRooms} suffix="个房间" prefix={<TeamOutlined />} />
            <Text type="secondary">配置 {summary.configuredRooms} · 完整追踪 {summary.fullyTracedRooms}</Text>
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card size="small">
            <Statistic
              title="共享限流等待者峰值"
              value={summary.queueDepthPeak ?? 0}
              suffix="个"
              prefix={<DeploymentUnitOutlined />}
            />
            <Text type="secondary">平台级竞争，不代表 FIFO 队列名次</Text>
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card size="small" className="diagnostic-kpi-bad">
            <Statistic
              title={`目标 gen${summary.targetGeneration ?? '—'} 进入时竞争等待者`}
              value={summary.targetQueuePosition ?? 0}
              suffix="个"
              prefix={<NodeIndexOutlined />}
            />
            <Text type="secondary">
              {lifecycle.explicitUserAction
                ? '数据包包含明确用户恢复事件'
                : `${lifecycle.label}进入共享限流竞争；未据此推断人工操作`}
            </Text>
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card size="small" className="diagnostic-kpi-bad">
            <Statistic
              title="目标共享限流等待"
              value={(summary.targetQueueWaitMs || 0) / 1000}
              precision={2}
              suffix="秒"
              prefix={<ClockCircleOutlined />}
            />
            <Text type="secondary">共观察 {summary.taskCount} task / {summary.goroutineCount} goroutine</Text>
          </Card>
        </Col>
      </Row>

      <div className="diagnostic-concurrency-grid">
        <Card
          title={<span><ApartmentOutlined /> 多房间全局并发概览</span>}
          extra={<Tag color="blue">目标 {summary.targetRoomId || '未指定'}</Tag>}
        >
          <GlobalMetricChart
            bundle={bundle}
            analysis={analysis}
            stopAtMs={summary.stopAtMs}
            resumeAtMs={summary.resumeAtMs}
          />
          <div className="diagnostic-room-population-heading">
            <Text strong>启动房间分布</Text>
            <Space wrap>
              <span><i className="diagnostic-room-legend-target" />Incident 目标</span>
              <span><i className="diagnostic-room-legend-traced" />包内实体</span>
              <span><i className="diagnostic-room-legend-aggregate" />仅聚合计数</span>
            </Space>
          </div>
          <RoomPopulation
            bundle={bundle}
            targetRoomId={summary.targetRoomId}
            configuredRooms={summary.configuredRooms}
          />
        </Card>
        <Card title={<span><BranchesOutlined /> 目标首字节的因果主链</span>}>
          <CausalChain events={targetEvents} analysis={analysis} onSelect={onSelect} />
          <Descriptions bordered size="small" column={1} className="diagnostic-chain-summary">
            <Descriptions.Item label="选中范围">
              {summary.targetRoomId || '—'} · generation {summary.targetGeneration ?? '—'}
            </Descriptions.Item>
            <Descriptions.Item label={`${lifecycle.label} → 首字节`}>
              {formatDuration(analysis.totalMs)}
            </Descriptions.Item>
            <Descriptions.Item label="进程启动 → 首字节">
              {formatDuration(analysis.processToFirstByteMs)}
            </Descriptions.Item>
            <Descriptions.Item label="旧代迟到事件">
              <Tag color={summary.staleEventCount > 0 ? 'orange' : 'green'}>
                {summary.staleEventCount} 条被标记为 stale / dropped
              </Tag>
            </Descriptions.Item>
          </Descriptions>
        </Card>
      </div>

      <Card
        className="diagnostic-execution-card"
        title={<span><NodeIndexOutlined /> 多线程观察顺序与显式因果边</span>}
        extra={<Tag>{targetEvents.length} 条目标范围事件</Tag>}
      >
        <ExecutionOrder
          events={targetEvents}
          targetGeneration={analysis.targetGeneration}
          selectedEventId={selectedEventId}
          onSelect={onSelect}
        />
      </Card>

      <Card
        className="diagnostic-runtime-card"
        title={<span><DeploymentUnitOutlined /> Go Flight Recorder 执行片段</span>}
        extra={<Tag color="purple">runtime_slices</Tag>}
      >
        <Title level={5}>Goroutine / task 状态泳道</Title>
        <Text type="secondary">
          running 表示实际占用 P 执行；waiting、runnable 和 syscall 来自 Flight Recorder。
          跨 goroutine 的先后只在存在 wakes/sends/unblocks 连接时成立。
        </Text>
        <RuntimeLanes
          slices={bundle.runtime_slices || []}
          analysis={analysis}
          events={targetEvents}
        />
      </Card>
    </div>
  );
};

export default ConcurrencyOverview;
