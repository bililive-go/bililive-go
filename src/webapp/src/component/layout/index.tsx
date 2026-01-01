import React from 'react';
import { HashRouter as Router, Link } from 'react-router-dom';
import { Layout, Menu } from 'antd';
import { MonitorOutlined } from '@ant-design/icons';
import './layout.css';

const { SubMenu } = Menu;
const { Header, Content, Sider } = Layout;

interface Props {
    children?: React.ReactNode;
}

class RootLayout extends React.Component<Props> {
    render() {
        return (
            <Layout className="all-layout">
                <Header className="header small-header">
                    <h3 className="logo-text">Bililive-go</h3>
                </Header>
                <Layout>
                    {/* @ts-ignore */}
                    <Router>
                        <Sider className="side-bar" width={200} style={{ background: '#fff' }}>
                            <Menu
                                mode="inline"
                                defaultSelectedKeys={['1']}
                                defaultOpenKeys={['sub1']}
                                style={{ height: '100%', borderRight: 0 }}
                            >
                                <SubMenu
                                    key="sub1"
                                    title={
                                        <span>
                                            <MonitorOutlined />
                                            LiveClient
                                        </span>
                                    }
                                >
                                    <Menu.Item key="1"><Link to="/">监控列表</Link></Menu.Item>
                                    <Menu.Item key="2"><Link to="/liveInfo">系统状态</Link></Menu.Item>
                                    <Menu.Item key="3"><Link to="/configInfo">设置</Link></Menu.Item>
                                    <Menu.Item key="4"><Link to="/fileList">文件</Link></Menu.Item>
                                    <Menu.Item key="5"><a href="/tools/">工具</a></Menu.Item>
                                </SubMenu>
                            </Menu>
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
                    </Router>
                </Layout>
            </Layout>
        )
    }
}

export default RootLayout;
