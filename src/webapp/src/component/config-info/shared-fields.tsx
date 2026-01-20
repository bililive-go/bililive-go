/**
 * 可复用的配置字段组件
 * 
 * 本文件包含以下可复用组件：
 * 1. OutputTemplatePreview - 输出文件名模板预览组件 (第 30-80 行)
 * 2. FFmpegPathField - FFmpeg 路径配置字段组件 (第 85-140 行)
 * 3. ConfigFieldSimple - 简化版配置字段，不使用 Tag 模式 (第 145-180 行)
 */

import React, { useState } from 'react';
import { Button, Space, Alert, Tag } from 'antd';
import { EyeOutlined } from '@ant-design/icons';
import API from '../../utils/api';

const api = new API();

// ============================================================================
// 输出模板预览结果类型
// ============================================================================
interface PreviewResult {
  success?: boolean;
  preview_path?: string;
  filename?: string;
  error?: string;
  err_msg?: string;
}

// ============================================================================
// 输出文件名模板预览组件
// 位置：第 30-80 行
// 使用方式：在 ConfigField 的 actions 属性中使用
// ============================================================================
interface OutputTemplatePreviewProps {
  form: any; // Antd Form 实例
  displayStyle?: 'global' | 'compact'; // global 使用 Alert，compact 使用 Tag
}

export const OutputTemplatePreview: React.FC<OutputTemplatePreviewProps> = ({
  form,
  displayStyle = 'compact'
}) => {
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<PreviewResult | null>(null);

  const handlePreview = async () => {
    setLoading(true);
    try {
      const values = form.getFieldsValue();
      const res = await api.previewOutputTemplate(
        values.out_put_tmpl || '',
        values.out_put_path || ''
      );
      setResult(res as PreviewResult);
    } catch (error) {
      setResult({ success: false, error: '预览失败' });
    } finally {
      setLoading(false);
    }
  };

  // 根据返回结果获取显示信息
  const getResultDisplay = () => {
    if (!result) return null;

    // 处理不同的返回格式
    const isSuccess = result.success || (!result.err_msg && !result.error);
    const displayPath = result.preview_path || result.filename || '(未返回路径)';
    const errorMsg = result.err_msg || result.error || '未知错误';

    if (displayStyle === 'global') {
      return (
        <Alert
          message={isSuccess ? '预览成功' : '预览失败'}
          description={isSuccess ? `生成的文件路径: ${displayPath}` : errorMsg}
          type={isSuccess ? 'success' : 'error'}
          showIcon
          style={{ marginTop: 8 }}
        />
      );
    }

    // compact 样式使用 Tag
    return (
      <Tag color={isSuccess ? 'blue' : 'red'}>
        {isSuccess ? `预览: ${displayPath}` : `错误: ${errorMsg}`}
      </Tag>
    );
  };

  return (
    <Space wrap>
      <Button
        size={displayStyle === 'compact' ? 'small' : 'middle'}
        icon={<EyeOutlined />}
        onClick={handlePreview}
        loading={loading}
      >
        预览输出路径
      </Button>
      {getResultDisplay()}
    </Space>
  );
};

// ============================================================================
// FFmpeg 路径配置字段组件
// 位置：第 85-140 行
// 用于统一全局、平台、直播间的 FFmpeg 路径显示逻辑
// ============================================================================
// FFmpegPathFieldProps 接口预留供后续使用
// interface FFmpegPathFieldProps {
//   form: any;
//   level: 'global' | 'platform' | 'room';
//   globalFfmpegPath?: string | null;
//   platformFfmpegPath?: string | null;
//   actualFfmpegPath?: string;
//   fieldName?: string | string[];
// }

/**
 * 获取 FFmpeg 路径的显示值
 * 统一处理空值显示为 "(自动查找)"
 */
export const getFFmpegDisplayValue = (
  value: string | null | undefined,
  fallbackValue?: string | null | undefined
): string => {
  const effectiveValue = value ?? fallbackValue;
  if (!effectiveValue || effectiveValue.trim() === '') {
    return '(自动查找)';
  }
  return effectiveValue;
};

/**
 * 计算 FFmpeg 路径的继承信息
 */
export const getFFmpegInheritance = (
  level: 'global' | 'platform' | 'room',
  currentValue: string | null | undefined,
  platformValue: string | null | undefined,
  globalValue: string | null | undefined,
  platformKey?: string
): {
  source: 'global' | 'platform' | 'default';
  linkTo: string;
  isOverridden: boolean;
  inheritedValue: string;
} => {
  const hasOwnValue = currentValue != null && currentValue !== '';

  if (level === 'global') {
    return {
      source: 'default',
      linkTo: '',
      isOverridden: hasOwnValue,
      inheritedValue: '(自动查找)'
    };
  }

  if (level === 'platform') {
    return {
      source: 'global',
      linkTo: '/configInfo?tab=global#global-ffmpeg_path',
      isOverridden: hasOwnValue,
      inheritedValue: getFFmpegDisplayValue(globalValue)
    };
  }

  // room level
  // 关键修复：只有当平台有自己的设置时才显示"继承自平台"
  const platformHasOwnValue = platformValue != null && platformValue !== '';

  if (platformHasOwnValue) {
    return {
      source: 'platform',
      linkTo: `/configInfo?tab=platforms&platform=${platformKey}`,
      isOverridden: hasOwnValue,
      inheritedValue: getFFmpegDisplayValue(platformValue)
    };
  }

  return {
    source: 'global',
    linkTo: '/configInfo?tab=global#global-ffmpeg_path',
    isOverridden: hasOwnValue,
    inheritedValue: getFFmpegDisplayValue(globalValue)
  };
};

// ============================================================================
// 简化版配置字段
// 位置：第 145-180 行
// 用于 Switch 等不需要 Tag 交互的字段
// ============================================================================
interface SimpleConfigFieldProps {
  label: string;
  description?: string;
  children: React.ReactNode;
  id?: string;
}

export const SimpleConfigField: React.FC<SimpleConfigFieldProps> = ({
  label,
  description,
  children,
  id
}) => {
  return (
    <div className="config-item" id={id}>
      <div className="config-item-label">{label}</div>
      <div className="config-item-content">
        <div className="config-item-input">
          {children}
        </div>
        {description && (
          <div className="config-item-description">{description}</div>
        )}
      </div>
    </div>
  );
};

// ============================================================================
// 下载器类型相关的工具函数和组件
// ============================================================================

// 下载器类型常量
export type DownloaderType = 'ffmpeg' | 'native' | 'bililive-recorder' | '';

// 下载器可用性信息
export interface DownloaderAvailability {
  ffmpeg_available: boolean;
  ffmpeg_path?: string;
  native_available: boolean;
  bililive_recorder_available: boolean;
  bililive_recorder_path?: string;
}

/**
 * 获取下载器类型的显示名称
 */
export const getDownloaderDisplayName = (type: DownloaderType | undefined | null): string => {
  switch (type) {
    case 'ffmpeg':
      return 'FFmpeg';
    case 'native':
      return '原生 FLV 解析器';
    case 'bililive-recorder':
      return '录播姬';
    default:
      return 'FFmpeg (默认)';
  }
};

/**
 * 计算下载器类型的继承信息
 */
export const getDownloaderInheritance = (
  level: 'global' | 'platform' | 'room',
  currentValue: DownloaderType | undefined | null,
  platformValue: DownloaderType | undefined | null,
  globalValue: DownloaderType | undefined | null,
  platformKey?: string
): {
  source: 'global' | 'platform' | 'default';
  linkTo: string;
  isOverridden: boolean;
  inheritedValue: string;
} => {
  const hasOwnValue = currentValue != null && currentValue !== '';

  if (level === 'global') {
    return {
      source: 'default',
      linkTo: '',
      isOverridden: hasOwnValue,
      inheritedValue: 'FFmpeg (默认)'
    };
  }

  if (level === 'platform') {
    return {
      source: 'global',
      linkTo: '/configInfo?tab=global',
      isOverridden: hasOwnValue,
      inheritedValue: getDownloaderDisplayName(globalValue)
    };
  }

  // room level
  const platformHasOwnValue = platformValue != null && platformValue !== '';

  if (platformHasOwnValue) {
    return {
      source: 'platform',
      linkTo: `/configInfo?tab=platforms&platform=${platformKey}`,
      isOverridden: hasOwnValue,
      inheritedValue: getDownloaderDisplayName(platformValue)
    };
  }

  return {
    source: 'global',
    linkTo: '/configInfo?tab=global',
    isOverridden: hasOwnValue,
    inheritedValue: getDownloaderDisplayName(globalValue)
  };
};

// ============================================================================
// 流偏好配置相关的类型（新版）
// 注：旧版流选择配置（formats, qualities, max_bitrate 等）已删除
// 新版使用动态属性方式进行流选择，详见 src/webapp/src/types/stream.ts
// ============================================================================

// 流偏好配置接口（新版）
export interface StreamPreference {
  quality?: string;
  attributes?: Record<string, string>;
}

/**
 * 获取流偏好的显示值
 */
export const getStreamPreferenceDisplayValue = (preference?: StreamPreference): string => {
  if (!preference) {
    return '(自动选择)';
  }
  const parts: string[] = [];
  if (preference.quality) {
    parts.push(`画质: ${preference.quality}`);
  }
  if (preference.attributes && Object.keys(preference.attributes).length > 0) {
    const attrStr = Object.entries(preference.attributes)
      .map(([k, v]) => `${k}=${v}`)
      .join(', ');
    parts.push(attrStr);
  }
  return parts.length > 0 ? parts.join(', ') : '(自动选择)';
};
