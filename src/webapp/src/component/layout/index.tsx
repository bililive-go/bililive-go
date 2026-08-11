import React from 'react';
import { HashRouter as Router, Link, useLocation } from 'react-router-dom';
import { Layout, Menu, Button } from 'antd';
import {
    MonitorOutlined,
    UnorderedListOutlined,
    DashboardOutlined,
    SettingOutlined,
    FolderOutlined,
    ToolOutlined,
    MenuFoldOutlined,
    MenuUnfoldOutlined,
    LineChartOutlined,
    CloudUploadOutlined,
    CalendarOutlined,
    CommentOutlined,
    BugOutlined
} from '@ant-design/icons';
import './layout.css';

const { Header, Content, Sider } = Layout;
// 功能开关：IO 统计（开发中，设为 false 隐藏 UI）
const ENABLE_IO_STATS_UI = false;

interface Props {
    children?: React.ReactNode;
}

interface State {
    collapsed: boolean;
}

// localStorage key 用于保存侧边栏收起状态
const SIDER_COLLAPSED_KEY = 'siderCollapsed';

const getSelectedMenuKey = (path: string): string => {
    if (path.startsWith('/liveInfo')) return '2';
    if (path.startsWith('/configInfo')) return '3';
    if (path.startsWith('/danmaku')) return 'danmaku';
    if (path.startsWith('/fileList')) return '4';
    if (path.startsWith('/tasks')) return 'tasks';
    if (path.startsWith('/diagnostics')) return 'diagnostics';
    if (path.startsWith('/iostats')) return 'iostats';
    if (path.startsWith('/update')) return 'update';
    return '1';
};

interface RootLayoutContentProps extends Props {
    selectedKey: string;
}

class RootLayoutContent extends React.Component<RootLayoutContentProps, State> {
    constructor(props: RootLayoutContentProps) {
        super(props);
        // 从 localStorage 读取收起状态
        let collapsed = false;
        try {
            const saved = localStorage.getItem(SIDER_COLLAPSED_KEY);
            if (saved !== null) {
                collapsed = saved === 'true';
            }
        } catch (e) {
            console.error('读取侧边栏状态失败:', e);
        }
        this.state = { collapsed };
    }

    toggleCollapsed = () => {
        const collapsed = !this.state.collapsed;
        this.setState({ collapsed });
        // 保存到 localStorage
        try {
            localStorage.setItem(SIDER_COLLAPSED_KEY, String(collapsed));
        } catch (e) {
            console.error('保存侧边栏状态失败:', e);
        }
    };

    render() {
        const { collapsed } = this.state;
        return (
            <Layout className="all-layout">
                    <Header className="header small-header">
                        <h3 className="logo-text">Bililive-go</h3>
                        <Link className="mobile-diagnostic-link" to="/diagnostics">
                            <BugOutlined />
                            诊断分析
                        </Link>
                    </Header>
                    <Layout>
                        <Sider
                            className="side-bar"
                            width={200}
                            collapsedWidth={60}
                            style={{ background: '#fff' }}
                            trigger={null}
                            collapsible
                            collapsed={collapsed}
                        >
                            {/* 折叠按钮在顶部，与菜单图标对齐 */}
                            <div style={{
                                padding: '12px 0',
                                borderBottom: '1px solid #f0f0f0',
                                width: '100%'
                            }}>
                                <Button
                                    type="text"
                                    icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
                                    onClick={this.toggleCollapsed}
                                    style={{
                                        fontSize: 16,
                                        width: '100%',
                                        textAlign: 'left',
                                        paddingLeft: collapsed ? 20 : 24,
                                        height: 40
                                    }}
                                >
                                    {!collapsed && '收起菜单'}
                                </Button>
                            </div>
                            <Menu
                                mode="inline"
                                selectedKeys={[this.props.selectedKey]}
                                inlineCollapsed={collapsed}
                                style={{ borderRight: 0 }}
                                items={[
                                    {
                                        key: '1',
                                        icon: <MonitorOutlined />,
                                        label: <Link to="/">监控列表</Link>,
                                    },
                                    {
                                        key: '2',
                                        icon: <DashboardOutlined />,
                                        label: <Link to="/liveInfo">系统状态</Link>,
                                    },
                                    {
                                        key: '3',
                                        icon: <SettingOutlined />,
                                        label: <Link to="/configInfo">设置</Link>,
                                    },
                                    {
                                        key: 'danmaku',
                                        icon: <CommentOutlined />,
                                        label: <Link to="/danmaku">弹幕</Link>,
                                    },
                                    {
                                        key: '4',
                                        icon: <FolderOutlined />,
                                        label: <Link to="/fileList">文件</Link>,
                                    },
                                    {
                                        key: '5',
                                        icon: <ToolOutlined />,
                                        label: <a href="/tools/" target="_blank" rel="noopener noreferrer">工具</a>,
                                    },
                                    {
                                        key: 'tasks',
                                        icon: <UnorderedListOutlined />,
                                        label: <Link to="/tasks">任务队列</Link>,
                                    },
                                    {
                                        key: 'diagnostics',
                                        icon: <BugOutlined />,
                                        label: <Link to="/diagnostics">诊断分析</Link>,
                                    },
                                    {
                                        key: 'scheduler',
                                        icon: <CalendarOutlined />,
                                        label: <a href="/scheduler/" target="_blank" rel="noopener noreferrer">调度器</a>,
                                    },
                                    ...(ENABLE_IO_STATS_UI ? [{
                                        key: 'iostats',
                                        icon: <LineChartOutlined />,
                                        label: <Link to="/iostats">IO 统计</Link>,
                                    }] : []),
                                    {
                                        key: 'update',
                                        icon: <CloudUploadOutlined />,
                                        label: <Link to="/update">更新</Link>,
                                    }
                                ]}
                            />
                        </Sider>
                        <Layout className="content-padding">
                            <Content
                                className="inside-content-padding"
                                style={{
                                    background: '#fff',
                                    margin: 0,
                                    minHeight: 280,
                                    overflow: "auto",
                                }}>
                                {this.props.children}
                            </Content>
                        </Layout>
                    </Layout>
            </Layout>
        )
    }
}

const RoutedRootLayoutContent: React.FC<Props> = (props) => {
    const location = useLocation();
    return (
        <RootLayoutContent
            {...props}
            selectedKey={getSelectedMenuKey(location.pathname)}
        />
    );
};

const RootLayout: React.FC<Props> = (props) => (
    <Router>
        <RoutedRootLayoutContent {...props} />
    </Router>
);

export default RootLayout;
