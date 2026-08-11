export interface DiagnosticRunInfo {
  run_id: string;
  status?: string;
  started_at?: string;
  ended_at?: string;
  last_heartbeat?: string;
  last_heartbeat_at?: string;
  lease_renewed_at?: string;
  lease_expires_at?: string;
  clean?: boolean;
  acknowledged?: boolean;
  current?: boolean;
  active?: boolean;
  active_reason?: string;
  owner_pid?: number;
  event_count?: number;
  event_segments?: number;
  flight_recorder_available?: boolean;
  size_bytes?: number;
  has_panic?: boolean;
}

export interface DiagnosticStartupReport {
  current_run_id?: string;
  previous_run?: DiagnosticRunInfo;
  active_runs?: DiagnosticRunInfo[];
  abnormal_runs?: DiagnosticRunInfo[];
  // 兼容早期实验后端；正式 API 使用 abnormal_runs。
  unclean_runs?: DiagnosticRunInfo[];
}

/**
 * 后端按 started_at 从新到旧返回运行列表；当前进程优先，其次选择最新一次。
 * 把选择规则独立出来，避免 /diagnostics 初次加载时“真实列表请求”和默认示例
 * 两个 effect 竞态，最终反而展示合成数据。
 */
export const preferredDiagnosticRun = (
  runs: DiagnosticRunInfo[],
): DiagnosticRunInfo | undefined => (
  runs.find((run) => run.current) || runs[0]
);

const MAX_VIEWER_RESPONSE_BYTES = 25 * 1024 * 1024;

const readJSON = async <T,>(response: Response): Promise<T> => {
  if (!response.ok) {
    let message = `HTTP ${response.status}`;
    try {
      const body = await response.json() as { err_msg?: unknown; message?: unknown };
      if (typeof body.err_msg === 'string') {
        message = body.err_msg;
      } else if (typeof body.message === 'string') {
        message = body.message;
      }
    } catch {
      // 保留 HTTP 状态作为错误信息。
    }
    throw new Error(message);
  }
  return response.json() as Promise<T>;
};

export const listDiagnosticRuns = async (): Promise<DiagnosticRunInfo[]> => {
  const response = await fetch('/api/diagnostics/runs', {
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  });
  const body = await readJSON<{ runs?: DiagnosticRunInfo[] } | DiagnosticRunInfo[]>(response);
  return Array.isArray(body) ? body : body.runs || [];
};

export const getDiagnosticStartupReport = async (): Promise<DiagnosticStartupReport> => {
  const response = await fetch('/api/diagnostics/startup-status', {
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  });
  return readJSON<DiagnosticStartupReport>(response);
};

export const acknowledgeDiagnosticRun = async (runID: string): Promise<void> => {
  const response = await fetch(`/api/diagnostics/startup-status/${encodeURIComponent(runID)}/ack`, {
    method: 'POST',
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  });
  await readJSON(response);
};

export const snapshotDiagnosticRun = async (runID: string): Promise<void> => {
  const response = await fetch(`/api/diagnostics/runs/${encodeURIComponent(runID)}/snapshot`, {
    method: 'POST',
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  });
  await readJSON(response);
};

export const diagnosticViewerURL = (runID: string): string => (
  `/api/diagnostics/runs/${encodeURIComponent(runID)}/viewer`
);

export const getDiagnosticViewerText = async (runID: string): Promise<string> => {
  const response = await fetch(diagnosticViewerURL(runID), {
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) {
    await readJSON<never>(response);
    throw new Error(`读取运行现场失败：HTTP ${response.status}`);
  }
  const declaredLength = Number(response.headers.get('Content-Length'));
  if (Number.isFinite(declaredLength) && declaredLength > MAX_VIEWER_RESPONSE_BYTES) {
    throw new Error('运行现场超过 25 MiB，浏览器版 Viewer 拒绝加载。');
  }
  if (!response.body) {
    const text = await response.text();
    if (new Blob([text]).size > MAX_VIEWER_RESPONSE_BYTES) {
      throw new Error('运行现场超过 25 MiB，浏览器版 Viewer 拒绝加载。');
    }
    return text;
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  const parts: string[] = [];
  let total = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      total += next.value.byteLength;
      if (total > MAX_VIEWER_RESPONSE_BYTES) {
        throw new Error('运行现场超过 25 MiB，浏览器版 Viewer 拒绝加载。');
      }
      parts.push(decoder.decode(next.value, { stream: true }));
    }
    parts.push(decoder.decode());
    return parts.join('');
  } finally {
    try {
      await reader.cancel();
    } catch {
      // 响应正常结束时 reader 可能已经关闭。
    }
  }
};

export const diagnosticDownloadURL = (runID: string): string => (
  `/api/diagnostics/runs/${encodeURIComponent(runID)}/download`
);

export const diagnosticFlightRecorderURL = (runID: string): string => (
  `/api/diagnostics/runs/${encodeURIComponent(runID)}/flight-recorder`
);

export const diagnosticLogsDownloadURL = (): string => (
  '/api/diagnostics/logs/download'
);
