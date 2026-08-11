import React, { useEffect, useState } from 'react';
import { Alert, Button, Space } from 'antd';
import { BugOutlined } from '@ant-design/icons';
import { Link } from 'react-router-dom';
import {
  acknowledgeDiagnosticRun,
  DiagnosticRunInfo,
  getDiagnosticStartupReport,
} from '../diagnostic-viewer/api';

const DiagnosticStartupBanner: React.FC = () => {
  const [runs, setRuns] = useState<DiagnosticRunInfo[]>([]);
  const [acknowledging, setAcknowledging] = useState<string>();
  const [acknowledgeError, setAcknowledgeError] = useState<string>();

  useEffect(() => {
    let active = true;
    getDiagnosticStartupReport()
      .then((report) => {
        if (active) {
          setRuns((report.abnormal_runs || report.unclean_runs || []).filter((run) => !run.acknowledged));
        }
      })
      .catch(() => {
        // 老版本后端没有诊断 API 时保持静默，不能影响主界面。
      });
    return () => {
      active = false;
    };
  }, []);

  const first = runs[0];
  if (!first) return null;

  const acknowledge = async () => {
    setAcknowledging(first.run_id);
    setAcknowledgeError(undefined);
    try {
      await acknowledgeDiagnosticRun(first.run_id);
      setRuns((current) => current.filter((run) => run.run_id !== first.run_id));
    } catch (cause) {
      setAcknowledgeError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setAcknowledging(undefined);
    }
  };

  return (
    <>
      <Alert
        banner
        showIcon
        icon={<BugOutlined />}
        type="warning"
        message="上一次运行可能没有正常结束，现场轨迹已被保留"
        description={`运行 ${first.run_id}${runs.length > 1 ? `，另有 ${runs.length - 1} 个未确认现场` : ''}。重启不会覆盖这些证据。`}
        action={(
          <Space wrap>
            <Link to={`/diagnostics?run=${encodeURIComponent(first.run_id)}`}>
              <Button size="small" type="primary">立即调查</Button>
            </Link>
            <Button
              size="small"
              loading={acknowledging === first.run_id}
              onClick={acknowledge}
            >
              我已知晓
            </Button>
          </Space>
        )}
      />
      {acknowledgeError && (
        <Alert
          banner
          closable
          type="error"
          message="确认诊断现场失败"
          description={acknowledgeError}
          onClose={() => setAcknowledgeError(undefined)}
        />
      )}
    </>
  );
};

export default DiagnosticStartupBanner;
