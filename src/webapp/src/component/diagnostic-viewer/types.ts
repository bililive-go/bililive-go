export type DiagnosticSeverity = 'trace' | 'info' | 'debug' | 'warn' | 'error';

export type DiagnosticValue = string | number | boolean | null;

export interface DiagnosticManifest {
  bundle_id: string;
  schema_version: string;
  generated_at: string;
  app_version?: string;
  commit?: string;
  go_version?: string;
  platform?: string;
  run_id: string;
  synthetic?: boolean;
  completeness?: 'complete' | 'partial' | 'unknown';
  dropped_events?: number;
  actual_window_ms?: number;
  source_files?: string[];
  sequence_scope?: string;
  room_population?: {
    configured?: number;
    started?: number;
    fully_traced?: number;
    representative?: number;
    aggregate_only?: number;
  };
}

export interface DiagnosticIncident {
  id: string;
  title: string;
  summary?: string;
  severity: 'info' | 'warning' | 'error';
  trigger: string;
  room_id?: string;
  room_label?: string;
  target_room_id?: string;
  target_generation?: number;
  initial_generation?: number;
  configured_room_count?: number;
  anchor_start_event_id?: string;
  focus_start_event_id?: string;
  goal_event_id?: string;
  trigger_event_id?: string;
  observed_monitor_to_first_byte_ms?: number;
  observed_resume_to_first_byte_ms?: number;
  expected_detection_interval_ms?: number;
  started_at?: string;
  ended_at?: string;
  tags?: string[];
}

export interface DiagnosticEventLink {
  rel: string;
  event_id?: string;
  span_id?: string;
}

export interface DiagnosticEvent {
  id: string;
  seq?: number;
  ts: number;
  wall_time?: string;
  kind?: string;
  message?: string;
  severity: DiagnosticSeverity;
  category: string;
  name: string;
  component: string;
  flow_id?: string;
  span_id?: string;
  parent_span_id?: string;
  duration_ms?: number;
  status?: string;
  attrs?: Record<string, DiagnosticValue>;
  links?: DiagnosticEventLink[];
  lane?: string;
  entity_id?: string;
  room_id?: string;
  generation?: number;
  global_seq?: number;
  dispatch_id?: string;
  handler_id?: string;
  task_id?: string;
  trace_task_id?: string;
  goroutine_id?: string;
  goroutine_seq?: number;
  disposition?: 'accepted' | 'dropped' | 'ignored';
}

export interface DiagnosticMetricPoint {
  ts: number;
  value: number;
}

export interface DiagnosticMetric {
  name: string;
  label?: string;
  unit: string;
  series: DiagnosticMetricPoint[];
}

export interface DiagnosticRuntimeSlice {
  id: string;
  goroutine_id: string;
  task_id?: string;
  trace_task_id?: string;
  start_ms: number;
  end_ms: number;
  state: 'running' | 'runnable' | 'waiting' | 'syscall' | 'unknown';
  wait_reason?: string;
  stack_fingerprint?: string;
  processor_id?: string;
  thread_id?: string;
  seq_on_g?: number;
  generation?: number;
  flow_id?: string;
  links?: DiagnosticEventLink[];
}

export interface DiagnosticExpectedPhase {
  key: string;
  label: string;
  start_ms: number;
  end_ms: number;
  status?: 'normal' | 'warning' | 'critical' | 'neutral';
  event_ids?: string[];
}

export interface DiagnosticExpectedAnalysis {
  root_cause_code?: string;
  root_cause_title?: string;
  summary?: string;
  confidence?: 'high' | 'medium' | 'low';
  phases?: DiagnosticExpectedPhase[];
  evidence_event_ids?: string[];
}

export interface DiagnosticBundle {
  schema: string;
  manifest: DiagnosticManifest;
  incident: DiagnosticIncident;
  entities?: Array<Record<string, unknown>>;
  configuration?: Record<string, unknown>;
  events: DiagnosticEvent[];
  metrics?: DiagnosticMetric[];
  runtime_slices?: DiagnosticRuntimeSlice[];
  expected_analysis?: DiagnosticExpectedAnalysis;
  raw_bundle?: unknown;
}

export interface DiagnosticPhase {
  key: string;
  label: string;
  startMs: number;
  endMs: number;
  durationMs: number;
  status: 'normal' | 'warning' | 'critical' | 'neutral';
  eventIds: string[];
  detail: string;
}

export interface DiagnosticEvidence {
  kind: 'fact' | 'runtime' | 'inference' | 'counter' | 'missing';
  title: string;
  detail: string;
  eventId?: string;
}

export interface DiagnosticFinding {
  code: string;
  title: string;
  summary: string;
  confidence: 'high' | 'medium' | 'low';
  rootPhaseKey: string;
  evidence: DiagnosticEvidence[];
  suggestions: string[];
}

export interface DiagnosticAnalysis {
  totalMs: number;
  detectionMs: number;
  recordingPreparationMs: number;
  configuredIntervalMs: number;
  firstLiveAtMs: number;
  recorderStartedAtMs: number;
  firstByteAtMs: number;
  windowStartMs: number;
  processStartMs: number;
  processToFirstByteMs: number;
  targetRoomId?: string;
  targetGeneration?: number;
  scopedEventIds: string[];
  intervalWithinExpectation: boolean;
  completeness: 'complete' | 'partial' | 'unknown';
  phases: DiagnosticPhase[];
  finding: DiagnosticFinding;
}

export interface DiagnosticScope {
  roomId?: string;
  generation?: number;
  startEventId?: string;
  goalEventId?: string;
}

export interface DiagnosticConcurrencySummary {
  configuredRooms: number;
  startedRooms: number;
  fullyTracedRooms: number;
  targetRoomId?: string;
  targetGeneration?: number;
  generations: number[];
  queueDepthPeak?: number;
  targetQueuePosition?: number;
  targetQueueWaitMs?: number;
  stopAtMs?: number;
  resumeAtMs?: number;
  staleEventCount: number;
  taskCount: number;
  goroutineCount: number;
}

export interface TimelineItem {
  id: string;
  event: DiagnosticEvent;
  lane: string;
  laneLabel: string;
  startMs: number;
  endMs: number;
  label: string;
  status: 'normal' | 'warning' | 'critical' | 'runtime' | 'neutral';
  milestone: boolean;
}
