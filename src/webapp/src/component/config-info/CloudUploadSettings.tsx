import React from 'react';
import { Card, Form, Switch, Select, Input, Tag, Alert } from 'antd';
import { CloudUploadOutlined } from '@ant-design/icons';

const { TextArea } = Input;

interface ConfigFieldProps {
  label: string;
  description?: string;
  children: React.ReactElement;
}

// 简化版 ConfigField 组件
const ConfigField: React.FC<ConfigFieldProps> = ({ label, description, children }) => (
  <div className="config-item" style={{ marginBottom: 16 }}>
    <div className="config-item-label" style={{ marginBottom: 4, fontWeight: 500 }}>{label}</div>
    <div className="config-item-content">
      <div className="config-item-input">{children}</div>
      {description && (
        <div className="config-item-description" style={{ marginTop: 4, color: '#888', fontSize: 12 }}>
          {description}
        </div>
      )}
    </div>
  </div>
);

interface CloudUploadSettingsProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  config: any;
}

/**
 * 云盘上传设置组件
 * 用于 GlobalSettings 中显示云上传配置
 */
const CloudUploadSettings: React.FC<CloudUploadSettingsProps> = ({ config }) => {
  const isEnabled = config.on_record_finished?.cloud_upload?.enable;

  return (
    <Card
      title={<><CloudUploadOutlined /> 云盘上传</>}
      size="small"
      style={{ marginBottom: 16 }}
      extra={
        <Tag color={isEnabled ? 'green' : 'default'}>
          {isEnabled ? '已启用' : '未启用'}
        </Tag>
      }
    >
      <Alert
        message="云盘自动上传功能"
        description={
          <>
            录制完成后自动上传到网盘。需要先在{' '}
            <a href="/remotetools/tool/openlist/" target="_blank" rel="noopener noreferrer">
              OpenList 管理页面
            </a>{' '}
            配置网盘存储。
          </>
        }
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />
      <ConfigField
        label="启用云上传"
        description="开启后录制完成的视频会自动上传到配置的网盘"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'enable']} valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="上传时机"
        description="选择何时开始上传：立即上传原始文件，或等待后处理（修复/转码）完成后上传"
      >
        <Form.Item name={['on_record_finished', 'upload_timing']} noStyle>
          <Select style={{ width: 250 }} placeholder="选择上传时机">
            <Select.Option value="">使用默认（立即）</Select.Option>
            <Select.Option value="immediate">立即上传原始文件</Select.Option>
            <Select.Option value="after_process">后处理完成后上传</Select.Option>
          </Select>
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="存储名称"
        description="在 OpenList 中配置的存储名称，例如：115、阿里云盘"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'storage_name']} noStyle>
          <Input placeholder="例如: 115" style={{ width: 200 }} />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="上传路径模板"
        description='支持变量: {{ .Platform }}, {{ .HostName }}, {{ .RoomName }}, {{ .Ext }}, {{ now | date "2006-01-02" }}'
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'upload_path_tmpl']} noStyle>
          <TextArea
            rows={2}
            placeholder='/录播归档/{{ .Platform }}/{{ .HostName }}/{{ .RoomName }}-{{ now | date "2006-01-02" }}.{{ .Ext }}'
            style={{ width: 500 }}
          />
        </Form.Item>
      </ConfigField>
      <ConfigField
        label="上传后删除本地文件"
        description="上传成功后自动删除本地文件以节省空间"
      >
        <Form.Item name={['on_record_finished', 'cloud_upload', 'delete_after_upload']} valuePropName="checked" noStyle>
          <Switch />
        </Form.Item>
      </ConfigField>
    </Card>
  );
};

export default CloudUploadSettings;
