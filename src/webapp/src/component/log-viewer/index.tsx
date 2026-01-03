import React from "react";
import API from '../../utils/api';
import {
    PageHeader,
    Button,
    Spin,
    Alert
} from 'antd';
import './log-viewer.css';

const api = new API();

const AUTO_REFRESH_INTERVAL_MS = 3000; // 自动刷新间隔（毫秒）

interface IState {
    logs: string
    loading: boolean
    error: string
    totalLines: number
    shownLines: number
    autoRefresh: boolean
}

class LogViewer extends React.Component<{}, IState> {
    private refreshInterval: NodeJS.Timeout | null = null;
    private logsContainerRef = React.createRef<HTMLDivElement>();

    constructor(props: {}) {
        super(props);
        this.state = {
            logs: "",
            loading: false,
            error: "",
            totalLines: 0,
            shownLines: 0,
            autoRefresh: false
        };
    }

    componentDidMount() {
        this.fetchLogs();
    }

    componentWillUnmount() {
        this.stopAutoRefresh();
    }

    fetchLogs = () => {
        this.setState({ loading: true, error: "" });
        api.getLogs()
            .then((rsp: any) => {
                if (rsp.err_msg) {
                    this.setState({
                        error: rsp.err_msg,
                        loading: false
                    });
                } else {
                    this.setState({
                        logs: rsp.logs || "",
                        totalLines: rsp.total_lines || 0,
                        shownLines: rsp.shown_lines || 0,
                        loading: false,
                        error: ""
                    });
                    // Auto scroll to bottom
                    setTimeout(() => {
                        if (this.logsContainerRef.current) {
                            this.logsContainerRef.current.scrollTop = this.logsContainerRef.current.scrollHeight;
                        }
                    }, 100);
                }
            })
            .catch(err => {
                this.setState({
                    error: "请求服务器失败: " + err,
                    loading: false
                });
            });
    }

    toggleAutoRefresh = () => {
        if (this.state.autoRefresh) {
            this.stopAutoRefresh();
        } else {
            this.startAutoRefresh();
        }
    }

    startAutoRefresh = () => {
        this.setState({ autoRefresh: true });
        this.refreshInterval = setInterval(() => {
            this.fetchLogs();
        }, AUTO_REFRESH_INTERVAL_MS);
    }

    stopAutoRefresh = () => {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
            this.refreshInterval = null;
        }
        this.setState({ autoRefresh: false });
    }

    render() {
        return (
            <div>
                <div style={{ backgroundColor: '#F5F5F5', }}>
                    <PageHeader
                        ghost={false}
                        title="日志查看器"
                        subTitle="Log Viewer"
                        extra={[
                            <Button
                                key="refresh"
                                type="primary"
                                onClick={this.fetchLogs}
                                loading={this.state.loading}
                                disabled={this.state.autoRefresh}
                            >
                                刷新
                            </Button>,
                            <Button
                                key="auto-refresh"
                                type={this.state.autoRefresh ? "danger" : "default"}
                                onClick={this.toggleAutoRefresh}
                            >
                                {this.state.autoRefresh ? "停止自动刷新" : "自动刷新"}
                            </Button>
                        ]}
                    >
                    </PageHeader>
                </div>
                {this.state.error && (
                    <Alert
                        message="错误"
                        description={this.state.error}
                        type="error"
                        closable
                        style={{ margin: '16px' }}
                    />
                )}
                {this.state.shownLines > 0 && (
                    <div style={{ padding: '8px 16px', backgroundColor: '#f0f0f0' }}>
                        显示最近 {this.state.shownLines} 行日志 (共 {this.state.totalLines} 行)
                    </div>
                )}
                <Spin spinning={this.state.loading}>
                    <div ref={this.logsContainerRef} className="logs-container">
                        <pre className="logs-content">{this.state.logs || "暂无日志内容"}</pre>
                    </div>
                </Spin>
            </div>
        );
    }
}

export default LogViewer;
