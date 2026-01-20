import React, { Component } from 'react';
import { Table, Button, Tag, Space, Progress, Tooltip, Card, Statistic, Row, Col, Modal, message, Badge, Collapse, Typography, Select, Alert, Steps } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ReloadOutlined,
  PlayCircleOutlined,
  DeleteOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  ClockCircleOutlined,
  LoadingOutlined,
  ClearOutlined,
  StopOutlined
} from '@ant-design/icons';
import './index.css';

const { Text, Paragraph } = Typography;
const { Panel } = Collapse;

// Pipeline 状态
type PipelineStatus = 'pending' | 'running' | 'completed' | 'failed' | 'cancelled';

// 阶段状态
type StageStatus = 'pending' | 'running' | 'completed' | 'failed' | 'skipped';

// 文件信息
interface FileInfo {
  path: string;
  type: 'video' | 'cover' | 'other';
  source_path?: string;
}

// 录制信息
interface RecordInfo {
  live_id: string;
  platform: string;
  host_name: string;
  room_name: string;
  start_time: string;
}

// 阶段配置
interface StageConfig {
  name: string;
  enabled?: boolean;
  options?: Record<string, unknown>;
}

// 阶段结果
interface StageResult {
  stage_name: string;
  stage_index: number;
  status: StageStatus;
  input_files?: FileInfo[];
  output_files?: FileInfo[];
  started_at: string;
  completed_at?: string;
  commands?: string[];
  logs?: string;
  error_message?: string;
}

// Pipeline 任务
interface PipelineTask {
  id: number;
  status: PipelineStatus;
  record_info: RecordInfo;
  pipeline_config: {
    stages: StageConfig[];
  };
  initial_files: FileInfo[];
  current_files: FileInfo[];
  current_stage: number;
  total_stages: number;
  stage_results: StageResult[];
  progress: number;
  created_at: string;
  started_at?: string;
  completed_at?: string;
  error_message?: string;
  can_retry: boolean;
}

// 队列统计
interface PipelineStats {
  max_concurrent: number;
  running_count: number;
  pending_count: number;
  completed_count: number;
  failed_count: number;
  cancelled_count: number;
}

interface PipelineTaskListState {
  tasks: PipelineTask[];
  stats: PipelineStats | null;
  loading: boolean;
  statusFilter: PipelineStatus | 'all';
  expandedRowKeys: number[];
}

class PipelineTaskList extends Component<object, PipelineTaskListState> {
  private pollInterval: ReturnType<typeof setInterval> | null = null;
  private eventSource: EventSource | null = null;

  constructor(props: object) {
    super(props);
    this.state = {
      tasks: [],
      stats: null,
      loading: false,
      statusFilter: 'all',
      expandedRowKeys: [],
    };
  }

  componentDidMount() {
    this.loadData();
    // 每30秒刷新一次（有 SSE 实时更新后可以降低轮询频率）
    this.pollInterval = setInterval(() => this.loadData(), 30000);
    // 订阅 SSE 事件
    this.connectSSE();
  }

  componentWillUnmount() {
    if (this.pollInterval) {
      clearInterval(this.pollInterval);
    }
    if (this.eventSource) {
      this.eventSource.close();
    }
  }

  // 连接 SSE 事件源
  connectSSE = () => {
    try {
      this.eventSource = new EventSource('/api/sse');

      // 处理 Pipeline 任务更新事件
      this.eventSource.addEventListener('pipeline_task_update', (event) => {
        try {
          const data = JSON.parse(event.data);
          if (data.data) {
            this.handleTaskUpdate(data.data as PipelineTask);
          }
        } catch (error) {
          console.error('Failed to parse pipeline task update:', error);
        }
      });

      this.eventSource.addEventListener('connected', () => {
        console.log('SSE connected for pipeline tasks');
      });

      this.eventSource.onerror = () => {
        console.warn('SSE connection error, will retry...');
        // 关闭当前连接，依赖轮询作为备用
        if (this.eventSource) {
          this.eventSource.close();
          this.eventSource = null;
        }
        // 5秒后重连
        setTimeout(() => this.connectSSE(), 5000);
      };
    } catch (error) {
      console.error('Failed to connect SSE:', error);
    }
  };

  // 处理单个任务更新
  handleTaskUpdate = (updatedTask: PipelineTask) => {
    this.setState(prevState => {
      const tasks = [...prevState.tasks];
      const index = tasks.findIndex(t => t.id === updatedTask.id);
      if (index >= 0) {
        // 更新现有任务
        tasks[index] = updatedTask;
      } else {
        // 添加新任务到顶部
        tasks.unshift(updatedTask);
      }
      return { tasks };
    });
    // 同时刷新统计数据
    this.loadStats();
  };

  // 仅加载统计数据
  loadStats = async () => {
    try {
      const res = await fetch('/api/pipeline/tasks/stats');
      if (res.ok) {
        const stats = await res.json();
        this.setState({ stats });
      }
    } catch (error) {
      console.error('Failed to load stats:', error);
    }
  };

  loadData = async () => {
    try {
      const [tasksRes, statsRes] = await Promise.all([
        fetch('/api/pipeline/tasks'),
        fetch('/api/pipeline/tasks/stats'),
      ]);

      if (tasksRes.ok && statsRes.ok) {
        const tasks = await tasksRes.json();
        const stats = await statsRes.json();
        this.setState({ tasks: tasks || [], stats, loading: false });
      }
    } catch (error) {
      console.error('Failed to load pipeline tasks:', error);
    }
  };

  handleCancel = async (taskId: number) => {
    try {
      const res = await fetch(`/api/pipeline/tasks/${taskId}/cancel`, { method: 'POST' });
      if (res.ok) {
        message.success('任务已取消');
        this.loadData();
      } else {
        message.error('取消失败');
      }
    } catch (error) {
      message.error('取消失败');
    }
  };

  handleRetry = async (taskId: number) => {
    try {
      const res = await fetch(`/api/pipeline/tasks/${taskId}/retry`, { method: 'POST' });
      if (res.ok) {
        message.success('任务已重新排队');
        this.loadData();
      } else {
        message.error('重试失败');
      }
    } catch (error) {
      message.error('重试失败');
    }
  };

  handleDelete = async (taskId: number) => {
    Modal.confirm({
      title: '确认删除',
      content: '确定要删除这个任务吗？',
      onOk: async () => {
        try {
          const res = await fetch(`/api/pipeline/tasks/${taskId}`, { method: 'DELETE' });
          if (res.ok) {
            message.success('任务已删除');
            this.loadData();
          } else {
            message.error('删除失败');
          }
        } catch (error) {
          message.error('删除失败');
        }
      },
    });
  };

  handleClearCompleted = async () => {
    Modal.confirm({
      title: '确认清除',
      content: '确定要清除所有已完成的任务记录吗？',
      onOk: async () => {
        try {
          const res = await fetch('/api/pipeline/tasks/clear-completed', { method: 'POST' });
          if (res.ok) {
            const data = await res.json();
            message.success(`已清除 ${data.deleted} 条已完成任务`);
            this.loadData();
          } else {
            message.error('清除失败');
          }
        } catch (error) {
          message.error('清除失败');
        }
      },
    });
  };

  getStageLabel = (stageName: string): string => {
    const labels: Record<string, string> = {
      'fix_flv': '修复FLV',
      'convert_mp4': '转换MP4',
      'extract_cover': '提取封面',
      'cloud_upload': '云盘上传',
      'custom_command': '自定义命令',
    };
    return labels[stageName] || stageName;
  };

  getStatusTag = (status: PipelineStatus) => {
    switch (status) {
      case 'pending':
        return <Tag icon={<ClockCircleOutlined />} color="default">等待中</Tag>;
      case 'running':
        return <Tag icon={<LoadingOutlined spin />} color="processing">运行中</Tag>;
      case 'completed':
        return <Tag icon={<CheckCircleOutlined />} color="success">已完成</Tag>;
      case 'failed':
        return <Tag icon={<CloseCircleOutlined />} color="error">失败</Tag>;
      case 'cancelled':
        return <Tag icon={<StopOutlined />} color="warning">已取消</Tag>;
      default:
        return <Tag>{status}</Tag>;
    }
  };

  getStageStatusIcon = (status: StageStatus) => {
    switch (status) {
      case 'pending':
        return <ClockCircleOutlined />;
      case 'running':
        return <LoadingOutlined spin />;
      case 'completed':
        return <CheckCircleOutlined style={{ color: '#52c41a' }} />;
      case 'failed':
        return <CloseCircleOutlined style={{ color: '#ff4d4f' }} />;
      case 'skipped':
        return <ClockCircleOutlined style={{ color: '#d9d9d9' }} />;
      default:
        return null;
    }
  };

  formatTime = (time: string | undefined): string => {
    if (!time) return '-';
    return new Date(time).toLocaleString('zh-CN');
  };

  formatFileName = (path: string): string => {
    if (!path) return '-';
    const parts = path.split(/[/\\]/);
    return parts[parts.length - 1];
  };

  formatDuration = (startTime: string | undefined, endTime: string | undefined): string => {
    if (!startTime) return '-';
    const start = new Date(startTime).getTime();
    const end = endTime ? new Date(endTime).getTime() : Date.now();
    const durationMs = end - start;

    if (durationMs < 0) return '-';

    const hours = Math.floor(durationMs / (1000 * 60 * 60));
    const minutes = Math.floor((durationMs % (1000 * 60 * 60)) / (1000 * 60));
    const seconds = Math.floor((durationMs % (1000 * 60)) / 1000);

    if (hours > 0) {
      return `${hours}小时${minutes}分钟`;
    } else if (minutes > 0) {
      return `${minutes}分钟${seconds}秒`;
    } else {
      return `${seconds}秒`;
    }
  };

  getFilteredTasks = (): PipelineTask[] => {
    const { tasks, statusFilter } = this.state;
    return tasks.filter(t => {
      if (statusFilter !== 'all' && t.status !== statusFilter) return false;
      return true;
    });
  };

  // 渲染阶段进度
  renderStageProgress = (task: PipelineTask) => {
    const stages = task.pipeline_config?.stages || [];
    const results = task.stage_results || [];

    if (stages.length === 0) {
      return <Text type="secondary">无阶段</Text>;
    }

    // 获取阶段状态
    const getStageStatus = (index: number): 'wait' | 'process' | 'finish' | 'error' => {
      const result = results.find(r => r.stage_index === index);
      if (!result) {
        if (index < task.current_stage) return 'finish';
        if (index === task.current_stage && task.status === 'running') return 'process';
        return 'wait';
      }
      switch (result.status) {
        case 'completed':
          return 'finish';
        case 'running':
          return 'process';
        case 'failed':
          return 'error';
        default:
          return 'wait';
      }
    };

    const items = stages.filter(s => s.enabled !== false).map((stage, index) => ({
      key: index,
      title: this.getStageLabel(stage.name),
      status: getStageStatus(index),
    }));

    return (
      <Steps size="small" current={task.current_stage} items={items} />
    );
  };

  // 渲染文件列表
  renderFileList = (files: FileInfo[], title: string) => {
    if (!files || files.length === 0) return null;

    return (
      <div style={{ marginBottom: 8 }}>
        <Text strong>{title}：</Text>
        {files.length === 1 ? (
          <Tooltip title={files[0].path}>
            <Text copyable={{ text: files[0].path }}>
              {this.formatFileName(files[0].path)}
            </Text>
          </Tooltip>
        ) : (
          <div style={{ marginTop: 4, paddingLeft: 12 }}>
            {files.map((file, index) => (
              <div key={index} style={{ marginBottom: 2 }}>
                <Text type="secondary">{index + 1}. </Text>
                <Tooltip title={file.path}>
                  <Text copyable={{ text: file.path }}>
                    {this.formatFileName(file.path)}
                  </Text>
                </Tooltip>
                {file.type !== 'video' && (
                  <Tag style={{ marginLeft: 4 }}>
                    {file.type === 'cover' ? '封面' : file.type}
                  </Tag>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    );
  };

  // 渲染阶段详情
  renderStageResults = (task: PipelineTask) => {
    const results = task.stage_results || [];
    if (results.length === 0) return null;

    return (
      <Collapse size="small" style={{ marginTop: 12 }}>
        {results.map((result, index) => (
          <Panel
            header={
              <Space>
                {this.getStageStatusIcon(result.status)}
                <Text>{this.getStageLabel(result.stage_name)}</Text>
                {result.status === 'failed' && (
                  <Text type="danger">失败</Text>
                )}
              </Space>
            }
            key={index}
          >
            {this.renderFileList(result.input_files || [], '输入文件')}
            {this.renderFileList(result.output_files || [], '输出文件')}
            {result.error_message && (
              <Alert
                message="错误信息"
                description={result.error_message}
                type="error"
                style={{ marginBottom: 8 }}
              />
            )}
            {result.commands && result.commands.length > 0 && (
              <div style={{ marginBottom: 8 }}>
                <Text strong>执行命令：</Text>
                {result.commands.map((cmd, idx) => (
                  <Paragraph
                    key={idx}
                    code
                    copyable
                    style={{ fontSize: 12, marginBottom: 4, wordBreak: 'break-all' }}
                  >
                    {cmd}
                  </Paragraph>
                ))}
              </div>
            )}
            {result.logs && (
              <div>
                <Text strong>执行日志：</Text>
                <Paragraph
                  style={{
                    whiteSpace: 'pre-wrap',
                    background: '#f5f5f5',
                    padding: 8,
                    borderRadius: 4,
                    fontSize: 12,
                    maxHeight: 200,
                    overflow: 'auto',
                  }}
                >
                  {result.logs}
                </Paragraph>
              </div>
            )}
          </Panel>
        ))}
      </Collapse>
    );
  };

  // 渲染任务详情（展开行）
  renderTaskDetail = (task: PipelineTask) => {
    return (
      <div style={{ padding: '16px 20px', background: '#fafafa' }}>
        <Row gutter={[16, 16]}>
          <Col span={24}>
            {/* 阶段进度 */}
            <div style={{ marginBottom: 16 }}>
              <Text strong style={{ marginBottom: 8, display: 'block' }}>处理进度：</Text>
              {this.renderStageProgress(task)}
            </div>
          </Col>
          <Col span={12}>
            <Space direction="vertical" style={{ width: '100%' }}>
              {this.renderFileList(task.initial_files, '输入文件')}
              {this.renderFileList(task.current_files, '当前/输出文件')}
              {task.record_info && (
                <div>
                  <Text strong>直播间：</Text>
                  <Text>
                    {task.record_info.room_name} ({task.record_info.host_name} - {task.record_info.platform})
                  </Text>
                </div>
              )}
              <div>
                <Text strong>创建时间：</Text>
                <Text>{this.formatTime(task.created_at)}</Text>
              </div>
            </Space>
          </Col>
          <Col span={12}>
            <Space direction="vertical" style={{ width: '100%' }}>
              {task.error_message && (
                <Alert
                  message="任务失败"
                  description={task.error_message}
                  type="error"
                />
              )}
            </Space>
          </Col>
        </Row>

        {/* 阶段详情 */}
        {this.renderStageResults(task)}

        {/* 操作按钮 */}
        <div style={{ marginTop: 16, borderTop: '1px solid #e8e8e8', paddingTop: 12 }}>
          <Space>
            {task.status === 'running' && (
              <Button danger icon={<StopOutlined />} onClick={() => this.handleCancel(task.id)}>
                取消任务
              </Button>
            )}
            {(task.status === 'failed' || task.status === 'cancelled') && task.can_retry && (
              <Button icon={<PlayCircleOutlined />} onClick={() => this.handleRetry(task.id)}>
                重新执行
              </Button>
            )}
            {task.status !== 'running' && (
              <Button danger icon={<DeleteOutlined />} onClick={() => this.handleDelete(task.id)}>
                删除任务
              </Button>
            )}
          </Space>
        </div>
      </div>
    );
  };

  render() {
    const { stats, loading, statusFilter, expandedRowKeys } = this.state;
    const filteredTasks = this.getFilteredTasks();

    const columns: ColumnsType<PipelineTask> = [
      {
        title: '开始时间',
        dataIndex: 'started_at',
        key: 'started_at',
        width: 160,
        render: (time: string) => this.formatTime(time),
      },
      {
        title: '状态',
        dataIndex: 'status',
        key: 'status',
        width: 100,
        render: (status: PipelineStatus) => this.getStatusTag(status),
      },
      {
        title: '直播间',
        key: 'room',
        width: 200,
        render: (_, record: PipelineTask) => (
          <span>
            {record.record_info?.room_name || '-'}
            {record.record_info?.platform && (
              <Tag style={{ marginLeft: 4 }}>{record.record_info.platform}</Tag>
            )}
          </span>
        ),
      },
      {
        title: '阶段',
        key: 'stages',
        width: 150,
        render: (_, record: PipelineTask) => {
          const stages = record.pipeline_config?.stages?.filter(s => s.enabled !== false) || [];
          return (
            <span>
              {record.current_stage + 1}/{stages.length}
              {stages.length > 0 && (
                <Text type="secondary" style={{ marginLeft: 4 }}>
                  ({this.getStageLabel(stages[Math.min(record.current_stage, stages.length - 1)]?.name)})
                </Text>
              )}
            </span>
          );
        },
      },
      {
        title: '进度',
        dataIndex: 'progress',
        key: 'progress',
        width: 120,
        render: (progress: number, record: PipelineTask) => (
          record.status === 'running' ? (
            <Progress percent={progress} size="small" />
          ) : record.status === 'completed' ? (
            <Progress percent={100} size="small" status="success" />
          ) : record.status === 'failed' ? (
            <Progress percent={progress || 0} size="small" status="exception" />
          ) : null
        ),
      },
      {
        title: '耗时',
        key: 'duration',
        width: 100,
        render: (_: unknown, record: PipelineTask) => (
          record.started_at ? this.formatDuration(record.started_at, record.completed_at) : '-'
        ),
      },
      {
        title: '完成时间',
        dataIndex: 'completed_at',
        key: 'completed_at',
        width: 160,
        render: (time: string) => this.formatTime(time),
      },
    ];

    return (
      <div className="task-list-container">
        {/* 统计卡片 */}
        {stats && (
          <Row gutter={16} style={{ marginBottom: 16 }}>
            <Col span={4}>
              <Card size="small">
                <Statistic
                  title="最大并发"
                  value={stats.max_concurrent}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ statusFilter: 'running' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title={<Badge status="processing" text="运行中" />}
                  value={stats.running_count}
                  valueStyle={{ color: '#1890ff' }}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ statusFilter: 'pending' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title={<Badge status="default" text="等待中" />}
                  value={stats.pending_count}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ statusFilter: 'completed' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title={<Badge status="success" text="已完成" />}
                  value={stats.completed_count}
                  valueStyle={{ color: '#52c41a' }}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ statusFilter: 'failed' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title={<Badge status="error" text="失败" />}
                  value={stats.failed_count}
                  valueStyle={{ color: '#ff4d4f' }}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ statusFilter: 'all' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title="全部"
                  value={stats.running_count + stats.pending_count + stats.completed_count + stats.failed_count + stats.cancelled_count}
                />
              </Card>
            </Col>
          </Row>
        )}

        {/* 失败任务醒目提示 */}
        {stats && stats.failed_count > 0 && (
          <Alert
            message={`有 ${stats.failed_count} 个任务执行失败`}
            description="点击查看失败任务按钮可以查看详情并选择重新执行"
            type="error"
            showIcon
            icon={<CloseCircleOutlined />}
            style={{ marginBottom: 16 }}
            action={
              <Button
                size="small"
                danger
                onClick={() => this.setState({ statusFilter: 'failed' })}
              >
                查看失败任务
              </Button>
            }
          />
        )}

        {/* 工具栏 */}
        <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Space>
            <span>状态筛选:</span>
            <Select
              value={statusFilter}
              onChange={(v) => this.setState({ statusFilter: v })}
              style={{ width: 100 }}
              options={[
                { value: 'all', label: '全部' },
                { value: 'running', label: '运行中' },
                { value: 'pending', label: '等待中' },
                { value: 'completed', label: '已完成' },
                { value: 'failed', label: '失败' },
                { value: 'cancelled', label: '已取消' },
              ]}
            />
          </Space>
          <Space>
            {stats && stats.completed_count > 0 && (
              <Button
                icon={<ClearOutlined />}
                onClick={this.handleClearCompleted}
              >
                清除已完成 ({stats.completed_count})
              </Button>
            )}
            <Button
              icon={<ReloadOutlined />}
              onClick={this.loadData}
              loading={loading}
            >
              刷新
            </Button>
          </Space>
        </div>

        {/* 任务列表 */}
        <Table
          columns={columns}
          dataSource={filteredTasks}
          rowKey="id"
          size="small"
          expandable={{
            expandedRowRender: this.renderTaskDetail,
            expandedRowKeys: expandedRowKeys,
            expandRowByClick: true,
            expandIcon: () => null, // 隐藏展开图标
            onExpand: (expanded, record) => {
              this.setState({
                expandedRowKeys: expanded
                  ? [...expandedRowKeys, record.id]
                  : expandedRowKeys.filter(k => k !== record.id)
              });
            },
          }}
          onRow={(record) => ({
            onClick: () => {
              const isExpanded = expandedRowKeys.includes(record.id);
              this.setState({
                expandedRowKeys: isExpanded
                  ? expandedRowKeys.filter(k => k !== record.id)
                  : [...expandedRowKeys, record.id]
              });
            },
            style: { cursor: 'pointer' }
          })}
          pagination={{
            pageSize: 20,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total) => `共 ${total} 条`,
          }}
          loading={loading}
        />
      </div>
    );
  }
}

export default PipelineTaskList;
