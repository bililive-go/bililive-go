import React, { useState, useEffect, useCallback } from 'react';
import { Alert, Button, Modal, Progress, Typography, Space, Tag, Divider } from 'antd';
import {
  SyncOutlined,
  DownloadOutlined,
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  CloseCircleOutlined,
  CloudDownloadOutlined,
  ReloadOutlined
} from '@ant-design/icons';
import { subscribeSSE, unsubscribeSSE, SSEMessage } from '../../utils/sse';
import API from '../../utils/api';
import './update-banner.css';

const api = new API();
const { Text, Paragraph } = Typography;

// 更新状态类型
interface UpdateInfo {
  version: string;
  release_date?: string;
  changelog?: string;
  prerelease?: boolean;
  asset_name?: string;
  asset_size?: number;
}

interface DownloadProgress {
  downloaded_bytes: number;
  total_bytes: number;
  speed: number;
  percentage: number;
}

interface UpdateStatus {
  state: string;
  available_info?: UpdateInfo;
  progress?: DownloadProgress;
  error?: string;
  can_apply_now?: boolean;
  active_recordings_count?: number;
  graceful_update_pending?: boolean;
  graceful_update_version?: string;
}

// 更新横幅组件
const UpdateBanner: React.FC = () => {
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [downloadProgress, setDownloadProgress] = useState<DownloadProgress | null>(null);
  const [isReady, setIsReady] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activeRecordings, setActiveRecordings] = useState(0);
  const [showModal, setShowModal] = useState(false);
  const [applying, setApplying] = useState(false);
  const [dismissed, setDismissed] = useState(false);

  // 处理 SSE 更新事件
  const handleSSEMessage = useCallback((message: SSEMessage) => {
    const { data } = message;

    switch (message.type) {
      case 'update_available':
        setUpdateInfo(data as UpdateInfo);
        setError(null);
        setDismissed(false);
        break;

      case 'update_downloading':
        setDownloadProgress(data as DownloadProgress);
        break;

      case 'update_ready':
        setIsReady(true);
        setDownloadProgress(null);
        setActiveRecordings(data.active_recordings || 0);
        break;

      case 'update_error':
        setError(data.error);
        setDownloadProgress(null);
        break;
    }
  }, []);

  // 订阅 SSE 事件
  useEffect(() => {
    // 订阅所有更新相关事件
    const subIds = [
      subscribeSSE('*', 'update_available', handleSSEMessage),
      subscribeSSE('*', 'update_downloading', handleSSEMessage),
      subscribeSSE('*', 'update_ready', handleSSEMessage),
      subscribeSSE('*', 'update_error', handleSSEMessage),
    ];

    // 初始检查更新状态（恢复页面刷新前的状态）
    api.getUpdateStatus().then((response: any) => {
      // API 直接返回 UpdateStatusResponse，不嵌套在 data 中
      const status = response as UpdateStatus;
      if (!status || !status.state) return;

      if (status.available_info) {
        setUpdateInfo(status.available_info as UpdateInfo);
      }
      if (status.state === 'ready') {
        setIsReady(true);
      }
      if (status.state === 'downloading' && status.progress) {
        setDownloadProgress(status.progress as DownloadProgress);
      }
      if (status.error) {
        setError(status.error);
      }
    }).catch(() => {
      // 静默忽略
    });

    return () => {
      subIds.forEach(id => unsubscribeSSE(id));
    };
  }, [handleSSEMessage]);

  // 手动下载更新
  const handleDownload = async () => {
    try {
      setError(null);
      await api.downloadProgramUpdate();
    } catch (err: any) {
      setError(err?.message || '下载失败');
    }
  };

  // 显示更新详情
  const handleShowDetails = () => {
    setShowModal(true);
  };

  // 应用更新
  const handleApplyUpdate = async (graceful: boolean) => {
    setApplying(true);
    try {
      await api.applyUpdate({ gracefulWait: graceful, forceNow: !graceful });
      setShowModal(false);
    } catch (err: any) {
      setError(err?.message || '应用更新失败');
    } finally {
      setApplying(false);
    }
  };

  // 不显示条件：没有更新信息、已经被关闭、正在应用
  if (!updateInfo || dismissed || applying) {
    return null;
  }

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

  // 渲染消息内容
  const renderMessage = () => {
    if (error) {
      return (
        <Space>
          <CloseCircleOutlined />
          <Text>更新出错: {error}</Text>
          <Button size="small" type="link" onClick={handleDownload}>重试</Button>
        </Space>
      );
    }

    if (downloadProgress && !isReady) {
      return (
        <Space style={{ width: '100%' }} direction="vertical" size={4}>
          <Space>
            <SyncOutlined spin />
            <Text>正在下载更新 v{updateInfo.version}...</Text>
            <Text type="secondary">{formatSpeed(downloadProgress.speed)}</Text>
          </Space>
          <Progress
            percent={Math.round(downloadProgress.percentage)}
            size="small"
            status="active"
            format={() => `${formatSize(downloadProgress.downloaded_bytes)} / ${formatSize(downloadProgress.total_bytes)}`}
          />
        </Space>
      );
    }

    if (isReady) {
      return (
        <Space>
          <CheckCircleOutlined style={{ color: '#52c41a' }} />
          <Text>新版本 {updateInfo.version} 已下载完成，准备更新</Text>
          <Button size="small" type="primary" onClick={handleShowDetails}>
            立即更新
          </Button>
          {activeRecordings > 0 && (
            <Tag color="orange">
              <ExclamationCircleOutlined /> {activeRecordings} 个录制中
            </Tag>
          )}
        </Space>
      );
    }

    return (
      <Space>
        <CloudDownloadOutlined />
        <Text>发现新版本 v{updateInfo.version}</Text>
        {updateInfo.prerelease && <Tag color="gold">预发布</Tag>}
        <Button size="small" type="link" onClick={handleShowDetails}>
          查看详情
        </Button>
        <Button size="small" type="primary" icon={<DownloadOutlined />} onClick={handleDownload}>
          下载更新
        </Button>
      </Space>
    );
  };

  return (
    <>
      <Alert
        className="update-banner"
        message={renderMessage()}
        type={error ? 'error' : isReady ? 'success' : 'info'}
        closable
        onClose={() => setDismissed(true)}
        banner
      />

      <Modal
        title={
          <Space>
            <ReloadOutlined />
            更新到 v{updateInfo.version}
          </Space>
        }
        open={showModal}
        onCancel={() => setShowModal(false)}
        footer={null}
        width={520}
      >
        <div className="update-modal-content">
          <Space direction="vertical" size="middle" style={{ width: '100%' }}>
            {updateInfo.prerelease && (
              <Alert
                message="这是一个预发布版本，可能包含不稳定的功能"
                type="warning"
                showIcon
              />
            )}

            {updateInfo.release_date && (
              <Text type="secondary">发布日期: {updateInfo.release_date}</Text>
            )}

            {updateInfo.changelog && (
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

            {updateInfo.asset_size && (
              <Text type="secondary">
                文件大小: {formatSize(updateInfo.asset_size)}
              </Text>
            )}

            <Divider />

            {activeRecordings > 0 && (
              <Alert
                message={
                  <span>
                    当前有 <strong>{activeRecordings}</strong> 个直播正在录制中
                  </span>
                }
                description="选择「优雅更新」会等待所有录制完成后自动更新，选择「强制更新」会立即中断录制并更新。"
                type="warning"
                showIcon
              />
            )}

            <Space style={{ width: '100%', justifyContent: 'flex-end' }}>
              <Button onClick={() => setShowModal(false)}>
                稍后再说
              </Button>
              {activeRecordings > 0 ? (
                <>
                  <Button
                    type="primary"
                    onClick={() => handleApplyUpdate(true)}
                    loading={applying}
                  >
                    优雅更新
                  </Button>
                  <Button
                    danger
                    onClick={() => handleApplyUpdate(false)}
                    loading={applying}
                  >
                    强制更新
                  </Button>
                </>
              ) : (
                <Button
                  type="primary"
                  onClick={() => handleApplyUpdate(true)}
                  loading={applying}
                  icon={<ReloadOutlined />}
                >
                  {isReady ? '立即更新' : '下载并更新'}
                </Button>
              )}
            </Space>
          </Space>
        </div>
      </Modal>
    </>
  );
};

export default UpdateBanner;
