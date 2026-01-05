import React, { Component } from 'react';
import { Table, Button, Tag, Space, Progress, Tooltip, Card, Statistic, Row, Col, Modal, message, Badge, Collapse, Typography, Select } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ReloadOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  DeleteOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  ClockCircleOutlined,
  LoadingOutlined,
  ClearOutlined
} from '@ant-design/icons';
import './index.css';

const { Text, Paragraph } = Typography;
const { Panel } = Collapse;

// 任务类型
type TaskType = 'fix_flv' | 'convert_mp4';

// 任务状态
type TaskStatus = 'pending' | 'running' | 'completed' | 'failed' | 'cancelled' | 'skipped';

// 任务接口
interface Task {
  id: number;
  type: TaskType;
  status: TaskStatus;
  priority: number;
  input_file: string;
  output_file: string;
  live_id: string;
  room_name: string;
  host_name: string;
  platform: string;
  created_at: string;
  started_at: string | null;
  completed_at: string | null;
  error_message: string;
  progress: number;
  can_requeue: boolean;
  commands: string[];
  logs: string;
}

// 队列统计
interface QueueStats {
  max_concurrent: number;
  running_count: number;
  pending_count: number;
  completed_count: number;
  failed_count: number;
  cancelled_count: number;
}

interface TaskListState {
  tasks: Task[];
  stats: QueueStats | null;
  loading: boolean;
  statusFilter: TaskStatus | 'all';
  typeFilter: TaskType | 'all';
  expandedRowKeys: number[];
}

class TaskList extends Component<object, TaskListState> {
  private pollInterval: ReturnType<typeof setInterval> | null = null;

  constructor(props: object) {
    super(props);
    this.state = {
      tasks: [],
      stats: null,
      loading: false,
      statusFilter: 'all',
      typeFilter: 'all',
      expandedRowKeys: [],
    };
  }

  componentDidMount() {
    this.loadData();
    // 每5秒刷新一次
    this.pollInterval = setInterval(() => this.loadData(), 5000);
  }

  componentWillUnmount() {
    if (this.pollInterval) {
      clearInterval(this.pollInterval);
    }
  }

  loadData = async () => {
    try {
      const [tasksRes, statsRes] = await Promise.all([
        fetch('/api/tasks'),
        fetch('/api/tasks/stats'),
      ]);

      if (tasksRes.ok && statsRes.ok) {
        const tasks = await tasksRes.json();
        const stats = await statsRes.json();
        this.setState({ tasks: tasks || [], stats, loading: false });
      }
    } catch (error) {
      console.error('Failed to load tasks:', error);
    }
  };

  handleCancel = async (taskId: number) => {
    try {
      const res = await fetch(`/api/tasks/${taskId}/cancel`, { method: 'POST' });
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

  handleRequeue = async (taskId: number) => {
    try {
      const res = await fetch(`/api/tasks/${taskId}/requeue`, { method: 'POST' });
      if (res.ok) {
        message.success('任务已重新排队');
        this.loadData();
      } else {
        message.error('重新排队失败');
      }
    } catch (error) {
      message.error('重新排队失败');
    }
  };

  handleDelete = async (taskId: number) => {
    Modal.confirm({
      title: '确认删除',
      content: '确定要删除这个任务吗？',
      onOk: async () => {
        try {
          const res = await fetch(`/api/tasks/${taskId}`, { method: 'DELETE' });
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
          const res = await fetch('/api/tasks/clear-completed', { method: 'POST' });
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

  getTypeLabel = (type: TaskType): string => {
    switch (type) {
      case 'fix_flv': return '修复FLV';
      case 'convert_mp4': return '转换MP4';
      default: return type;
    }
  };

  getStatusTag = (status: TaskStatus) => {
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
        return <Tag color="warning">已取消</Tag>;
      case 'skipped':
        return <Tag color="cyan">已跳过</Tag>;
      default:
        return <Tag>{status}</Tag>;
    }
  };

  formatTime = (time: string | null): string => {
    if (!time) return '-';
    return new Date(time).toLocaleString('zh-CN');
  };

  formatFileName = (path: string): string => {
    if (!path) return '-';
    const parts = path.split(/[/\\]/);
    return parts[parts.length - 1];
  };

  formatDuration = (startTime: string | null, endTime: string | null): string => {
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

  getFilteredTasks = (): Task[] => {
    const { tasks, statusFilter, typeFilter } = this.state;
    return tasks.filter(t => {
      if (statusFilter !== 'all' && t.status !== statusFilter) return false;
      if (typeFilter !== 'all' && t.type !== typeFilter) return false;
      return true;
    });
  };

  // 渲染任务详情（展开行）
  renderTaskDetail = (task: Task) => {
    return (
      <div style={{ padding: '16px 20px', background: '#fafafa' }}>
        <Row gutter={[16, 16]}>
          <Col span={12}>
            <Space direction="vertical" style={{ width: '100%' }}>
              <div>
                <Text strong>输入文件：</Text>
                <Tooltip title={task.input_file}>
                  <Text copyable={{ text: task.input_file }}>{this.formatFileName(task.input_file)}</Text>
                </Tooltip>
              </div>
              {task.output_file && (
                <div>
                  <Text strong>输出文件：</Text>
                  <Tooltip title={task.output_file}>
                    <Text copyable={{ text: task.output_file }}>{this.formatFileName(task.output_file)}</Text>
                  </Tooltip>
                </div>
              )}
              {task.room_name && (
                <div>
                  <Text strong>直播间：</Text>
                  <Text>{task.room_name} ({task.host_name} - {task.platform})</Text>
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
                <div>
                  <Text strong type="danger">错误信息：</Text>
                  <br />
                  <Text type="danger">{task.error_message}</Text>
                </div>
              )}
              {task.logs && (
                <div>
                  <Text strong>执行日志：</Text>
                  <br />
                  <Paragraph style={{ whiteSpace: 'pre-wrap', background: '#f5f5f5', padding: 8, borderRadius: 4, marginBottom: 0 }}>
                    {task.logs}
                  </Paragraph>
                </div>
              )}
              {task.commands && task.commands.length > 0 && (
                <Collapse size="small" defaultActiveKey={['commands']}>
                  <Panel header="执行命令" key="commands">
                    {task.commands.map((cmd, idx) => (
                      <Paragraph
                        key={idx}
                        code
                        copyable
                        style={{ fontSize: 13, marginBottom: idx < task.commands.length - 1 ? 8 : 0, wordBreak: 'break-all' }}
                      >
                        {cmd}
                      </Paragraph>
                    ))}
                  </Panel>
                </Collapse>
              )}
            </Space>
          </Col>
        </Row>
        {/* 操作按钮 */}
        <div style={{ marginTop: 16, borderTop: '1px solid #e8e8e8', paddingTop: 12 }}>
          <Space>
            {task.status === 'running' && (
              <Button danger icon={<PauseCircleOutlined />} onClick={() => this.handleCancel(task.id)}>
                取消任务
              </Button>
            )}
            {(task.status === 'failed' || task.status === 'cancelled') && task.can_requeue && (
              <Button icon={<PlayCircleOutlined />} onClick={() => this.handleRequeue(task.id)}>
                重新排队
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
    const { stats, loading, statusFilter, typeFilter, expandedRowKeys } = this.state;
    const filteredTasks = this.getFilteredTasks();

    const columns: ColumnsType<Task> = [
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
        render: (status: TaskStatus) => this.getStatusTag(status),
      },
      {
        title: '类型',
        dataIndex: 'type',
        key: 'type',
        width: 100,
        render: (type: TaskType) => this.getTypeLabel(type),
      },
      {
        title: '进度',
        dataIndex: 'progress',
        key: 'progress',
        width: 120,
        render: (progress: number, record: Task) => (
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
        render: (_: unknown, record: Task) => (
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
              <Card size="small" onClick={() => this.setState({ statusFilter: 'all', typeFilter: 'all' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title="全部"
                  value={stats.running_count + stats.pending_count + stats.completed_count + stats.failed_count + stats.cancelled_count}
                />
              </Card>
            </Col>
          </Row>
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
                { value: 'skipped', label: '已跳过' },
                { value: 'failed', label: '失败' },
                { value: 'cancelled', label: '已取消' },
              ]}
            />
            <span>类型筛选:</span>
            <Select
              value={typeFilter}
              onChange={(v) => this.setState({ typeFilter: v })}
              style={{ width: 120 }}
              options={[
                { value: 'all', label: '全部' },
                { value: 'fix_flv', label: '修复FLV' },
                { value: 'convert_mp4', label: '转换MP4' },
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

export default TaskList;
