import React, { Component } from 'react';
import { Table, Button, Tag, Space, Progress, Tooltip, Card, Statistic, Row, Col, Modal, message, Badge } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ReloadOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  DeleteOutlined,
  ArrowUpOutlined,
  ArrowDownOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  ClockCircleOutlined,
  LoadingOutlined
} from '@ant-design/icons';
import './index.css';

// 任务类型
type TaskType = 'fix_flv' | 'convert_mp4';

// 任务状态
type TaskStatus = 'pending' | 'running' | 'completed' | 'failed' | 'cancelled';

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
  filter: TaskStatus | 'all';
}

class TaskList extends Component<object, TaskListState> {
  private pollInterval: ReturnType<typeof setInterval> | null = null;

  constructor(props: object) {
    super(props);
    this.state = {
      tasks: [],
      stats: null,
      loading: false,
      filter: 'all',
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

  handlePriorityChange = async (taskId: number, delta: number) => {
    const task = this.state.tasks.find(t => t.id === taskId);
    if (!task) return;

    const newPriority = task.priority + delta;
    try {
      const res = await fetch(`/api/tasks/${taskId}/priority`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ priority: newPriority }),
      });
      if (res.ok) {
        this.loadData();
      }
    } catch (error) {
      message.error('优先级更新失败');
    }
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

  getFilteredTasks = (): Task[] => {
    const { tasks, filter } = this.state;
    if (filter === 'all') return tasks;
    return tasks.filter(t => t.status === filter);
  };

  render() {
    const { stats, loading, filter } = this.state;
    const filteredTasks = this.getFilteredTasks();

    const columns: ColumnsType<Task> = [
      {
        title: 'ID',
        dataIndex: 'id',
        key: 'id',
        width: 60,
      },
      {
        title: '类型',
        dataIndex: 'type',
        key: 'type',
        width: 100,
        render: (type: TaskType) => this.getTypeLabel(type),
      },
      {
        title: '状态',
        dataIndex: 'status',
        key: 'status',
        width: 100,
        render: (status: TaskStatus) => this.getStatusTag(status),
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
            <Progress percent={100} size="small" />
          ) : null
        ),
      },
      {
        title: '输入文件',
        dataIndex: 'input_file',
        key: 'input_file',
        ellipsis: true,
        render: (path: string) => (
          <Tooltip title={path}>
            <span>{this.formatFileName(path)}</span>
          </Tooltip>
        ),
      },
      {
        title: '直播间',
        key: 'room',
        width: 150,
        render: (_: unknown, record: Task) => (
          record.room_name ? (
            <Tooltip title={`${record.host_name} - ${record.platform}`}>
              <span>{record.room_name}</span>
            </Tooltip>
          ) : '-'
        ),
      },
      {
        title: '优先级',
        dataIndex: 'priority',
        key: 'priority',
        width: 100,
        render: (priority: number, record: Task) => (
          <Space size={4}>
            <span>{priority}</span>
            {record.status === 'pending' && (
              <>
                <Button
                  type="text"
                  size="small"
                  icon={<ArrowUpOutlined />}
                  onClick={() => this.handlePriorityChange(record.id, 1)}
                />
                <Button
                  type="text"
                  size="small"
                  icon={<ArrowDownOutlined />}
                  onClick={() => this.handlePriorityChange(record.id, -1)}
                />
              </>
            )}
          </Space>
        ),
      },
      {
        title: '创建时间',
        dataIndex: 'created_at',
        key: 'created_at',
        width: 160,
        render: (time: string) => this.formatTime(time),
      },
      {
        title: '错误信息',
        dataIndex: 'error_message',
        key: 'error_message',
        ellipsis: true,
        render: (error: string) => error ? (
          <Tooltip title={error}>
            <span style={{ color: 'red' }}>{error}</span>
          </Tooltip>
        ) : null,
      },
      {
        title: '操作',
        key: 'actions',
        width: 150,
        render: (_: unknown, record: Task) => (
          <Space size={4}>
            {record.status === 'running' && (
              <Tooltip title="取消">
                <Button
                  type="text"
                  danger
                  icon={<PauseCircleOutlined />}
                  onClick={() => this.handleCancel(record.id)}
                />
              </Tooltip>
            )}
            {(record.status === 'failed' || record.status === 'cancelled') && record.can_requeue && (
              <Tooltip title="重新排队">
                <Button
                  type="text"
                  icon={<PlayCircleOutlined />}
                  onClick={() => this.handleRequeue(record.id)}
                />
              </Tooltip>
            )}
            {record.status !== 'running' && (
              <Tooltip title="删除">
                <Button
                  type="text"
                  danger
                  icon={<DeleteOutlined />}
                  onClick={() => this.handleDelete(record.id)}
                />
              </Tooltip>
            )}
          </Space>
        ),
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
              <Card size="small" onClick={() => this.setState({ filter: 'running' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title={<Badge status="processing" text="运行中" />}
                  value={stats.running_count}
                  valueStyle={{ color: '#1890ff' }}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ filter: 'pending' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title={<Badge status="default" text="等待中" />}
                  value={stats.pending_count}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ filter: 'completed' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title={<Badge status="success" text="已完成" />}
                  value={stats.completed_count}
                  valueStyle={{ color: '#52c41a' }}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ filter: 'failed' })} style={{ cursor: 'pointer' }}>
                <Statistic
                  title={<Badge status="error" text="失败" />}
                  value={stats.failed_count}
                  valueStyle={{ color: '#ff4d4f' }}
                />
              </Card>
            </Col>
            <Col span={4}>
              <Card size="small" onClick={() => this.setState({ filter: 'all' })} style={{ cursor: 'pointer' }}>
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
            <span>当前筛选: </span>
            <Tag
              color={filter === 'all' ? 'blue' : 'default'}
              style={{ cursor: 'pointer' }}
              onClick={() => this.setState({ filter: 'all' })}
            >
              全部
            </Tag>
            <Tag
              color={filter === 'running' ? 'blue' : 'default'}
              style={{ cursor: 'pointer' }}
              onClick={() => this.setState({ filter: 'running' })}
            >
              运行中
            </Tag>
            <Tag
              color={filter === 'pending' ? 'blue' : 'default'}
              style={{ cursor: 'pointer' }}
              onClick={() => this.setState({ filter: 'pending' })}
            >
              等待中
            </Tag>
            <Tag
              color={filter === 'failed' ? 'blue' : 'default'}
              style={{ cursor: 'pointer' }}
              onClick={() => this.setState({ filter: 'failed' })}
            >
              失败
            </Tag>
          </Space>
          <Button
            icon={<ReloadOutlined />}
            onClick={this.loadData}
            loading={loading}
          >
            刷新
          </Button>
        </div>

        {/* 任务列表 */}
        <Table
          columns={columns}
          dataSource={filteredTasks}
          rowKey="id"
          size="small"
          pagination={{
            pageSize: 20,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total) => `共 ${total} 条`,
          }}
          loading={loading}
          scroll={{ x: 1200 }}
        />
      </div>
    );
  }
}

export default TaskList;
