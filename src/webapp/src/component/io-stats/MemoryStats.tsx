import React, { useState, useEffect, useCallback } from 'react';
import { Card, Row, Col, Progress, Table, Statistic, Button, Empty } from 'antd';
import { ReloadOutlined, DashboardOutlined, BuildOutlined, AppstoreOutlined } from '@ant-design/icons';

// 数据单位转换工具函数
const formatBytes = (bytes: number, decimals = 2) => {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
};

interface SelfMemoryStats {
  alloc: number;
  total_alloc: number;
  sys: number;
  num_gc: number;
}

interface ProcessMemoryStats {
  pid: number;
  name: string;
  rss: number;
  vms: number;
}

interface ContainerMemoryStats {
  limit: number;
  used: number;
}

interface MemoryStatsResponse {
  self_memory: SelfMemoryStats;
  child_process_memory: ProcessMemoryStats[];
  container_memory?: ContainerMemoryStats;
}

const MemoryStats: React.FC = () => {
  const [data, setData] = useState<MemoryStatsResponse | null>(null);
  const [loading, setLoading] = useState(false);

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const response = await fetch('/api/memory');
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const jsonData = await response.json();
      setData(jsonData);
    } catch (error) {
      console.error('Failed to fetch memory stats:', error);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 5000); // 5秒自动刷新
    return () => clearInterval(interval);
  }, [fetchData]);

  const processColumns = [
    {
      title: 'PID',
      dataIndex: 'pid',
      key: 'pid',
      width: 80,
    },
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
    },
    {
      title: 'RSS (物理内存)',
      dataIndex: 'rss',
      key: 'rss',
      render: (val: number) => formatBytes(val),
    },
    {
      title: 'VMS (虚拟内存)',
      dataIndex: 'vms',
      key: 'vms',
      render: (val: number) => formatBytes(val),
    },
  ];

  if (!data) return <Empty description="暂无数据" />;

  return (
    <div className="memory-stats">
      <div style={{ marginBottom: 16, textAlign: 'right' }}>
        <Button
          icon={<ReloadOutlined />}
          onClick={fetchData}
          loading={loading}
          type="primary"
        >
          刷新
        </Button>
      </div>

      <Row gutter={[16, 16]}>
        {/* 自身内存统计 */}
        <Col xs={24} md={12} lg={8}>
          <Card
            title={<span><DashboardOutlined /> 自身内存</span>}
            size="small"
            hoverable
          >
            <Row gutter={16}>
              <Col span={12}>
                <Statistic title="当前分配 (Alloc)" value={formatBytes(data.self_memory.alloc)} />
              </Col>
              <Col span={12}>
                <Statistic title="系统占用 (Sys)" value={formatBytes(data.self_memory.sys)} />
              </Col>
              <Col span={12} style={{ marginTop: 16 }}>
                <Statistic title="累计分配 (Total)" value={formatBytes(data.self_memory.total_alloc)} />
              </Col>
              <Col span={12} style={{ marginTop: 16 }}>
                <Statistic title="GC 次数" value={data.self_memory.num_gc} />
              </Col>
            </Row>
          </Card>
        </Col>

        {/* 容器内存统计 (如果有) */}
        {data.container_memory && (
          <Col xs={24} md={12} lg={8}>
            <Card
              title={<span><BuildOutlined /> 容器内存</span>}
              size="small"
              hoverable
            >
              <div style={{ textAlign: 'center', marginBottom: 16 }}>
                <Progress
                  type="dashboard"
                  percent={data.container_memory.limit > 0 ? parseFloat((data.container_memory.used / data.container_memory.limit * 100).toFixed(1)) : 0}
                  format={percent => `${percent}%`}
                />
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span>已用: {formatBytes(data.container_memory.used)}</span>
                <span>限制: {data.container_memory.limit > 0 ? formatBytes(data.container_memory.limit) : '无限制'}</span>
              </div>
            </Card>
          </Col>
        )}

        {/* 如果没有容器内存，占位以保持布局整齐? 不需要，Row 可以自动处理 */}

        {/* 子进程内存统计 */}
        <Col xs={24}>
          <Card
            title={<span><AppstoreOutlined /> 子进程内存 (FFmpeg/Parsers)</span>}
            size="small"
            bodyStyle={{ padding: 0 }}
          >
            <Table
              dataSource={data.child_process_memory}
              columns={processColumns}
              rowKey="pid"
              pagination={false}
              size="small"
              locale={{
                emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="无活动子进程 (原生下载器的内存计入自身内存)" />
              }}
            />
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default MemoryStats;
