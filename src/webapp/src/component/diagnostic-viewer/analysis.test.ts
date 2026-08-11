import {
  analyzeBundle,
  buildConcurrencySummary,
  configuredRoomCount,
  diagnosticLifecycle,
  parseDiagnosticBundle,
} from './analysis';
import { DiagnosticBundle, DiagnosticEvent } from './types';

const event = (
  id: string,
  ts: number,
  name: string,
  generation?: number,
  attrs: DiagnosticEvent['attrs'] = {},
  spanId?: string,
): DiagnosticEvent => ({
  id,
  ts,
  name,
  severity: 'info',
  category: 'business',
  component: name.startsWith('scheduler.') ? 'scheduler' : 'listener',
  room_id: generation === undefined ? undefined : 'room_target',
  generation,
  attrs,
  span_id: spanId,
});

const realStyleBundle = (
  generation: number,
  operation: 'add' | 'replace',
  includePriorStop: boolean,
): DiagnosticBundle => {
  const events: DiagnosticEvent[] = [
    event('process-start', 0, 'process.start'),
    ...(includePriorStop ? [
      event('close-requested', 1000, 'listener.close.requested', generation - 1, { status: 'running' }),
      event('close-accepted', 1010, 'listener.close.accepted', generation - 1, { status: 'accepted' }),
      event('remove-end', 1020, 'listener.manager.remove.end', generation - 1, {
        operation: 'remove',
        result: 'removed',
      }, 'span-remove'),
    ] : []),
    event('listener-start-requested', 2000, 'listener.start.requested', generation, { operation }),
    event('listener-start-accepted', 2005, 'listener.start.accepted', generation, {
      operation,
      status: 'accepted',
    }),
    event('monitor-started', 2010, 'monitor.started', generation, {
      operation,
      configured_interval_ms: 20000,
    }),
    event('limit-start', 2020, 'scheduler.rate_limit.wait.start', generation, {
      waiter_count_at_enter: 87,
    }, 'span-rate-limit'),
    event('limit-end', 50020, 'scheduler.rate_limit.wait.end', generation, {
      status: 'ok',
    }, 'span-rate-limit'),
    event('refresh-start', 50020, 'live.refresh.start', generation, {}, 'span-refresh'),
    event('refresh-end', 52010, 'live.refresh.end', generation, {
      status: 'ok',
      live: true,
    }, 'span-refresh'),
    event('poll-end', 52010, 'listener.poll.end', generation, {
      status: 'ok',
      live: true,
    }, 'span-poll'),
    event('recorder-start', 52050, 'recorder.session.start', generation, { status: 'started' }),
    event('first-nonzero', 52110, 'segment.first_nonzero_observed', generation, {
      size_bytes: 4096,
    }),
  ];

  return {
    schema: 'bililive.diagnostic-bundle/v1',
    manifest: {
      bundle_id: `bundle-real-${generation}-${operation}`,
      schema_version: 'bililive.diagnostic-bundle/v1',
      generated_at: '2026-07-30T00:00:00Z',
      run_id: 'run-real-events',
      completeness: 'complete',
      dropped_events: 0,
    },
    incident: {
      id: 'incident-real-events',
      title: '真实 Listener 事件命名测试',
      severity: 'warning',
      trigger: 'recording_start_slow',
      target_room_id: 'room_target',
      target_generation: generation,
      focus_start_event_id: 'monitor-started',
      anchor_start_event_id: 'monitor-started',
      goal_event_id: 'first-nonzero',
      expected_detection_interval_ms: 20000,
    },
    configuration: {
      configured_room_count: 100,
      detection_interval_s: 20,
    },
    events,
  };
};

describe('诊断 Viewer 的真实后端事件契约', () => {
  test('读取 configuration.configured_room_count，并把 gen2 add 表述为中性的重新启动', () => {
    const bundle = parseDiagnosticBundle(JSON.stringify(realStyleBundle(2, 'add', true)));
    const analysis = analyzeBundle(bundle);
    const lifecycle = diagnosticLifecycle(bundle);
    const concurrency = buildConcurrencySummary(bundle);

    expect(configuredRoomCount(bundle)).toBe(100);
    expect(concurrency.configuredRooms).toBe(100);
    expect(concurrency.startedRooms).toBe(1);
    expect(concurrency.stopAtMs).toBeDefined();
    expect(concurrency.resumeAtMs).toBe(2010);
    expect(analysis.detectionMs).toBe(50000);
    expect(analysis.totalMs).toBe(50100);
    expect(analysis.finding.code).toBe('live.shared_rate_limit.rejoin_after_restart');
    expect(lifecycle.kind).toBe('restart');
    expect(lifecycle.label).toBe('重新开始监控 gen2');
    expect(lifecycle.explicitUserAction).toBe(false);
    expect(analysis.finding.title).not.toContain('手动');
  });

  test('operation=replace 的 gen2 是 Listener 实例切换，不误报为用户恢复', () => {
    const bundle = parseDiagnosticBundle(JSON.stringify(realStyleBundle(2, 'replace', true)));
    const lifecycle = diagnosticLifecycle(bundle);

    expect(lifecycle.kind).toBe('replacement');
    expect(lifecycle.label).toBe('监听实例切换 gen2');
    expect(lifecycle.explicitUserAction).toBe(false);
  });

  test('gen1 add 被识别为初次开始监控', () => {
    const bundle = parseDiagnosticBundle(JSON.stringify(realStyleBundle(1, 'add', false)));
    const lifecycle = diagnosticLifecycle(bundle);

    expect(lifecycle.kind).toBe('initial');
    expect(lifecycle.label).toBe('开始监控 gen1');
    expect(lifecycle.explicitUserAction).toBe(false);
  });

  test('识别真实 scheduler.rate_limit.in_flight.wait 串行槽位，并选择最长限流 span', () => {
    const source = realStyleBundle(2, 'add', true);
    source.events = source.events.flatMap((item) => {
      if (item.id === 'limit-start') {
        return [{
          ...item,
          name: 'scheduler.rate_limit.in_flight.wait.start',
          attrs: {
            queue_kind: 'platform_request_serialization',
            in_flight_limit: 1,
          },
        }];
      }
      if (item.id === 'limit-end') {
        return [
          {
            ...item,
            name: 'scheduler.rate_limit.in_flight.wait.end',
          },
          event(
            'ordinary-limit-start',
            50021,
            'scheduler.rate_limit.wait.start',
            2,
            { waiter_count_at_enter: 4 },
            'span-ordinary-limit',
          ),
          event(
            'ordinary-limit-end',
            51021,
            'scheduler.rate_limit.wait.end',
            2,
            { total_wait_ms: 1000 },
            'span-ordinary-limit',
          ),
        ];
      }
      return [item];
    });
    source.metrics = [{
      name: 'platform.rate_limiter.in_flight_waiting_rooms',
      unit: 'goroutines',
      series: [{ ts: 2000, value: 99 }],
    }];

    const bundle = parseDiagnosticBundle(JSON.stringify(source));
    const analysis = analyzeBundle(bundle);
    const concurrency = buildConcurrencySummary(bundle);

    expect(analysis.finding.code).toBe('live.shared_rate_limit.rejoin_after_restart');
    expect(analysis.finding.summary).toContain('共享限流等待 48.00 秒');
    expect(analysis.finding.evidence.some((item) => (
      item.title.includes('scheduler.rate_limit.in_flight.wait 持续 48.00 秒')
      && item.detail.includes('同平台请求串行槽位')
    ))).toBe(true);
    expect(concurrency.targetQueueWaitMs).toBe(48000);
    expect(concurrency.queueDepthPeak).toBe(99);
  });

  test('拒绝让 Runtime 样本绕过 100,000 条事件上限', () => {
    const source = realStyleBundle(1, 'add', false) as DiagnosticBundle & {
      runtime_samples?: unknown[];
    };
    source.runtime_samples = new Array(100000).fill({});

    expect(() => parseDiagnosticBundle(JSON.stringify(source))).toThrow(
      '业务事件与 Runtime 样本合计超过 100,000 条',
    );
  });
});
