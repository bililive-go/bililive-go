import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Collapse,
  Descriptions,
  Drawer,
  Empty,
  Grid,
  Row,
  Select,
  Space,
  Spin,
  Statistic,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import {
  AimOutlined,
  BugOutlined,
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloudUploadOutlined,
  CodeOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  ExperimentOutlined,
  FileSearchOutlined,
  HistoryOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { useSearchParams } from 'react-router-dom';
import {
  analyzeBundle,
  buildTimelineItems,
  configuredRoomCount,
  diagnosticLifecycle,
  eventGeneration,
  formatDuration,
  parseDiagnosticBundle,
  scopedDiagnosticEvents,
} from './analysis';
import ConcurrencyOverview from './ConcurrencyOverview';
import Metrics from './Metrics';
import Timeline from './Timeline';
import {
  DiagnosticAnalysis,
  DiagnosticBundle,
  DiagnosticEvent,
  DiagnosticEvidence,
  DiagnosticPhase,
} from './types';
import {
  diagnosticDownloadURL,
  diagnosticFlightRecorderURL,
  diagnosticLogsDownloadURL,
  DiagnosticRunInfo,
  getDiagnosticViewerText,
  listDiagnosticRuns,
  preferredDiagnosticRun,
  snapshotDiagnosticRun,
} from './api';
import { readDiagnosticFile } from './zip';
import './index.css';

const { Title, Text, Paragraph } = Typography;

interface SampleDefinition {
  key: string;
  label: string;
  shortLabel: string;
  description: string;
  file: string;
}

const SAMPLE_CATALOG: SampleDefinition[] = [
  {
    key: 'complex-100-rooms-manual-restart',
    label: '复杂场景：100 房间启动 + 手动停启 + 多线程追踪',
    shortLabel: '100 房间并发与 gen2',
    description: '100 个同平台房间启动竞争共享限流器；手动恢复后的 gen2 仍等待 50 秒，并保留旧 gen1 迟到事件。',
    file: 'complex-100-rooms-manual-restart.json',
  },
  {
    key: 'slow-ffmpeg-ready',
    label: '50 秒延迟：等待 FFmpeg 就绪',
    shortLabel: 'FFmpeg 初始化',
    description: '检测仅 0.38 秒；启动期下载和校验 FFmpeg 占 45.1 秒。',
    file: 'slow-ffmpeg-ready.json',
  },
  {
    key: 'slow-live-api-rate-limit',
    label: '50 秒延迟：平台限流排队',
    shortLabel: '检测阶段限流',
    description: '首次直播状态请求在限流器前排队，检测阶段本身超过 20 秒。',
    file: 'slow-live-api-rate-limit.json',
  },
  {
    key: 'slow-upstream-first-byte',
    label: '50 秒延迟：候选流超时与回退',
    shortLabel: '上游线路回退',
    description: '直播识别很快，但两个候选流超时和退避拖慢首字节。',
    file: 'slow-upstream-first-byte.json',
  },
];

const publicAsset = (path: string): string => {
  const base = process.env.PUBLIC_URL || '';
  return `${base}/${path}`.replace(/([^:]\/)\/+/g, '$1');
};

const evidenceMeta: Record<DiagnosticEvidence['kind'], { label: string; icon: React.ReactNode }> = {
  fact: { label: '事实', icon: <CheckCircleOutlined /> },
  runtime: { label: '运行时佐证', icon: <DatabaseOutlined /> },
  inference: { label: '推断', icon: <AimOutlined /> },
  counter: { label: '排除项', icon: <SafetyCertificateOutlined /> },
  missing: { label: '证据不足', icon: <WarningOutlined /> },
};

const confidenceLabel: Record<DiagnosticAnalysis['finding']['confidence'], string> = {
  high: '高置信度',
  medium: '中等置信度',
  low: '低置信度',
};

const phaseStatusLabel: Record<DiagnosticPhase['status'], string> = {
  normal: '正常',
  warning: '偏慢',
  critical: '主要延迟',
  neutral: '信息',
};

const attributeText = (value: unknown): string => {
  if (value === null) return 'null';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'number') return String(value);
  if (typeof value === 'string') return value;
  return JSON.stringify(value);
};

interface JSONPreviewBudget {
  nodes: number;
  stringCharacters: number;
  truncated: boolean;
}

const boundedJSONValue = (
  value: unknown,
  budget: JSONPreviewBudget,
  depth = 0,
): unknown => {
  if (budget.nodes <= 0 || budget.stringCharacters <= 0) {
    budget.truncated = true;
    return '…（预览预算已用完）';
  }
  budget.nodes -= 1;
  if (typeof value === 'undefined') return undefined;
  if (value === null || typeof value === 'number' || typeof value === 'boolean') return value;
  if (typeof value === 'string') {
    const available = Math.min(8192, budget.stringCharacters);
    budget.stringCharacters -= Math.min(value.length, available);
    if (value.length > available) {
      budget.truncated = true;
      return `${value.slice(0, available)}…`;
    }
    return value;
  }
  if (depth >= 8) {
    budget.truncated = true;
    return '…（嵌套层级超过 8）';
  }
  if (Array.isArray(value)) {
    const count = Math.min(value.length, 300);
    if (value.length > count) budget.truncated = true;
    const result = value.slice(0, count).map((item) => (
      boundedJSONValue(item, budget, depth + 1)
    ));
    if (value.length > count) result.push(`…（另有 ${value.length - count} 项）`);
    return result;
  }
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>);
    const count = Math.min(entries.length, 150);
    if (entries.length > count) budget.truncated = true;
    return entries.slice(0, count).reduce<Record<string, unknown>>((result, [key, item]) => {
      result[key] = boundedJSONValue(item, budget, depth + 1);
      return result;
    }, entries.length > count ? { __more_keys__: `另有 ${entries.length - count} 个字段` } : {});
  }
  budget.truncated = true;
  return String(value);
};

const RawBundlePreview: React.FC<{ bundle: DiagnosticBundle }> = ({ bundle }) => {
  const preview = useMemo(() => {
    const budget: JSONPreviewBudget = {
      nodes: 6000,
      stringCharacters: 512 * 1024,
      truncated: false,
    };
    const value = boundedJSONValue(bundle, budget);
    return {
      text: JSON.stringify(value, null, 2),
      truncated: budget.truncated,
    };
  }, [bundle]);
  return (
    <>
      <Alert
        type={preview.truncated ? 'warning' : 'info'}
        showIcon
        message={preview.truncated ? '为保护手机内存，这里只显示有界预览' : '规范化后的调查数据'}
        description="完整原始证据仍保留在下载的调查包中；预览最多展开 6,000 个节点、每个数组 300 项。"
      />
      <pre className="diagnostic-raw-json">{preview.text}</pre>
    </>
  );
};

const EventDetail: React.FC<{ event?: DiagnosticEvent }> = ({ event }) => {
  if (!event) {
    return <Empty description="点击阶段、证据或时间线事件查看详情" />;
  }
  return (
    <div className="diagnostic-event-detail" data-testid="event-detail">
      <Space wrap className="diagnostic-event-tags">
        <Tag color="blue">事实事件</Tag>
        <Tag>{event.component}</Tag>
        <Tag color={event.severity === 'error' ? 'red' : event.severity === 'warn' ? 'orange' : 'default'}>
          {event.severity}
        </Tag>
      </Space>
      <Title level={5}>{event.name}</Title>
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label="事件 ID">{event.id}</Descriptions.Item>
        <Descriptions.Item label="Writer 观察序号（非执行顺序）">
          {event.global_seq ?? event.seq ?? '—'}
        </Descriptions.Item>
        <Descriptions.Item label="相对时间">+{formatDuration(event.ts)}</Descriptions.Item>
        <Descriptions.Item label="持续时间">
          {typeof event.duration_ms === 'number' ? formatDuration(event.duration_ms) : '由配对 span 计算'}
        </Descriptions.Item>
        <Descriptions.Item label="span / flow">
          {event.span_id || '—'} / {event.flow_id || '—'}
        </Descriptions.Item>
        <Descriptions.Item label="room / generation">
          {event.room_id || event.attrs?.room_scope_id || '—'} / {eventGeneration(event) ?? '—'}
        </Descriptions.Item>
        <Descriptions.Item label="task / goroutine">
          {event.task_id || event.trace_task_id || event.attrs?.task_id || '—'} /{' '}
          {event.goroutine_id || event.attrs?.goroutine_id || '—'}
        </Descriptions.Item>
        <Descriptions.Item label="dispatch / handler">
          {event.dispatch_id || event.attrs?.dispatch_id || '—'} /{' '}
          {event.handler_id || event.attrs?.handler_id || '—'}
        </Descriptions.Item>
        <Descriptions.Item label="状态">{event.status || '—'}</Descriptions.Item>
      </Descriptions>
      {event.links && event.links.length > 0 && (
        <div className="diagnostic-attribute-list">
          <Text strong>显式因果边</Text>
          {event.links.map((link, index) => (
            <div className="diagnostic-attribute-row" key={`${link.rel}-${link.event_id || link.span_id}-${index}`}>
              <code>{link.rel}</code>
              <span>{link.event_id || link.span_id || '—'}</span>
            </div>
          ))}
        </div>
      )}
      {event.attrs && Object.keys(event.attrs).length > 0 && (
        <div className="diagnostic-attribute-list">
          <Text strong>白名单属性</Text>
          {Object.entries(event.attrs).map(([key, value]) => (
            <div className="diagnostic-attribute-row" key={key}>
              <code>{key}</code>
              <span>{attributeText(value)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

const PhaseWaterfall: React.FC<{
  phases: DiagnosticPhase[];
  totalMs: number;
  referenceMs: number;
  selectedEventId?: string;
  onSelect: (eventId?: string) => void;
}> = ({ phases, totalMs, referenceMs, selectedEventId, onSelect }) => {
  const referencePercent = Math.max(0, Math.min(100, (referenceMs / Math.max(totalMs, 1)) * 100));
  return (
  <div className="diagnostic-waterfall" data-testid="phase-waterfall">
    {phases.map((item) => {
      const left = Math.max(0, Math.min(100, (item.startMs / Math.max(totalMs, 1)) * 100));
      const width = Math.max(0.8, Math.min(100 - left, (item.durationMs / Math.max(totalMs, 1)) * 100));
      const active = item.eventIds.includes(selectedEventId || '');
      return (
        <button
          type="button"
          key={item.key}
          className={`diagnostic-phase-row ${active ? 'diagnostic-phase-row-active' : ''}`}
          onClick={() => onSelect(item.eventIds[0])}
        >
          <span className="diagnostic-phase-name">
            <strong>{item.label}</strong>
            <small>{item.detail}</small>
          </span>
          <span className="diagnostic-phase-track">
            <i
              className={`diagnostic-phase-bar diagnostic-phase-bar-${item.status}`}
              style={{ left: `${left}%`, width: `${width}%` }}
            />
            <i className="diagnostic-phase-reference" style={{ left: `${referencePercent}%` }} />
          </span>
          <span className="diagnostic-phase-value">
            <b>{formatDuration(item.durationMs)}</b>
            <small className={`phase-status-${item.status}`}>{phaseStatusLabel[item.status]}</small>
          </span>
        </button>
      );
    })}
    <div className="diagnostic-waterfall-reference">
      <span style={{ left: `${referencePercent}%` }}>
        {(referenceMs / 1000).toFixed(0)}s 检测配置参考
      </span>
      <span className="diagnostic-waterfall-end">
        {(totalMs / 1000).toFixed(0)}s FLV 首字节
      </span>
    </div>
  </div>
  );
};

const EvidenceList: React.FC<{
  evidence: DiagnosticEvidence[];
  onSelect: (eventId?: string) => void;
}> = ({ evidence, onSelect }) => (
  <div className="diagnostic-evidence-list">
    {evidence.map((item, index) => {
      const meta = evidenceMeta[item.kind];
      return (
        <button
          type="button"
          key={`${item.kind}-${index}`}
          className={`diagnostic-evidence diagnostic-evidence-${item.kind}`}
          onClick={() => onSelect(item.eventId)}
          disabled={!item.eventId}
        >
          <span className="diagnostic-evidence-kind">{meta.icon}{meta.label}</span>
          <strong>{item.title}</strong>
          <span>{item.detail}</span>
        </button>
      );
    })}
  </div>
);

const DiagnosticViewer: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedSample = searchParams.get('sample');
  const requestedRun = searchParams.get('run');
  const requestedSource = searchParams.get('source');
  const initialSample = SAMPLE_CATALOG.some((sample) => sample.key === requestedSample)
    ? requestedSample as string
    : SAMPLE_CATALOG[0].key;
  const [bundle, setBundle] = useState<DiagnosticBundle | null>(null);
  const [sourceName, setSourceName] = useState('');
  const [sampleKey, setSampleKey] = useState(initialSample);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedEvent, setSelectedEvent] = useState<DiagnosticEvent | undefined>();
  const [detailOpen, setDetailOpen] = useState(false);
  const [localRuns, setLocalRuns] = useState<DiagnosticRunInfo[]>([]);
  const [localRunsAvailable, setLocalRunsAvailable] = useState(false);
  const [selectedRunID, setSelectedRunID] = useState(requestedRun || '');
  const [snapshotting, setSnapshotting] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const loadedSourceRef = useRef<string>();
  const screens = Grid.useBreakpoint();
  const isDesktop = Boolean(screens.md);

  const analysisResult = useMemo(() => {
    if (!bundle) return { analysis: null, error: null };
    try {
      return { analysis: analyzeBundle(bundle), error: null };
    } catch (cause) {
      return {
        analysis: null,
        error: cause instanceof Error ? cause.message : String(cause),
      };
    }
  }, [bundle]);
  const analysis = analysisResult.analysis;

  const timelineItems = useMemo(() => (
    bundle ? buildTimelineItems(bundle).filter((item) => (
      !analysis || (item.endMs >= analysis.processStartMs && item.startMs <= analysis.firstByteAtMs)
    )) : []
  ), [analysis, bundle]);

  const scopedEvents = useMemo(() => (
    bundle ? scopedDiagnosticEvents(bundle) : []
  ), [bundle]);

  const loadText = (text: string, name: string) => {
    const parsed = parseDiagnosticBundle(text);
    let parsedAnalysis: DiagnosticAnalysis | null = null;
    try {
      parsedAnalysis = analyzeBundle(parsed);
    } catch {
      // 异常退出包可能在任何生命周期点停止，缺少首文件并不等于数据包无效。
    }
    const root = parsedAnalysis?.phases.find((item) => item.key === parsedAnalysis?.finding.rootPhaseKey);
    const initialEvent = parsed.events.find((event) => root?.eventIds.includes(event.id))
      || parsed.events.find((event) => (
        event.name === 'segment.first_nonzero_observed' || event.name === 'segment.first_byte'
      ))
      || [...parsed.events].reverse().find((event) => event.severity === 'error' || event.name.includes('panic'))
      || parsed.events[parsed.events.length - 1];
    setBundle(parsed);
    setSourceName(name);
    setSelectedEvent(initialEvent);
    setDetailOpen(false);
    setError(null);
  };

  const loadSample = async (key: string) => {
    const sample = SAMPLE_CATALOG.find((item) => item.key === key) || SAMPLE_CATALOG[0];
    setLoading(true);
    setError(null);
    setSampleKey(sample.key);
    loadedSourceRef.current = `sample:${sample.key}`;
    setSearchParams({ sample: sample.key }, { replace: true });
    try {
      const response = await fetch(publicAsset(`diagnostic-samples/${sample.file}`), { cache: 'no-store' });
      if (!response.ok) {
        throw new Error(`读取示例失败：HTTP ${response.status}`);
      }
      loadText(await response.text(), sample.file);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoading(false);
    }
  };

  const refreshLocalRuns = async (): Promise<DiagnosticRunInfo[]> => {
    try {
      const runs = await listDiagnosticRuns();
      setLocalRuns(runs);
      setLocalRunsAvailable(true);
      setSelectedRunID((current) => (
        current && runs.some((run) => run.run_id === current)
          ? current
          : preferredDiagnosticRun(runs)?.run_id || ''
      ));
      return runs;
    } catch {
      // 静态演示服务器或旧后端没有该 API 时，只展示内置示例。
      setLocalRunsAvailable(false);
      return [];
    }
  };

  const loadServerRun = async (runID: string) => {
    if (!runID) return;
    setLoading(true);
    setError(null);
    setSelectedRunID(runID);
    loadedSourceRef.current = `run:${runID}`;
    setSearchParams({ run: runID }, { replace: true });
    try {
      loadText(await getDiagnosticViewerText(runID), `${runID}.bgo-diag`);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    const selectInitialSource = async () => {
      if (requestedSource === 'local') {
        refreshLocalRuns();
        setLoading(false);
        if (!loadedSourceRef.current?.startsWith('file:')) {
          setError('本地文件不会被上传或写入 URL；刷新页面后请重新选择调查包。');
        }
        return;
      }

      if (requestedRun) {
        refreshLocalRuns();
        if (loadedSourceRef.current !== `run:${requestedRun}`) {
          await loadServerRun(requestedRun);
        }
        return;
      }

      const explicitSample = SAMPLE_CATALOG.find((sample) => sample.key === requestedSample);
      if (explicitSample) {
        refreshLocalRuns();
        if (loadedSourceRef.current !== `sample:${explicitSample.key}`) {
          await loadSample(explicitSample.key);
        }
        return;
      }

      // URL 没有显式来源时，必须先等待真实 bgo 运行列表。只有 API 不可用或
      // 后端确实没有任何 run 才退回合成示例，不能让两个请求竞态误导用户。
      setLoading(true);
      const runs = await refreshLocalRuns();
      if (cancelled) return;
      const preferred = preferredDiagnosticRun(runs);
      if (preferred) {
        await loadServerRun(preferred.run_id);
      } else {
        await loadSample(SAMPLE_CATALOG[0].key);
      }
    };
    selectInitialSource();
    return () => {
      cancelled = true;
    };
    // 初次载入及浏览器前进/后退时跟随 URL；加载函数会先写 loadedSourceRef，
    // 再更新 URL，从而避免重复请求。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [requestedRun, requestedSample, requestedSource]);

  const openEvent = (event?: DiagnosticEvent) => {
    if (!event) return;
    setSelectedEvent(event);
    if (!isDesktop) setDetailOpen(true);
  };

  const openEventById = (eventId?: string) => {
    if (!bundle || !eventId) return;
    openEvent(bundle.events.find((event) => event.id === eventId));
  };

  const handleFile = async (file: File) => {
    setLoading(true);
    try {
      loadText(await readDiagnosticFile(file), file.name);
      loadedSourceRef.current = `file:${file.name}`;
      setSearchParams({ source: 'local' }, { replace: true });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoading(false);
      if (inputRef.current) inputRef.current.value = '';
    }
  };

  const columns: ColumnsType<DiagnosticEvent> = [
    {
      title: '观察序号',
      key: 'global_seq',
      width: 92,
      render: (_, event) => `#${event.global_seq ?? event.seq ?? '—'}`,
      sorter: (a, b) => (a.global_seq ?? a.seq ?? 0) - (b.global_seq ?? b.seq ?? 0),
    },
    {
      title: '时间',
      dataIndex: 'ts',
      width: 105,
      render: (value: number) => `+${formatDuration(value)}`,
      sorter: (a, b) => a.ts - b.ts,
      defaultSortOrder: 'ascend',
    },
    {
      title: '事件',
      dataIndex: 'name',
      render: (value: string, event) => (
        <Button type="link" className="diagnostic-event-link" onClick={() => openEvent(event)}>
          {value}
        </Button>
      ),
    },
    {
      title: '代次',
      key: 'generation',
      width: 78,
      render: (_, event) => {
        const generation = eventGeneration(event);
        return generation !== undefined ? <Tag color={generation === analysis?.targetGeneration ? 'blue' : 'default'}>gen{generation}</Tag> : '—';
      },
    },
    {
      title: 'Task / G',
      key: 'task',
      width: 170,
      ellipsis: true,
      render: (_, event) => (
        event.task_id
        || event.trace_task_id
        || event.goroutine_id
        || event.attrs?.task_id
        || event.attrs?.goroutine_id
        || '—'
      ),
    },
    { title: '组件', dataIndex: 'component', width: 135 },
    {
      title: '级别',
      dataIndex: 'severity',
      width: 90,
      render: (value: DiagnosticEvent['severity']) => (
        <Tag color={value === 'error' ? 'red' : value === 'warn' ? 'orange' : value === 'debug' ? 'default' : 'blue'}>
          {value}
        </Tag>
      ),
    },
    { title: '状态', dataIndex: 'status', width: 100, render: (value?: string) => value || '—' },
  ];

  const selectedSample = SAMPLE_CATALOG.find((item) => item.key === sampleKey) || SAMPLE_CATALOG[0];
  const showingSample = Boolean(
    requestedSample && SAMPLE_CATALOG.some((sample) => sample.key === requestedSample),
  );
  const selectedRun = localRuns.find((run) => run.run_id === selectedRunID);
  const sourceActions = (
    <div className="diagnostic-toolbar-actions">
      <Select
        aria-label="选择演示数据包"
        value={showingSample ? sampleKey : undefined}
        placeholder="选择演示数据包"
        onChange={loadSample}
        options={SAMPLE_CATALOG.map((sample) => ({
          value: sample.key,
          label: sample.label,
        }))}
      />
      <Button
        type="primary"
        icon={<CloudUploadOutlined />}
        onClick={() => inputRef.current?.click()}
      >
        打开本地数据包
      </Button>
      <input
        ref={inputRef}
        type="file"
        accept=".json,.zip,.tar.gz,.tgz,.bgo-trace,.bgo-diag"
        hidden
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) handleFile(file);
        }}
      />
      {requestedRun ? (
        <Button
          icon={<DownloadOutlined />}
          href={diagnosticDownloadURL(requestedRun)}
          download={`${requestedRun}.bgo-diag.tar.gz`}
        >
          下载冻结调查包
        </Button>
      ) : requestedSource !== 'local' ? (
        <Button
          icon={<DownloadOutlined />}
          href={publicAsset(`diagnostic-samples/${selectedSample.file}`)}
          download={selectedSample.file}
        >
          下载当前示例
        </Button>
      ) : null}
    </div>
  );

  const localRunsPanel = localRunsAvailable ? (
    <Card
      size="small"
      className="diagnostic-local-runs"
      title={<span><HistoryOutlined /> 本机持久化运行现场</span>}
      extra={(
        <Button size="small" type="text" icon={<ReloadOutlined />} onClick={refreshLocalRuns}>
          刷新
        </Button>
      )}
    >
      <Space wrap>
        <Select
          className="diagnostic-run-select"
          value={selectedRunID || undefined}
          placeholder={localRuns.length > 0 ? '选择一次运行' : '暂无运行'}
          onChange={setSelectedRunID}
          options={localRuns.map((run) => ({
            value: run.run_id,
            label: `${run.current ? '当前 · ' : run.active ? '其他活跃进程 · ' : ''}${run.clean ? '正常结束 · ' : run.active ? '' : '疑似异常 · '}${run.run_id}`,
          }))}
        />
        <Button
          type="primary"
          disabled={!selectedRunID}
          onClick={() => loadServerRun(selectedRunID)}
        >
          在 Viewer 调查
        </Button>
        <Button
          loading={snapshotting}
          disabled={!selectedRunID || !selectedRun?.current}
          onClick={async () => {
            setSnapshotting(true);
            try {
              await snapshotDiagnosticRun(selectedRunID);
              await refreshLocalRuns();
            } catch (cause) {
              setError(cause instanceof Error ? cause.message : String(cause));
            } finally {
              setSnapshotting(false);
            }
          }}
        >
          冻结最新黑盒
        </Button>
        {selectedRunID && (
          <Button href={diagnosticDownloadURL(selectedRunID)} icon={<DownloadOutlined />}>
            下载调查包
          </Button>
        )}
        {selectedRunID && selectedRun?.flight_recorder_available && (
          <Button href={diagnosticFlightRecorderURL(selectedRunID)}>
            下载 Go Flight Recorder
          </Button>
        )}
        <Button href={diagnosticLogsDownloadURL()} icon={<DownloadOutlined />}>
          下载最近文本日志快照
        </Button>
        {selectedRun && (
          <>
            <Tag color={selectedRun.active ? 'blue' : selectedRun.clean ? 'green' : 'orange'}>
              {selectedRun.active ? (selectedRun.current ? '当前运行' : '其他进程仍活跃') : selectedRun.clean ? '已正常结束' : '未发现 clean marker'}
            </Tag>
            {selectedRun.acknowledged && <Tag>已确认</Tag>}
            {selectedRun.event_count !== undefined && <Text type="secondary">{selectedRun.event_count} 条业务事件</Text>}
          </>
        )}
      </Space>
    </Card>
  ) : null;

  if (loading && !bundle) {
    return <div className="diagnostic-loading"><Spin size="large" tip="正在载入合成诊断包…" /></div>;
  }

  if (!bundle) {
    return (
      <div className="diagnostic-viewer">
        <Alert type="error" showIcon message="无法载入诊断包" description={error || '未知错误'} />
        <Button onClick={() => loadSample(SAMPLE_CATALOG[0].key)}>重新载入示例</Button>
      </div>
    );
  }

  if (!analysis) {
    const errorEvents = bundle.events.filter((event) => (
      event.severity === 'error' || event.name.includes('panic') || event.name.includes('abnormal')
    ));
    const lastEvent = bundle.events[bundle.events.length - 1];
    const lastWallTime = lastEvent?.wall_time || bundle.incident.ended_at || '—';
    return (
      <div
        className="diagnostic-viewer"
        onDragOver={(event) => event.preventDefault()}
        onDrop={(event) => {
          event.preventDefault();
          const file = event.dataTransfer.files?.[0];
          if (file) handleFile(file);
        }}
        data-testid="diagnostic-viewer-generic"
      >
        <section className="diagnostic-toolbar">
          <div className="diagnostic-title-block">
            <div className="diagnostic-title-icon"><BugOutlined /></div>
            <div>
              <Title level={2}>异常退出现场调查</Title>
              <Paragraph>即使生命周期在首文件之前中断，也可以检查最后心跳、panic 与完整业务事件尾部。</Paragraph>
            </div>
          </div>
          {sourceActions}
        </section>
        {localRunsPanel}
        {error && <Alert type="error" showIcon message="最近一次操作失败" description={error} />}
        <Alert
          showIcon
          type="warning"
          message="这份轨迹没有形成完整的“开始监控 → 直播确认 → Recorder → 首文件”关键路径"
          description={analysisResult.error || '程序可能在关键里程碑之前退出。Viewer 不会拿其它房间的事件拼出一个看似完整的结论。'}
        />
        <Row gutter={[12, 12]} className="diagnostic-kpi-grid">
          <Col xs={12} lg={6}>
            <Card size="small"><Statistic title="已持久化业务事件" value={bundle.events.length} /></Card>
          </Col>
          <Col xs={12} lg={6}>
            <Card size="small"><Statistic title="错误 / panic 证据" value={errorEvents.length} /></Card>
          </Col>
          <Col xs={12} lg={6}>
            <Card size="small">
              <Statistic title="丢失事件" value={bundle.manifest.dropped_events || 0} />
            </Card>
          </Col>
          <Col xs={12} lg={6}>
            <Card size="small">
              <Statistic title="最后事件序号" value={lastEvent?.global_seq ?? lastEvent?.seq ?? 0} />
              <Text type="secondary">{lastWallTime}</Text>
            </Card>
          </Col>
        </Row>
        <Card
          title="运行状态与调查建议"
          className="diagnostic-waterfall-card"
        >
          <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
            <Descriptions.Item label="运行 ID">{bundle.manifest.run_id}</Descriptions.Item>
            <Descriptions.Item label="完整度">{bundle.manifest.completeness || 'unknown'}</Descriptions.Item>
            <Descriptions.Item label="事件类型">{bundle.incident.trigger}</Descriptions.Item>
            <Descriptions.Item label="现场标题">{bundle.incident.title}</Descriptions.Item>
          </Descriptions>
          <ol>
            <li>先按时间倒序检查 error、panic 和最后一个成功结束的 span。</li>
            <li>下载 Go Flight Recorder，用 <code>go tool trace</code> 检查退出前 goroutine、网络等待、锁竞争和调度延迟。</li>
            <li>“没有 clean marker”只表示疑似未正常结束；同时检查容器 OOM、宿主机关机和强制停止记录。</li>
          </ol>
        </Card>
        <Tabs
          className="diagnostic-tabs"
          defaultActiveKey={errorEvents.length > 0 ? 'errors' : 'events'}
          items={[
            {
              key: 'errors',
              label: `异常证据 (${errorEvents.length})`,
              children: errorEvents.length > 0 ? (
                <Table
                  rowKey="id"
                  size="small"
                  columns={columns}
                  dataSource={errorEvents}
                  pagination={{ pageSize: 12, showSizeChanger: false }}
                  scroll={{ x: 1050 }}
                  onRow={(event) => ({ onClick: () => openEvent(event) })}
                />
              ) : <Empty description="没有捕获到显式 panic；请结合最后事件与 Flight Recorder 调查 SIGKILL/OOM" />,
            },
            {
              key: 'events',
              label: `全部事件 (${bundle.events.length})`,
              children: (
                <Table
                  rowKey="id"
                  size="small"
                  columns={columns}
                  dataSource={[...bundle.events].reverse()}
                  pagination={{ pageSize: 20, showSizeChanger: false }}
                  scroll={{ x: 1050 }}
                  onRow={(event) => ({ onClick: () => openEvent(event) })}
                />
              ),
            },
            {
              key: 'raw',
              label: '原始数据包',
              children: <RawBundlePreview bundle={bundle} />,
            },
          ]}
        />
        {isDesktop && <div className="diagnostic-desktop-detail"><EventDetail event={selectedEvent} /></div>}
        {!isDesktop && (
          <Drawer
            title="事件证据详情"
            placement="bottom"
            height="78dvh"
            open={detailOpen}
            onClose={() => setDetailOpen(false)}
          >
            <EventDetail event={selectedEvent} />
          </Drawer>
        )}
      </div>
    );
  }

  const liveObservations = scopedEvents.filter((event) => (
    ['listener.poll.end', 'live.refresh.end'].includes(event.name)
    && event.attrs?.live === true
    && (
      analysis.targetGeneration === undefined
      || eventGeneration(event) === undefined
      || eventGeneration(event) === analysis.targetGeneration
    )
    && event.disposition !== 'dropped'
  )).length;
  const configuredRooms = configuredRoomCount(bundle);
  const lifecycle = diagnosticLifecycle(bundle, {
    roomId: analysis.targetRoomId,
    generation: analysis.targetGeneration,
    startEventId: bundle.incident.focus_start_event_id || bundle.incident.anchor_start_event_id,
  });
  const generations = Array.from(new Set(
    scopedEvents
      .map(eventGeneration)
      .filter((generation): generation is number => generation !== undefined),
  )).sort((a, b) => a - b);
  const isConcurrencyBundle = configuredRooms > 1 || generations.length > 1 || (bundle.runtime_slices?.length || 0) > 0;
  const synthetic = bundle.manifest.synthetic === true;
  const confidence = confidenceLabel[analysis.finding.confidence];

  const detailCard = (
    <Card
      className="diagnostic-detail-card"
      title={<span><FileSearchOutlined /> 选中事件</span>}
      size="small"
    >
      <EventDetail event={selectedEvent} />
    </Card>
  );

  return (
    <div
      className="diagnostic-viewer"
      onDragOver={(event) => event.preventDefault()}
      onDrop={(event) => {
        event.preventDefault();
        const file = event.dataTransfer.files?.[0];
        if (file) handleFile(file);
      }}
      data-testid="diagnostic-viewer"
    >
      <section className="diagnostic-toolbar">
        <div className="diagnostic-title-block">
          <div className="diagnostic-title-icon"><BugOutlined /></div>
          <div>
            <Title level={2}>诊断轨迹分析</Title>
            <Paragraph>把用户发来的诊断包留在当前浏览器中，自动还原“50 秒花在哪里”。</Paragraph>
          </div>
        </div>
        {sourceActions}
      </section>

      {localRunsPanel}

      <div className="diagnostic-local-notice">
        <SafetyCertificateOutlined />
        <span><b>仅在浏览器本地解析</b>：选择的文件不会上传到 bililive-go 或任何远程服务器。</span>
        <span className="diagnostic-drop-hint">也可以把 JSON 包拖到本页</span>
      </div>

      {error && (
        <Alert
          type="error"
          showIcon
          closable
          message="最近一次操作失败"
          description={error}
          onClose={() => setError(null)}
        />
      )}

      <section className="diagnostic-bundle-strip">
        <Space wrap>
          <Text strong>{sourceName}</Text>
          {synthetic && <Tag color="purple" icon={<ExperimentOutlined />}>合成示例</Tag>}
          <Tag color={analysis.completeness === 'complete' ? 'green' : 'orange'}>
            轨迹{analysis.completeness === 'complete' ? '完整' : '不完整'}
          </Tag>
          {configuredRooms > 1 && <Tag color="geekblue">{configuredRooms} 个房间并发启动</Tag>}
          {analysis.targetRoomId && <Tag color="blue">目标 {analysis.targetRoomId}</Tag>}
          {analysis.targetGeneration !== undefined && (
            <Tag color="purple">分析 generation {analysis.targetGeneration}</Tag>
          )}
          {generations.length > 1 && <Tag>上下文含 gen {generations.join(' / ')}</Tag>}
          {liveObservations > 1 && <Tag color="cyan">窗口内 {liveObservations} 次观测均为 live</Tag>}
          <Text type="secondary">run {bundle.manifest.run_id}</Text>
        </Space>
      </section>

      <section className="diagnostic-hero" data-testid="diagnosis-hero">
        <div className="diagnostic-hero-main">
          <span className="diagnostic-eyebrow">自动根因分析 · {confidence}</span>
          <Title level={3}>{analysis.finding.title}</Title>
          <Paragraph className="diagnostic-finding-summary">{analysis.finding.summary}</Paragraph>
          <Alert
            type={analysis.intervalWithinExpectation ? 'info' : 'warning'}
            showIcon
            message={analysis.intervalWithinExpectation
              ? `${(analysis.configuredIntervalMs / 1000).toFixed(0)} 秒是直播状态检测的调度配置，不是“开始监控到写出文件”的端到端 SLA。`
              : `目标 generation 的状态确认已经超过 ${(analysis.configuredIntervalMs / 1000).toFixed(0)} 秒，应先排查共享限流、锁竞争和平台请求。`}
          />
        </div>
        <div className="diagnostic-total-latency">
          <span>
            {lifecycle.label}
            {' '}→ FLV 首字节
          </span>
          <b>{(analysis.totalMs / 1000).toFixed(2)}</b>
          <small>秒</small>
          {analysis.processToFirstByteMs !== analysis.totalMs && (
            <em>进程启动口径：{formatDuration(analysis.processToFirstByteMs)}</em>
          )}
        </div>
      </section>

      <Row gutter={[12, 12]} className="diagnostic-kpi-grid">
        <Col xs={12} lg={6}>
          <Card
            size="small"
            className={`diagnostic-kpi ${analysis.intervalWithinExpectation ? 'diagnostic-kpi-good' : 'diagnostic-kpi-bad'}`}
          >
            <Statistic
              title="直播识别"
              value={analysis.detectionMs / 1000}
              precision={2}
              suffix="秒"
              prefix={analysis.intervalWithinExpectation ? <CheckCircleOutlined /> : <WarningOutlined />}
            />
            <Text type="secondary">目标 gen{analysis.targetGeneration ?? '—'} · 配置参考 {(analysis.configuredIntervalMs / 1000).toFixed(0)} 秒</Text>
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card size="small" className="diagnostic-kpi diagnostic-kpi-good">
            <Statistic
              title="Live → Recorder"
              value={(analysis.recorderStartedAtMs - analysis.firstLiveAtMs) / 1000}
              precision={3}
              suffix="秒"
              prefix={<CheckCircleOutlined />}
            />
            <Text type="secondary">事件派发没有形成主要等待</Text>
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card
            size="small"
            className={`diagnostic-kpi ${analysis.finding.rootPhaseKey === 'detection' ? '' : 'diagnostic-kpi-bad'}`}
          >
            <Statistic
              title="录制准备"
              value={analysis.recordingPreparationMs / 1000}
              precision={2}
              suffix="秒"
              prefix={<ClockCircleOutlined />}
            />
            <Text type="secondary">
              {analysis.finding.rootPhaseKey === 'detection' ? '不是本次主要延迟' : '根因位于检测之后'}
            </Text>
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card size="small" className="diagnostic-kpi">
            <Statistic
              title="轨迹完整度"
              value={bundle.manifest.dropped_events || 0}
              suffix="条丢失"
              prefix={analysis.completeness === 'complete' ? <CheckCircleOutlined /> : <WarningOutlined />}
            />
            <Text type="secondary">{confidence}</Text>
          </Card>
        </Col>
      </Row>

      <div className="diagnostic-analysis-grid">
        <Card
          className="diagnostic-waterfall-card"
          title={<span><ClockCircleOutlined /> 关键路径瀑布图</span>}
          extra={<Tag color="blue">配置参考：{(analysis.configuredIntervalMs / 1000).toFixed(0)}s</Tag>}
        >
          <PhaseWaterfall
            phases={analysis.phases}
            totalMs={analysis.totalMs}
            referenceMs={analysis.configuredIntervalMs}
            selectedEventId={selectedEvent?.id}
            onSelect={openEventById}
          />
        </Card>
        <Card
          className="diagnostic-evidence-card"
          title={<span><AimOutlined /> 结论依据</span>}
          extra={<Tag color={analysis.finding.confidence === 'high' ? 'green' : 'orange'}>{confidence}</Tag>}
        >
          <EvidenceList evidence={analysis.finding.evidence} onSelect={openEventById} />
          <div className="diagnostic-suggestions">
            <Text strong>下一步建议</Text>
            <ol>
              {analysis.finding.suggestions.map((suggestion) => <li key={suggestion}>{suggestion}</li>)}
            </ol>
          </div>
        </Card>
      </div>

      <Tabs
        className="diagnostic-tabs"
        defaultActiveKey={isConcurrencyBundle ? 'concurrency' : 'timeline'}
        items={[
          ...(isConcurrencyBundle ? [{
            key: 'concurrency',
            label: '多房间并发与因果',
            children: (
              <ConcurrencyOverview
                bundle={bundle}
                analysis={analysis}
                selectedEventId={selectedEvent?.id}
                onSelect={openEvent}
              />
            ),
          }] : []),
          {
            key: 'timeline',
            label: isConcurrencyBundle ? '目标房间业务时间线' : '业务时间线',
            children: (
              <>
                <Timeline
                  items={timelineItems}
                  totalMs={analysis.processToFirstByteMs}
                  windowStartMs={analysis.processStartMs}
                  referenceStartMs={analysis.windowStartMs}
                  configuredIntervalMs={analysis.configuredIntervalMs}
                  selectedEventId={selectedEvent?.id}
                  onSelect={openEvent}
                />
                {!isDesktop && (
                  <Alert
                    className="diagnostic-mobile-tip"
                    type="info"
                    showIcon
                    message={`点击时间线事件会从底部打开证据详情；可左右滑动查看完整 ${formatDuration(analysis.processToFirstByteMs)}。`}
                  />
                )}
              </>
            ),
          },
          {
            key: 'metrics',
            label: '指标与反证',
            children: (
              <Metrics
                metrics={bundle.metrics || []}
                configuredIntervalMs={analysis.configuredIntervalMs}
                totalMs={analysis.processToFirstByteMs}
                windowStartMs={analysis.processStartMs}
                referenceStartMs={analysis.windowStartMs}
              />
            ),
          },
          {
            key: 'events',
            label: `目标范围事件 (${scopedEvents.length}/${bundle.events.length})`,
            children: (
              <Table
                rowKey="id"
                size="small"
                columns={columns}
                dataSource={scopedEvents}
                pagination={{ pageSize: 12, showSizeChanger: false }}
                scroll={{ x: 1050 }}
                onRow={(event) => ({ onClick: () => openEvent(event) })}
              />
            ),
          },
          {
            key: 'bundle',
            label: '数据包内容',
            children: (
              <Collapse
                items={[
                  {
                    key: 'manifest',
                    label: 'Manifest 与环境',
                    children: (
                      <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
                        <Descriptions.Item label="bundle">{bundle.manifest.bundle_id}</Descriptions.Item>
                        <Descriptions.Item label="incident">{bundle.incident.id}</Descriptions.Item>
                        <Descriptions.Item label="应用">{bundle.manifest.app_version || '—'}</Descriptions.Item>
                        <Descriptions.Item label="Go">{bundle.manifest.go_version || '—'}</Descriptions.Item>
                        <Descriptions.Item label="平台">{bundle.manifest.platform || '—'}</Descriptions.Item>
                        <Descriptions.Item label="生成时间">{bundle.manifest.generated_at}</Descriptions.Item>
                      </Descriptions>
                    ),
                  },
                  {
                    key: 'raw',
                    label: <span><CodeOutlined /> 原始 JSON（只读）</span>,
                    children: <RawBundlePreview bundle={bundle} />,
                  },
                ]}
              />
            ),
          },
        ]}
      />

      {isDesktop && <div className="diagnostic-desktop-detail">{detailCard}</div>}
      {!isDesktop && (
        <Drawer
          title="事件证据详情"
          placement="bottom"
          height="78dvh"
          open={detailOpen}
          onClose={() => setDetailOpen(false)}
        >
          <EventDetail event={selectedEvent} />
        </Drawer>
      )}
    </div>
  );
};

export default DiagnosticViewer;
