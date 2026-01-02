import React from 'react';
import { HashRouter as Router, Link } from 'react-router-dom';
import { Layout, Menu } from 'antd';
import { MonitorOutlined } from '@ant-design/icons';
import './layout.css';

const { Header, Content, Sider } = Layout;

interface Props {
    children?: React.ReactNode;
}

class RootLayout extends React.Component<Props> {
    render() {
        return (
            <Router>
                <Layout className="all-layout">
                    <Header className="header small-header">
                        <h3 className="logo-text">Bililive-go</h3>
                    </Header>
                    <Layout>
                        <Sider className="side-bar" width={200} style={{ background: '#fff' }}>
                            <Menu
                                mode="inline"
                                defaultSelectedKeys={['1']}
                                defaultOpenKeys={['sub1']}
                                style={{ height: '100%', borderRight: 0 }}
                                items={[
                                    {
                                        key: 'sub1',
                                        icon: <MonitorOutlined />,
                                        label: 'LiveClient',
                                        children: [
                                            { key: '1', label: <Link to="/">监控列表</Link> },
                                            { key: '2', label: <Link to="/liveInfo">系统状态</Link> },
                                            { key: '3', label: <Link to="/configInfo">设置</Link> },
                                            { key: '4', label: <Link to="/fileList">文件</Link> },
                                            { key: '5', label: <a href="/tools/">工具</a> },
                                        ]
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
            </Router>
        )
    }
}

export default RootLayout;
