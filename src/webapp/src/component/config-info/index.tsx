import React from "react";
import Editor from 'react-simple-code-editor';
import { highlight, languages } from 'prismjs';
import 'prismjs/components/prism-yaml';
import 'prismjs/components/prism-clike';
import 'prismjs/components/prism-javascript';
import 'prismjs/themes/prism.css'; //Example style, you can use another
import * as yaml from 'js-yaml';
import API from '../../utils/api';
import {
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Row,
  Col,
  Select,
  Tooltip,
  message,
  Collapse
} from "antd";
import { InfoCircleOutlined, SettingOutlined, FormOutlined, CodeOutlined } from '@ant-design/icons';
import { RouteComponentProps } from "react-router-dom";
import './config-info.css';

const { Option } = Select;
const { Panel } = Collapse;
const api = new API();

interface Props extends RouteComponentProps<any> {
  children?: React.ReactNode;
}

interface IState {
  config: any;
  parsedConfig: any;
  editMode: 'gui' | 'text';
  loading: boolean;
  selectedPlatform: string;
  expandAllPlatforms: boolean;
  outputTemplatePreview: string;
}

// 平台配置列表 - 包含所有支持的直播平台
const PLATFORM_OPTIONS = [
  { key: 'bilibili', name: '哔哩哔哩', domain: 'live.bilibili.com' },
  { key: 'douyin', name: '抖音', domain: 'live.douyin.com' },
  { key: 'douyu', name: '斗鱼', domain: 'www.douyu.com' },
  { key: 'huya', name: '虎牙', domain: 'www.huya.com' },
  { key: 'kuaishou', name: '快手', domain: 'live.kuaishou.com' },
  { key: 'yy', name: 'YY直播', domain: 'www.yy.com' },
  { key: 'acfun', name: 'AcFun', domain: 'live.acfun.cn' },
  { key: 'lang', name: '浪live', domain: 'www.lang.live' },
  { key: 'missevan', name: '猫耳', domain: 'fm.missevan.com' },
  { key: 'openrec', name: 'OpenRec', domain: 'www.openrec.tv' },
  { key: 'weibolive', name: '微博直播', domain: 'weibo.com' },
  { key: 'xiaohongshu', name: '小红书', domain: 'www.xiaohongshu.com' },
  { key: 'yizhibo', name: '一直播', domain: 'www.yizhibo.com' },
  { key: 'hongdoufm', name: '克拉克拉', domain: 'www.hongdoufm.com' },
  { key: 'zhanqi', name: '战旗', domain: 'www.zhanqi.tv' },
  { key: 'cc', name: 'CC直播', domain: 'cc.163.com' },
  { key: 'twitch', name: 'Twitch', domain: 'www.twitch.tv' },
  { key: 'qq', name: '企鹅电竞', domain: 'egame.qq.com' },
  { key: 'huajiao', name: '花椒', domain: 'www.huajiao.com' },
];

class ConfigInfo extends React.Component<Props, IState> {

  constructor(props: Props) {
    super(props);
    this.state = {
      config: null,
      parsedConfig: {},
      editMode: 'gui',
      loading: false,
      selectedPlatform: 'bilibili',
      expandAllPlatforms: false,
      outputTemplatePreview: '',
    }
  }

  componentDidMount(): void {
    this.loadConfig();
  }

  loadConfig = () => {
    this.setState({ loading: true });
    api.getConfigInfo()
      .then((rsp: any) => {
        try {
          // 使用js-yaml解析YAML配置
          const parsedConfig = yaml.load(rsp.config) as any;
          this.setState({
            config: rsp.config,
            parsedConfig: parsedConfig || {},
            loading: false,
          });
          this.generateOutputTemplatePreview(parsedConfig);
        } catch (e) {
          // 解析失败时回退到文本模式
          console.error('YAML解析失败:', e);
          this.setState({
            config: rsp.config,
            parsedConfig: {},
            editMode: 'text',
            loading: false,
          });
          message.warning('配置文件解析失败，已切换到文本模式');
        }
      })
      .catch(err => {
        message.error("获取配置信息失败");
        this.setState({ loading: false });
      });
  }

  generateOutputTemplatePreview = (config: any) => {
    const template = config.out_put_tmpl || '';
    if (!template) {
      this.setState({ outputTemplatePreview: '使用默认模板：./平台名称/主播名字/[时间戳][主播名字][房间名字].flv' });
      return;
    }

    // 生成示例预览
    const now = new Date();
    const timeFormat = now.getFullYear() + '-' +
      String(now.getMonth() + 1).padStart(2, '0') + '-' +
      String(now.getDate()).padStart(2, '0') + ' ' +
      String(now.getHours()).padStart(2, '0') + '-' +
      String(now.getMinutes()).padStart(2, '0') + '-' +
      String(now.getSeconds()).padStart(2, '0');

    let preview = template
      .replace(/\{\{\s*\.Live\.GetPlatformCNName\s*\}\}/g, '哔哩哔哩')
      .replace(/\{\{\s*\.HostName\s*\|\s*filenameFilter\s*\}\}/g, '永雏塔菲')
      .replace(/\{\{\s*\.RoomName\s*\|\s*filenameFilter\s*\}\}/g, '你见过6点起床种田的塔菲吗？牧场物语4')
      .replace(/\{\{\s*now\s*\|\s*date\s*"[^"]*"\s*\}\}/g, timeFormat);

    this.setState({ outputTemplatePreview: `预览：${preview}` });
  }

  generateYamlConfig = () => {
    const { parsedConfig } = this.state;
    try {
      return yaml.dump(parsedConfig, {
        indent: 2,
        lineWidth: 120,
        noRefs: true,
        quotingType: '"'
      });
    } catch (e) {
      console.error('YAML生成失败:', e);
      return this.state.config;
    }
  }

  onGuiConfigChange = (field: string, value: any, platform?: string) => {
    const { parsedConfig } = this.state;
    const newConfig = { ...parsedConfig };

    if (platform) {
      if (!newConfig.platform_configs) newConfig.platform_configs = {};
      if (!newConfig.platform_configs[platform]) newConfig.platform_configs[platform] = {};
      newConfig.platform_configs[platform][field] = value;
    } else {
      newConfig[field] = value;
    }

    // 重新生成YAML
    const newYaml = this.generateYamlConfig();

    // 如果是输出模板字段，更新预览
    if (field === 'out_put_tmpl') {
      this.generateOutputTemplatePreview(newConfig);
    }

    this.setState({
      parsedConfig: newConfig,
      config: newYaml,
    });
  }

  onSettingSave = () => {
    this.setState({ loading: true });
    api.saveRawConfig({ config: this.state.config })
      .then((rsp: any) => {
        if (rsp.err_no === 0) {
          message.success("设置保存成功");
        } else {
          message.error(`Server Error!\n${rsp.err_msg}`);
        }
        this.setState({ loading: false });
      })
      .catch(err => {
        message.error("设置保存失败！");
        this.setState({ loading: false });
      })
  }

  renderGlobalSettings = () => {
    const { parsedConfig, outputTemplatePreview } = this.state;

    return (
      <Card title="全局设置" style={{ marginBottom: 16 }}>
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item label="检测间隔 (秒)">
              <Tooltip title="全局检测间隔，设置程序多久检测一次直播状态。较小的值能更快发现开播，但会增加服务器负担。推荐设置：20-60秒">
                <InputNumber
                  value={parsedConfig.interval || 30}
                  min={1}
                  max={3600}
                  onChange={(value) => this.onGuiConfigChange('interval', value)}
                />
              </Tooltip>
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="输出路径">
              <Tooltip title="全局录制文件保存路径，可被平台和房间级设置覆盖。支持相对路径和绝对路径。示例：./recordings 或 /home/user/videos">
                <Input
                  value={parsedConfig.out_put_path || './'}
                  onChange={(e) => this.onGuiConfigChange('out_put_path', e.target.value)}
                />
              </Tooltip>
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item label="FFmpeg 路径">
              <Tooltip title="FFmpeg 可执行文件路径，用于视频处理。留空会自动在系统PATH中寻找。Windows示例：C:\ffmpeg\bin\ffmpeg.exe">
                <Input
                  value={parsedConfig.ffmpeg_path || ''}
                  placeholder="留空自动在环境变量中寻找"
                  onChange={(e) => this.onGuiConfigChange('ffmpeg_path', e.target.value)}
                />
              </Tooltip>
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="超时设置 (微秒)">
              <Tooltip title="网络请求超时时间，单位微秒。默认60秒。如果网络较慢可以适当增加">
                <InputNumber
                  value={parsedConfig.timeout_in_us || 60000000}
                  min={1000000}
                  max={300000000}
                  step={1000000}
                  onChange={(value) => this.onGuiConfigChange('timeout_in_us', value)}
                />
              </Tooltip>
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col span={24}>
            <Form.Item label="输出文件名模板">
              <Tooltip title="自定义输出文件名格式。支持变量：{{.Live.GetPlatformCNName}}平台名、{{.HostName}}主播名、{{.RoomName}}房间名、{{now | date &quot;2006-01-02 15-04-05&quot;}}时间。留空使用默认模板">
                <Input
                  value={parsedConfig.out_put_tmpl || ''}
                  placeholder="留空使用默认模板"
                  onChange={(e) => this.onGuiConfigChange('out_put_tmpl', e.target.value)}
                />
              </Tooltip>
              {outputTemplatePreview && (
                <div style={{ marginTop: 8, fontSize: '12px', color: '#666' }}>
                  {outputTemplatePreview}
                </div>
              )}
            </Form.Item>
          </Col>
        </Row>
      </Card>
    );
  }

  renderPlatformSettings = () => {
    const { parsedConfig, selectedPlatform, expandAllPlatforms } = this.state;
    const platformConfigs = parsedConfig.platform_configs || {};

    if (expandAllPlatforms) {
      return this.renderAllPlatformSettings();
    }

    return (
      <Card
        title="平台特定设置"
        extra={
          <div>
            <Button
              size="small"
              onClick={() => this.setState({ expandAllPlatforms: true })}
              style={{ marginRight: 8 }}
            >
              展开所有平台
            </Button>
            <Select
              value={selectedPlatform}
              style={{ width: 120 }}
              onChange={(value) => this.setState({ selectedPlatform: value })}
            >
              {PLATFORM_OPTIONS.map(platform => (
                <Option key={platform.key} value={platform.key}>
                  {platform.name}
                </Option>
              ))}
            </Select>
          </div>
        }
        style={{ marginBottom: 16 }}
      >
        <div style={{ marginBottom: 16 }}>
          <Tooltip title="平台级设置将覆盖全局设置，但会被房间级设置覆盖">
            <InfoCircleOutlined /> 设置优先级：房间级 &gt; 平台级 &gt; 全局级
          </Tooltip>
        </div>

        {selectedPlatform && this.renderSinglePlatformSettings(selectedPlatform, platformConfigs[selectedPlatform] || {})}
      </Card>
    );
  }

  renderAllPlatformSettings = () => {
    const { parsedConfig } = this.state;
    const platformConfigs = parsedConfig.platform_configs || {};

    return (
      <Card
        title="所有平台设置总览"
        extra={
          <Button
            size="small"
            onClick={() => this.setState({ expandAllPlatforms: false })}
          >
            折叠
          </Button>
        }
        style={{ marginBottom: 16 }}
      >
        <Collapse accordion>
          {PLATFORM_OPTIONS.map(platform => {
            const config = platformConfigs[platform.key] || {};
            const hasConfig = Object.keys(config).length > 0;

            return (
              <Panel
                header={
                  <div>
                    <span style={{ fontWeight: hasConfig ? 'bold' : 'normal' }}>
                      {platform.name}
                    </span>
                    {hasConfig && <SettingOutlined style={{ marginLeft: 8, color: '#1890ff' }} />}
                  </div>
                }
                key={platform.key}
              >
                {this.renderSinglePlatformSettings(platform.key, config)}
              </Panel>
            );
          })}
        </Collapse>
      </Card>
    );
  }

  renderSinglePlatformSettings = (platformKey: string, platformConfig: any) => {
    return (
      <div>
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item label="平台访问频率限制 (秒)">
              <Tooltip title="对该平台的最小访问间隔，防止触发反机器人机制。建议：抖音5秒、哔哩哔哩3秒、YY直播10秒">
                <InputNumber
                  value={platformConfig.min_access_interval_sec || 0}
                  min={0}
                  max={60}
                  onChange={(value) => this.onGuiConfigChange('min_access_interval_sec', value, platformKey)}
                  placeholder="0 = 无限制"
                />
              </Tooltip>
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="检测间隔 (秒)">
              <Tooltip title="覆盖全局检测间隔，仅对该平台生效。可以为高质量平台设置更短的间隔">
                <InputNumber
                  value={platformConfig.interval}
                  min={1}
                  max={3600}
                  placeholder="使用全局设置"
                  onChange={(value) => this.onGuiConfigChange('interval', value, platformKey)}
                />
              </Tooltip>
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item label="输出路径">
              <Tooltip title="覆盖全局输出路径，仅对该平台生效。可以为不同平台创建专门的文件夹">
                <Input
                  value={platformConfig.out_put_path || ''}
                  placeholder="使用全局设置"
                  onChange={(e) => this.onGuiConfigChange('out_put_path', e.target.value, platformKey)}
                />
              </Tooltip>
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="平台名称">
              <Tooltip title="平台的中文显示名称，用于界面显示和文件路径">
                <Input
                  value={platformConfig.name || ''}
                  placeholder="平台中文名"
                  onChange={(e) => this.onGuiConfigChange('name', e.target.value, platformKey)}
                />
              </Tooltip>
            </Form.Item>
          </Col>
        </Row>
      </div>
    );
  }

  render() {
    const { config, editMode, loading } = this.state;

    if (loading || config === null) {
      return <div>loading...</div>;
    }

    return (
      <div>
        <div style={{ marginBottom: 16, textAlign: 'right' }}>
          <Button.Group>
            <Button
              type={editMode === 'gui' ? 'primary' : 'default'}
              onClick={() => this.setState({ editMode: 'gui' })}
            >
              <FormOutlined /> GUI 模式
            </Button>
            <Button
              type={editMode === 'text' ? 'primary' : 'default'}
              onClick={() => this.setState({ editMode: 'text' })}
            >
              <CodeOutlined /> 文本模式
            </Button>
          </Button.Group>
        </div>

        {editMode === 'gui' ? (
          <div>
            {this.renderGlobalSettings()}
            {this.renderPlatformSettings()}

            <Card title="配置层次结构说明">
              <p><InfoCircleOutlined style={{ color: '#1890ff' }} /> 本程序支持三级配置覆盖：</p>
              <ul>
                <li><strong>全局级</strong>：适用于所有直播间的默认设置</li>
                <li><strong>平台级</strong>：适用于特定平台的所有直播间，覆盖全局设置</li>
                <li><strong>房间级</strong>：适用于单个直播间，覆盖平台和全局设置</li>
              </ul>
              <p>平台访问频率限制可以有效防止被直播平台风控，建议根据平台特性合理设置。</p>
              <p><strong>支持的平台：</strong>{PLATFORM_OPTIONS.map(p => p.name).join('、')}</p>
            </Card>
          </div>
        ) : (
          <Editor
            value={config}
            onValueChange={code => this.setState({ config: code })}
            highlight={code => {
              return highlight(code, languages.yaml, 'yaml');
            }}
            padding={10}
            style={{
              fontFamily: '"Fira code", "Fira Mono", monospace',
              fontSize: 12,
              border: '1px solid #d9d9d9',
              borderRadius: 4,
            }}
          />
        )}

        <div style={{ marginTop: 16, textAlign: 'center' }}>
          <Button
            type="primary"
            loading={loading}
            onClick={this.onSettingSave}
          >
            保存设置
          </Button>
        </div>
      </div>
    );
  }
}

export default ConfigInfo;