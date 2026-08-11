import { preferredDiagnosticRun } from './api';

describe('诊断 Viewer 初始真实运行选择', () => {
  test('优先选择 current，而不是列表中的合成或历史占位', () => {
    const selected = preferredDiagnosticRun([
      { run_id: 'run-newest-history' },
      { run_id: 'run-current', current: true },
      { run_id: 'run-older' },
    ]);

    expect(selected?.run_id).toBe('run-current');
  });

  test('没有 current 时选择后端按时间倒序返回的第一项', () => {
    const selected = preferredDiagnosticRun([
      { run_id: 'run-newest' },
      { run_id: 'run-older' },
    ]);

    expect(selected?.run_id).toBe('run-newest');
    expect(preferredDiagnosticRun([])).toBeUndefined();
  });
});
