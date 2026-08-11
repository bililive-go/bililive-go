import {
  DiagnosticAnalysis,
  DiagnosticBundle,
  DiagnosticEvent,
  DiagnosticEvidence,
  DiagnosticFinding,
  DiagnosticPhase,
  DiagnosticScope,
  DiagnosticConcurrencySummary,
  DiagnosticRuntimeSlice,
  DiagnosticValue,
  TimelineItem,
} from './types';

const MAX_BUNDLE_TEXT_CHARACTERS = 25 * 1024 * 1024;
const MAX_NORMALIZED_EVENTS = 100000;
const MAX_RUNTIME_SLICES = 100000;
const MAX_METRICS = 512;
const MAX_METRIC_POINTS = 200000;
const MAX_ENTITIES = 10000;
const MAX_EVENT_LINKS = 64;
const MAX_EVENT_ATTRIBUTES = 256;
const MAX_FIELD_CHARACTERS = 64 * 1024;

const EVENT_ALIASES = {
  monitorStarted: [
    'monitor.add.requested',
    'monitor.started',
    'monitor.resume.accepted',
    'monitor.user_resume.accepted',
    'listener.start.accepted',
    'listener.generation.created',
    'listener.monitor.start',
  ],
  listenerStarted: ['listener.start.accepted', 'listener.run.start'],
  refreshStart: ['live.refresh.start', 'listener.poll.start'],
  refreshEnd: ['live.refresh.end', 'listener.poll.end'],
  liveObserved: ['live.state.transition', 'live.state.observed', 'live.online'],
  recorderStarted: ['recorder.session.start', 'recorder.start'],
  firstByte: ['segment.first_nonzero_observed', 'segment.first_byte', 'flv.first_byte'],
  resolveStart: ['stream.resolve.start'],
  resolveEnd: ['stream.resolve.end'],
  toolWaitStart: ['ffmpeg.wait.ready.start', 'tools.ffmpeg.wait.start'],
  toolWaitEnd: ['ffmpeg.wait.ready.end', 'tools.ffmpeg.wait.end'],
  connectStart: ['stream.connect.start'],
  connectEnd: ['stream.connect.end'],
  probeStart: ['stream.probe.start'],
  probeEnd: ['stream.probe.end'],
  upstreamWaitStart: ['stream.first_byte.wait.start', 'upstream.first_byte.wait.start'],
  upstreamWaitEnd: ['stream.first_byte.wait.end', 'upstream.first_byte.wait.end'],
  rateLimitStart: [
    'scheduler.rate_limit.in_flight.wait.start',
    'scheduler.rate_limit.wait.start',
  ],
  rateLimitEnd: [
    'scheduler.rate_limit.in_flight.wait.end',
    'scheduler.rate_limit.wait.end',
  ],
  parserStart: ['parser.start'],
  stopRequested: [
    'monitor.stop.requested',
    'monitor.user_stop.requested',
    'listener.close.requested',
    'listener.manager.remove.start',
  ],
  stopAccepted: [
    'monitor.stop.accepted',
    'monitor.user_stop.accepted',
    'listener.close.accepted',
    'listener.manager.remove.end',
  ],
  resumeRequested: ['monitor.resume.requested', 'monitor.user_resume.requested'],
  resumeAccepted: ['monitor.resume.accepted', 'monitor.user_resume.accepted'],
  staleDrop: ['event.stale.drop', 'generation.guard.drop', 'event.generation_mismatch.drop'],
};

const PHASE_LABELS: Record<string, string> = {
  detection: '确认直播状态',
  dispatch: '事件派发与 Recorder 创建',
  resolve: '获取与选择流地址',
  tool_wait: '等待 FFmpeg 就绪',
  connect_probe: '建连与流探测',
  upstream_wait: '等待上游首字节',
  parser_first_byte: 'Parser 到 FLV 首字节',
  recording_prepare: '录制准备',
};

const EVENT_LABELS: Record<string, string> = {
  'monitor.add.requested': '开始监控',
  'monitor.started': '开始监控',
  'monitor.batch.start': '启动 100 房间监控批次',
  'monitor.batch.end': '监控批次创建完成',
  'monitor.user_stop.requested': '用户请求停止监控',
  'monitor.user_stop.accepted': '停止监控已接受',
  'monitor.user_resume.requested': '用户请求恢复监控',
  'monitor.user_resume.accepted': '恢复监控已接受',
  'listener.generation.created': '创建 Listener generation',
  'listener.generation.ended': 'Listener generation 结束',
  'context.cancel.requested': '请求取消旧任务',
  'context.cancel.observed': '旧任务观察到取消',
  'scheduler.poll.enqueued': '检测任务进入共享队列',
  'scheduler.poll.dequeued': '检测任务取得执行令牌',
  'scheduler.poll.canceled': '检测任务取消',
  'listener.start.requested': '请求启动 Listener',
  'listener.start.accepted': 'Listener 已启动',
  'listener.close.requested': '请求关闭 Listener',
  'listener.close.accepted': 'Listener 已关闭',
  'listener.manager.add.start': 'Listener 管理器开始添加实例',
  'listener.manager.add.end': 'Listener 管理器完成添加实例',
  'listener.manager.remove.start': 'Listener 管理器开始移除实例',
  'listener.manager.remove.end': 'Listener 管理器完成移除实例',
  'listener.manager.replace.start': 'Listener 管理器开始切换实例',
  'listener.manager.replace.end': 'Listener 管理器完成切换实例',
  'live.refresh.start': '首次直播状态请求',
  'live.refresh.end': '确认正在直播',
  'listener.poll.start': '直播状态请求',
  'listener.poll.end': '直播状态响应',
  'live.state.transition': '状态变为直播中',
  'event.dispatch': '派发 LiveStart',
  'event.handler.start': 'Handler 开始',
  'event.handler.end': 'Handler 完成',
  'event.dispatch.complete': '事件派发完成',
  'event.stale.drop': '丢弃旧 generation 迟到事件',
  'recorder.session.start': 'Recorder 会话开始',
  'recorder.stream_attempt.start': '录制尝试开始',
  'stream.resolve.start': '解析流地址',
  'stream.resolve.end': '流地址就绪',
  'ffmpeg.wait.ready.start': '等待 FFmpeg ready',
  'ffmpeg.wait.ready.end': 'FFmpeg ready',
  'tools.ffmpeg.wait.start': '等待 FFmpeg ready',
  'tools.ffmpeg.wait.end': 'FFmpeg ready',
  'ffmpeg.init.start': 'FFmpeg 初始化',
  'ffmpeg.download.start': '下载 FFmpeg',
  'ffmpeg.download.end': '下载完成',
  'ffmpeg.verify.start': '校验 FFmpeg',
  'ffmpeg.verify.end': '校验完成',
  'ffmpeg.state.transition': 'FFmpeg 状态变化',
  'stream.connect.start': '连接直播流',
  'stream.connect.end': '连接完成',
  'stream.probe.start': '探测直播流',
  'stream.probe.end': '探测完成',
  'stream.first_byte.wait.start': '等待上游首字节',
  'stream.first_byte.wait.end': '收到上游首字节',
  'upstream.first_byte.wait.start': '等待上游首字节',
  'upstream.first_byte.wait.end': '收到上游首字节',
  'scheduler.rate_limit.wait.start': '等待平台限流器',
  'scheduler.rate_limit.wait.end': '限流等待结束',
  'scheduler.rate_limit.in_flight.wait.start': '等待平台请求串行槽位',
  'scheduler.rate_limit.in_flight.wait.end': '取得平台请求串行槽位',
  'scheduler.rate_limit.in_flight.enter': '进入平台请求串行槽位等待',
  'scheduler.rate_limit.in_flight.acquired': '取得平台请求串行槽位',
  'scheduler.rate_limit.in_flight.released': '释放平台请求串行槽位',
  'parser.start': 'Parser 启动',
  'segment.open': '创建 FLV 文件',
  'segment.first_nonzero_observed': '首次观察到输出文件非空',
  'segment.first_byte': 'FLV 首字节',
  'runtime.blocked': 'Goroutine 阻塞',
  'runtime.unblocked': 'Goroutine 唤醒',
  'diagnostic.trigger': '触发诊断快照',
  'trace.loss': '轨迹缺失',
};

const LANE_ORDER = [
  ['monitor', '监控请求'],
  ['listener', 'Listener / Live API'],
  ['events', 'EventDispatcher'],
  ['recorder', 'Recorder'],
  ['tools', 'Tools / FFmpeg'],
  ['stream', 'Stream / Parser'],
  ['file', 'FLV 文件'],
  ['runtime', 'Runtime / OS'],
] as const;

const findFirst = (
  events: DiagnosticEvent[],
  names: string[],
  predicate?: (event: DiagnosticEvent) => boolean,
): DiagnosticEvent | undefined => (
  events.find((event) => names.includes(event.name) && (!predicate || predicate(event)))
);

const isLiveEvent = (event: DiagnosticEvent): boolean => {
  const to = event.attrs?.to;
  const live = event.attrs?.live;
  const status = event.status || event.attrs?.status;
  return to === 'live' || live === true || status === 'live' || status === 'online';
};

const numericValue = (value: unknown): number | undefined => (
  typeof value === 'number' && Number.isFinite(value) ? value : undefined
);

const positiveValue = (value: unknown): number | undefined => {
  const numeric = numericValue(value);
  return numeric !== undefined && numeric > 0 ? numeric : undefined;
};

/**
 * 真实程序把启动时的房间数写在 configuration.configured_room_count。
 * incident/manifest 是手工诊断包与旧 schema 使用的位置，因此按可靠性依次兼容。
 */
export const configuredRoomCount = (bundle: DiagnosticBundle): number => {
  const roomEntities = (bundle.entities || []).filter((entity) => (
    entity.type === 'room' || entity.type === 'live_room'
  ));
  return positiveValue(bundle.incident.configured_room_count)
    ?? positiveValue(bundle.manifest.room_population?.configured)
    ?? positiveValue(bundle.configuration?.configured_room_count)
    ?? positiveValue(bundle.configuration?.room_count)
    ?? roomEntities.length;
};

export const eventRoomId = (event: DiagnosticEvent): string | undefined => {
  if (event.room_id) return event.room_id;
  const attrRoom = event.attrs?.room_id || event.attrs?.live_id;
  if (typeof attrRoom === 'string') return attrRoom;
  return event.entity_id?.startsWith('room_') ? event.entity_id : undefined;
};

export const eventGeneration = (event: DiagnosticEvent): number | undefined => (
  event.generation
  ?? numericValue(event.attrs?.generation)
  ?? numericValue(event.attrs?.origin_generation)
);

const eventTaskId = (event: DiagnosticEvent): string | undefined => (
  event.task_id
  || event.trace_task_id
  || event.goroutine_id
  || (typeof event.attrs?.task_id === 'string' ? event.attrs.task_id : undefined)
  || (typeof event.attrs?.trace_task_id === 'string' ? event.attrs.trace_task_id : undefined)
  || (typeof event.attrs?.goroutine_id === 'string' ? event.attrs.goroutine_id : undefined)
);

const eventOperationIdentity = (event: DiagnosticEvent): string => (
  event.span_id
    ? `span:${event.span_id}`
    : [
      `flow:${event.flow_id || ''}`,
      `room:${eventRoomId(event) || ''}`,
      `generation:${eventGeneration(event) ?? ''}`,
      `task:${eventTaskId(event) || ''}`,
    ].join('|')
);

export const diagnosticScope = (
  bundle: DiagnosticBundle,
  override: DiagnosticScope = {},
): DiagnosticScope => ({
  roomId: override.roomId || bundle.incident.target_room_id || bundle.incident.room_id,
  generation: override.generation ?? bundle.incident.target_generation,
  startEventId: override.startEventId
    || bundle.incident.focus_start_event_id
    || bundle.incident.anchor_start_event_id,
  goalEventId: override.goalEventId
    || bundle.incident.goal_event_id
    || bundle.incident.trigger_event_id,
});

/**
 * 先用 room_id 建立种子，再只沿 flow、显式 event link 和 span 关联扩展。
 * 这样全局指标仍可展示，但不会把其它房间的业务里程碑拼到目标房间上。
 */
export const scopedDiagnosticEvents = (
  bundle: DiagnosticBundle,
  override: DiagnosticScope = {},
): DiagnosticEvent[] => {
  const scope = diagnosticScope(bundle, override);
  const allEvents = [...bundle.events].sort((a, b) => a.ts - b.ts || (a.seq || 0) - (b.seq || 0));
  if (!scope.roomId && !scope.startEventId && !scope.goalEventId) {
    return allEvents;
  }

  const included = new Set<string>();
  const roomFlows = new Set<string>();
  allEvents.forEach((event) => {
    if (
      (scope.roomId && eventRoomId(event) === scope.roomId)
      || event.id === scope.startEventId
      || event.id === scope.goalEventId
    ) {
      included.add(event.id);
      if (event.flow_id) roomFlows.add(event.flow_id);
    }
  });
  allEvents.forEach((event) => {
    if (event.flow_id && roomFlows.has(event.flow_id)) {
      included.add(event.id);
    }
  });

  // 显式因果边和 runtime 的 linked span 允许把共享 scheduler 任务纳入目标链。
  for (let round = 0; round < 8; round += 1) {
    const before = included.size;
    const linkedEventIds = new Set<string>();
    const linkedSpanIds = new Set<string>();
    allEvents.forEach((event) => {
      if (!included.has(event.id)) return;
      if (event.span_id) linkedSpanIds.add(event.span_id);
      if (event.parent_span_id) linkedSpanIds.add(event.parent_span_id);
      event.links?.forEach((link) => {
        if (link.event_id) linkedEventIds.add(link.event_id);
        if (link.span_id) linkedSpanIds.add(link.span_id);
      });
    });
    allEvents.forEach((event) => {
      const linksIncluded = event.links?.some((link) => (
        Boolean(link.event_id && included.has(link.event_id))
        || Boolean(link.span_id && linkedSpanIds.has(link.span_id))
      ));
      if (
        linkedEventIds.has(event.id)
        || linksIncluded
        || Boolean(event.span_id && linkedSpanIds.has(event.span_id))
        || Boolean(event.parent_span_id && linkedSpanIds.has(event.parent_span_id))
        || (
          event.attrs?.related_span_id !== undefined
          && linkedSpanIds.has(String(event.attrs.related_span_id))
        )
      ) {
        included.add(event.id);
      }
    });
    if (included.size === before) break;
  }

  return allEvents.filter((event) => included.has(event.id));
};

export type DiagnosticLifecycleKind = 'initial' | 'user_resume' | 'restart' | 'replacement' | 'unknown';

export interface DiagnosticLifecycle {
  kind: DiagnosticLifecycleKind;
  label: string;
  explicitUserAction: boolean;
  startEvent?: DiagnosticEvent;
  actionEvent?: DiagnosticEvent;
}

const operationOf = (event?: DiagnosticEvent): string => (
  typeof event?.attrs?.operation === 'string' ? event.attrs.operation : ''
);

/**
 * 判断目标 generation 是初次启动、用户明确恢复、普通重启，还是初始化 Live
 * 被真实 Live 替换。只有数据包中存在 monitor.user_resume.* 才使用“用户恢复”；
 * generation > 1 本身不能证明发生过人工操作。
 */
export const diagnosticLifecycle = (
  bundle: DiagnosticBundle,
  override: DiagnosticScope = {},
): DiagnosticLifecycle => {
  const scope = diagnosticScope(bundle, override);
  const events = scopedDiagnosticEvents(bundle, scope);
  const explicitStart = scope.startEventId
    ? events.find((event) => event.id === scope.startEventId)
    : undefined;
  const targetGeneration = scope.generation
    ?? (explicitStart ? eventGeneration(explicitStart) : undefined);
  const generationEvents = events.filter((event) => (
    eventMatchesGeneration(event, targetGeneration)
  ));
  const startEvent = explicitStart || generationEvents.find((event) => (
    EVENT_ALIASES.monitorStarted.includes(event.name)
  ));
  const startAt = startEvent?.ts ?? Number.POSITIVE_INFINITY;
  const explicitUserResume = [...generationEvents]
    .reverse()
    .find((event) => (
      [...EVENT_ALIASES.resumeRequested, ...EVENT_ALIASES.resumeAccepted].includes(event.name)
      && event.ts <= startAt
    ));
  const generationLabel = targetGeneration !== undefined ? ` gen${targetGeneration}` : '';

  if (explicitUserResume) {
    return {
      kind: 'user_resume',
      label: `用户恢复监控${generationLabel}`,
      explicitUserAction: true,
      startEvent,
      actionEvent: explicitUserResume,
    };
  }

  const replacementEvent = generationEvents.find((event) => (
    operationOf(event) === 'replace'
    || event.name === 'listener.manager.replace.start'
    || event.name === 'listener.manager.replace.end'
  ));
  if (operationOf(startEvent) === 'replace' || replacementEvent) {
    return {
      kind: 'replacement',
      label: `监听实例切换${generationLabel}`,
      explicitUserAction: false,
      startEvent,
      actionEvent: replacementEvent || startEvent,
    };
  }

  const priorStop = events.find((event) => (
    [...EVENT_ALIASES.stopRequested, ...EVENT_ALIASES.stopAccepted].includes(event.name)
    && event.ts <= startAt
    && (
      targetGeneration === undefined
      || eventGeneration(event) === undefined
      || eventGeneration(event) !== targetGeneration
    )
  ));
  if (priorStop || (targetGeneration !== undefined && targetGeneration > 1)) {
    return {
      kind: 'restart',
      label: `重新开始监控${generationLabel}`,
      explicitUserAction: false,
      startEvent,
      actionEvent: startEvent || priorStop,
    };
  }

  if (targetGeneration === 1 || startEvent) {
    return {
      kind: 'initial',
      label: `开始监控${generationLabel}`,
      explicitUserAction: false,
      startEvent,
      actionEvent: startEvent,
    };
  }

  return {
    kind: 'unknown',
    label: `目标监控实例${generationLabel}`,
    explicitUserAction: false,
    startEvent,
  };
};

const eventMatchesGeneration = (event: DiagnosticEvent, generation?: number): boolean => (
  generation === undefined || eventGeneration(event) === undefined || eventGeneration(event) === generation
);

const eventSameOperation = (start: DiagnosticEvent, end: DiagnosticEvent): boolean => {
  if (start.span_id || end.span_id) {
    return Boolean(start.span_id && end.span_id && start.span_id === end.span_id);
  }
  if (start.flow_id && end.flow_id && start.flow_id !== end.flow_id) return false;
  const startRoom = eventRoomId(start);
  const endRoom = eventRoomId(end);
  if (startRoom && endRoom && startRoom !== endRoom) return false;
  const startGeneration = eventGeneration(start);
  const endGeneration = eventGeneration(end);
  if (startGeneration !== undefined && endGeneration !== undefined && startGeneration !== endGeneration) return false;
  const startTask = eventTaskId(start);
  const endTask = eventTaskId(end);
  if (startTask && endTask && startTask !== endTask) return false;
  return true;
};

const findPair = (
  events: DiagnosticEvent[],
  startNames: string[],
  endNames: string[],
): { start: DiagnosticEvent; end: DiagnosticEvent; durationMs: number } | undefined => {
  const start = findFirst(events, startNames);
  if (!start) {
    return undefined;
  }
  const end = events.find((event) => (
    endNames.includes(event.name)
    && event.ts >= start.ts
    && eventSameOperation(start, event)
  ));
  if (end) {
    return { start, end, durationMs: Math.max(0, end.ts - start.ts) };
  }
  if (typeof start.duration_ms === 'number') {
    return {
      start,
      end: { ...start, id: `${start.id}:synthetic-end`, ts: start.ts + start.duration_ms },
      durationMs: start.duration_ms,
    };
  }
  return undefined;
};

const findLongestPair = (
  events: DiagnosticEvent[],
  startNames: string[],
  endNames: string[],
): ReturnType<typeof findPair> => {
  const endBuckets = new Map<string, DiagnosticEvent[]>();
  const endCursors = new Map<string, number>();
  events.forEach((event) => {
    if (!endNames.includes(event.name)) return;
    const key = `${event.name}\0${eventOperationIdentity(event)}`;
    const bucket = endBuckets.get(key) || [];
    bucket.push(event);
    endBuckets.set(key, bucket);
  });
  const pairs = events
    .filter((event) => startNames.includes(event.name))
    .map((start) => {
      const matchingEndName = start.name.endsWith('.start')
        ? `${start.name.slice(0, -6)}.end`
        : undefined;
      const bucketNames = matchingEndName ? [matchingEndName] : endNames;
      let indexedEnd: DiagnosticEvent | undefined;
      for (const name of bucketNames) {
        const key = `${name}\0${eventOperationIdentity(start)}`;
        const bucket = endBuckets.get(key);
        if (!bucket) continue;
        let cursor = endCursors.get(key) || 0;
        while (cursor < bucket.length && bucket[cursor].ts < start.ts) cursor += 1;
        if (cursor < bucket.length) {
          indexedEnd = bucket[cursor];
          endCursors.set(key, cursor + 1);
          break;
        }
      }
      const end = indexedEnd || (events.length <= 5000
        ? events.find((event) => (
          endNames.includes(event.name)
          && (!matchingEndName || event.name === matchingEndName)
          && event.ts >= start.ts
          && eventSameOperation(start, event)
        ))
        : undefined);
      if (end) {
        return { start, end, durationMs: Math.max(0, end.ts - start.ts) };
      }
      if (typeof start.duration_ms === 'number') {
        return {
          start,
          end: { ...start, id: `${start.id}:synthetic-end`, ts: start.ts + start.duration_ms },
          durationMs: start.duration_ms,
        };
      }
      return undefined;
    })
    .filter((pair): pair is NonNullable<ReturnType<typeof findPair>> => Boolean(pair));
  return pairs.sort((left, right) => right.durationMs - left.durationMs)[0];
};

const metricMax = (bundle: DiagnosticBundle, names: string[]): number | undefined => {
  const matching = bundle.metrics?.filter((candidate) => names.includes(candidate.name)) || [];
  if (matching.length === 0) {
    return undefined;
  }
  return matching.reduce<number | undefined>((maximum, metric) => (
    metric.series.reduce<number | undefined>((metricMaximum, point) => (
      metricMaximum === undefined || point.value > metricMaximum ? point.value : metricMaximum
    ), maximum)
  ), undefined);
};

const phase = (
  key: string,
  startMs: number,
  endMs: number,
  status: DiagnosticPhase['status'],
  detail: string,
  eventIds: string[],
): DiagnosticPhase => ({
  key,
  label: PHASE_LABELS[key] || key,
  startMs,
  endMs,
  durationMs: Math.max(0, endMs - startMs),
  status,
  detail,
  eventIds,
});

const confidenceFromBundle = (bundle: DiagnosticBundle, hasExactRootSpan: boolean): DiagnosticFinding['confidence'] => {
  if (bundle.manifest.completeness === 'partial' || (bundle.manifest.dropped_events || 0) > 0) {
    return 'low';
  }
  return hasExactRootSpan ? 'high' : 'medium';
};

const buildFinding = (
  bundle: DiagnosticBundle,
  evidenceEvents: DiagnosticEvent[],
  analysisBase: Omit<DiagnosticAnalysis, 'finding'>,
  rootPhase: DiagnosticPhase,
  rootEvent?: DiagnosticEvent,
): DiagnosticFinding => {
  const { totalMs, detectionMs, configuredIntervalMs, intervalWithinExpectation } = analysisBase;
  const activeEvidenceEvents = evidenceEvents.filter((event) => (
    eventMatchesGeneration(event, analysisBase.targetGeneration)
  ));
  const ratio = totalMs > 0 ? Math.round((rootPhase.durationMs / totalMs) * 1000) / 10 : 0;
  const evidence: DiagnosticEvidence[] = [];
  const detectionEvent = findFirst(activeEvidenceEvents, EVENT_ALIASES.refreshEnd, isLiveEvent)
    || findFirst(activeEvidenceEvents, EVENT_ALIASES.liveObserved, isLiveEvent);

  if (detectionEvent) {
    evidence.push({
      kind: 'fact',
      title: `首次请求在 ${(detectionMs / 1000).toFixed(2)} 秒确认开播`,
      detail: configuredIntervalMs > 0
        ? `配置检测间隔为 ${(configuredIntervalMs / 1000).toFixed(1)} 秒；本次直播识别${intervalWithinExpectation ? '没有超过该参考值' : '已经超过该参考值'}。`
        : '数据包没有提供检测间隔配置。',
      eventId: detectionEvent.id,
    });
  }
  const liveObservations = activeEvidenceEvents.filter((event) => (
    EVENT_ALIASES.refreshEnd.includes(event.name) && isLiveEvent(event)
  ));
  if (liveObservations.length > 1) {
    evidence.push({
      kind: 'fact',
      title: `窗口内 ${liveObservations.length} 次平台观测均为 live`,
      detail: '这能排除“直播间在等待期间才刚开播”这一解释。',
      eventId: liveObservations[liveObservations.length - 1].id,
    });
  }

  if (rootEvent) {
    evidence.push({
      kind: 'fact',
      title: `${rootPhase.label}持续 ${(rootPhase.durationMs / 1000).toFixed(2)} 秒`,
      detail: `该区间占“开始监控 → FLV 首字节”总时间的 ${ratio}%，具有成对的开始/结束事件。`,
      eventId: rootEvent.id,
    });
  }

  const runtimeEvidence = activeEvidenceEvents.find((event) => (
    event.category === 'runtime'
    && (
      event.attrs?.region === rootPhase.key
      || event.attrs?.related_span_id === rootEvent?.span_id
      || event.attrs?.wait_reason !== undefined
    )
  ));
  if (runtimeEvidence) {
    const waitReason = String(runtimeEvidence.attrs?.wait_reason || runtimeEvidence.attrs?.state || 'waiting');
    const stack = runtimeEvidence.attrs?.stack ? `，栈顶 ${String(runtimeEvidence.attrs.stack)}` : '';
    evidence.push({
      kind: 'runtime',
      title: `运行时同时观察到 ${waitReason}`,
      detail: `关联 goroutine 在该业务区间内没有消失，而是处于明确等待状态${stack}。`,
      eventId: runtimeEvidence.id,
    });
  }

  const schedulerMax = metricMax(bundle, ['scheduler_latency_p99_ms', 'runtime.scheduler_latency_p99_ms']);
  const gcMax = metricMax(bundle, ['gc_pause_ms', 'runtime.gc_pause_ms']);
  const diskLatencyMax = metricMax(bundle, ['disk.write_latency_p95', 'disk.write_latency_p99_ms']);
  if (schedulerMax !== undefined && schedulerMax < 10) {
    evidence.push({
      kind: 'counter',
      title: '没有看到明显的 Go 调度拥塞',
      detail: `窗口内 scheduler latency P99 峰值为 ${schedulerMax.toFixed(2)} ms。`,
    });
  }
  if (gcMax !== undefined && gcMax < 20) {
    evidence.push({
      kind: 'counter',
      title: '没有看到足以解释几十秒延迟的 GC 暂停',
      detail: `窗口内最大 GC pause 为 ${gcMax.toFixed(2)} ms。`,
    });
  }
  if (diskLatencyMax !== undefined && diskLatencyMax < 20) {
    evidence.push({
      kind: 'counter',
      title: '磁盘延迟不足以解释几十秒等待',
      detail: `窗口内磁盘写入延迟峰值为 ${diskLatencyMax.toFixed(2)} ms，且在首字节之前还没有持续文件写入。`,
    });
  }

  if (bundle.manifest.completeness === 'partial' || (bundle.manifest.dropped_events || 0) > 0) {
    evidence.push({
      kind: 'missing',
      title: '轨迹不完整',
      detail: `数据包声明丢失 ${bundle.manifest.dropped_events || 0} 条事件，自动结论只能作为低置信度线索。`,
    });
  }

  let code = 'recording.preparation.slow';
  let title = '录制准备阶段耗时异常';
  let summary = `直播识别完成后，${rootPhase.label}占用了 ${rootPhase.durationMs / 1000} 秒。`;
  let suggestions = ['展开根因阶段，检查其结束状态、错误码与重试次数。'];

  if (rootPhase.key === 'detection') {
    const rateLimit = findLongestPair(
      activeEvidenceEvents,
      EVENT_ALIASES.rateLimitStart,
      EVENT_ALIASES.rateLimitEnd,
    );
    const configuredRooms = configuredRoomCount(bundle);
    const lifecycle = diagnosticLifecycle(bundle, {
      roomId: analysisBase.targetRoomId,
      generation: analysisBase.targetGeneration,
      startEventId: bundle.incident.focus_start_event_id || bundle.incident.anchor_start_event_id,
    });
    const staleEvents = evidenceEvents.filter((event) => (
      EVENT_ALIASES.staleDrop.includes(event.name)
      || event.disposition === 'dropped'
      || event.attrs?.stale === true
      || event.attrs?.stale_generation === true
      || event.attrs?.outcome_code === 'stale_generation_ignored'
    ));
    const isRestart = ['user_resume', 'restart', 'replacement'].includes(lifecycle.kind);
    const isSharedLimiterRestart = Boolean(rateLimit && isRestart && configuredRooms > 1);
    const restartDescription = lifecycle.kind === 'user_resume'
      ? '用户恢复监控后'
      : lifecycle.kind === 'replacement'
        ? '监听实例切换后'
        : '重新开始监控后';
    const detectionSubject = lifecycle.kind === 'user_resume'
      ? '用户恢复后的新 generation'
      : lifecycle.kind === 'replacement'
        ? '实例切换后的新 generation'
        : lifecycle.kind === 'restart'
          ? '重新启动的 generation'
          : '首次检测';
    code = isSharedLimiterRestart
      ? 'live.shared_rate_limit.rejoin_after_restart'
      : rateLimit ? 'live.rate_limit.wait.slow' : 'live.detection.slow';
    title = isSharedLimiterRestart
      ? `${configuredRooms} 个房间共享限流器；${restartDescription}目标任务重新参与竞争`
      : rateLimit ? '平台限流器等待推迟了首次检测' : '直播状态检测阶段超过配置参考值';
    summary = rateLimit
      ? `${isSharedLimiterRestart ? detectionSubject : '首次检测'}确认开播用了 ${(detectionMs / 1000).toFixed(2)} 秒，其中共享限流等待 ${(rateLimit.durationMs / 1000).toFixed(2)} 秒。${(configuredIntervalMs / 1000).toFixed(0)} 秒配置是轮询调度参考，不是包含竞争等待与请求耗时的硬上限。`
      : `首次确认开播用了 ${(detectionMs / 1000).toFixed(2)} 秒，超过 ${(configuredIntervalMs / 1000).toFixed(1)} 秒检测间隔。重点检查 scheduler、平台限流和 Live API 请求耗时。`;
    if (rateLimit) {
      const waiterCount = numericValue(rateLimit.start.attrs?.waiter_count_at_enter)
        ?? numericValue(rateLimit.start.attrs?.waiting_rooms)
        ?? numericValue(rateLimit.start.attrs?.queue_depth_at_enqueue);
      evidence.push({
        kind: 'fact',
        title: `${rateLimit.start.name.replace(/\.start$/, '')} 持续 ${(rateLimit.durationMs / 1000).toFixed(2)} 秒`,
        detail: `该 span 位于目标 generation 的 Live API 请求之前${waiterCount !== undefined ? `；进入时共有 ${waiterCount} 个竞争等待者` : ''}。${rateLimit.start.name.includes('.in_flight.') ? '它表示等待同平台请求串行槽位，不是普通轮询 sleep。' : ''}它直接解释了检测为什么晚于配置间隔，但不代表限流器是 FIFO 队列。`,
        eventId: rateLimit.start.id,
      });
    }
    if (isSharedLimiterRestart && lifecycle.actionEvent) {
      evidence.push({
        kind: 'fact',
        title: `${lifecycle.label}，目标检测任务进入了新的限流等待区间`,
        detail: lifecycle.explicitUserAction
          ? '数据包含有明确的用户恢复事件；恢复后的检测任务重新参与共享平台限流竞争。观察顺序不能证明严格的 FIFO 排名。'
          : '数据包只能证明 Listener 实例或 generation 发生变化，不能据此声称由用户手动触发；目标任务随后参与共享平台限流竞争。',
        eventId: lifecycle.actionEvent.id,
      });
    }
    if (staleEvents.length > 0) {
      const stale = staleEvents[0];
      evidence.push({
        kind: 'counter',
        title: `旧 generation 的 ${staleEvents.length} 条迟到结果被安全丢弃`,
        detail: '旧任务曾返回 live，但 generation guard 没有让它创建 Recorder；它是并发旁支，不在最终 FLV 首字节的因果链上。',
        eventId: stale.id,
      });
    }
    suggestions = [
      '检查目标任务进入共享限流器时的 waiter_count、recheck_count 和 total_wait_ms。',
      '把“用户请求停/启”“新 generation 创建”“旧 context 观察到取消”分开记录，避免把点击时间当成取消完成时间。',
      '确认 20 秒是轮询调度间隔，而不是包含共享限流等待与平台请求的硬上限。',
    ];
  } else if (rootPhase.key === 'tool_wait') {
    code = 'tools.ffmpeg.ready.slow';
    title = '等待 FFmpeg 就绪是主要延迟';
    summary = `慢不在直播状态检测。Recorder 等待 FFmpeg ready ${(rootPhase.durationMs / 1000).toFixed(2)} 秒，占总耗时 ${ratio}%。`;
    suggestions = [
      '展开 FFmpeg checking/downloading/verifying 子事件，确认是下载、校验还是锁等待。',
      '检查工具缓存目录、网络下载速度和文件权限。',
      '考虑让工具预热完成后再声明进程 ready，或在 WebUI 明确显示“等待工具”。',
    ];
  } else if (rootPhase.key === 'connect_probe') {
    code = 'stream.probe.slow';
    title = '取流建连或探测重试是主要延迟';
    summary = `检测间隔工作正常；额外时间主要花在流候选建连、超时、退避或 probe，共 ${(rootPhase.durationMs / 1000).toFixed(2)} 秒。`;
    suggestions = [
      '展开每个 stream candidate，比较 connect/TLS/probe timeout。',
      '检查代理、DNS和线路优先级，避免失效候选长期占用超时。',
      '记录重试退避及最终选中的 CDN/线路诊断哈希。',
    ];
  } else if (rootPhase.key === 'upstream_wait') {
    code = 'stream.first_byte.slow';
    title = 'TCP 已连接，但上游迟迟没有首字节';
    summary = `直播和 Recorder 启动都很快，主要延迟来自连接后等待上游数据 ${(rootPhase.durationMs / 1000).toFixed(2)} 秒。`;
    suggestions = [
      '检查 runtime netpoll 与 socket read deadline。',
      '切换 CDN/线路复测，并记录候选流诊断哈希。',
      '将“TCP连接成功”和“收到媒体首字节”分成不同健康状态。',
    ];
  }

  return {
    code,
    title,
    summary,
    confidence: confidenceFromBundle(bundle, Boolean(rootEvent)),
    rootPhaseKey: rootPhase.key,
    evidence,
    suggestions,
  };
};

export const analyzeBundle = (
  bundle: DiagnosticBundle,
  override: DiagnosticScope = {},
): DiagnosticAnalysis => {
  const scope = diagnosticScope(bundle, override);
  const scopedEvents = scopedDiagnosticEvents(bundle, scope);
  const goalById = scope.goalEventId
    ? scopedEvents.find((event) => event.id === scope.goalEventId)
    : undefined;
  const inferredGeneration = scope.generation ?? (goalById ? eventGeneration(goalById) : undefined);
  const targetFlows = new Set(
    scopedEvents
      .filter((event) => (
        (!scope.roomId || eventRoomId(event) === scope.roomId)
        && eventMatchesGeneration(event, inferredGeneration)
      ))
      .map((event) => event.flow_id)
      .filter((flow): flow is string => Boolean(flow)),
  );
  if (goalById?.flow_id) targetFlows.add(goalById.flow_id);

  const activeEvents = scopedEvents.filter((event) => (
    event.id === scope.startEventId
    || event.id === scope.goalEventId
    || (
      eventMatchesGeneration(event, inferredGeneration)
      && (
        targetFlows.size === 0
        || !event.flow_id
        || targetFlows.has(event.flow_id)
        || eventRoomId(event) === scope.roomId
      )
    )
  ));
  const firstByte = (
    goalById && EVENT_ALIASES.firstByte.includes(goalById.name)
      ? goalById
      : findFirst(activeEvents, EVENT_ALIASES.firstByte)
  );

  const explicitStart = scope.startEventId
    ? activeEvents.find((event) => event.id === scope.startEventId)
    : undefined;
  const startCandidates = activeEvents.filter((event) => (
    EVENT_ALIASES.monitorStarted.includes(event.name)
    && (!firstByte || event.ts <= firstByte.ts)
    && eventMatchesGeneration(event, inferredGeneration)
  ));
  const resumeStart = startCandidates.find((event) => EVENT_ALIASES.resumeAccepted.includes(event.name));
  const monitor = explicitStart || resumeStart || startCandidates[0];

  if (!monitor || !firstByte) {
    throw new Error(
      '无法在同一 room / generation 因果范围内找到开始监控与 segment.first_byte。'
      + ' 多房间数据包必须提供 incident.target_room_id、target_generation 和起止事件 ID。',
    );
  }

  const pathEvents = activeEvents.filter((event) => event.ts >= monitor.ts && event.ts <= firstByte.ts);
  const live = findFirst(pathEvents, EVENT_ALIASES.refreshEnd, isLiveEvent)
    || findFirst(pathEvents, EVENT_ALIASES.liveObserved, isLiveEvent);
  const recorder = live
    ? findFirst(pathEvents, EVENT_ALIASES.recorderStarted, (event) => event.ts >= live.ts)
    : undefined;

  if (!live || !recorder) {
    throw new Error(
      '目标 room / generation 缺少 live 或 recorder 关键里程碑；'
      + '为避免把其它房间或旧 generation 拼入结论，Viewer 已停止自动分析。',
    );
  }

  const configuredIntervalMs = bundle.incident.expected_detection_interval_ms || 0;
  const detectionMs = Math.max(0, live.ts - monitor.ts);
  const totalMs = Math.max(0, firstByte.ts - monitor.ts);
  const recordingPreparationMs = Math.max(0, firstByte.ts - recorder.ts);
  const intervalWithinExpectation = configuredIntervalMs === 0 || detectionMs <= configuredIntervalMs + 500;
  const relative = (absoluteMs: number): number => Math.max(0, absoluteMs - monitor.ts);

  const phases: DiagnosticPhase[] = [
    phase(
      'detection',
      0,
      relative(live.ts),
      intervalWithinExpectation ? 'normal' : 'critical',
      intervalWithinExpectation
        ? '目标 generation 在检测间隔参考值内确认直播。'
        : '目标 generation 的直播状态确认超过配置间隔，应检查共享限流、锁竞争和请求耗时。',
      [monitor.id, live.id],
    ),
    phase(
      'dispatch',
      relative(live.ts),
      relative(recorder.ts),
      recorder.ts - live.ts < 500 ? 'normal' : 'warning',
      '同一 generation 从确认开播到 Recorder 会话创建。',
      [live.id, recorder.id],
    ),
  ];

  const rootCandidates: Array<{ phase: DiagnosticPhase; event: DiagnosticEvent }> = [];
  const addPairPhase = (
    key: string,
    pair: ReturnType<typeof findPair>,
    criticalMs: number,
    detail: string,
  ) => {
    if (!pair || pair.end.ts < monitor.ts || pair.start.ts > firstByte.ts) {
      return;
    }
    const item = phase(
      key,
      relative(pair.start.ts),
      relative(pair.end.ts),
      pair.durationMs >= criticalMs ? 'critical' : 'normal',
      detail,
      [pair.start.id, pair.end.id],
    );
    phases.push(item);
    rootCandidates.push({ phase: item, event: pair.start });
  };

  addPairPhase(
    'resolve',
    findPair(pathEvents, EVENT_ALIASES.resolveStart, EVENT_ALIASES.resolveEnd),
    5000,
    '取得播放地址并选择候选流。',
  );
  addPairPhase(
    'tool_wait',
    findPair(pathEvents, EVENT_ALIASES.toolWaitStart, EVENT_ALIASES.toolWaitEnd),
    3000,
    'Recorder 等待 FFmpeg 状态变为 ready。',
  );

  const connect = findPair(pathEvents, EVENT_ALIASES.connectStart, EVENT_ALIASES.connectEnd);
  const probe = findPair(pathEvents, EVENT_ALIASES.probeStart, EVENT_ALIASES.probeEnd);
  if (connect || probe) {
    const start = Math.min(connect?.start.ts ?? Number.POSITIVE_INFINITY, probe?.start.ts ?? Number.POSITIVE_INFINITY);
    const end = Math.max(connect?.end.ts ?? start, probe?.end.ts ?? start);
    const item = phase(
      'connect_probe',
      relative(start),
      relative(end),
      end - start >= 5000 ? 'critical' : 'normal',
      '连接候选直播流并完成可读性探测。',
      [connect?.start.id, connect?.end.id, probe?.start.id, probe?.end.id].filter((id): id is string => Boolean(id)),
    );
    phases.push(item);
    rootCandidates.push({ phase: item, event: connect?.start || probe!.start });
  }

  addPairPhase(
    'upstream_wait',
    findPair(pathEvents, EVENT_ALIASES.upstreamWaitStart, EVENT_ALIASES.upstreamWaitEnd),
    3000,
    '连接建立后等待上游返回第一个媒体字节。',
  );

  const parser = findFirst(pathEvents, EVENT_ALIASES.parserStart);
  if (parser && firstByte.ts >= parser.ts) {
    phases.push(phase(
      'parser_first_byte',
      relative(parser.ts),
      relative(firstByte.ts),
      firstByte.ts - parser.ts >= 3000 ? 'warning' : 'normal',
      'Parser 启动、创建输出文件并写入首字节。',
      [parser.id, firstByte.id],
    ));
  }

  let rootPhase: DiagnosticPhase;
  let rootEvent: DiagnosticEvent | undefined;
  if (!intervalWithinExpectation) {
    rootPhase = phases[0];
    rootEvent = live;
  } else if (rootCandidates.length > 0) {
    const root = [...rootCandidates].sort((a, b) => b.phase.durationMs - a.phase.durationMs)[0];
    rootPhase = root.phase;
    rootEvent = root.event;
  } else {
    rootPhase = phase(
      'recording_prepare',
      relative(recorder.ts),
      relative(firstByte.ts),
      recordingPreparationMs >= 5000 ? 'critical' : 'normal',
      '数据包没有提供更细的录制准备 span。',
      [recorder.id, firstByte.id],
    );
    phases.push(rootPhase);
  }

  const processStart = (
    findFirst(bundle.events, ['process.start', 'monitor.batch.start'])
    || [...bundle.events].sort((a, b) => a.ts - b.ts)[0]
    || monitor
  ).ts;
  const base: Omit<DiagnosticAnalysis, 'finding'> = {
    totalMs,
    detectionMs,
    recordingPreparationMs,
    configuredIntervalMs,
    firstLiveAtMs: live.ts,
    recorderStartedAtMs: recorder.ts,
    firstByteAtMs: firstByte.ts,
    windowStartMs: monitor.ts,
    processStartMs: processStart,
    processToFirstByteMs: Math.max(0, firstByte.ts - processStart),
    targetRoomId: scope.roomId,
    targetGeneration: inferredGeneration,
    scopedEventIds: scopedEvents.map((event) => event.id),
    intervalWithinExpectation,
    completeness: bundle.manifest.completeness || 'unknown',
    phases: phases.sort((a, b) => a.startMs - b.startMs || b.durationMs - a.durationMs),
  };

  return {
    ...base,
    finding: buildFinding(bundle, scopedEvents, base, rootPhase, rootEvent),
  };
};

export const buildConcurrencySummary = (
  bundle: DiagnosticBundle,
  override: DiagnosticScope = {},
): DiagnosticConcurrencySummary => {
  const scope = diagnosticScope(bundle, override);
  const targetEvents = scopedDiagnosticEvents(bundle, scope);
  const lifecycle = diagnosticLifecycle(bundle, scope);
  const roomEntities = (bundle.entities || []).filter((entity) => (
    entity.type === 'room' || entity.type === 'live_room'
  ));
  const observedStartedRooms = new Set(
    bundle.events
      .filter((event) => (
        event.name === 'listener.start.accepted' || event.name === 'monitor.started'
      ))
      .map(eventRoomId)
      .filter((roomId): roomId is string => Boolean(roomId)),
  ).size;
  const generations = Array.from(new Set(
    targetEvents
      .map(eventGeneration)
      .filter((generation): generation is number => generation !== undefined),
  )).sort((a, b) => a - b);
  const limiterPair = findLongestPair(
    targetEvents.filter((event) => eventMatchesGeneration(event, scope.generation)),
    EVENT_ALIASES.rateLimitStart,
    EVENT_ALIASES.rateLimitEnd,
  );
  const limiterStart = limiterPair?.start || targetEvents.find((event) => (
    EVENT_ALIASES.rateLimitStart.includes(event.name)
    && eventMatchesGeneration(event, scope.generation)
  ));
  const lifecycleStartAt = lifecycle.startEvent?.ts ?? Number.POSITIVE_INFINITY;
  const stop = [...targetEvents].reverse().find((event) => (
    [...EVENT_ALIASES.stopRequested, ...EVENT_ALIASES.stopAccepted].includes(event.name)
    && event.ts <= lifecycleStartAt
  ));
  const tasks = new Set<string>();
  const goroutines = new Set<string>();
  targetEvents.forEach((event) => {
    const task = event.task_id
      || event.trace_task_id
      || (typeof event.attrs?.task_id === 'string' ? event.attrs.task_id : undefined);
    const goroutine = event.goroutine_id
      || (typeof event.attrs?.goroutine_id === 'string' ? event.attrs.goroutine_id : undefined);
    if (task) tasks.add(task);
    if (goroutine) goroutines.add(goroutine);
  });
  bundle.runtime_slices?.forEach((slice) => {
    if (slice.task_id) tasks.add(slice.task_id);
    if (slice.goroutine_id) goroutines.add(slice.goroutine_id);
  });

  return {
    configuredRooms: configuredRoomCount(bundle),
    startedRooms: bundle.manifest.room_population?.started
      || numericValue(bundle.configuration?.started_room_count)
      || observedStartedRooms,
    fullyTracedRooms: bundle.manifest.room_population?.fully_traced
      || Math.min(Math.max(roomEntities.length, observedStartedRooms), 1),
    targetRoomId: scope.roomId,
    targetGeneration: scope.generation,
    generations,
    queueDepthPeak: metricMax(bundle, [
      'platform.rate_limiter.in_flight_waiting_rooms',
      'platform.rate_limiter.waiting_rooms',
      'platform.rate_limiter.waiter_count',
      'scheduler.waiter_count',
      'scheduler.queue_depth',
    ]),
    targetQueuePosition: numericValue(limiterStart?.attrs?.waiter_count_at_enter)
      ?? numericValue(limiterStart?.attrs?.waiting_rooms)
      ?? numericValue(limiterStart?.attrs?.queue_depth_at_enqueue),
    targetQueueWaitMs: limiterPair?.durationMs
      ?? numericValue(limiterStart?.attrs?.total_wait_ms),
    stopAtMs: stop?.ts,
    resumeAtMs: lifecycle.kind === 'initial'
      ? undefined
      : (lifecycle.actionEvent?.ts ?? lifecycle.startEvent?.ts),
    staleEventCount: targetEvents.filter((event) => (
      EVENT_ALIASES.staleDrop.includes(event.name)
      || event.disposition === 'dropped'
      || event.attrs?.stale === true
      || event.attrs?.stale_generation === true
      || event.attrs?.outcome_code === 'stale_generation_ignored'
    )).length,
    taskCount: tasks.size,
    goroutineCount: goroutines.size,
  };
};

const laneForEvent = (event: DiagnosticEvent): { key: string; label: string } => {
  const component = event.component.toLowerCase();
  const name = event.name.toLowerCase();
  let lane = 'recorder';
  if (component.includes('monitor') || name.startsWith('monitor.')) lane = 'monitor';
  else if (
    component.includes('listener')
    || component.includes('live')
    || component.includes('scheduler')
    || component.includes('rate')
  ) lane = 'listener';
  else if (component.includes('event') || name.startsWith('event.')) lane = 'events';
  else if (component.includes('tool') || component.includes('ffmpeg') || name.startsWith('ffmpeg.')) lane = 'tools';
  else if (component.includes('stream') || component.includes('parser') || name.startsWith('stream.') || name.startsWith('parser.')) lane = 'stream';
  else if (component.includes('file') || component.includes('segment') || name.startsWith('segment.')) lane = 'file';
  else if (component.includes('runtime') || event.category === 'runtime') lane = 'runtime';

  const item = LANE_ORDER.find(([key]) => key === lane) || LANE_ORDER[3];
  return { key: item[0], label: item[1] };
};

const displayLabel = (event: DiagnosticEvent): string => (
  EVENT_LABELS[event.name]
  || (typeof event.attrs?.label === 'string' ? event.attrs.label : event.name)
);

export const buildTimelineItems = (
  bundle: DiagnosticBundle,
  override: DiagnosticScope = {},
): TimelineItem[] => {
  const events = scopedDiagnosticEvents(bundle, override);
  const consumedEndIds = new Set<string>();
  const result: TimelineItem[] = [];
  const endBuckets = new Map<string, DiagnosticEvent[]>();
  const endBucketCursors = new Map<string, number>();
  events.forEach((event) => {
    if (!event.name.endsWith('.end')) return;
    const key = `${event.name}\0${eventOperationIdentity(event)}`;
    const bucket = endBuckets.get(key) || [];
    bucket.push(event);
    endBuckets.set(key, bucket);
  });
  const takeIndexedEnd = (
    expectedEndName: string,
    start: DiagnosticEvent,
  ): DiagnosticEvent | undefined => {
    const key = `${expectedEndName}\0${eventOperationIdentity(start)}`;
    const bucket = endBuckets.get(key);
    if (!bucket) return undefined;
    let cursor = endBucketCursors.get(key) || 0;
    while (cursor < bucket.length && (
      consumedEndIds.has(bucket[cursor].id) || bucket[cursor].ts < start.ts
    )) {
      cursor += 1;
    }
    const end = bucket[cursor];
    if (end) endBucketCursors.set(key, cursor + 1);
    return end;
  };

  events.forEach((event) => {
    if (consumedEndIds.has(event.id)) {
      return;
    }
    let endMs = event.ts + (event.duration_ms || 0);
    let milestone = !event.duration_ms;

    if (event.name.endsWith('.start')) {
      const expectedEndName = `${event.name.slice(0, -6)}.end`;
      const indexedEnd = takeIndexedEnd(expectedEndName, event);
      // 旧 schema 可能只在一侧记录 flow/generation。小包继续使用宽松匹配；
      // 大包必须走有界索引，避免畸形的 100k 事件包触发 O(n²) 扫描。
      const end = indexedEnd || (events.length <= 5000
        ? events.find((candidate) => (
          !consumedEndIds.has(candidate.id)
          && candidate.name === expectedEndName
          && candidate.ts >= event.ts
          && eventSameOperation(event, candidate)
        ))
        : undefined);
      if (end) {
        endMs = end.ts;
        milestone = false;
        consumedEndIds.add(end.id);
      }
    }

    const lane = laneForEvent(event);
    let status: TimelineItem['status'] = 'neutral';
    if (event.category === 'runtime') status = 'runtime';
    else if (event.severity === 'error') status = 'critical';
    else if (event.severity === 'warn') status = 'warning';
    else if (event.status === 'ok' || event.status === 'live' || event.status === 'ready') status = 'normal';
    else if (!milestone && endMs - event.ts > 5000 && (
      event.name.includes('wait')
      || event.name.includes('probe')
      || event.name.includes('refresh')
    )) status = 'critical';

    result.push({
      id: event.id,
      event,
      lane: lane.key,
      laneLabel: lane.label,
      startMs: event.ts,
      endMs,
      label: displayLabel(event),
      status,
      milestone,
    });
  });

  return result;
};

export const timelineLanes = LANE_ORDER.map(([key, label]) => ({ key, label }));

export const formatDuration = (durationMs: number): string => {
  if (durationMs < 1) return `${Math.round(durationMs * 1000)} µs`;
  if (durationMs < 1000) return `${durationMs.toFixed(durationMs < 10 ? 2 : 0)} ms`;
  return `${(durationMs / 1000).toFixed(durationMs < 10000 ? 2 : 1)} s`;
};

export const parseDiagnosticBundle = (text: string): DiagnosticBundle => {
  if (text.length > MAX_BUNDLE_TEXT_CHARACTERS) {
    throw new Error('JSON 调查包超过 25 MiB，浏览器版 Viewer 拒绝加载。');
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (error) {
    throw new Error(`不是有效的 JSON 数据包：${error instanceof Error ? error.message : String(error)}`);
  }

  if (!parsed || typeof parsed !== 'object') {
    throw new Error('数据包顶层必须是 JSON 对象。');
  }
  const rawBundle = parsed as Record<string, unknown>;
  const schema = typeof rawBundle.schema === 'string' ? rawBundle.schema : '';
  if (!rawBundle.manifest || !rawBundle.incident || !Array.isArray(rawBundle.events)) {
    throw new Error('数据包缺少 manifest、incident 或 events。');
  }
  if (rawBundle.events.length > MAX_NORMALIZED_EVENTS) {
    throw new Error('数据包包含超过 100,000 条事件，浏览器版 Viewer 暂不加载。');
  }
  if (!schema.startsWith('bililive.diagnostic-bundle/')) {
    throw new Error(`不支持的数据包 schema：${schema || '未提供'}`);
  }

  const isRecord = (value: unknown): value is Record<string, unknown> => (
    Boolean(value) && typeof value === 'object' && !Array.isArray(value)
  );
  const stringValue = (value: unknown, fallback = ''): string => {
    if (typeof value !== 'string') return fallback;
    if (value.length > MAX_FIELD_CHARACTERS) {
      throw new Error('数据包包含超过 64 KiB 的单个文本字段，浏览器版 Viewer 拒绝加载。');
    }
    return value;
  };
  const numberValue = (value: unknown, fallback = 0): number => (
    typeof value === 'number' && Number.isFinite(value) ? value : fallback
  );
  const scalarAttrs = (value: unknown): Record<string, string | number | boolean | null> => {
    if (!isRecord(value)) {
      return {};
    }
    const keys = Object.keys(value);
    if (keys.length > MAX_EVENT_ATTRIBUTES) {
      throw new Error(`单个事件包含超过 ${MAX_EVENT_ATTRIBUTES} 个属性，浏览器版 Viewer 拒绝加载。`);
    }
    return keys.reduce<Record<string, string | number | boolean | null>>((result, key) => {
      const item = value[key];
      if (item === null || ['string', 'number', 'boolean'].includes(typeof item)) {
        if (typeof item === 'string' && item.length > MAX_FIELD_CHARACTERS) {
          throw new Error('事件属性包含超过 64 KiB 的文本，浏览器版 Viewer 拒绝加载。');
        }
        result[key] = item as string | number | boolean | null;
      }
      return result;
    }, {});
  };

  const rawManifest = isRecord(rawBundle.manifest) ? rawBundle.manifest : {};
  const rawIncident = isRecord(rawBundle.incident) ? rawBundle.incident : {};
  const rawConfiguration = isRecord(rawBundle.configuration) ? rawBundle.configuration : {};

  const normalizedEvents: DiagnosticEvent[] = rawBundle.events
    .filter(isRecord)
    .map((event, index) => {
      const outcome = isRecord(event.outcome) ? event.outcome : {};
      const entity = isRecord(event.entity) ? event.entity : {};
      const attrs: Record<string, DiagnosticValue> = {
        ...scalarAttrs(event.attrs),
        ...(typeof outcome.code === 'string' ? { outcome_code: outcome.code } : {}),
        ...(typeof outcome.status === 'string' ? { outcome_status: outcome.status } : {}),
        ...(typeof outcome.duration_ms === 'number' ? { outcome_duration_ms: outcome.duration_ms } : {}),
        ...(typeof event.room_scope_id === 'string' ? { room_scope_id: event.room_scope_id } : {}),
        ...(typeof event.live_id_at_event === 'string' ? { live_id_at_event: event.live_id_at_event } : {}),
      };
      const component = stringValue(event.component, 'unknown');
      const rawLinks = Array.isArray(event.links) ? event.links : [];
      if (rawLinks.length > MAX_EVENT_LINKS) {
        throw new Error(`单个事件包含超过 ${MAX_EVENT_LINKS} 条因果边，浏览器版 Viewer 拒绝加载。`);
      }
      const eventId = stringValue(event.id, stringValue(event.event_id, `event_${index + 1}`));
      const roomId = stringValue(
        event.room_scope_id,
        stringValue(event.room_id, stringValue(entity.room_scope_id, stringValue(entity.room_id))),
      );
      const generation = numericValue(event.generation) ?? numericValue(entity.generation);
      const rawSeverity = stringValue(event.severity);
      return {
        id: eventId,
        seq: typeof event.seq === 'number' ? event.seq : index + 1,
        global_seq: numericValue(event.global_seq),
        ts: numberValue(event.ts, numberValue(event.at_ms, numberValue(event.mono_ns) / 1000000)),
        wall_time: stringValue(event.wall_time) || undefined,
        kind: stringValue(event.kind) || undefined,
        message: stringValue(event.message) || undefined,
        severity: (
          ['trace', 'info', 'debug', 'warn', 'error'].includes(rawSeverity)
            ? rawSeverity
            : 'info'
        ) as DiagnosticEvent['severity'],
        category: stringValue(event.category, component === 'runtime' ? 'runtime' : 'business'),
        name: stringValue(event.name, 'unknown.event'),
        component,
        flow_id: stringValue(event.flow_id) || undefined,
        span_id: stringValue(event.span_id) || undefined,
        parent_span_id: stringValue(event.parent_span_id) || undefined,
        duration_ms: typeof event.duration_ms === 'number' ? event.duration_ms : undefined,
        status: typeof event.status === 'string'
          ? event.status
          : (typeof outcome.status === 'string' ? outcome.status : undefined),
        attrs,
        lane: stringValue(event.lane) || undefined,
        entity_id: stringValue(event.entity_id, stringValue(entity.id)) || undefined,
        room_id: roomId || undefined,
        generation,
        dispatch_id: typeof event.dispatch_id === 'string'
          ? stringValue(event.dispatch_id)
          : (typeof attrs.dispatch_id === 'string' ? attrs.dispatch_id : undefined),
        handler_id: typeof event.handler_id === 'string'
          ? stringValue(event.handler_id)
          : (typeof attrs.handler_id === 'string' ? attrs.handler_id : undefined),
        task_id: typeof event.task_id === 'string'
          ? stringValue(event.task_id)
          : (typeof attrs.task_id === 'string' ? attrs.task_id : undefined),
        trace_task_id: typeof event.trace_task_id === 'string'
          ? stringValue(event.trace_task_id)
          : (typeof attrs.trace_task_id === 'string' ? attrs.trace_task_id : undefined),
        goroutine_id: typeof event.goroutine_id === 'string'
          ? stringValue(event.goroutine_id)
          : (typeof attrs.goroutine_id === 'string' ? attrs.goroutine_id : undefined),
        goroutine_seq: numericValue(event.goroutine_seq),
        disposition: (
          ['accepted', 'dropped', 'ignored'].includes(stringValue(event.disposition))
            ? stringValue(event.disposition)
            : undefined
        ) as DiagnosticEvent['disposition'],
        links: rawLinks.filter(isRecord).map((link) => ({
          rel: stringValue(link.rel, 'related'),
          event_id: typeof link.event_id === 'string' ? link.event_id : undefined,
          span_id: typeof link.span_id === 'string' ? link.span_id : undefined,
        })),
      };
    });

  const runtimeSamples = Array.isArray(rawBundle.runtime_samples) ? rawBundle.runtime_samples : [];
  if (runtimeSamples.length > MAX_NORMALIZED_EVENTS - normalizedEvents.length) {
    throw new Error('业务事件与 Runtime 样本合计超过 100,000 条，浏览器版 Viewer 暂不加载。');
  }
  runtimeSamples.filter(isRecord).forEach((sample, index) => {
    const topFrames = Array.isArray(sample.top_frames)
      ? sample.top_frames
        .filter((frame): frame is string => typeof frame === 'string')
        .slice(0, 128)
        .map((frame) => stringValue(frame))
      : [];
    const sampleKind = stringValue(sample.sample_kind, 'snapshot');
    const runtimeName = sampleKind === 'unblock'
      ? 'runtime.unblocked'
      : sampleKind === 'flow' ? 'runtime.flow' : 'runtime.blocked';
    normalizedEvents.push({
      id: stringValue(sample.id, `runtime_sample_${index + 1}`),
      seq: numericValue(sample.global_seq) ?? normalizedEvents.length + index + 1,
      global_seq: numericValue(sample.global_seq),
      ts: numberValue(sample.at_ms),
      severity: 'debug',
      category: 'runtime',
      name: runtimeName,
      component: 'runtime',
      flow_id: typeof sample.flow_id === 'string' ? sample.flow_id : undefined,
      span_id: typeof sample.linked_span_id === 'string' ? sample.linked_span_id : undefined,
      task_id: typeof sample.task_id === 'string' ? sample.task_id : undefined,
      trace_task_id: typeof sample.trace_task_id === 'string' ? sample.trace_task_id : undefined,
      goroutine_id: stringValue(sample.goroutine_id, 'unknown'),
      generation: numericValue(sample.generation),
      attrs: {
        goroutine_id: stringValue(sample.goroutine_id, 'unknown'),
        task_id: stringValue(sample.task_id),
        trace_task_id: stringValue(sample.trace_task_id),
        sample_kind: sampleKind,
        state: stringValue(sample.state, 'waiting'),
        wait_reason: stringValue(sample.wait_reason, 'unknown'),
        stack: topFrames.join(' → ') || stringValue(sample.stack_fingerprint),
        related_span_id: stringValue(sample.linked_span_id),
        from_goroutine_id: stringValue(sample.from_goroutine_id),
        to_goroutine_id: stringValue(sample.to_goroutine_id),
      },
    });
  });

  const rawRuntimeSlices = Array.isArray(rawBundle.runtime_slices) ? rawBundle.runtime_slices : [];
  if (rawRuntimeSlices.length > MAX_RUNTIME_SLICES) {
    throw new Error('Runtime 调度片段超过 100,000 条，浏览器版 Viewer 暂不加载。');
  }
  const runtimeSlices: DiagnosticRuntimeSlice[] = rawRuntimeSlices.filter(isRecord).map((slice, index) => {
    const state = stringValue(slice.state, 'unknown');
    const rawLinks = Array.isArray(slice.links) ? slice.links : [];
    if (rawLinks.length > MAX_EVENT_LINKS) {
      throw new Error(`单个 Runtime 片段包含超过 ${MAX_EVENT_LINKS} 条因果边，浏览器版 Viewer 拒绝加载。`);
    }
    return {
      id: stringValue(slice.id, `runtime_slice_${index + 1}`),
      goroutine_id: stringValue(slice.goroutine_id, 'unknown'),
      task_id: typeof slice.task_id === 'string' ? slice.task_id : undefined,
      trace_task_id: typeof slice.trace_task_id === 'string' ? slice.trace_task_id : undefined,
      start_ms: numberValue(slice.start_ms, numberValue(slice.at_ms)),
      end_ms: numberValue(slice.end_ms, numberValue(slice.at_ms) + numberValue(slice.duration_ms)),
      state: (
        ['running', 'runnable', 'waiting', 'syscall'].includes(state) ? state : 'unknown'
      ) as DiagnosticRuntimeSlice['state'],
      wait_reason: typeof slice.wait_reason === 'string' ? slice.wait_reason : undefined,
      stack_fingerprint: typeof slice.stack_fingerprint === 'string' ? slice.stack_fingerprint : undefined,
      processor_id: typeof slice.processor_id === 'string' ? slice.processor_id : undefined,
      thread_id: typeof slice.thread_id === 'string' ? slice.thread_id : undefined,
      seq_on_g: numericValue(slice.seq_on_g),
      generation: numericValue(slice.generation),
      flow_id: typeof slice.flow_id === 'string' ? slice.flow_id : undefined,
      links: rawLinks.filter(isRecord).map((link) => ({
        rel: stringValue(link.rel, 'related'),
        event_id: typeof link.event_id === 'string' ? link.event_id : undefined,
        span_id: typeof link.span_id === 'string' ? link.span_id : undefined,
      })),
    };
  });

  const rawMetrics = Array.isArray(rawBundle.metrics) ? rawBundle.metrics : [];
  if (rawMetrics.length > MAX_METRICS) {
    throw new Error(`指标数量超过 ${MAX_METRICS} 个，浏览器版 Viewer 暂不加载。`);
  }
  const metricPointCount = rawMetrics.filter(isRecord).reduce((total, metric) => (
    total
    + (Array.isArray(metric.points) ? metric.points.length : 0)
    + (Array.isArray(metric.series) ? metric.series.length : 0)
  ), 0);
  if (metricPointCount > MAX_METRIC_POINTS) {
    throw new Error('指标数据点合计超过 200,000 个，浏览器版 Viewer 暂不加载。');
  }
  const metrics = rawMetrics.filter(isRecord).map((metric) => {
    const points = Array.isArray(metric.points) ? metric.points : [];
    const series = Array.isArray(metric.series) ? metric.series : [];
    return {
      name: stringValue(metric.name, 'unknown.metric'),
      label: typeof metric.label === 'string' ? metric.label : undefined,
      unit: stringValue(metric.unit, 'value'),
      series: (
        series.length > 0
          ? series.filter(isRecord).map((point) => ({
            ts: numberValue(point.ts),
            value: numberValue(point.value),
          }))
          : points
            .filter((point): point is unknown[] => Array.isArray(point) && point.length >= 2)
            .map((point) => ({
              ts: numberValue(point[0]),
              value: numberValue(point[1]),
            }))
      ),
    };
  });

  const droppedEvents = numberValue(rawManifest.dropped_events);
  const captureStart = numberValue(rawManifest.capture_start_ms);
  const captureEnd = numberValue(rawManifest.capture_end_ms);
  const roomPopulation = isRecord(rawManifest.room_population) ? rawManifest.room_population : {};
  const incidentId = stringValue(rawIncident.id, stringValue(rawManifest.incident_id, 'incident_unknown'));
  const runId = stringValue(rawManifest.run_id, 'run_unknown');
  const expectedInterval = numberValue(
    rawIncident.expected_detection_interval_ms,
    numberValue(rawConfiguration.room_check_interval_ms),
  );
  const seenEventIds = new Set<string>();
  const duplicateEventIds: string[] = [];
  normalizedEvents.forEach((event) => {
    if (seenEventIds.has(event.id) && duplicateEventIds.length < 3) {
      duplicateEventIds.push(event.id);
    }
    seenEventIds.add(event.id);
  });
  if (duplicateEventIds.length > 0) {
    throw new Error(`数据包包含重复事件 ID：${duplicateEventIds.slice(0, 3).join('、')}`);
  }
  const explicitCompleteness = stringValue(rawManifest.completeness);
  const traceLoss = normalizedEvents.some((event) => event.name === 'trace.loss');
  const targetRoomId = stringValue(
    rawIncident.target_room_id,
    stringValue(rawIncident.room_scope_id, stringValue(rawIncident.anchor_entity_id)),
  );

  return {
    schema,
    manifest: {
      bundle_id: stringValue(rawManifest.bundle_id, 'bundle_unknown'),
      schema_version: stringValue(rawManifest.schema_version, schema),
      generated_at: stringValue(rawManifest.generated_at, stringValue(rawManifest.time_origin, new Date(0).toISOString())),
      app_version: typeof rawManifest.app_version === 'string' ? rawManifest.app_version : undefined,
      commit: typeof rawManifest.commit === 'string' ? rawManifest.commit : undefined,
      go_version: typeof rawManifest.go_version === 'string' ? rawManifest.go_version : undefined,
      platform: typeof rawManifest.platform === 'string'
        ? rawManifest.platform
        : [rawManifest.os, rawManifest.arch].filter((value) => typeof value === 'string').join('/'),
      run_id: runId,
      synthetic: rawManifest.synthetic === true,
      completeness: (
        droppedEvents > 0 || traceLoss || explicitCompleteness === 'partial'
          ? 'partial'
          : explicitCompleteness === 'unknown' ? 'unknown' : 'complete'
      ),
      dropped_events: droppedEvents,
      actual_window_ms: captureEnd > captureStart ? captureEnd - captureStart : undefined,
      source_files: Array.isArray(rawManifest.source_files)
        ? rawManifest.source_files.filter((item): item is string => typeof item === 'string')
        : undefined,
      sequence_scope: typeof rawManifest.sequence_scope === 'string'
        ? rawManifest.sequence_scope
        : undefined,
      room_population: Object.keys(roomPopulation).length > 0 ? {
        configured: numericValue(roomPopulation.configured),
        started: numericValue(roomPopulation.started),
        fully_traced: numericValue(roomPopulation.fully_traced),
        representative: numericValue(roomPopulation.representative),
        aggregate_only: numericValue(roomPopulation.aggregate_only),
      } : undefined,
    },
    incident: {
      id: incidentId,
      title: stringValue(rawIncident.title, stringValue(rawManifest.title, '未命名诊断事件')),
      summary: stringValue(rawIncident.summary, stringValue(rawIncident.user_report)),
      severity: (
        ['info', 'warning', 'error'].includes(stringValue(rawIncident.severity))
          ? stringValue(rawIncident.severity)
          : 'warning'
      ) as DiagnosticBundle['incident']['severity'],
      trigger: stringValue(rawIncident.trigger, stringValue(rawIncident.type, 'manual')),
      room_id: targetRoomId || undefined,
      room_label: typeof rawIncident.room_label === 'string' ? rawIncident.room_label : undefined,
      target_room_id: targetRoomId || undefined,
      target_generation: numericValue(rawIncident.target_generation),
      initial_generation: numericValue(rawIncident.initial_generation),
      configured_room_count: numericValue(rawIncident.configured_room_count),
      anchor_start_event_id: typeof rawIncident.anchor_start_event_id === 'string'
        ? rawIncident.anchor_start_event_id
        : undefined,
      focus_start_event_id: typeof rawIncident.focus_start_event_id === 'string'
        ? rawIncident.focus_start_event_id
        : undefined,
      goal_event_id: typeof rawIncident.goal_event_id === 'string'
        ? rawIncident.goal_event_id
        : undefined,
      trigger_event_id: typeof rawIncident.trigger_event_id === 'string'
        ? rawIncident.trigger_event_id
        : undefined,
      observed_monitor_to_first_byte_ms: numericValue(rawIncident.observed_monitor_to_first_byte_ms),
      observed_resume_to_first_byte_ms: numericValue(rawIncident.observed_resume_to_first_byte_ms),
      expected_detection_interval_ms: expectedInterval,
      started_at: typeof rawIncident.started_at === 'string' ? rawIncident.started_at : undefined,
      ended_at: typeof rawIncident.ended_at === 'string' ? rawIncident.ended_at : undefined,
      tags: Array.isArray(rawIncident.tags)
        ? rawIncident.tags.filter((item): item is string => typeof item === 'string')
        : undefined,
    },
    entities: Array.isArray(rawBundle.entities)
      ? (() => {
        if (rawBundle.entities.length > MAX_ENTITIES) {
          throw new Error(`实体数量超过 ${MAX_ENTITIES} 个，浏览器版 Viewer 暂不加载。`);
        }
        return rawBundle.entities.filter(isRecord);
      })()
      : undefined,
    configuration: rawConfiguration,
    events: normalizedEvents.sort((a, b) => a.ts - b.ts || (a.seq || 0) - (b.seq || 0)),
    metrics,
    runtime_slices: runtimeSlices,
  };
};
