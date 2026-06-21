import React, { useState, useEffect } from 'react';
import { Alert, Space, Typography } from 'antd';
import {
  SyncOutlined,
  ExclamationCircleOutlined,
  CloseCircleOutlined,
} from '@ant-design/icons';
import { subscribeSSE, unsubscribeSSE, SSEMessage } from '../../utils/sse';
import API from '../../utils/api';
import './ffmpeg-banner.css';

const api = new API();
const { Text } = Typography;

interface FFmpegStatus {
  state: 'checking' | 'downloading' | 'ready' | 'not_found' | 'error';
  message?: string;
  source?: string;
}

const FFmpegBanner: React.FC = () => {
  const [status, setStatus] = useState<FFmpegStatus | null>(null);

  useEffect(() => {
    let active = true;
    let sseReceived = false;

    const handleSSEMessage = (message: SSEMessage) => {
      if (message.type === 'ffmpeg_status') {
        sseReceived = true;
        if (active) setStatus(message.data as FFmpegStatus);
      }
    };

    const subId = subscribeSSE('*', 'ffmpeg_status', handleSSEMessage);

    // 初始化时查询当前状态；若 SSE 已推送更新则忽略（避免旧 HTTP 响应覆盖新状态）
    api.getFFmpegStatus().then((res: any) => {
      if (active && !sseReceived && res?.state) {
        setStatus(res as FFmpegStatus);
      }
    }).catch(() => {});

    return () => {
      active = false;
      unsubscribeSSE(subId);
    };
  }, []);

  // FFmpeg 已就绪或状态未知时不显示横幅
  if (!status || status.state === 'ready') {
    return null;
  }

  const renderMessage = () => {
    switch (status.state) {
      case 'checking':
        return (
          <Space>
            <SyncOutlined spin />
            <Text>正在检测 FFmpeg...</Text>
          </Space>
        );
      case 'downloading':
        return (
          <Space>
            <SyncOutlined spin />
            <Text>正在下载 FFmpeg，录制功能暂不可用...</Text>
            {status.message && <Text type="secondary">{status.message}</Text>}
          </Space>
        );
      case 'not_found':
        return (
          <Space>
            <ExclamationCircleOutlined />
            <Text>未找到 FFmpeg，录制功能不可用。</Text>
            {status.message && <Text type="secondary">{status.message}</Text>}
          </Space>
        );
      case 'error':
        return (
          <Space>
            <CloseCircleOutlined />
            <Text>FFmpeg 下载失败，录制功能不可用。</Text>
            {status.message && <Text type="secondary">{status.message}</Text>}
          </Space>
        );
      default:
        return null;
    }
  };

  const alertType = () => {
    switch (status.state) {
      case 'checking':
      case 'downloading':
        return 'info' as const;
      case 'not_found':
        return 'warning' as const;
      case 'error':
        return 'error' as const;
      default:
        return 'info' as const;
    }
  };

  const message = renderMessage();
  if (!message) return null;

  return (
    <Alert
      className="ffmpeg-banner"
      message={message}
      type={alertType()}
      banner
    />
  );
};

export default FFmpegBanner;
