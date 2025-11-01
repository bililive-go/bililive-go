import React from 'react';
import { HashRouter as Router, Link } from 'react-router-dom';
import { Layout, Menu, Icon, Drawer } from 'antd';
import './layout.css';

const { SubMenu } = Menu;
const { Header, Content, Sider } = Layout;

interface IState {
    drawerVisible: boolean;
}

class RootLayout extends React.Component<{}, IState> {
    constructor(props: {}) {
        super(props);
        this.state = {
            drawerVisible: false
        };
    }

    showDrawer = () => {
        this.setState({ drawerVisible: true });
    };

    closeDrawer = () => {
        this.setState({ drawerVisible: false });
    };

    renderMenuItems() {
        return (
            <Menu
                mode="inline"
                defaultSelectedKeys={['1']}
                defaultOpenKeys={['sub1']}
                style={{ height: '100%', borderRight: 0 }}
                onClick={this.closeDrawer}
            >
                <SubMenu
                    key="sub1"
                    title={
                        <span>
                            <Icon type="monitor" />
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
        );
    }

    render() {
        return (
            <Layout className="all-layout">
                <Header className="header small-header">
                    <Icon 
                        type="menu" 
                        className="mobile-menu-icon"
                        onClick={this.showDrawer}
                    />
                    <h3 className="logo-text">Bililive-go</h3>
                </Header>
                <Layout>
                    <Router>
                        <Sider className="side-bar" width={200} style={{ background: '#fff' }}>
                            {this.renderMenuItems()}
                        </Sider>
                        <Drawer
                            title="菜单"
                            placement="left"
                            closable={true}
                            onClose={this.closeDrawer}
                            visible={this.state.drawerVisible}
                            className="mobile-drawer"
                        >
                            {this.renderMenuItems()}
                        </Drawer>
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
