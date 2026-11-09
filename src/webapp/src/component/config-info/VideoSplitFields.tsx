import React from 'react';
import { Alert, Form, Input, InputNumber, Switch } from 'antd';
import type { FormInstance } from 'antd';

const VideoSplitFields: React.FC<{ form: FormInstance; source: string }> = ({ form, source }) => {
  const enabled = Form.useWatch('video_split_override', form);
  return (
    <>
      <Form.Item label="覆盖视频分片策略" name="video_split_override" valuePropName="checked"
        extra={`关闭后继承${source}的整组策略；开启后可用 false / 0 禁用分片。保存后从下一次录制启动起使用新策略。`}>
        <Switch />
      </Form.Item>
      <Form.Item label="房间标题变化时分片" name={['video_split_strategies', 'on_room_name_changed']} valuePropName="checked">
        <Switch disabled={!enabled} />
      </Form.Item>
      <Form.Item label="最大分片时长（秒，0 为不限）" name={['video_split_strategies', 'max_duration']}
        rules={enabled ? [{ type: 'number', min: 0, message: '时长不能为负数' }] : []}>
        <InputNumber min={0} disabled={!enabled} />
      </Form.Item>
      <Form.Item label="最大分片大小（如 1GB、500MB，0 为不限）" name={['video_split_strategies', 'max_file_size']}
        rules={enabled ? [{ pattern: /^\s*\d+(\.\d+)?\s*(B|KB|MB|GB|TB)?\s*$/i, message: '请输入有效的文件大小' }] : []}>
        <Input disabled={!enabled} style={{ maxWidth: 300 }} />
      </Form.Item>
      <Alert type="info" showIcon style={{ marginBottom: 16 }}
        message="原生 FLV 下载器不支持按最大时长或最大文件大小分片，需要这些功能时请选择 FFmpeg 或录播姬。" />
    </>
  );
};

export default VideoSplitFields;
