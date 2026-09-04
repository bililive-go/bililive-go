import React, { useState, useEffect, useCallback, useMemo } from 'react';
import {
  Card, Form, Switch, InputNumber, Select, Button, message, Spin, Collapse, Tag, Popconfirm, Space, Input
} from 'antd';
import { UndoOutlined } from '@ant-design/icons';
import API from '../../utils/api';

const api = new API();

// 字幕烧录：视频编码器选项
const BURN_CODEC_OPTIONS = [
  { label: 'libx264 (H.264，兼容性好)', value: 'libx264' },
  { label: 'libx265 (H.265，压缩率高)', value: 'libx265' },
  { label: 'h264_nvenc (NVIDIA H.264 硬件编码，速度快)', value: 'h264_nvenc' },
  { label: 'hevc_nvenc (NVIDIA H.265 硬件编码，压缩率更高)', value: 'hevc_nvenc' },
  { label: 'av1_nvenc (NVIDIA AV1 硬件编码，需 RTX 40 系及以上)', value: 'av1_nvenc' },
];

// 与后端 isNvencCodec 保持一致的判断逻辑：编码器名包含 nvenc 即视为 NVIDIA 硬件编码器
// （h264_nvenc / hevc_nvenc / av1_nvenc 等），保证通过 YAML/API 写入的编码器
// 在界面上同样按 NVENC 处理（CQ 标签、p1-p7 预设），避免预设被静默改写
const isNvencCodec = (codec?: string) =>
  typeof codec === 'string' && codec.toLowerCase().includes('nvenc');

// 字幕烧录：libx264/libx265 编码预设
const X264_PRESET_OPTIONS = [
  { label: 'ultrafast (最快，画质最差)', value: 'ultrafast' },
  { label: 'superfast', value: 'superfast' },
  { label: 'veryfast', value: 'veryfast' },
  { label: 'faster', value: 'faster' },
  { label: 'fast', value: 'fast' },
  { label: 'medium (默认)', value: 'medium' },
  { label: 'slow', value: 'slow' },
  { label: 'slower', value: 'slower' },
  { label: 'veryslow (最慢，画质最好)', value: 'veryslow' },
];

// 字幕烧录：NVIDIA NVENC 编码预设（p1-p7，适用于 h264_nvenc/hevc_nvenc/av1_nvenc 等）
const NVENC_PRESET_OPTIONS = [
  { label: 'p1 (NVENC 最快，画质最差)', value: 'p1' },
  { label: 'p2 (NVENC)', value: 'p2' },
  { label: 'p3 (NVENC)', value: 'p3' },
  { label: 'p4 (NVENC)', value: 'p4' },
  { label: 'p5 (NVENC 常用)', value: 'p5' },
  { label: 'p6 (NVENC)', value: 'p6' },
  { label: 'p7 (NVENC 最慢，画质最好)', value: 'p7' },
];

// 切换编码器后，预设不属于该编码器时使用的默认档位
const DEFAULT_PRESET_BY_CODEC: Record<string, string> = {
  libx264: 'medium',
  libx265: 'medium',
};
// NVENC 系列编码器（含未在选项列表中的自定义名称）的默认预设
const NVENC_DEFAULT_PRESET = 'p5';

const DEFAULT_DANMAKU: DanmakuConfig = {
  font_size: 36,
  font_name: 'Microsoft YaHei',
  scroll_area: 'full',
  scroll_time: 10,
  resolution: '1920x1080',
  outline: 1,
  opacity: 128,
  record_gift: true,
  record_douyu_gift: true,
  record_douyin_gift: true,
  record_guard: true,
  record_super_chat: true,
  guard_position: 'bottom-left',
  sc_position: 'bottom-left',
};

interface DanmakuConfig {
  font_size: number;
  font_name: string;
  scroll_area: string;
  scroll_time: number;
  resolution: string;
  outline: number;
  opacity: number;
  record_gift: boolean;
  record_douyu_gift: boolean;
  record_douyin_gift: boolean;
  record_guard: boolean;
  record_super_chat: boolean;
  guard_position: string;
  sc_position: string;
}

interface EffectiveConfig {
  danmaku_enable: boolean;
  danmaku: DanmakuConfig;
  platform_configs?: Record<string, any>;
  on_record_finished?: {
    burn_subtitles?: boolean;
    burn_subtitles_codec?: string;
    burn_subtitles_crf?: string;
    burn_subtitles_preset?: string;
    burn_delete_ass?: boolean;
    burn_delete_source?: boolean;
  };
}

interface RoomInfo {
  live_id: string;
  url: string;
  host_name?: string;
  room_name?: string;
  platform_key?: string;
  room_config?: {
    danmaku_enable?: boolean;
    danmaku?: Partial<DanmakuConfig>;
    [key: string]: any;
  };
}

const DanmakuParamForm: React.FC<{
  initialValues?: Partial<DanmakuConfig> | null;
  globalDefaults?: DanmakuConfig;
  onSave: (values: any) => Promise<void>;
  onReset?: () => Promise<void>;
  loading?: boolean;
  showEnable?: boolean;
  danmakuEnable?: boolean;
  label?: string;
  isRoom?: boolean;
  showBilibiliContent?: boolean;
  showDouyuContent?: boolean;
  showDouyinContent?: boolean;
}> = ({ initialValues, globalDefaults, onSave, onReset, loading, showEnable, danmakuEnable, label, isRoom, showBilibiliContent, showDouyuContent, showDouyinContent }) => {
  const [form] = Form.useForm();

  const baseDefaults = useMemo(() => globalDefaults || DEFAULT_DANMAKU, [globalDefaults]);

  useEffect(() => {
    form.setFieldsValue({
      danmaku_enable: danmakuEnable ?? false,
      danmaku: {
        ...baseDefaults,
        ...initialValues,
      },
    });
  }, [initialValues, danmakuEnable, form, baseDefaults]);

  const handleSave = async () => {
    try {
      const values = await form.validateFields();
      await onSave(values);
      message.success(`${label || '弹幕'}配置已保存`);
    } catch (error: any) {
      if (error?.errorFields) {
        message.error('表单校验失败，请检查输入项');
      } else {
        message.error('保存失败: ' + (error?.message || '未知错误'));
      }
    }
  };

  const handleReset = async () => {
    if (isRoom && onReset) {
      // Room: clear override, inherit from global
      await onReset();
    } else {
      // Global: reset to hardcoded defaults and save
      form.setFieldsValue({
        danmaku_enable: false,
        danmaku: { ...DEFAULT_DANMAKU },
      });
      try {
        await onSave({
          danmaku_enable: false,
          danmaku: { ...DEFAULT_DANMAKU },
        });
        message.success('已恢复默认配置');
      } catch (error: any) {
        message.error('恢复默认失败: ' + (error?.message || '未知错误'));
      }
    }
  };

  return (
    <Form form={form} layout="vertical">
      {showEnable && (
        <Form.Item label="录制弹幕" name="danmaku_enable" valuePropName="checked">
          <Switch />
        </Form.Item>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 24px' }}>
        <Form.Item
          label={<span>字体大小 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>弹幕文字的像素大小，12~120</span></span>}
          name={['danmaku', 'font_size']}
          rules={[{ required: true, message: '必填' }, { type: 'number', min: 12, max: 120, message: '12~120' }]}>
          <InputNumber min={12} max={120} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item
          label={<span>字体名称 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>播放器需安装该字体才能正确显示</span></span>}
          name={['danmaku', 'font_name']}
          rules={[{ required: true, message: '不能为空' }]}>
          <Select style={{ width: '100%' }} showSearch options={[
            { label: '微软雅黑 (Microsoft YaHei)', value: 'Microsoft YaHei' },
            { label: '黑体 (SimHei)', value: 'SimHei' },
            { label: '宋体 (SimSun)', value: 'SimSun' },
            { label: '楷体 (KaiTi)', value: 'KaiTi' },
            { label: '仿宋 (FangSong)', value: 'FangSong' },
            { label: '思源黑体 (Source Han Sans)', value: 'Source Han Sans' },
            { label: '思源宋体 (Source Han Serif)', value: 'Source Han Serif' },
            { label: '等线 (DengXian)', value: 'DengXian' },
            { label: '霞鹜文楷 (LXGW WenKai)', value: 'LXGW WenKai' },
            { label: 'Arial', value: 'Arial' },
            { label: 'Arial Black', value: 'Arial Black' },
            { label: 'Consolas', value: 'Consolas' },
            { label: 'Segoe UI', value: 'Segoe UI' },
          ]} />
        </Form.Item>
        <Form.Item
          label={<span>滚动区域 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>弹幕在屏幕上的滚动范围</span></span>}
          name={['danmaku', 'scroll_area']}>
          <Select options={[
            { label: '全屏', value: 'full' },
            { label: '顶部半屏', value: 'top' },
            { label: '底部半屏', value: 'bottom' },
            { label: '1/4屏', value: 'quarter' },
            { label: '3/4屏', value: 'three-quarter' },
          ]} />
        </Form.Item>
        <Form.Item
          label={<span>滚动时间 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>弹幕滚过整屏的秒数，越短越快</span></span>}
          name={['danmaku', 'scroll_time']}
          rules={[{ required: true, message: '必填' }, { type: 'number', min: 5, max: 20, message: '5~20' }]}>
          <InputNumber min={5} max={20} style={{ width: '100%' }} addonAfter="秒" />
        </Form.Item>
        <Form.Item
          label={<span>播放分辨率 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>ASS 字幕的画布尺寸，建议与视频分辨率一致</span></span>}
          name={['danmaku', 'resolution']}>
          <Select options={[
            { label: '1920x1080 (1080p)', value: '1920x1080' },
            { label: '1280x720 (720p)', value: '1280x720' },
            { label: '2560x1440 (2K)', value: '2560x1440' },
            { label: '3840x2160 (4K)', value: '3840x2160' },
          ]} />
        </Form.Item>
        <Form.Item
          label={<span>描边粗细 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>文字外轮廓线宽度，0 为无描边</span></span>}
          name={['danmaku', 'outline']}
          rules={[{ type: 'number', min: 0, max: 4, message: '0~4' }]}>
          <InputNumber min={0} max={4} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item
          label={<span>背景透明度 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>弹幕文字背景的不透明程度，0 完全透明，255 完全不透明</span></span>}
          name={['danmaku', 'opacity']}
          rules={[{ type: 'number', min: 0, max: 255, message: '0~255' }]}>
          <InputNumber min={0} max={255} style={{ width: '100%' }} />
        </Form.Item>
      </div>

      {showBilibiliContent && (
        <Collapse
          defaultActiveKey={[]}
          size="small"
          style={{ marginBottom: 16 }}
          items={[{
            key: 'bilibili-content',
            label: '哔哩哔哩录制内容',
            children: (
              <>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 24px' }}>
                  <Form.Item
                    label={<span>礼物消息 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>用户赠送礼物的通知</span></span>}
                    name={['danmaku', 'record_gift']} valuePropName="checked">
                    <Switch />
                  </Form.Item>
                  <Form.Item
                    label={<span>上舰消息 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>开通舰长/提督/总督的通知</span></span>}
                    name={['danmaku', 'record_guard']} valuePropName="checked">
                    <Switch />
                  </Form.Item>
                  <Form.Item
                    label={<span>醒目留言 (SC) <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>Super Chat 付费留言</span></span>}
                    name={['danmaku', 'record_super_chat']} valuePropName="checked">
                    <Switch />
                  </Form.Item>
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 24px' }}>
                  <Form.Item
                    label="上舰消息位置"
                    name={['danmaku', 'guard_position']}>
                    <Select options={[
                      { label: '左下角', value: 'bottom-left' },
                      { label: '右下角', value: 'bottom-right' },
                      { label: '左上角', value: 'top-left' },
                      { label: '右上角', value: 'top-right' },
                    ]} />
                  </Form.Item>
                  <Form.Item
                    label="SC 消息位置"
                    name={['danmaku', 'sc_position']}>
                    <Select options={[
                      { label: '左下角', value: 'bottom-left' },
                      { label: '右下角', value: 'bottom-right' },
                      { label: '左上角', value: 'top-left' },
                      { label: '右上角', value: 'top-right' },
                    ]} />
                  </Form.Item>
                </div>
              </>
            ),
          }]}
        />
      )}

      {showDouyuContent && (
        <Collapse
          defaultActiveKey={[]}
          size="small"
          style={{ marginBottom: 16 }}
          items={[{
            key: 'douyu-content',
            label: '斗鱼录制内容',
            children: (
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 24px' }}>
                <Form.Item
                  label={<span>礼物消息 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>用户赠送礼物的通知</span></span>}
                  name={['danmaku', 'record_douyu_gift']} valuePropName="checked">
                  <Switch />
                </Form.Item>
              </div>
            ),
          }]}
        />
      )}

      {showDouyinContent && (
        <Collapse
          defaultActiveKey={[]}
          size="small"
          style={{ marginBottom: 16 }}
          items={[{
            key: 'douyin-content',
            label: '抖音录制内容',
            children: (
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 24px' }}>
                <Form.Item
                  label={<span>礼物消息 <span style={{ fontWeight: 400, fontSize: 12, color: '#999' }}>用户赠送礼物的通知</span></span>}
                  name={['danmaku', 'record_douyin_gift']} valuePropName="checked">
                  <Switch />
                </Form.Item>
              </div>
            ),
          }]}
        />
      )}
      <Form.Item style={{ marginBottom: 0 }}>
        <Space>
          <Button type="primary" onClick={handleSave} loading={loading}>
            保存
          </Button>
          <Popconfirm
            title={isRoom ? '恢复为全局默认？' : '恢复为系统默认？'}
            description={isRoom ? '将清除房间级弹幕配置，使用全局设置' : '将重置所有弹幕参数为系统默认值'}
            onConfirm={handleReset}
            okText="确认"
            cancelText="取消"
          >
            <Button icon={<UndoOutlined />}>
              恢复默认
            </Button>
          </Popconfirm>
        </Space>
      </Form.Item>
    </Form>
  );
};

// 支持弹幕录制的平台
const DANMAKU_PLATFORMS: Record<string, string> = {
  bilibili: '哔哩哔哩',
  douyin: '抖音',
  douyu: '斗鱼',
};

const DanmakuSettings: React.FC = () => {
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [config, setConfig] = useState<EffectiveConfig | null>(null);
  const [platformRooms, setPlatformRooms] = useState<Record<string, RoomInfo[]>>({});
  const [burnForm] = Form.useForm();
  const burnCodec = Form.useWatch('burn_subtitles_codec', burnForm);
  const isNvenc = isNvencCodec(burnCodec);

  const loadData = useCallback(async () => {
    setLoading(true);
    try {
      const [effective, platformStats] = await Promise.all([
        api.getEffectiveConfig(),
        api.getPlatformStats(),
      ]);
      setConfig(effective as EffectiveConfig);

      // Extract rooms from all danmaku-supported platforms
      const stats = platformStats as any;
      const grouped: Record<string, RoomInfo[]> = {};
      if (Array.isArray(stats?.platforms)) {
        for (const platform of stats.platforms) {
          const key = Object.keys(DANMAKU_PLATFORMS).find(k => platform.platform_key?.includes(k));
          if (key && Array.isArray(platform.rooms)) {
            grouped[key] = platform.rooms;
          }
        }
      }
      setPlatformRooms(grouped);
    } catch (error) {
      message.error('加载配置失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  // 设置字幕烧录表单初始值
  useEffect(() => {
    if (config?.on_record_finished) {
      burnForm.setFieldsValue({
        burn_subtitles: config.on_record_finished.burn_subtitles ?? false,
        burn_subtitles_codec: config.on_record_finished.burn_subtitles_codec || 'libx264',
        burn_subtitles_crf: config.on_record_finished.burn_subtitles_crf || '18',
        burn_subtitles_preset: config.on_record_finished.burn_subtitles_preset || 'medium',
        burn_delete_ass: config.on_record_finished.burn_delete_ass ?? false,
        burn_delete_source: config.on_record_finished.burn_delete_source ?? false,
      });
    }
  }, [config, burnForm]);

  // 编码预设与编码器联动：预设只能选择当前编码器支持的档位，
  // 切换编码器时若当前预设不适用，自动重置为该编码器的默认档位。
  // 注意：codec/preset 必须用 getFieldsValue 一次性原子读取，不能使用渲染闭包中的
  // burnCodec/isNvenc —— 配置异步加载时 setFieldsValue 先写入"编码器+预设"组合，
  // 而 useWatch 的值要等下一轮渲染才更新，用旧编码器校验刚写入的预设会把
  // 有效的 p1-p7 误判为无效并重置，导致用户已保存的配置在界面加载后丢失。
  useEffect(() => {
    if (!burnCodec) return;
    const { burn_subtitles_codec: codec, burn_subtitles_preset: preset } =
      burnForm.getFieldsValue(['burn_subtitles_codec', 'burn_subtitles_preset']);
    const nvenc = isNvencCodec(codec);
    const validPresets = nvenc ? NVENC_PRESET_OPTIONS : X264_PRESET_OPTIONS;
    if (!validPresets.some(option => option.value === preset)) {
      burnForm.setFieldsValue({
        burn_subtitles_preset: nvenc
          ? NVENC_DEFAULT_PRESET
          : (DEFAULT_PRESET_BY_CODEC[codec] ?? 'medium'),
      });
    }
  }, [burnCodec, burnForm]);

  const handleSaveGlobal = async (values: any) => {
    setSaving(true);
    try {
      await api.updateConfig({
        danmaku_enable: values.danmaku_enable,
        danmaku: values.danmaku,
      });
      await loadData();
    } finally {
      setSaving(false);
    }
  };

  const DEFAULT_BURN = {
    burn_subtitles: false,
    burn_subtitles_codec: 'libx264',
    burn_subtitles_crf: '18',
    burn_subtitles_preset: 'medium',
    burn_delete_ass: false,
    burn_delete_source: false,
  };

  const handleSaveBurnSettings = async () => {
    try {
      const values = await burnForm.validateFields();
      // 空白质量值保存时归一化为默认值，避免后端生成 "-crf """ / "-cq """ 导致 FFmpeg 烧录失败
      if (String(values.burn_subtitles_crf ?? '').trim() === '') {
        values.burn_subtitles_crf = DEFAULT_BURN.burn_subtitles_crf;
        burnForm.setFieldsValue({ burn_subtitles_crf: values.burn_subtitles_crf });
      }
      setSaving(true);
      await api.updateConfig({
        on_record_finished: {
          burn_subtitles: values.burn_subtitles,
          burn_subtitles_codec: values.burn_subtitles_codec,
          burn_subtitles_crf: values.burn_subtitles_crf,
          burn_subtitles_preset: values.burn_subtitles_preset,
          burn_delete_ass: values.burn_delete_ass,
          burn_delete_source: values.burn_delete_source,
        },
      });
      message.success('字幕烧录配置已保存');
      await loadData();
    } catch (error: any) {
      if (error?.errorFields) {
        message.error('表单校验失败，请检查输入项');
      } else {
        message.error('保存失败: ' + (error?.message || '未知错误'));
      }
    } finally {
      setSaving(false);
    }
  };

  const handleResetBurnSettings = async () => {
    burnForm.setFieldsValue(DEFAULT_BURN);
    setSaving(true);
    try {
      await api.updateConfig({ on_record_finished: DEFAULT_BURN });
      message.success('已恢复默认烧录配置');
      await loadData();
    } catch (error: any) {
      message.error('恢复默认失败: ' + (error?.message || '未知错误'));
    } finally {
      setSaving(false);
    }
  };

  const BILIBILI_DANMAKU_FIELDS = ['record_gift', 'record_guard', 'record_super_chat', 'guard_position', 'sc_position'];
  const DOUYU_DANMAKU_FIELDS = ['record_douyu_gift'];
  const DOUYIN_DANMAKU_FIELDS = ['record_douyin_gift'];

  const filterDanmakuByPlatform = (danmaku: Record<string, any>, platformKey: string): Record<string, any> => {
    if (!danmaku) return danmaku;
    const result = { ...danmaku };
    if (platformKey !== 'bilibili') {
      for (const f of BILIBILI_DANMAKU_FIELDS) delete result[f];
    }
    if (platformKey !== 'douyu') {
      for (const f of DOUYU_DANMAKU_FIELDS) delete result[f];
    }
    if (platformKey !== 'douyin') {
      for (const f of DOUYIN_DANMAKU_FIELDS) delete result[f];
    }
    return result;
  };

  const handleSaveRoom = async (liveId: string, values: any, platformKey: string) => {
    setSaving(true);
    try {
      await api.updateRoomConfigById(liveId, {
        danmaku_enable: values.danmaku_enable,
        danmaku: filterDanmakuByPlatform(values.danmaku, platformKey),
      });
      await loadData();
    } finally {
      setSaving(false);
    }
  };

  const handleResetRoom = async (liveId: string) => {
    setSaving(true);
    try {
      // Send null to clear room-level overrides, reverting to global inheritance
      await api.updateRoomConfigById(liveId, {
        danmaku_enable: null,
        danmaku: null,
      });
      message.success('已恢复为全局默认配置');
      await loadData();
    } catch (error: any) {
      message.error('恢复失败: ' + (error?.message || '未知错误'));
    } finally {
      setSaving(false);
    }
  };

  if (loading && !config) {
    return <Spin size="large" style={{ display: 'block', margin: '100px auto' }} />;
  }

  return (
    <div>
      <h2 style={{ marginBottom: 16 }}>弹幕录制配置</h2>

      <Card title="全局设置" size="small" style={{ marginBottom: 16 }}>
        <DanmakuParamForm
          initialValues={config?.danmaku}
          danmakuEnable={config?.danmaku_enable}
          showEnable
          onSave={handleSaveGlobal}
          loading={saving}
          label="全局弹幕"
          showBilibiliContent
          showDouyuContent
          showDouyinContent
        />
      </Card>

      <Card title="字幕烧录设置" size="small" style={{ marginBottom: 16 }}>
        <Form form={burnForm} layout="vertical" initialValues={DEFAULT_BURN}>
          <Form.Item
            label="烧录弹幕字幕"
            name="burn_subtitles"
            valuePropName="checked"
            extra="将 ASS 弹幕字幕硬编码到视频中（需要开启弹幕录制）"
          >
            <Switch />
          </Form.Item>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 24px' }}>
            <Form.Item
              label="视频编码器"
              name="burn_subtitles_codec"
              extra="默认 libx264，可选 libx265 或 NVIDIA 硬件编码 h264_nvenc / hevc_nvenc / av1_nvenc（需要显卡驱动和受支持的 GPU）"
            >
              <Select options={BURN_CODEC_OPTIONS} />
            </Form.Item>
            <Form.Item
              label={isNvenc ? 'CQ 质量值' : 'CRF 质量值'}
              name="burn_subtitles_crf"
              extra={isNvenc
                ? '1-51，越小画质越好（NVENC 恒定质量参数，对应 FFmpeg -cq）；0 表示自动质量而非最高画质，默认 18'
                : '0-51，越小画质越好（对应 FFmpeg -crf），默认 18'}
              rules={[
                {
                  validator: (_, value) => {
                    // 空白输入表示使用默认值（保存时归一化为 18），这里放行
                    const str = String(value ?? '').trim();
                    if (str === '') return Promise.resolve();
                    const quality = Number(str);
                    if (!Number.isInteger(quality) || quality < 0 || quality > 51) {
                      return Promise.reject(new Error('质量值必须是 0-51 的整数，留空使用默认值 18'));
                    }
                    return Promise.resolve();
                  },
                },
              ]}
            >
              <Input placeholder="18" />
            </Form.Item>
            <Form.Item
              label="编码预设"
              name="burn_subtitles_preset"
              extra={isNvenc
                ? 'NVENC 预设：p1 最快、p7 画质最好，常用 p5'
                : 'x264/x265 预设：速度越慢，画质越好，文件越小'}
              rules={[
                {
                  validator: (_, value) => {
                    if (!value) return Promise.resolve();
                    const validPresets = isNvenc ? NVENC_PRESET_OPTIONS : X264_PRESET_OPTIONS;
                    return validPresets.some(option => option.value === value)
                      ? Promise.resolve()
                      : Promise.reject(new Error(`预设 ${value} 不是当前编码器支持的预设，请重新选择`));
                  },
                },
              ]}
            >
              <Select options={isNvenc ? NVENC_PRESET_OPTIONS : X264_PRESET_OPTIONS} />
            </Form.Item>
            <Form.Item
              label="烧录后删除 ASS 文件"
              name="burn_delete_ass"
              valuePropName="checked"
              extra="烧录成功后标记 ASS 文件为待删除，全部处理阶段完成后才真正删除"
            >
              <Switch />
            </Form.Item>
            <Form.Item
              label="烧录后删除源视频"
              name="burn_delete_source"
              valuePropName="checked"
              extra="烧录成功后标记源视频为待删除，全部处理阶段完成后才真正删除。仅保留 MKV"
            >
              <Switch />
            </Form.Item>
          </div>
          <Form.Item style={{ marginBottom: 0 }}>
            <Space>
              <Button type="primary" onClick={handleSaveBurnSettings} loading={saving}>
                保存烧录设置
              </Button>
              <Popconfirm
                title="恢复为系统默认？"
                description="将重置所有烧录参数为系统默认值"
                onConfirm={handleResetBurnSettings}
                okText="确认"
                cancelText="取消"
              >
                <Button icon={<UndoOutlined />}>
                  恢复默认
                </Button>
              </Popconfirm>
            </Space>
          </Form.Item>
        </Form>
      </Card>

      {Object.keys(DANMAKU_PLATFORMS).map((platformKey) => {
        const rooms = platformRooms[platformKey];
        if (!rooms || rooms.length === 0) return null;
        return (
          <Card key={platformKey} title={`房间设置 (${DANMAKU_PLATFORMS[platformKey]})`} size="small" style={{ marginBottom: 16 }}>
            <Collapse
              items={rooms.map((room) => ({
                key: room.live_id,
                label: (
                  <span>
                    {room.room_config?.room_name || room.room_name || room.url}
                    {room.host_name && <Tag style={{ marginLeft: 8 }}>{room.host_name}</Tag>}
                  </span>
                ),
                children: (
                  <DanmakuParamForm
                    initialValues={room.room_config?.danmaku}
                    globalDefaults={config?.danmaku}
                    danmakuEnable={room.room_config?.danmaku_enable ?? config?.danmaku_enable}
                    showEnable
                    onSave={(values) => handleSaveRoom(room.live_id, values, platformKey)}
                    onReset={() => handleResetRoom(room.live_id)}
                    loading={saving}
                    label={room.host_name || '房间弹幕'}
                    isRoom
                    showBilibiliContent={platformKey === 'bilibili'}
                    showDouyuContent={platformKey === 'douyu'}
                    showDouyinContent={platformKey === 'douyin'}
                  />
                ),
              }))}
            />
          </Card>
        );
      })}
    </div>
  );
};

export default DanmakuSettings;
