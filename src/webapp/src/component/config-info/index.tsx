import React, { useState, useEffect, useCallback, useRef } from 'react';
import {
  Tabs, Button, message, Spin, Input, Switch, InputNumber, Form,
  Tag, Space, Divider, Alert, Modal, Select,
  List, Badge, Tooltip, Card, Collapse
} from 'antd';
// @ts-ignore
import {
  SettingOutlined, GlobalOutlined, AppstoreOutlined,
  BellOutlined, LinkOutlined, InfoCircleOutlined, SaveOutlined,
  ReloadOutlined, EditOutlined, DeleteOutlined,
  RightOutlined, PlusOutlined, WarningOutlined,
  ExclamationCircleOutlined
} from '@ant-design/icons';
import { useLocation, Link } from 'react-router-dom';
import Editor from 'react-simple-code-editor';
import { highlight, languages } from 'prismjs';
import 'prismjs/components/prism-yaml';
import 'prismjs/themes/prism.css';
import API from '../../utils/api';
import './config-info.css';
import './config-gui.css';
import {
  OutputTemplatePreview, getFFmpegInheritance, getFFmpegDisplayValue,
} from './shared-fields';
import CloudUploadSettings from './CloudUploadSettings';

const api = new API();
const { TextArea } = Input;
const { Panel } = Collapse;

// 配置项类型定义
// 下载器可用性信息
interface DownloaderAvailability {
  ffmpeg_available: boolean;
  ffmpeg_path?: string;
  native_available: boolean;
  bililive_recorder_available: boolean;
  bililive_recorder_path?: string;
}

// 下载器类型常量
type DownloaderType = 'ffmpeg' | 'native' | 'bililive-recorder' | '';

interface EffectiveConfig {
  rpc: { enable: boolean; bind: string };
  debug: boolean;
  interval: number;
  out_put_path: string;
  actual_out_put_path: string;
  ffmpeg_path: string;
  actual_ffmpeg_path: string;
  log: {
    out_put_folder: string;
    save_last_log: boolean;
    save_every_log: boolean;
    rotate_days: number;
  };
  actual_log_folder: string;
  feature: {
    downloader_type?: DownloaderType;
    use_native_flv_parser?: boolean; // 已废弃，保留用于向后兼容
    remove_symbol_other_character: boolean;
  };
  out_put_tmpl: string;
  default_out_put_tmpl: string;
  video_split_strategies: {
    on_room_name_changed: boolean;
    max_duration: number;
    max_file_size: number;
  };
  on_record_finished: {
    convert_to_mp4: boolean;
    delete_flv_after_convert: boolean;
    custom_commandline: string;
    fix_flv_at_first: boolean;
  };
  timeout_in_us: number;
  timeout_in_seconds: number;
  notify: {
    telegram: {
      enable: boolean;
      withNotification: boolean;
      botToken: string;
      chatID: string;
    };
    email: {
      enable: boolean;
      smtpHost: string;
      smtpPort: number;
      senderEmail: string;
      senderPassword: string;
      recipientEmail: string;
    };
  };
  app_data_path: string;
  actual_app_data_path: string;
  read_only_tool_folder: string;
  actual_read_only_tool_folder: string;
  tool_root_folder: string;
  actual_tool_root_folder: string;
  platform_configs: Record<string, any>;
  live_rooms_count: number;
  // 下载器相关字段
  downloader_availability: DownloaderAvailability;
  available_downloaders: string[];
  // 代理配置
  proxy: {
    enable: boolean;
    url: string;
  };
  // 流偏好配置（新版）
  stream_preference?: {
    quality?: string;
    attributes?: Record<string, string>;
  };
}

interface PlatformStat {
  platform_key: string;
  platform_name?: string;
  room_count: number;
  listening_count: number;
  rooms: any[];
  has_config: boolean;
  has_rooms: boolean;
  min_access_interval_sec?: number;
  interval?: number;
  effective_interval?: number;
  actual_access_interval?: number;
  warning_message?: string;
  out_put_path?: string;
  ffmpeg_path?: string;
}

interface PlatformStatsResponse {
  platforms: PlatformStat[];
  available_platforms: string[];
  global_interval: number;
}

// 实际生效值显示组件
const EffectiveValue: React.FC<{ value: string; label?: string }> = ({ value, label }) => {
  if (!value) return null;
  return (
    <div className="config-effective-value">
      <InfoCircleOutlined />
      {label || '实际生效'}: {value}
    </div>
  );
};

// 继承标识组件
const InheritanceIndicator: React.FC<{
  source: 'global' | 'platform' | 'room' | 'default';
  linkTo?: string;
  isOverridden?: boolean;
  inheritedValue?: string | number | boolean;
}> = ({ source, linkTo, isOverridden, inheritedValue }) => {
  const inheritedText = inheritedValue !== undefined ? String(inheritedValue) : '';
  let sourceName = '';
  switch (source) {
    case 'global': sourceName = '全局'; break;
    case 'platform': sourceName = '平台'; break;
    case 'default': sourceName = '默认'; break;
    default: sourceName = '平台';
  }

  const className = `inheritance-indicator ${source} ${isOverridden ? 'overridden' : 'inherited'}`;

  if (isOverridden) {
    return (
      <Tag className={className}>
        <Tooltip title={
          <span>
            已覆盖{sourceName}项值: <strong>{inheritedText}</strong>
            {linkTo && <div>点击跳转查看配置源</div>}
          </span>
        }>
          <span>已覆盖{sourceName}配置</span>
          {linkTo && (
            <Link to={linkTo} style={{ color: 'inherit' }}>
              <LinkOutlined style={{ marginLeft: 4, cursor: 'pointer' }} />
            </Link>
          )}
        </Tooltip>
      </Tag>
    );
  }

  return (
    <Tag className={className}>
      <Tooltip title={
        <span>
          {source === 'default' ? '使用默认值:' : `继承自${sourceName}项:`} <strong>{inheritedText}</strong>
          {linkTo && <div>点击跳转查看配置源</div>}
        </span>
      }>
        <span>{source === 'default' ? '默认值' : `继承自${sourceName}`}</span>
        {linkTo && (
          <Link to={linkTo} style={{ color: 'inherit' }}>
            <LinkOutlined style={{ marginLeft: 4, cursor: 'pointer' }} />
          </Link>
        )}
      </Tooltip>
    </Tag>
  );
};

// 配置项组件
interface ConfigFieldProps {
  label: string;
  description?: string;
  children: React.ReactElement;
  effectiveValue?: string;
  inheritance?: {
    source: 'global' | 'platform' | 'room' | 'default';
    linkTo?: string;
    isOverridden?: boolean;
    inheritedValue?: string | number | boolean;
  };
  warning?: string;
  id?: string;
  valueDisplay?: string | number | boolean | React.ReactNode;
  actions?: React.ReactNode;
  /** 是否使用 Tag 交互模式。默认 false（直接显示控件）。设为 true 时，显示 Tag，点击后变为输入框 */
  useTagMode?: boolean;
}

const ConfigField: React.FC<ConfigFieldProps> = ({
  label, description, children, effectiveValue, inheritance, warning, id, valueDisplay, actions, useTagMode = false
}) => {
  const [isEditing, setIsEditing] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // 计算显示内容
  let displayContent: React.ReactNode | string = valueDisplay;
  if (valueDisplay === undefined || valueDisplay === null || valueDisplay === '') {
    if (inheritance && !inheritance.isOverridden) {
      displayContent = inheritance.inheritedValue;
    }
  }
  const displayText = (displayContent !== undefined && displayContent !== null && String(displayContent) !== '')
    ? displayContent
    : (inheritance?.inheritedValue !== undefined ? inheritance.inheritedValue : '点击编辑');

  const showInput = isEditing || !useTagMode;
  const tagSource = inheritance?.source || 'global';

  useEffect(() => {
    if (isEditing && containerRef.current && useTagMode) {
      const input = containerRef.current.querySelector('input, textarea, .ant-input-number-input, [tabindex="0"]');
      if (input) {
        (input as HTMLElement).focus();
      }
    }
  }, [isEditing, useTagMode]);

  const handleBlur = (e: React.FocusEvent) => {
    if (useTagMode && !containerRef.current?.contains(e.relatedTarget as Node)) {
      setIsEditing(false);
    }
  };

  return (
    <div className="config-item" id={id} ref={containerRef}>
      <div className="config-item-label">
        {label}
        {inheritance && (
          <div style={{ marginTop: 4 }}>
            <InheritanceIndicator {...inheritance} />
          </div>
        )}
      </div>
      <div className="config-item-content">
        <div className="config-item-input" onBlur={handleBlur}>
          {showInput ? (
            useTagMode ? React.cloneElement(children, {
              style: { ...children.props.style, minWidth: '200px' }
            }) : children
          ) : (
            <Tag
              className={`inheritance-indicator ${tagSource}`}
              style={{ cursor: 'pointer', fontSize: '14px', padding: '4px 10px', height: 'auto', display: 'inline-flex', alignItems: 'center' }}
              onClick={() => setIsEditing(true)}
            >
              <EditOutlined style={{ marginRight: 4 }} />
              <span style={{
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                maxWidth: '400px',
                display: 'inline-block',
                verticalAlign: 'middle'
              }}>
                {displayText}
              </span>
            </Tag>
          )}
        </div>

        {actions && (
          <div className="config-item-actions" style={{ marginTop: 8 }}>
            {actions}
          </div>
        )}

        {description && (
          <div className="config-item-description">{description}</div>
        )}
        {effectiveValue && <EffectiveValue value={effectiveValue} />}
        {warning && (
          <Alert
            message={warning}
            type="warning"
            showIcon
            icon={<WarningOutlined />}
            style={{ marginTop: 8, padding: '4px 12px' }}
          />
        )}
      </div>
    </div>
  );
};

// 全局设置组件
const GlobalSettings: React.FC<{
  config: EffectiveConfig;
  onUpdate: (updates: any) => Promise<void>;
  loading: boolean;
}> = ({ config, onUpdate, loading }) => {
  const [form] = Form.useForm();

  useEffect(() => {
    if (config) {
      // 转换单位：纳秒 -> 秒
      // 转换 attributes: map -> array
      const displayConfig = {
        ...config,
        video_split_strategies: config.video_split_strategies ? {
          ...config.video_split_strategies,
          max_duration: config.video_split_strategies.max_duration / 1000000000
        } : undefined,
        stream_preference: config.stream_preference ? {
          ...config.stream_preference,
          attributes: config.stream_preference.attributes
            ? Object.entries(config.stream_preference.attributes).map(([key, value]) => ({ key, value }))
            : []
        } : { attributes: [] }
      };
      form.setFieldsValue(displayConfig);
    }
  }, [config, form]);

  const handleSave = async () => {
    try {
      const values = await form.validateFields();
      // 转换单位：秒 -> 纳秒
      // 转换 attributes: array -> map
      const attributesArray = values.stream_preference?.attributes || [];
      const attributesMap: Record<string, string> = {};
      for (const item of attributesArray) {
        if (item.key && item.value !== undefined) {
          attributesMap[item.key] = item.value;
        }
      }
      const updates = {
        ...values,
        video_split_strategies: values.video_split_strategies ? {
          ...values.video_split_strategies,
          max_duration: (values.video_split_strategies.max_duration || 0) * 1000000000
        } : undefined,
        stream_preference: values.stream_preference ? {
          quality: values.stream_preference.quality || undefined,
          attributes: Object.keys(attributesMap).length > 0 ? attributesMap : undefined
        } : undefined
      };
      await onUpdate(updates);
      message.success('设置已保存');
    } catch (error: any) {
      console.error('保存全局设置失败:', error);
      if (error?.errorFields) {
        message.error('表单校验失败，请检查输入项');
      } else {
        const errorMsg = error?.err_msg || error?.message || '未知错误';
        message.error('保存失败: ' + errorMsg);
      }
    }
  };

  if (!config) {
    return <Spin />;
  }

  return (
    <div className="config-content">
      <Form form={form} layout="vertical">
        {/* RPC 设置 */}
        <Card title="RPC 服务设置" size="small" style={{ marginBottom: 16 }} id="global-rpc">
          <ConfigField label="启用 RPC" description="启用后可通过 Web 界面管理录播机">
            <Form.Item name={['rpc', 'enable']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="绑定地址" description="RPC 服务监听的地址和端口">
            <Form.Item name={['rpc', 'bind']} noStyle>
              <Input placeholder="例如: :8080 或 127.0.0.1:8080" style={{ width: 300 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        {/* 基础设置 */}
        <Card title="基础设置" size="small" style={{ marginBottom: 16 }} id="global-base">
          <ConfigField
            label="调试模式"
            description="启用后会输出更多日志信息"
            valueDisplay={config.debug ? '已启用' : '已禁用'}
          >
            <Form.Item name="debug" valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField
            label="检测间隔 (秒)"
            description="检测直播状态的间隔时间"
            id="global-interval"
            valueDisplay={config.interval}
          >
            <Form.Item
              name="interval"
              rules={[{ required: true, message: '请输入检测间隔' }]}
            >
              <InputNumber min={1} max={3600} style={{ width: 200 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField
            label="输出路径"
            description="录制文件的保存目录"
            effectiveValue={config.actual_out_put_path}
            id="global-out_put_path"
            valueDisplay={config.out_put_path || './'}
          >
            <Form.Item name="out_put_path" noStyle>
              <Input placeholder="例如: ./ 或 /data/recordings" style={{ width: 400 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField
            label="FFmpeg 路径"
            description="留空则自动查找"
            effectiveValue={config.actual_ffmpeg_path}
            id="global-ffmpeg_path"
            valueDisplay={config.ffmpeg_path || '(自动查找)'}
          >
            <Form.Item name="ffmpeg_path" noStyle>
              <Input placeholder="留空则自动在环境变量中查找" style={{ width: 400 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField
            label="输出文件名模板"
            description="自定义录制文件的命名模板"
            actions={<OutputTemplatePreview form={form} displayStyle="global" />}
          >
            <Form.Item name="out_put_tmpl" noStyle>
              <TextArea
                rows={2}
                placeholder={`留空使用默认模板: ${config.default_out_put_tmpl || ''}`}
                style={{ width: 500 }}
              />
            </Form.Item>
          </ConfigField>
          <ConfigField label="超时时间 (秒)" description="网络请求超时时间">
            <Form.Item name="timeout_in_seconds" noStyle>
              <InputNumber min={1} max={300} style={{ width: 200 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        {/* 日志设置 */}
        <Card title="日志设置" size="small" style={{ marginBottom: 16 }}>
          <ConfigField
            label="日志输出目录"
            effectiveValue={config.actual_log_folder}
          >
            <Form.Item name={['log', 'out_put_folder']} noStyle>
              <Input placeholder="例如: ./" style={{ width: 400 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField label="保留上次日志" description="程序启动时保留上次运行的日志">
            <Form.Item name={['log', 'save_last_log']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="保存每次日志" description="每次录制都单独保存日志">
            <Form.Item name={['log', 'save_every_log']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="日志保留天数" description="自动清理超过指定天数的日志，0表示不清理">
            <Form.Item name={['log', 'rotate_days']} noStyle>
              <InputNumber min={0} max={365} style={{ width: 200 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        {/* 功能特性 */}
        <Card title="功能特性" size="small" style={{ marginBottom: 16 }}>
          <ConfigField
            label="下载器类型"
            description="选择用于下载直播流的工具。录播姬需要单独安装。"
          >
            <Form.Item name={['feature', 'downloader_type']} noStyle>
              <Select style={{ width: 280 }} placeholder="选择下载器">
                <Select.Option
                  value="ffmpeg"
                  disabled={!config.downloader_availability?.ffmpeg_available}
                >
                  <Tooltip title={!config.downloader_availability?.ffmpeg_available ? '未找到 FFmpeg，请先安装' : config.downloader_availability?.ffmpeg_path}>
                    FFmpeg {!config.downloader_availability?.ffmpeg_available && '(不可用)'}
                  </Tooltip>
                </Select.Option>
                <Select.Option value="native">
                  原生 FLV 解析器 (内置)
                </Select.Option>
                <Select.Option
                  value="bililive-recorder"
                  disabled={!config.downloader_availability?.bililive_recorder_available}
                >
                  <Tooltip title={!config.downloader_availability?.bililive_recorder_available ? '未安装录播姬 CLI，请在工具页面安装' : config.downloader_availability?.bililive_recorder_path}>
                    录播姬 {!config.downloader_availability?.bililive_recorder_available && '(未安装)'}
                  </Tooltip>
                </Select.Option>
              </Select>
            </Form.Item>
          </ConfigField>
          <ConfigField label="移除特殊字符" description="从文件名中移除特殊字符">
            <Form.Item name={['feature', 'remove_symbol_other_character']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
        </Card>

        {/* 流偏好配置 */}
        <Card title="流偏好配置" size="small" style={{ marginBottom: 16 }}>
          <ConfigField
            label="清晰度偏好"
            description="偏好的清晰度名称，留空则自动选择最高画质"
            valueDisplay={config.stream_preference?.quality || '(自动选择)'}
          >
            <Form.Item name={['stream_preference', 'quality']} noStyle>
              <Input placeholder="例如: 原画、1080p、720p" style={{ width: 300 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField
            label="流属性偏好"
            description="键值对形式的流属性筛选条件，例如 format=flv, codec=h264"
          >
            <Form.List name={['stream_preference', 'attributes']}>
              {(fields, { add, remove }) => (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {fields.map(({ key, name, ...restField }) => (
                    <Space key={key} style={{ display: 'flex' }} align="baseline">
                      <Form.Item
                        {...restField}
                        name={[name, 'key']}
                        rules={[{ required: true, message: '请输入属性名' }]}
                        noStyle
                      >
                        <Input placeholder="属性名 (如: format)" style={{ width: 150 }} />
                      </Form.Item>
                      <span>=</span>
                      <Form.Item
                        {...restField}
                        name={[name, 'value']}
                        rules={[{ required: true, message: '请输入属性值' }]}
                        noStyle
                      >
                        <Input placeholder="属性值 (如: flv)" style={{ width: 150 }} />
                      </Form.Item>
                      <Button
                        type="text"
                        danger
                        icon={<DeleteOutlined />}
                        onClick={() => remove(name)}
                      />
                    </Space>
                  ))}
                  <Button
                    type="dashed"
                    onClick={() => add({ key: '', value: '' })}
                    icon={<PlusOutlined />}
                    style={{ width: 320 }}
                  >
                    添加属性
                  </Button>
                </div>
              )}
            </Form.List>
          </ConfigField>
        </Card>

        {/* 视频分割策略 */}
        <Card title="视频分割策略" size="small" style={{ marginBottom: 16 }}>
          <ConfigField label="房间名变化时分割" description="当主播更换直播间标题时自动分割视频">
            <Form.Item name={['video_split_strategies', 'on_room_name_changed']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="最大时长 (秒)" description="单个视频的最大录制时长，0表示不限制">
            <Form.Item
              name={['video_split_strategies', 'max_duration']}
              rules={[
                {
                  validator: (_, value) => {
                    if (value > 0 && value < 60) {
                      return Promise.reject(new Error('最小录制时长为 60 秒'));
                    }
                    return Promise.resolve();
                  }
                }
              ]}
            >
              <InputNumber min={0} style={{ width: 200 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField label="最大文件大小 (字节)" description="单个视频的最大文件大小，0表示不限制">
            <Form.Item name={['video_split_strategies', 'max_file_size']} noStyle>
              <InputNumber min={0} style={{ width: 200 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        {/* 录制完成后动作 */}
        <Card title="录制完成后动作" size="small" style={{ marginBottom: 16 }}>
          <ConfigField label="修复 FLV 文件" description="录制完成后自动修复 FLV 文件">
            <Form.Item name={['on_record_finished', 'fix_flv_at_first']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="转换为 MP4" description="录制完成后自动将 FLV 转换为 MP4">
            <Form.Item name={['on_record_finished', 'convert_to_mp4']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="转换后删除 FLV" description="MP4 转换成功后删除原始 FLV 文件">
            <Form.Item name={['on_record_finished', 'delete_flv_after_convert']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="自定义命令" description="录制完成后执行的自定义命令，设置后会忽略转换MP4设置">
            <Form.Item name={['on_record_finished', 'custom_commandline']} noStyle>
              <TextArea rows={3} placeholder="留空则不执行自定义命令" style={{ width: 500 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        {/* 云盘上传设置 */}
        <CloudUploadSettings config={config} />

        {/* 高级设置 */}
        <Card title="高级设置" size="small" style={{ marginBottom: 16 }}>
          <ConfigField
            label="应用数据目录"
            description="应用数据的存储目录"
            effectiveValue={config.actual_app_data_path}
          >
            <Form.Item name="app_data_path" noStyle>
              <Input placeholder="留空使用默认目录" style={{ width: 400 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField
            label="只读工具目录"
            description="预置工具的只读目录（Docker 镜像内使用）"
            effectiveValue={config.actual_read_only_tool_folder}
          >
            <Form.Item name="read_only_tool_folder" noStyle>
              <Input placeholder="留空则不使用" style={{ width: 400 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField
            label="可写工具目录"
            description="下载的外部工具存储目录"
            effectiveValue={config.actual_tool_root_folder}
          >
            <Form.Item name="tool_root_folder" noStyle>
              <Input placeholder="留空使用默认目录" style={{ width: 400 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        {/* 代理设置 */}
        <Card title="代理设置" size="small" style={{ marginBottom: 16 }}>
          <ConfigField
            label="启用代理配置"
            description="关闭时使用系统环境变量 (HTTP_PROXY, HTTPS_PROXY, ALL_PROXY)"
            valueDisplay={config.proxy?.enable ? '已启用' : '使用系统环境变量'}
          >
            <Form.Item name={['proxy', 'enable']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField
            label="代理地址"
            description="支持 HTTP 代理 (http://host:port) 和 SOCKS5 代理 (socks5://host:port)"
            valueDisplay={config.proxy?.url || '(未设置)'}
          >
            <Form.Item name={['proxy', 'url']} noStyle>
              <Input placeholder="例如: socks5://127.0.0.1:1080 或 http://127.0.0.1:7890" style={{ width: 400 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        <div className="config-actions">
          <Button
            type="primary"
            icon={<SaveOutlined />}
            onClick={handleSave}
            loading={loading}
          >
            保存设置
          </Button>
        </div>
      </Form>
    </div>
  );
};

// 通知服务设置组件
const NotifySettings: React.FC<{
  config: EffectiveConfig;
  onUpdate: (updates: any) => Promise<void>;
  loading: boolean;
}> = ({ config, onUpdate, loading }) => {
  const [form] = Form.useForm();

  useEffect(() => {
    if (config?.notify) {
      form.setFieldsValue(config.notify);
    }
  }, [config, form]);

  const handleSave = async () => {
    try {
      const updates = {
        notify: form.getFieldsValue(),
      };
      await onUpdate(updates);
      message.success('通知设置已保存');
    } catch (error: any) {
      console.error('保存通知设置失败:', error);
      const errorMsg = error?.err_msg || error?.message || '未知错误';
      message.error('保存失败: ' + errorMsg);
    }
  };

  if (!config) {
    return <Spin />;
  }

  return (
    <div className="config-content">
      <Form form={form} layout="vertical">
        {/* Telegram 通知 */}
        <Card title={<><BellOutlined /> Telegram 通知</>} size="small" style={{ marginBottom: 16 }}>
          <ConfigField label="启用" description="开启后会在直播开始/结束时发送 Telegram 通知">
            <Form.Item name={['telegram', 'enable']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="启用声音通知">
            <Form.Item name={['telegram', 'withNotification']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="Bot Token" description="从 @BotFather 获取">
            <Form.Item name={['telegram', 'botToken']} noStyle>
              <Input.Password placeholder="你的 Bot Token" style={{ width: 400 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField label="Chat ID" description="接收通知的聊天 ID">
            <Form.Item name={['telegram', 'chatID']} noStyle>
              <Input placeholder="你的 Chat ID" style={{ width: 300 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        {/* Email 通知 */}
        <Card title={<><BellOutlined /> 邮件通知</>} size="small" style={{ marginBottom: 16 }}>
          <ConfigField label="启用" description="开启后会在直播开始/结束时发送邮件通知">
            <Form.Item name={['email', 'enable']} valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </ConfigField>
          <ConfigField label="SMTP 服务器" description="例如: smtp.qq.com">
            <Form.Item name={['email', 'smtpHost']} noStyle>
              <Input placeholder="SMTP 服务器地址" style={{ width: 300 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField label="SMTP 端口" description="常用端口: 25, 465, 587">
            <Form.Item name={['email', 'smtpPort']} noStyle>
              <InputNumber min={1} max={65535} style={{ width: 150 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField label="发件人邮箱">
            <Form.Item name={['email', 'senderEmail']} noStyle>
              <Input placeholder="你的邮箱地址" style={{ width: 300 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField label="发件人密码" description="邮箱授权码或应用专用密码">
            <Form.Item name={['email', 'senderPassword']} noStyle>
              <Input.Password placeholder="邮箱密码或授权码" style={{ width: 300 }} />
            </Form.Item>
          </ConfigField>
          <ConfigField label="收件人邮箱">
            <Form.Item name={['email', 'recipientEmail']} noStyle>
              <Input placeholder="接收通知的邮箱" style={{ width: 300 }} />
            </Form.Item>
          </ConfigField>
        </Card>

        <div className="config-actions">
          <Button
            type="primary"
            icon={<SaveOutlined />}
            onClick={handleSave}
            loading={loading}
          >
            保存通知设置
          </Button>
        </div>
      </Form>
    </div>
  );
};

// 平台设置组件
const PlatformSettings: React.FC<{
  platformStats: PlatformStatsResponse | null;
  globalConfig: EffectiveConfig;
  onUpdate: (platformKey: string, updates: any) => Promise<void>;
  onDelete: (platformKey: string) => Promise<void>;
  loading: boolean;
  onRefresh: () => void;
}> = ({ platformStats, globalConfig, onUpdate, onDelete, loading, onRefresh }) => {
  const [expandedKeys, setExpandedKeys] = useState<string[]>([]);

  const location = useLocation();

  useEffect(() => {
    const handleExpand = () => {
      const searchParams = new URLSearchParams(location.search);
      const platformKeyP = searchParams.get('platform');

      const hash = location.hash;
      let platformKeyH = '';
      if (hash.startsWith('#platforms-')) {
        const parts = hash.split('-');
        if (parts.length >= 2) {
          platformKeyH = parts[1];
        }
      }

      const targetKey = platformKeyP || platformKeyH;
      if (targetKey) {
        setExpandedKeys(prev => prev.includes(targetKey) ? prev : [...prev, targetKey]);
      }
    };

    handleExpand();
  }, [location]);

  const [selectedNewPlatform, setSelectedNewPlatform] = useState<string>('');

  if (!platformStats) {
    return <Spin />;
  }

  const { platforms, available_platforms, global_interval } = platformStats;

  // 分组平台：有直播间的 vs 只有配置没有直播间的
  const platformsWithRooms = platforms.filter(p => p.has_rooms);
  const platformsWithoutRooms = platforms.filter(p => !p.has_rooms && p.has_config);

  const handleSave = async (platformKey: string, values: any) => {
    try {
      await onUpdate(platformKey, values);
      message.success('平台设置已保存');
    } catch (error: any) {
      console.error(`保存平台设置 (${platformKey}) 失败:`, error);
      const errorMsg = error?.err_msg || error?.message || '未知错误';
      message.error('保存失败: ' + errorMsg);
    }
  };

  const handleDelete = async (platformKey: string) => {
    Modal.confirm({
      title: '确认删除',
      icon: <ExclamationCircleOutlined />,
      content: `确定要删除平台 "${platformKey}" 的配置吗？删除后该平台将使用全局配置。`,
      onOk: async () => {
        await onDelete(platformKey);
        message.success('平台配置已删除');
      }
    });
  };

  const handleAddPlatform = async () => {
    if (!selectedNewPlatform) return;
    try {
      await onUpdate(selectedNewPlatform, { name: selectedNewPlatform });
      setSelectedNewPlatform('');
      onRefresh();
      message.success('平台配置已添加');
    } catch (error: any) {
      console.error('添加平台配置失败:', error);
      const errorMsg = error?.err_msg || error?.message || '未知错误';
      message.error('添加失败: ' + errorMsg);
    }
  };



  const renderPlatformCard = (platform: PlatformStat) => {
    const isExpanded = expandedKeys.includes(platform.platform_key);

    return (
      <Card
        key={platform.platform_key}
        size="small"
        style={{ marginBottom: 16 }}
        title={
          <div
            style={{ display: 'flex', alignItems: 'center', cursor: 'pointer', width: '100%' }}
            onClick={(e) => {
              // 避免与其他交互元素冲突
              if ((e.target as HTMLElement).closest('.ant-tag') || (e.target as HTMLElement).closest('.ant-btn')) {
                return;
              }
              setExpandedKeys(prev =>
                prev.includes(platform.platform_key)
                  ? prev.filter(k => k !== platform.platform_key)
                  : [...prev, platform.platform_key]
              );
            }}
          >
            <Space>
              <RightOutlined
                style={{
                  transition: 'transform 0.3s',
                  transform: isExpanded ? 'rotate(90deg)' : 'none',
                  fontSize: 12,
                  marginRight: 4
                }}
              />
              <span style={{ fontWeight: 600 }}>
                {platform.platform_name || platform.platform_key}
              </span>
              {platform.has_config ? (
                <Tag color="blue">已配置</Tag>
              ) : (
                <Tag>使用全局配置</Tag>
              )}
              {platform.listening_count > 0 && (
                // @ts-ignore
                <Badge
                  count={platform.listening_count}
                  showZero
                  style={{ backgroundColor: '#f0f0f0', color: 'rgba(0,0,0,0.45)', boxShadow: '0 0 0 1px #d9d9d9 inset' }}
                >
                  <Tag color="success">监控中</Tag>
                </Badge>
              )}
              <Tag color="default">{platform.room_count} 个直播间</Tag>
            </Space>
          </div>
        }
      >
        {isExpanded && (
          <PlatformConfigForm
            platform={platform}
            globalConfig={globalConfig}
            globalInterval={global_interval}
            onSave={(values) => handleSave(platform.platform_key, values)}
            onDelete={() => handleDelete(platform.platform_key)}
            loading={loading}
            onNavigateToRoom={(liveId) => {
              // 在新 Tab 中打开并定位
              window.open(`/#/configInfo#rooms-live-${liveId}`, '_blank');
            }}
          />
        )}
      </Card>
    );
  };

  return (
    <div className="config-content">
      <Alert
        message="平台配置说明"
        description="平台配置会覆盖全局配置，并被直播间配置覆盖。未配置的项将继承全局配置。"
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />

      {/* 正在监控的平台 */}
      {platformsWithRooms.length > 0 && (
        <>
          <Divider style={{ fontSize: 14 }}>正在监控的平台 ({platformsWithRooms.length})</Divider>
          {platformsWithRooms.map(renderPlatformCard)}
        </>
      )}

      {/* 只有配置没有直播间的平台 */}
      {platformsWithoutRooms.length > 0 && (
        <>
          <Divider style={{ fontSize: 14 }}>已配置但未监控的平台 ({platformsWithoutRooms.length})</Divider>
          {platformsWithoutRooms.map(renderPlatformCard)}
        </>
      )}

      {/* 添加新平台配置 */}
      <Divider style={{ fontSize: 14 }}>添加新平台配置</Divider>
      <Card size="small">
        <Space>
          {/* @ts-ignore */}
          <Select
            placeholder="选择平台"
            style={{ width: 200 }}
            value={selectedNewPlatform || undefined}
            onChange={setSelectedNewPlatform}
            options={available_platforms.map(p => ({ label: p, value: p }))}
          />
          <Button
            type="primary"
            // @ts-ignore
            icon={<PlusOutlined />}
            onClick={handleAddPlatform}
            disabled={!selectedNewPlatform}
          >
            添加配置
          </Button>
        </Space>
      </Card>
    </div>
  );
};

// 平台配置表单组件
const PlatformConfigForm: React.FC<{
  platform: PlatformStat;
  globalConfig: EffectiveConfig;
  globalInterval: number;
  onSave: (values: any) => void;
  onDelete: () => void;
  loading: boolean;
  onNavigateToRoom: (liveId: string) => void;
}> = ({ platform, globalConfig, globalInterval, onSave, onDelete, loading, onNavigateToRoom }) => {
  const [form] = Form.useForm();

  useEffect(() => {
    if (platform) {
      // 转换 attributes: map -> array
      const displayPlatform = {
        ...platform,
        stream_preference: (platform as any).stream_preference ? {
          ...(platform as any).stream_preference,
          attributes: (platform as any).stream_preference.attributes
            ? Object.entries((platform as any).stream_preference.attributes).map(([key, value]) => ({ key, value }))
            : []
        } : { attributes: [] }
      };
      form.setFieldsValue(displayPlatform);
    }
  }, [platform, form]);


  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      // 转换 attributes: array -> map
      const attributesArray = values.stream_preference?.attributes || [];
      const attributesMap: Record<string, string> = {};
      for (const item of attributesArray) {
        if (item.key && item.value !== undefined) {
          attributesMap[item.key] = item.value;
        }
      }
      const updatedValues = {
        ...values,
        stream_preference: values.stream_preference ? {
          quality: values.stream_preference.quality || undefined,
          attributes: Object.keys(attributesMap).length > 0 ? attributesMap : undefined
        } : undefined
      };
      onSave(updatedValues);
    } catch (error) {
      // Validation failed
    }
  };


  const effectiveInterval = platform.interval ?? globalInterval;
  const actualAccessInterval = platform.listening_count > 0
    ? effectiveInterval / platform.listening_count
    : 0;

  const platformKey = platform.platform_key;

  return (
    <div id={`platforms-${platformKey}`}>
      <Form form={form} layout="vertical">
        <ConfigField
          label="检测间隔 (秒)"
          description={`当前平台有 ${platform.listening_count} 个直播间正在监控`}
          inheritance={{
            source: 'global',
            linkTo: '#global-interval',
            isOverridden: platform.interval != null,
            inheritedValue: globalInterval
          }}
          effectiveValue={platform.listening_count > 0
            ? `对平台的平均访问间隔: ${actualAccessInterval.toFixed(1)} 秒`
            : undefined}
          warning={platform.warning_message}
          id={`platforms-${platformKey}-interval`}
        >
          <Form.Item name="interval" rules={[{ type: 'number', min: 1, message: '必须大于 0' }]}>
            <InputNumber
              min={1}
              placeholder={`继承全局: ${globalInterval}`}
              style={{ width: 200 }}
            />
          </Form.Item>
        </ConfigField>

        <ConfigField
          label="最小访问间隔 (秒)"
          description="该平台 API 的最小访问间隔，用于防风控。若监控数量过多导致频率过快，系统会自动增加检测间隔。"
          id={`platforms-${platformKey}-min_access_interval_sec`}
          inheritance={{
            source: 'default',
            isOverridden: (platform.min_access_interval_sec || 0) > 0,
            inheritedValue: '不限制 (0)',
          }}
          valueDisplay={(platform.min_access_interval_sec || 0) > 0 ? platform.min_access_interval_sec : '不限制 (0)'}
        >
          <Form.Item name="min_access_interval_sec" rules={[{ type: 'number', min: 0, message: '不能为负数' }]}>
            <InputNumber min={0} max={3600} style={{ width: 200 }} placeholder="0 (不限制)" />
          </Form.Item>
        </ConfigField>

        <ConfigField
          label="输出路径"
          inheritance={{
            source: 'global',
            linkTo: '#global-out_put_path',
            isOverridden: platform.out_put_path != null,
            inheritedValue: globalConfig?.out_put_path || './'
          }}
          effectiveValue={platform.out_put_path == null ? globalConfig?.actual_out_put_path : undefined}
          id={`platforms-${platformKey}-out_put_path`}
        >
          <Form.Item name="out_put_path" noStyle>
            <Input
              placeholder={`继承全局: ${globalConfig?.out_put_path || './'}`}
              style={{ width: 400 }}
            />
          </Form.Item>
        </ConfigField>

        <ConfigField
          label="FFmpeg 路径"
          inheritance={{
            source: 'global',
            linkTo: '/configInfo?tab=global#global-ffmpeg_path',
            isOverridden: platform.ffmpeg_path != null && platform.ffmpeg_path !== '',
            inheritedValue: getFFmpegDisplayValue(globalConfig?.ffmpeg_path)
          }}
          effectiveValue={platform.ffmpeg_path == null ? globalConfig?.actual_ffmpeg_path : undefined}
          id={`platforms-${platformKey}-ffmpeg_path`}
          useTagMode
        >
          <Form.Item name="ffmpeg_path" noStyle>
            <Input
              placeholder={`继承全局: ${getFFmpegDisplayValue(globalConfig?.ffmpeg_path)}`}
              style={{ width: 400 }}
            />
          </Form.Item>
        </ConfigField>

        <ConfigField
          label="输出文件名模板"
          inheritance={{
            source: 'global',
            linkTo: '/configInfo?tab=global#global-out_put_tmpl',
            isOverridden: (platform as any).out_put_tmpl != null,
            inheritedValue: globalConfig?.out_put_tmpl || globalConfig?.default_out_put_tmpl
          }}
          id={`platforms-${platformKey}-out_put_tmpl`}
          actions={<OutputTemplatePreview form={form} displayStyle="compact" />}
          useTagMode
        >
          <Form.Item name="out_put_tmpl" noStyle>
            <TextArea
              rows={2}
              placeholder={`继承全局: ${globalConfig?.default_out_put_tmpl}`}
              style={{ width: 500 }}
            />
          </Form.Item>
        </ConfigField>

        <ConfigField
          label="下载器类型"
          description="选择用于下载直播流的工具"
          inheritance={{
            source: 'global',
            linkTo: '/configInfo?tab=global',
            isOverridden: (platform as any).feature?.downloader_type != null && (platform as any).feature?.downloader_type !== '',
            inheritedValue: globalConfig?.feature?.downloader_type ?
              (globalConfig.feature.downloader_type === 'native' ? '原生 FLV 解析器' :
                globalConfig.feature.downloader_type === 'bililive-recorder' ? '录播姬' : 'FFmpeg')
              : 'FFmpeg (默认)'
          }}
          id={`platforms-${platformKey}-downloader_type`}
        >
          <Form.Item name={['feature', 'downloader_type']} noStyle>
            {/* @ts-ignore */}
            <Select
              style={{ width: 280 }}
              placeholder="继承全局设置"
              allowClear
            >
              <Select.Option
                value="ffmpeg"
                disabled={!globalConfig?.downloader_availability?.ffmpeg_available}
              >
                <Tooltip title={!globalConfig?.downloader_availability?.ffmpeg_available ? '未找到 FFmpeg，请先安装' : undefined}>
                  FFmpeg {!globalConfig?.downloader_availability?.ffmpeg_available && '(不可用)'}
                </Tooltip>
              </Select.Option>
              <Select.Option value="native">
                原生 FLV 解析器 (内置)
              </Select.Option>
              <Select.Option
                value="bililive-recorder"
                disabled={!globalConfig?.downloader_availability?.bililive_recorder_available}
              >
                <Tooltip title={!globalConfig?.downloader_availability?.bililive_recorder_available ? '未安装录播姬 CLI' : undefined}>
                  录播姬 {!globalConfig?.downloader_availability?.bililive_recorder_available && '(未安装)'}
                </Tooltip>
              </Select.Option>
            </Select>
          </Form.Item>
        </ConfigField>

        {/* 流偏好配置 - 平台级 */}
        <Divider style={{ fontSize: 12 }}>流偏好配置 (覆盖全局)</Divider>

        <ConfigField
          label="清晰度偏好"
          description="留空则继承全局设置"
          inheritance={{
            source: 'global',
            linkTo: '/configInfo?tab=global',
            isOverridden: !!(platform as any).stream_preference?.quality,
            inheritedValue: globalConfig?.stream_preference?.quality || '(自动选择)'
          }}
        >
          <Form.Item name={['stream_preference', 'quality']} noStyle>
            <Input
              placeholder={globalConfig?.stream_preference?.quality || '继承全局'}
              style={{ width: 300 }}
              allowClear
            />
          </Form.Item>
        </ConfigField>

        <ConfigField
          label="流属性偏好"
          description="键值对形式的流属性筛选条件，留空则继承全局设置"
        >
          <Form.List name={['stream_preference', 'attributes']}>
            {(fields, { add, remove }) => (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {fields.map(({ key, name, ...restField }) => (
                  <Space key={key} style={{ display: 'flex' }} align="baseline">
                    <Form.Item
                      {...restField}
                      name={[name, 'key']}
                      rules={[{ required: true, message: '请输入属性名' }]}
                      noStyle
                    >
                      <Input placeholder="属性名" style={{ width: 150 }} />
                    </Form.Item>
                    <span>=</span>
                    <Form.Item
                      {...restField}
                      name={[name, 'value']}
                      rules={[{ required: true, message: '请输入属性值' }]}
                      noStyle
                    >
                      <Input placeholder="属性值" style={{ width: 150 }} />
                    </Form.Item>
                    <Button
                      type="text"
                      danger
                      icon={<DeleteOutlined />}
                      onClick={() => remove(name)}
                    />
                  </Space>
                ))}
                <Button
                  type="dashed"
                  onClick={() => add({ key: '', value: '' })}
                  icon={<PlusOutlined />}
                  style={{ width: 320 }}
                >
                  添加属性
                </Button>
              </div>
            )}
          </Form.List>
        </ConfigField>
      </Form>

      <div className="config-actions">
        <Button
          type="primary"
          // @ts-ignore
          icon={<SaveOutlined />}
          onClick={handleSubmit}
          loading={loading}
        >
          保存
        </Button>
        {platform.has_config && (
          <Button
            danger
            // @ts-ignore
            icon={<DeleteOutlined />}
            onClick={onDelete}
          >
            删除配置
          </Button>
        )}
      </div>

      {/* 该平台的直播间列表 */}
      {platform.rooms.length > 0 && (
        <>
          {/* @ts-ignore */}
          <Divider>该平台的直播间 ({platform.rooms.length})</Divider>
          <List
            size="small"
            dataSource={platform.rooms}
            renderItem={(room: any) => (
              <div className="room-list-item">
                {/* @ts-ignore */}
                <div className="room-list-item-info">
                  <span className="room-list-item-name">
                    {room.nick_name || room.host_name || '未知主播'}
                  </span>
                  <span className="room-list-item-url">{room.url}</span>
                </div>
                <Space>
                  <Tag color={room.is_listening ? 'green' : 'default'}>
                    {room.is_listening ? '监控中' : '已停止'}
                  </Tag>
                  {room.live_id && (
                    <Tooltip title="跳转到直播间设置页并展开此直播间">
                      <Link to={`/configInfo?tab=rooms&room=${room.live_id}`}>
                        <Button type="link" size="small">直播间设置</Button>
                      </Link>
                    </Tooltip>
                  )}
                  {room.live_id && (
                    <Tooltip title="在首页查看及控制此直播间">
                      <Link to={`/?room=${room.live_id}`}>
                        <Button type="link" size="small">监控页</Button>
                      </Link>
                    </Tooltip>
                  )}
                </Space>
              </div>
            )}
          />
        </>
      )}
    </div>
  );
};

// 直播间配置表单组件 (可复用)
export const RoomConfigForm: React.FC<{
  room: any;
  globalConfig: EffectiveConfig;
  onSave: (updates: any) => Promise<void>;
  loading: boolean;
  onRefresh?: () => void;
  platformId?: string; // New prop for explicit platform ID
}> = ({ room, globalConfig, onSave, loading, onRefresh, platformId }) => {
  const [form] = Form.useForm();

  useEffect(() => {
    if (room) {
      // 转换 attributes: map -> array
      const displayRoom = {
        ...room,
        stream_preference: room.stream_preference ? {
          ...room.stream_preference,
          attributes: room.stream_preference.attributes
            ? Object.entries(room.stream_preference.attributes).map(([key, value]) => ({ key, value }))
            : []
        } : { attributes: [] }
      };
      form.setFieldsValue(displayRoom);
    }
  }, [room, form]);

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      // 转换 attributes: array -> map
      const attributesArray = values.stream_preference?.attributes || [];
      const attributesMap: Record<string, string> = {};
      for (const item of attributesArray) {
        if (item.key && item.value !== undefined) {
          attributesMap[item.key] = item.value;
        }
      }
      const updatedValues = {
        ...values,
        stream_preference: values.stream_preference ? {
          quality: values.stream_preference.quality || undefined,
          attributes: Object.keys(attributesMap).length > 0 ? attributesMap : undefined
        } : undefined
      };
      await onSave(updatedValues);
      message.success('直播间配置已更新');
      if (onRefresh) onRefresh();
    } catch (error: any) {
      console.error('保存直播间配置失败:', error);
      if (error?.errorFields) {
        message.error('表单校验失败，请检查输入项');
      } else {
        const errorMsg = error?.err_msg || error?.message || '未知错误';
        message.error('保存失败: ' + errorMsg);
      }
    }
  };


  // Use platformId if provided, derived from backend using raw URL usually
  // Fallback to room.address (CN Name) only if platformId is missing, but that usually fails for config lookup
  const platformKey = platformId || room.address || '';
  const platformConfig = (globalConfig?.platform_configs as any)?.[platformKey];

  return (
    <Form form={form} layout="vertical">
      <ConfigField
        label="别名"
        description="在列表中显示的名称"
        inheritance={{
          source: 'default',
          isOverridden: !!room.nick_name,
          inheritedValue: room.host_name || '主播名'
        }}
      >
        <Form.Item name="nick_name" noStyle>
          <Input placeholder="如果不填则使用主播名" style={{ width: 300 }} />
        </Form.Item>
      </ConfigField>

      <ConfigField label="启用监控">
        <Form.Item name="is_listening" valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>

      <ConfigField
        label="录制质量"
        description="0表示原画"
        inheritance={{
          source: 'default',
          // Assuming 0 is default behavior (Original Quality)
          isOverridden: room.quality !== undefined && room.quality !== 0,
          inheritedValue: '原画'
        }}
        effectiveValue={room.quality === 0 ? '原画' : undefined}
      >
        <Form.Item name="quality" noStyle>
          <InputNumber min={0} style={{ width: 150 }} placeholder="0 (原画)" />
        </Form.Item>
      </ConfigField>

      <ConfigField label="仅录制音频">
        <Form.Item name="audio_only" valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>

      <Divider style={{ margin: '12px 0' }}>配置覆盖</Divider>

      <ConfigField
        label="检测间隔 (秒)"
        inheritance={{
          source: platformConfig ? 'platform' : 'global',
          linkTo: platformConfig ? `/configInfo?tab=platforms&platform=${platformKey}` : '/configInfo?tab=global#global-interval',
          isOverridden: room.interval != null,
          inheritedValue: platformConfig?.interval ?? globalConfig?.interval
        }}
        id={`rooms-live-${room.live_id}-interval`}
      >
        <Form.Item name="interval" rules={[{ type: 'number', min: 0, message: '不能为负数' }]}>
          <InputNumber
            min={0}
            style={{ width: 200 }}
            placeholder={`继承${platformConfig ? '平台' : '全局'}: ${platformConfig?.interval ?? globalConfig?.interval}`}
          />
        </Form.Item>
      </ConfigField>

      <ConfigField
        label="输出路径"
        inheritance={{
          source: platformConfig ? 'platform' : 'global',
          linkTo: platformConfig ? `/configInfo?tab=platforms&platform=${platformKey}` : '/configInfo?tab=global#global-out_put_path',
          isOverridden: room.out_put_path != null,
          inheritedValue: platformConfig?.out_put_path ?? globalConfig?.out_put_path ?? './'
        }}
        effectiveValue={room.effective_out_put_path ?? (platformConfig?.out_put_path ?? globalConfig?.actual_out_put_path)}
        id={`rooms-live-${room.live_id}-out_put_path`}
      >
        <Form.Item name="out_put_path" noStyle>
          <Input
            placeholder={`继承${platformConfig ? '平台' : '全局'}: ${platformConfig?.out_put_path ?? globalConfig?.out_put_path ?? './'}`}
            style={{ width: 400 }}
          />
        </Form.Item>
      </ConfigField>

      <ConfigField
        label="FFmpeg 路径"
        inheritance={getFFmpegInheritance(
          'room',
          room.ffmpeg_path,
          platformConfig?.ffmpeg_path,
          globalConfig?.ffmpeg_path,
          platformKey
        )}
        effectiveValue={room.effective_ffmpeg_path ?? (platformConfig?.ffmpeg_path ?? globalConfig?.actual_ffmpeg_path)}
        id={`rooms-live-${room.live_id}-ffmpeg_path`}
        useTagMode
      >
        <Form.Item name="ffmpeg_path" noStyle>
          <Input
            placeholder={`继承${(platformConfig?.ffmpeg_path) ? '平台' : '全局'}: ${getFFmpegDisplayValue(platformConfig?.ffmpeg_path, globalConfig?.ffmpeg_path)}`}
            style={{ width: 400 }}
          />
        </Form.Item>
      </ConfigField>

      <ConfigField
        label="输出文件名模板"
        inheritance={{
          source: (platformConfig as any)?.out_put_tmpl ? 'platform' : 'global',
          linkTo: (platformConfig as any)?.out_put_tmpl ? `/configInfo?tab=platforms&platform=${platformKey}` : '/configInfo?tab=global#global-out_put_tmpl',
          isOverridden: room.out_put_tmpl != null,
          inheritedValue: (platformConfig as any)?.out_put_tmpl || globalConfig?.out_put_tmpl || globalConfig?.default_out_put_tmpl
        }}
        id={`rooms-live-${room.live_id}-out_put_tmpl`}
        actions={<OutputTemplatePreview form={form} displayStyle="compact" />}
        useTagMode
      >
        <Form.Item name="out_put_tmpl" noStyle>
          <TextArea
            rows={2}
            placeholder={`继承${(platformConfig as any)?.out_put_tmpl ? '平台' : '全局'}: ${(platformConfig as any)?.out_put_tmpl || globalConfig?.default_out_put_tmpl}`}
            style={{ width: 500 }}
          />
        </Form.Item>
      </ConfigField>

      <ConfigField
        label="下载器类型"
        description="选择用于下载直播流的工具"
        inheritance={{
          source: (platformConfig as any)?.feature?.downloader_type ? 'platform' : 'global',
          linkTo: (platformConfig as any)?.feature?.downloader_type ? `/configInfo?tab=platforms&platform=${platformKey}` : '/configInfo?tab=global',
          isOverridden: room.feature?.downloader_type != null && room.feature?.downloader_type !== '',
          inheritedValue: (() => {
            const inheritedType = (platformConfig as any)?.feature?.downloader_type || globalConfig?.feature?.downloader_type;
            if (inheritedType === 'native') return '原生 FLV 解析器';
            if (inheritedType === 'bililive-recorder') return '录播姬';
            return 'FFmpeg (默认)';
          })()
        }}
        id={`rooms-live-${room.live_id}-downloader_type`}
      >
        <Form.Item name={['feature', 'downloader_type']} noStyle>
          {/* @ts-ignore */}
          <Select
            style={{ width: 280 }}
            placeholder={`继承${(platformConfig as any)?.feature?.downloader_type ? '平台' : '全局'}设置`}
            allowClear
          >
            <Select.Option
              value="ffmpeg"
              disabled={!globalConfig?.downloader_availability?.ffmpeg_available}
            >
              <Tooltip title={!globalConfig?.downloader_availability?.ffmpeg_available ? '未找到 FFmpeg' : undefined}>
                FFmpeg {!globalConfig?.downloader_availability?.ffmpeg_available && '(不可用)'}
              </Tooltip>
            </Select.Option>
            <Select.Option value="native">
              原生 FLV 解析器 (内置)
            </Select.Option>
            <Select.Option
              value="bililive-recorder"
              disabled={!globalConfig?.downloader_availability?.bililive_recorder_available}
            >
              <Tooltip title={!globalConfig?.downloader_availability?.bililive_recorder_available ? '未安装录播姬 CLI' : undefined}>
                录播姬 {!globalConfig?.downloader_availability?.bililive_recorder_available && '(未安装)'}
              </Tooltip>
            </Select.Option>
          </Select>
        </Form.Item>
      </ConfigField>


      {/* 流偏好配置 - 房间级 */}
      <Divider style={{ fontSize: 12, margin: '12px 0' }}>流偏好配置 (覆盖平台/全局)</Divider>

      <ConfigField
        label="清晰度偏好"
        description="留空则继承平台/全局设置"
        inheritance={{
          source: (platformConfig as any)?.stream_preference?.quality ? 'platform' : 'global',
          linkTo: (platformConfig as any)?.stream_preference?.quality
            ? `/configInfo?tab=platforms&platform=${platformKey}`
            : '/configInfo?tab=global',
          isOverridden: !!room.stream_preference?.quality,
          inheritedValue: (platformConfig as any)?.stream_preference?.quality || globalConfig?.stream_preference?.quality || '(自动选择)'
        }}
      >
        <Form.Item name={['stream_preference', 'quality']} noStyle>
          <Input
            placeholder={(platformConfig as any)?.stream_preference?.quality || globalConfig?.stream_preference?.quality || '继承上级'}
            style={{ width: 300 }}
            allowClear
          />
        </Form.Item>
      </ConfigField>

      <ConfigField
        label="流属性偏好"
        description="键值对形式的流属性筛选条件，留空则继承平台/全局设置"
      >
        <Form.List name={['stream_preference', 'attributes']}>
          {(fields, { add, remove }) => (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {fields.map(({ key, name, ...restField }) => (
                <Space key={key} style={{ display: 'flex' }} align="baseline">
                  <Form.Item
                    {...restField}
                    name={[name, 'key']}
                    rules={[{ required: true, message: '请输入属性名' }]}
                    noStyle
                  >
                    <Input placeholder="属性名" style={{ width: 150 }} />
                  </Form.Item>
                  <span>=</span>
                  <Form.Item
                    {...restField}
                    name={[name, 'value']}
                    rules={[{ required: true, message: '请输入属性值' }]}
                    noStyle
                  >
                    <Input placeholder="属性值" style={{ width: 150 }} />
                  </Form.Item>
                  <Button
                    type="text"
                    danger
                    icon={<DeleteOutlined />}
                    onClick={() => remove(name)}
                  />
                </Space>
              ))}
              <Button
                type="dashed"
                onClick={() => add({ key: '', value: '' })}
                icon={<PlusOutlined />}
                style={{ width: 320 }}
              >
                添加属性
              </Button>
            </div>
          )}
        </Form.List>
      </ConfigField>

      <div className="config-actions" style={{ marginTop: 16 }}>
        <Button
          type="primary"
          // @ts-ignore
          icon={<SaveOutlined />}
          onClick={handleSubmit}
          loading={loading}
        >
          保存直播间配置
        </Button>
        {/* @ts-ignore */}
        <Link to={`/configInfo?tab=platforms&platform=${platformKey}`}>
          <Button
            // @ts-ignore
            icon={<AppstoreOutlined />}
          >
            查看所属平台设置
          </Button>
        </Link>
      </div>
    </Form>
  );
};

// 后增的直播间设置整体面板
const RoomSettings: React.FC<{
  platformStats: PlatformStatsResponse | null;
  globalConfig: EffectiveConfig;
  onUpdate: (liveId: string, updates: any) => Promise<void>;
  loading: boolean;
  onRefresh: () => void;
}> = ({ platformStats, globalConfig, onUpdate, loading, onRefresh }) => {
  const [expandedKeys, setExpandedKeys] = useState<string[]>([]);
  const [searchText, setSearchText] = useState('');

  const location = useLocation();

  useEffect(() => {
    const handleExpand = () => {
      const searchParams = new URLSearchParams(location.search);
      const roomIdP = searchParams.get('room');

      const hash = location.hash;
      let roomIdH = '';
      if (hash.startsWith('#rooms-live-')) {
        roomIdH = hash.replace('#rooms-live-', '');
      }

      const targetId = roomIdP || roomIdH;
      if (targetId) {
        setExpandedKeys(prev => prev.includes(`live-${targetId}`) ? prev : [...prev, `live-${targetId}`]);
      }
    };

    handleExpand();
  }, [location]);

  if (!platformStats) return <Spin />;

  const allRooms = platformStats.platforms.flatMap(p => p.rooms.map(r => ({ ...r, platform_name: p.platform_name || p.platform_key, address: p.platform_key })));

  const filteredRooms = allRooms.filter(r =>
    (r.nick_name || r.host_name || '').toLowerCase().includes(searchText.toLowerCase()) ||
    r.url.toLowerCase().includes(searchText.toLowerCase()) ||
    r.address.toLowerCase().includes(searchText.toLowerCase())
  );

  return (
    <div className="config-content">
      <div style={{ marginBottom: 16 }}>
        <Input.Search
          placeholder="搜索主播名、URL或平台"
          allowClear
          onChange={e => setSearchText(e.target.value)}
          style={{ width: 400 }}
        />
      </div>

      {/* @ts-ignore */}
      <Collapse
        activeKey={expandedKeys}
        onChange={keys => setExpandedKeys(keys as string[])}
      >
        {filteredRooms.map(room => (
          // @ts-ignore
          <Panel
            key={`live-${room.live_id}`}
            header={
              <div id={`rooms-live-${room.live_id}`} style={{ display: 'flex', justifyContent: 'space-between', width: '100%', paddingRight: 24 }}>
                <Space>
                  <span style={{ fontWeight: 600 }}>{room.nick_name || room.host_name || '未知主播'}</span>
                  <Tag>{room.platform_name}</Tag>
                  <Tag color={room.is_listening ? 'green' : 'default'}>{room.is_listening ? '监控中' : '已停止'}</Tag>
                </Space>
                <span style={{ fontSize: 12, color: '#999' }}>{room.url}</span>
              </div>
            }
          >
            <RoomConfigForm
              room={room}
              globalConfig={globalConfig}
              onSave={(updates) => onUpdate(room.live_id, updates)}
              loading={loading}
              onRefresh={onRefresh}
            />
          </Panel>
        ))}
      </Collapse>
      {filteredRooms.length === 0 && (
        <div style={{ textAlign: 'center', padding: '40px', color: '#999' }}>未找到匹配的直播间</div>
      )}
    </div>
  );
};

// 主配置页面组件
const ConfigInfo: React.FC = () => {
  const [mode, setMode] = useState<'gui' | 'yaml'>('gui');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [effectiveConfig, setEffectiveConfig] = useState<EffectiveConfig | null>(null);
  const [platformStats, setPlatformStats] = useState<PlatformStatsResponse | null>(null);
  const [rawConfig, setRawConfig] = useState('');
  const [activeTab, setActiveTab] = useState('global');

  // 加载配置
  const loadConfig = useCallback(async () => {
    setLoading(true);
    try {
      const [effective, platforms, raw] = await Promise.all([
        api.getEffectiveConfig(),
        api.getPlatformStats(),
        api.getConfigInfo()
      ]);
      setEffectiveConfig(effective as EffectiveConfig);
      setPlatformStats(platforms as PlatformStatsResponse);
      setRawConfig((raw as any).config || '');
    } catch (error) {
      message.error('加载配置失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  // 处理 Hash 路由
  // 处理路由导航 (Hash & Search Params)
  const location = useLocation();
  useEffect(() => {
    const handleNavigation = () => {
      // 优先解析 Search Params
      const searchParams = new URLSearchParams(location.search);
      let tab = searchParams.get('tab');

      // 兼容旧的 Hash 路由
      const hash = location.hash;
      if (!tab && hash) {
        if (hash.startsWith('#global')) tab = 'global';
        else if (hash.startsWith('#platforms')) tab = 'platforms';
        else if (hash.startsWith('#rooms')) tab = 'rooms';
        else if (hash.startsWith('#notify')) tab = 'notify';
      }

      if (tab && ['global', 'platforms', 'rooms', 'notify'].includes(tab)) {
        setActiveTab(tab);
      }

      // 尝试定位元素 (Scrolling)
      // 如果有 param id (platform=xxx or room=xxx)，或者是 hash element id
      setTimeout(() => {
        let elementId = '';

        // 1. Check params
        const platformKey = searchParams.get('platform');
        const roomId = searchParams.get('room');

        if (tab === 'platforms' && platformKey) {
          elementId = `platforms-${platformKey}`;
        } else if (tab === 'rooms' && roomId) {
          elementId = `rooms-live-${roomId}`;
        } else if (hash.startsWith('#')) {
          elementId = hash.substring(1);
        }

        if (elementId) {
          const element = document.getElementById(elementId);
          if (element) {
            element.scrollIntoView({ behavior: 'smooth', block: 'center' });
            element.classList.add('config-item-highlight');
            setTimeout(() => element.classList.remove('config-item-highlight'), 2000);
          }
        }
      }, 500);
    };

    handleNavigation();
  }, [location, platformStats]); // 监听 location 变化


  // 更新全局配置
  const handleUpdateConfig = async (updates: any) => {
    setSaving(true);
    try {
      await api.updateConfig(updates);
      await loadConfig();
    } finally {
      setSaving(false);
    }
  };

  // 更新平台配置
  const handleUpdatePlatformConfig = async (platformKey: string, updates: any) => {
    setSaving(true);
    try {
      await api.updatePlatformConfig(platformKey, updates);
      await loadConfig();
    } finally {
      setSaving(false);
    }
  };

  // 更新直播间配置
  const handleUpdateRoomConfig = async (liveId: string, updates: any) => {
    setSaving(true);
    try {
      await api.updateRoomConfigById(liveId, updates);
      await loadConfig();
    } finally {
      setSaving(false);
    }
  };

  // 删除平台配置
  const handleDeletePlatformConfig = async (platformKey: string) => {
    setSaving(true);
    try {
      await api.deletePlatformConfig(platformKey);
      await loadConfig();
    } finally {
      setSaving(false);
    }
  };

  // 保存 YAML 配置
  const handleSaveYaml = async () => {
    setSaving(true);
    try {
      await api.saveRawConfig({ config: rawConfig });
      message.success('配置已保存');
      await loadConfig();
    } catch (error) {
      message.error('保存失败');
    } finally {
      setSaving(false);
    }
  };

  // GUI 模式内容
  const renderGuiMode = () => (
    // @ts-ignore
    <Tabs
      activeKey={activeTab}
      onChange={setActiveTab}
      tabPosition="left"
      style={{ minHeight: 400 }}
      items={[
        {
          key: 'global',
          label: (
            <span>
              <GlobalOutlined /> 全局设置
            </span>
          ),
          children: effectiveConfig ? (
            <GlobalSettings
              config={effectiveConfig}
              onUpdate={handleUpdateConfig}
              loading={saving}
            />
          ) : <Spin />
        },
        {
          key: 'platforms',
          label: (
            <span>
              <AppstoreOutlined /> 平台设置
              <Badge
                count={platformStats?.platforms.length || 0}
                style={{ marginLeft: 8 }}
              />
            </span>
          ),
          children: effectiveConfig ? (
            <PlatformSettings
              platformStats={platformStats}
              globalConfig={effectiveConfig}
              onUpdate={handleUpdatePlatformConfig}
              onDelete={handleDeletePlatformConfig}
              loading={saving}
              onRefresh={loadConfig}
            />
          ) : <Spin />
        },
        {
          key: 'rooms',
          label: (
            <span>
              <EditOutlined /> 直播间设置
              <Badge
                count={effectiveConfig?.live_rooms_count || 0}
                style={{ marginLeft: 8 }}
                color="#108ee9"
              />
            </span>
          ),
          children: effectiveConfig ? (
            <RoomSettings
              platformStats={platformStats}
              globalConfig={effectiveConfig}
              onUpdate={handleUpdateRoomConfig}
              loading={saving}
              onRefresh={loadConfig}
            />
          ) : <Spin />
        },
        {
          key: 'notify',
          label: (
            <span>
              <BellOutlined /> 通知服务
            </span>
          ),
          children: effectiveConfig ? (
            <NotifySettings
              config={effectiveConfig}
              onUpdate={handleUpdateConfig}
              loading={saving}
            />
          ) : <Spin />
        }
      ]}
    />
  );

  // YAML 模式内容
  const renderYamlMode = () => (
    <div className="config-content">
      <Editor
        value={rawConfig}
        onValueChange={code => setRawConfig(code)}
        highlight={code => highlight(code, languages.yaml, 'yaml')}
        padding={10}
        style={{
          fontFamily: '"Fira code", "Fira Mono", monospace',
          fontSize: 14,
          border: '1px solid #d9d9d9',
          borderRadius: 4,
          minHeight: 400
        }}
      />
      <div className="config-actions">
        <Button
          type="primary"
          icon={<SaveOutlined />}
          onClick={handleSaveYaml}
          loading={saving}
        >
          保存配置
        </Button>
      </div>
    </div>
  );

  return (
    <div className="config-gui-container">
      <div className="config-gui-header">
        {/* @ts-ignore */}
        <div>
          <span className="config-gui-title">设置</span>
          <span className="config-gui-subtitle">Settings</span>
        </div>
        <Space>
          <Button
            icon={<ReloadOutlined />}
            onClick={loadConfig}
            loading={loading}
          >
            刷新
          </Button>
        </Space>
      </div>

      {/* @ts-ignore */}
      <Tabs
        activeKey={mode}
        onChange={key => setMode(key as 'gui' | 'yaml')}
        type="card"
        className="config-mode-tabs"
        style={{ padding: '0 16px' }}
        items={[
          {
            key: 'gui',
            label: (
              <span>
                <SettingOutlined /> GUI 模式
              </span>
            ),
            children: loading ? (
              <div className="config-loading">
                <Spin size="large" />
              </div>
            ) : renderGuiMode()
          },
          {
            key: 'yaml',
            label: (
              <span>
                <EditOutlined /> YAML 模式
              </span>
            ),
            children: loading ? (
              <div className="config-loading">
                <Spin size="large" />
              </div>
            ) : renderYamlMode()
          }
        ]}
      />
    </div>
  );
};

export default ConfigInfo;
