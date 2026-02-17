import React, { useState, useEffect, useCallback } from 'react';
import {
  Card,
  Button,
  Progress,
  Typography,
  Space,
  Tag,
  Divider,
  Alert,
  Descriptions,
  Spin,
  Badge,
  Modal,
  message
} from 'antd';
import {
  SyncOutlined,
  DownloadOutlined,
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  CloudDownloadOutlined,
  ReloadOutlined,
  InfoCircleOutlined,
  RocketOutlined,
  WarningOutlined,
  RollbackOutlined
} from '@ant-design/icons';
import { subscribeSSE, unsubscribeSSE, SSEMessage } from '../../utils/sse';
import API from '../../utils/api';
import './update-page.css';

const api = new API();
const { Title, Text, Paragraph } = Typography;

// 版本切换/回滚功能开关
// 默认隐藏，本地开发时改为 true 以显示完整的版本管理 UI
const ENABLE_VERSION_SWITCH_UI = false;

// 更新状态类型
interface UpdateInfo {
  version: string;
  release_date?: string;
  changelog?: string;
  prerelease?: boolean;
  asset_name?: string;
  asset_size?: number;
  download_urls?: string[];
}

interface DownloadProgress {
  downloaded_bytes: number;
  total_bytes: number;
  speed: number;
  percentage: number;
}

interface LauncherStatus {
  connected: boolean;
  launched_by: string;
  is_docker: boolean;
  current_version: string;
  update_available: boolean;
  graceful_update_pending: boolean;
  graceful_update_version?: string;
  active_recordings: number;
  available_version?: string;
  // 启动器和 bgo 进程信息
  is_launcher_managed: boolean;
  launcher_pid?: number;
  launcher_exe_path?: string;
  bgo_pid?: number;
  bgo_exe_path?: string;
}

interface UpdateStatus {
  state: string;
  progress?: DownloadProgress;
  error?: string;
  graceful_update_pending: boolean;
  graceful_update_version?: string;
  active_recordings_count: number;
  can_apply_now: boolean;
  available_info?: UpdateInfo;
}

interface RollbackInfo {
  available: boolean;
  reason?: string;
  current_version: string;
  backup_version?: string;
  backup_binary_path?: string;
  active_version?: string;
}

// 更新页面组件
const UpdatePage: React.FC = () => {
  const [loading, setLoading] = useState(true);
  const [checking, setChecking] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [applying, setApplying] = useState(false);

  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [downloadProgress, setDownloadProgress] = useState<DownloadProgress | null>(null);
  const [launcherStatus, setLauncherStatus] = useState<LauncherStatus | null>(null);
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus | null>(null);
  const [rollbackInfo, setRollbackInfo] = useState<RollbackInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [includePrerelease, setIncludePrerelease] = useState(false);
  const [rollingBack, setRollingBack] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [restartCountdown, setRestartCountdown] = useState(0);

  // 加载初始数据
  const loadData = useCallback(async () => {
    setLoading(true);
    try {
      // launcher 和 update status 是核心数据，必须加载
      const [launcherRes, statusRes] = await Promise.all([
        api.getLauncherStatus(),
        api.getUpdateStatus()
      ]) as [any, any];

      if (launcherRes) {
        setLauncherStatus(launcherRes as LauncherStatus);
      }
      if (statusRes) {
        setUpdateStatus(statusRes as UpdateStatus);
        // 从 status 响应中恢复 updateInfo（刷新页面后保持状态）
        if (statusRes.available_info) {
          setUpdateInfo(statusRes.available_info as UpdateInfo);
        }
        if (statusRes.state === 'downloading' && statusRes.progress) {
          setDownloadProgress(statusRes.progress);
          setDownloading(true);
        } else {
          // 非下载状态时清除下载标记
          setDownloading(false);
        }
      }

      // rollback 是新 API，旧后端可能不支持，单独加载并容错
      try {
        const rollbackRes = await api.getRollbackInfo() as any;
        if (rollbackRes) {
          setRollbackInfo(rollbackRes as RollbackInfo);
        }
      } catch {
        // 旧后端没有回滚 API，静默忽略
        setRollbackInfo(null);
      }
    } catch (err: any) {
      console.error('加载更新状态失败:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  // 处理 SSE 更新事件
  const handleSSEMessage = useCallback((msg: SSEMessage) => {
    const { data } = msg;

    switch (msg.type) {
      case 'update_available':
        setUpdateInfo(data as UpdateInfo);
        setError(null);
        break;

      case 'update_downloading':
        setDownloadProgress(data as DownloadProgress);
        setDownloading(true);
        break;

      case 'update_ready':
        setDownloading(false);
        setDownloadProgress(null);
        // 如果是版本切换/重启中，启动自动重载
        if (data?.status === 'transitioning') {
          waitForServerRestart();
        } else {
          loadData();
        }
        break;

      case 'update_error':
        setError(data.error);
        setDownloading(false);
        setDownloadProgress(null);
        break;
    }
  }, [loadData]);

  // 初始化
  useEffect(() => {
    loadData();

    // 从后端配置读取 includePrerelease 初始值
    api.getEffectiveConfig().then((res: any) => {
      if (res?.update?.include_prerelease !== undefined) {
        setIncludePrerelease(res.update.include_prerelease);
      }
    }).catch(() => {
      // 静默忽略
    });

    // 订阅 SSE 事件
    const subIds = [
      subscribeSSE('*', 'update_available', handleSSEMessage),
      subscribeSSE('*', 'update_downloading', handleSSEMessage),
      subscribeSSE('*', 'update_ready', handleSSEMessage),
      subscribeSSE('*', 'update_error', handleSSEMessage),
    ];

    return () => {
      subIds.forEach(id => unsubscribeSSE(id));
    };
  }, [loadData, handleSSEMessage]);

  // 检查更新
  const handleCheckUpdate = async () => {
    setChecking(true);
    setError(null);
    try {
      const response = await api.checkProgramUpdate(includePrerelease) as any;
      if (response) {
        if (response.available && response.latest_info) {
          setUpdateInfo(response.latest_info);
          message.success('发现新版本！');
        } else {
          message.info('当前已是最新版本');
          setUpdateInfo(null);
        }
      }
      await loadData();
    } catch (err: any) {
      setError(err?.message || '检查更新失败');
      message.error('检查更新失败');
    } finally {
      setChecking(false);
    }
  };

  // 下载更新
  const handleDownload = async () => {
    setDownloading(true);
    setError(null);
    try {
      await api.downloadProgramUpdate();
      message.info('开始下载更新...');
    } catch (err: any) {
      setError(err?.message || '下载失败');
      message.error('下载失败');
      setDownloading(false);
    }
  };

  // 取消下载
  const handleCancelDownload = async () => {
    try {
      await api.cancelUpdate();
      setDownloading(false);
      setDownloadProgress(null);
      message.info('下载已取消');
    } catch (err: any) {
      message.error('取消失败');
    }
  };

  // 应用更新
  const handleApplyUpdate = async (graceful: boolean) => {
    const activeRecordings = updateStatus?.active_recordings_count || 0;

    if (activeRecordings > 0 && !graceful) {
      Modal.confirm({
        title: '确认强制更新?',
        icon: <WarningOutlined />,
        content: `当前有 ${activeRecordings} 个直播正在录制，强制更新将中断所有录制。`,
        okText: '强制更新',
        okType: 'danger',
        cancelText: '取消',
        onOk: async () => {
          await doApplyUpdate(false);
        }
      });
    } else {
      await doApplyUpdate(graceful);
    }
  };

  const doApplyUpdate = async (graceful: boolean) => {
    setApplying(true);
    try {
      await api.applyUpdate({ gracefulWait: graceful, forceNow: !graceful });
      if (graceful) {
        message.success('已启用优雅更新模式，将在所有录制结束后自动更新');
        await loadData();
      } else {
        // 非优雅更新：服务器即将重启，启动自动重载
        waitForServerRestart();
      }
    } catch (err: any) {
      setError(err?.message || '应用更新失败');
      message.error('应用更新失败');
    } finally {
      setApplying(false);
    }
  };

  // 等待服务器重启后自动刷新页面
  const waitForServerRestart = () => {
    setRestarting(true);
    setRestartCountdown(0);
    message.loading({ content: '正在重启服务器...', key: 'restart', duration: 0 });

    let elapsed = 0;
    const interval = setInterval(async () => {
      elapsed += 2;
      setRestartCountdown(elapsed);

      try {
        // 尝试请求服务器，如果成功说明新版本已启动
        const res = await fetch('/api/info', { signal: AbortSignal.timeout(2000) });
        if (res.ok) {
          clearInterval(interval);
          message.success({ content: '服务器已重启，正在刷新页面...', key: 'restart', duration: 2 });
          setTimeout(() => {
            window.location.reload();
          }, 1000);
        }
      } catch {
        // 服务器尚未就绪，继续等待
      }

      // 超过 60 秒仍未就绪，停止轮询
      if (elapsed >= 60) {
        clearInterval(interval);
        setRestarting(false);
        message.warning({ content: '服务器重启超时，请手动刷新页面', key: 'restart', duration: 5 });
      }
    }, 2000);
  };

  // 回滚到备份版本
  const handleRollback = async () => {
    if (!rollbackInfo?.available) return;

    Modal.confirm({
      title: '确认版本回滚?',
      icon: <RollbackOutlined />,
      content: `将从当前版本回滚到 ${rollbackInfo.backup_version}。程序将重启。`,
      okText: '确认回滚',
      okType: 'danger',
      cancelText: '取消',
      onOk: async () => {
        setRollingBack(true);
        try {
          await api.doRollback();
          // 回滚成功，服务器即将重启
          waitForServerRestart();
        } catch (err: any) {
          setError(err?.message || '回滚失败');
          message.error('回滚失败');
          setRollingBack(false);
        }
      }
    });
  };

  // 格式化文件大小
  const formatSize = (bytes: number) => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  };

  // 格式化速度
  const formatSpeed = (bytesPerSecond: number) => {
    if (bytesPerSecond < 1024) return `${bytesPerSecond.toFixed(0)} B/s`;
    if (bytesPerSecond < 1024 * 1024) return `${(bytesPerSecond / 1024).toFixed(1)} KB/s`;
    return `${(bytesPerSecond / 1024 / 1024).toFixed(1)} MB/s`;
  };

  // 获取状态徽章
  const getStateBadge = (state: string) => {
    switch (state) {
      case 'idle':
        return <Badge status="default" text="空闲" />;
      case 'checking':
        return <Badge status="processing" text="检查中" />;
      case 'available':
        return <Badge status="warning" text="有可用更新" />;
      case 'downloading':
        return <Badge status="processing" text="下载中" />;
      case 'ready':
        return <Badge status="success" text="准备就绪" />;
      case 'applying':
        return <Badge status="processing" text="应用中" />;
      case 'failed':
        return <Badge status="error" text="失败" />;
      default:
        return <Badge status="default" text={state} />;
    }
  };

  if (loading) {
    return (
      <div className="update-page">
        <Spin size="large" tip="加载中..." />
      </div>
    );
  }

  // 服务器正在重启中，显示等待提示
  if (restarting) {
    return (
      <div className="update-page">
        <div style={{ textAlign: 'center', padding: '80px 0' }}>
          <Spin size="large" />
          <Title level={4} style={{ marginTop: 24 }}>正在重启服务器...</Title>
          <Text type="secondary">
            已等待 {restartCountdown} 秒，服务器重启后页面将自动刷新
          </Text>
        </div>
      </div>
    );
  }

  const isReady = updateStatus?.state === 'ready';
  const hasUpdate = updateInfo || updateStatus?.state === 'available' || isReady;

  return (
    <div className="update-page">
      <Title level={4}>
        <RocketOutlined /> 程序更新
      </Title>

      {/* 错误提示 */}
      {error && (
        <Alert
          message="更新错误"
          description={error}
          type="error"
          showIcon
          closable
          onClose={() => setError(null)}
          style={{ marginBottom: 16 }}
        />
      )}

      {/* 优雅更新等待提示 */}
      {updateStatus?.graceful_update_pending && (
        <Alert
          message="优雅更新等待中"
          description={`将在所有录制结束后自动更新到 ${updateStatus.graceful_update_version || '最新版本'}。当前还有 ${updateStatus.active_recordings_count} 个录制进行中。`}
          type="info"
          showIcon
          icon={<SyncOutlined spin />}
          action={
            <Button size="small" onClick={handleCancelDownload}>
              取消等待
            </Button>
          }
          style={{ marginBottom: 16 }}
        />
      )}

      {/* 当前版本信息 */}
      <Card title="版本信息" style={{ marginBottom: 16 }}>
        <Descriptions column={2}>
          <Descriptions.Item label="当前版本">
            <Text strong>{launcherStatus?.current_version || '未知'}</Text>
          </Descriptions.Item>
          <Descriptions.Item label="更新状态">
            {getStateBadge(updateStatus?.state || 'idle')}
          </Descriptions.Item>
          <Descriptions.Item label="运行环境">
            {launcherStatus?.is_docker ? (
              <Tag color="blue">Docker</Tag>
            ) : (
              <Tag color="green">本地运行</Tag>
            )}
          </Descriptions.Item>
          <Descriptions.Item label="启动器模式">
            {launcherStatus?.is_launcher_managed ? (
              <Tag color="purple">由启动器管理</Tag>
            ) : (
              <Tag>独立运行</Tag>
            )}
          </Descriptions.Item>
          <Descriptions.Item label="BGO PID">
            {launcherStatus?.bgo_pid || '-'}
          </Descriptions.Item>
          <Descriptions.Item label="活跃录制">
            {updateStatus?.active_recordings_count || 0} 个
          </Descriptions.Item>
          <Descriptions.Item label="BGO 路径" span={2}>
            <Text copyable style={{ wordBreak: 'break-all' }}>
              {launcherStatus?.bgo_exe_path || '-'}
            </Text>
          </Descriptions.Item>
          {launcherStatus?.is_launcher_managed && (
            <Descriptions.Item label="Launcher PID">
              {launcherStatus?.launcher_pid || '-'}
            </Descriptions.Item>
          )}
          {launcherStatus?.is_launcher_managed && (
            <Descriptions.Item label="Launcher 路径" span={launcherStatus?.is_launcher_managed ? 2 : 1}>
              <Text copyable style={{ wordBreak: 'break-all' }}>
                {launcherStatus?.launcher_exe_path || '-'}
              </Text>
            </Descriptions.Item>
          )}
        </Descriptions>
      </Card>

      {/* 检查更新 */}
      <Card
        title="检查更新"
        style={{ marginBottom: 16 }}
        extra={
          <Space>
            <Text type="secondary">包含预发布版本</Text>
            <Button
              type={includePrerelease ? 'primary' : 'default'}
              size="small"
              onClick={() => setIncludePrerelease(!includePrerelease)}
            >
              {includePrerelease ? '是' : '否'}
            </Button>
          </Space>
        }
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Button
            type="primary"
            icon={checking ? <SyncOutlined spin /> : <SyncOutlined />}
            onClick={handleCheckUpdate}
            loading={checking}
            disabled={downloading}
          >
            检查更新
          </Button>

          {/* 下载进度 */}
          {downloading && downloadProgress && (
            <div style={{ marginTop: 16 }}>
              <Space style={{ marginBottom: 8 }}>
                <SyncOutlined spin />
                <Text>正在下载更新...</Text>
                <Text type="secondary">{formatSpeed(downloadProgress.speed)}</Text>
              </Space>
              <Progress
                percent={Math.round(downloadProgress.percentage)}
                status="active"
                format={() => `${formatSize(downloadProgress.downloaded_bytes)} / ${formatSize(downloadProgress.total_bytes)}`}
              />
              <Button
                type="link"
                danger
                onClick={handleCancelDownload}
                style={{ padding: 0 }}
              >
                取消下载
              </Button>
            </div>
          )}
        </Space>
      </Card>

      {/* 可用更新详情 */}
      {hasUpdate && (
        <Card
          title={
            <Space>
              <CloudDownloadOutlined />
              {updateInfo ? (
                <>
                  可用更新: v{updateInfo.version}
                  {updateInfo.prerelease && <Tag color="gold">预发布</Tag>}
                </>
              ) : (
                <>可用更新{isReady ? '（已准备就绪）' : ''}</>
              )}
            </Space>
          }
          style={{ marginBottom: 16 }}
        >
          <Space direction="vertical" style={{ width: '100%' }} size="middle">
            {updateInfo?.release_date && (
              <Text type="secondary">发布日期: {updateInfo.release_date}</Text>
            )}

            {updateInfo?.changelog && (
              <>
                <Divider plain>更新日志</Divider>
                <Paragraph
                  style={{
                    maxHeight: 200,
                    overflow: 'auto',
                    padding: '12px',
                    background: '#f5f5f5',
                    borderRadius: '4px',
                    whiteSpace: 'pre-wrap'
                  }}
                >
                  {updateInfo.changelog}
                </Paragraph>
              </>
            )}

            {updateInfo?.asset_size && (
              <Text type="secondary">
                文件大小: {formatSize(updateInfo.asset_size)}
              </Text>
            )}

            <Divider />

            {/* 操作按钮 */}
            <Space>
              {!isReady ? (
                <Button
                  type="primary"
                  icon={<DownloadOutlined />}
                  onClick={handleDownload}
                  loading={downloading}
                  disabled={downloading}
                >
                  下载更新
                </Button>
              ) : (
                <>
                  {updateStatus?.active_recordings_count && updateStatus.active_recordings_count > 0 ? (
                    <>
                      <Button
                        type="primary"
                        icon={<ReloadOutlined />}
                        onClick={() => handleApplyUpdate(true)}
                        loading={applying}
                      >
                        优雅更新
                      </Button>
                      <Button
                        danger
                        icon={<ExclamationCircleOutlined />}
                        onClick={() => handleApplyUpdate(false)}
                        loading={applying}
                      >
                        强制更新
                      </Button>
                      <Tag color="orange" icon={<ExclamationCircleOutlined />}>
                        {updateStatus.active_recordings_count} 个录制中
                      </Tag>
                    </>
                  ) : (
                    <Button
                      type="primary"
                      icon={<CheckCircleOutlined />}
                      onClick={() => handleApplyUpdate(true)}
                      loading={applying}
                    >
                      立即更新
                    </Button>
                  )}
                </>
              )}
            </Space>
          </Space>
        </Card>
      )}

      {/* 无更新提示 */}
      {!hasUpdate && !checking && (
        <Card>
          <div style={{ textAlign: 'center', padding: '40px 0' }}>
            <CheckCircleOutlined style={{ fontSize: 48, color: '#52c41a', marginBottom: 16 }} />
            <Title level={5} style={{ marginBottom: 8 }}>当前已是最新版本</Title>
            <Text type="secondary">
              版本 {launcherStatus?.current_version || '未知'} 是当前可用的最新版本
            </Text>
          </div>
        </Card>
      )}

      {/* 版本回滚（受功能开关控制） */}
      {ENABLE_VERSION_SWITCH_UI && rollbackInfo?.available && (
        <Card
          title={
            <Space>
              <RollbackOutlined />
              版本回滚
            </Space>
          }
          style={{ marginBottom: 16 }}
        >
          <Space direction="vertical" style={{ width: '100%' }} size="middle">
            <Descriptions column={2}>
              <Descriptions.Item label="当前版本">
                <Text strong>{rollbackInfo.current_version}</Text>
              </Descriptions.Item>
              <Descriptions.Item label="备份版本">
                <Tag color="orange">{rollbackInfo.backup_version}</Tag>
              </Descriptions.Item>
            </Descriptions>
            <Button
              icon={<RollbackOutlined />}
              onClick={handleRollback}
              loading={rollingBack}
              disabled={rollingBack || applying}
            >
              回滚到 {rollbackInfo.backup_version}
            </Button>
          </Space>
        </Card>
      )}

      {/* 更新说明 */}
      <Card title="更新说明" style={{ marginTop: 16 }}>
        <Space direction="vertical" size="small">
          <Text>
            <InfoCircleOutlined style={{ marginRight: 8 }} />
            <strong>优雅更新</strong>：等待所有正在进行的录制完成后自动更新，不会中断录制。
          </Text>
          <Text>
            <WarningOutlined style={{ marginRight: 8, color: '#faad14' }} />
            <strong>强制更新</strong>：立即更新程序，会中断所有正在进行的录制。
          </Text>
          {ENABLE_VERSION_SWITCH_UI && (
            <Text>
              <RollbackOutlined style={{ marginRight: 8 }} />
              <strong>版本回滚</strong>：如果更新后出现问题，可以回滚到上一个版本。
            </Text>
          )}
          <Text>
            <InfoCircleOutlined style={{ marginRight: 8 }} />
            更新后程序将自动切换到新版本，无需重启容器或服务。
          </Text>
        </Space>
      </Card>
    </div>
  );
};

export default UpdatePage;
