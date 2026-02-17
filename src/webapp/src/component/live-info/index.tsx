import React from "react";
import API from '../../utils/api';
import {
    Descriptions,
    Button,
    Tag
} from 'antd';
import copy from 'copy-to-clipboard';

const api = new API();

interface Props {
    // 不需要任何 props
}

interface IState {
    appName: string
    appVersion: string
    buildTime: string
    gitHash: string
    pid: string
    platform: string
    goVersion: string
    isDocker: string
    puid: string
    pgid: string
    umask: string
    isLauncherManaged: boolean
    launcherPid: number
    launcherExePath: string
    bgoExePath: string
}

class LiveInfo extends React.Component<Props, IState> {

    constructor(props: Props) {
        super(props);
        this.state = {
            appName: "",
            appVersion: "",
            buildTime: "",
            gitHash: "",
            pid: "",
            platform: "",
            goVersion: "",
            isDocker: "",
            puid: "",
            pgid: "",
            umask: "",
            isLauncherManaged: false,
            launcherPid: 0,
            launcherExePath: "",
            bgoExePath: ""
        };
    }

    componentDidMount() {
        api.getLiveInfo()
            .then((rsp: any) => {
                this.setState({
                    appName: rsp.app_name,
                    appVersion: rsp.app_version,
                    buildTime: rsp.build_time,
                    gitHash: rsp.git_hash,
                    pid: rsp.pid,
                    platform: rsp.platform,
                    goVersion: rsp.go_version,
                    isDocker: rsp.is_docker,
                    puid: rsp.puid,
                    pgid: rsp.pgid,
                    umask: rsp.umask,
                    isLauncherManaged: rsp.is_launcher_managed || false,
                    launcherPid: rsp.launcher_pid || 0,
                    launcherExePath: rsp.launcher_exe_path || "",
                    bgoExePath: rsp.bgo_exe_path || ""
                })
            })
            .catch(err => {
                alert("请求服务器失败");
            })
    }

    isInContainer(): boolean {
        const v = (this.state.isDocker || "").toLowerCase();
        return v === "true";
    }

    getTextForCopy(): string {
        const inContainer = this.isInContainer();
        const extra = inContainer ? `\nPUID: ${this.state.puid}\nPGID: ${this.state.pgid}\nUMASK: ${this.state.umask}` : "";
        const launcherInfo = this.state.isLauncherManaged
            ? `\nLauncher Managed: 是\nLauncher PID: ${this.state.launcherPid}\nLauncher Path: ${this.state.launcherExePath}`
            : `\nLauncher Managed: 否`;
        return `
App Name: ${this.state.appName}
App Version: ${this.state.appVersion}
Build Time: ${this.state.buildTime}
BGO PID: ${this.state.pid}
BGO Path: ${this.state.bgoExePath}
Platform: ${this.state.platform}
Go Version: ${this.state.goVersion}
Git Hash: ${this.state.gitHash}
Is In Container: ${inContainer ? "是" : "否"}${extra}${launcherInfo}
`;
    }

    render() {
        return (
            <div>
                <div style={{
                    padding: '16px 24px',
                    backgroundColor: '#fff',
                    borderBottom: '1px solid #e8e8e8',
                    marginBottom: 16,
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center'
                }}>
                    <div>
                        <span style={{ fontSize: '20px', fontWeight: 600, color: 'rgba(0,0,0,0.85)', marginRight: 12 }}>系统状态</span>
                        <span style={{ fontSize: '14px', color: 'rgba(0,0,0,0.45)' }}>System Info</span>
                    </div>
                </div>
                <Descriptions bordered>
                    <Descriptions.Item label="App Name">{this.state.appName}</Descriptions.Item>
                    <Descriptions.Item label="App Version">{this.state.appVersion}</Descriptions.Item>
                    <Descriptions.Item label="Build Time">{this.state.buildTime}</Descriptions.Item>
                    <Descriptions.Item label="BGO PID">{this.state.pid}</Descriptions.Item>
                    <Descriptions.Item label="BGO Path" span={2}>{this.state.bgoExePath || "-"}</Descriptions.Item>
                    <Descriptions.Item label="Platform">{this.state.platform}</Descriptions.Item>
                    <Descriptions.Item label="Go Version">{this.state.goVersion}</Descriptions.Item>
                    <Descriptions.Item label="Git Hash">{this.state.gitHash}</Descriptions.Item>
                    <Descriptions.Item label="Is In Container">{this.isInContainer() ? "是" : "否"}</Descriptions.Item>
                    {this.isInContainer() && <Descriptions.Item label="PUID">{this.state.puid || ""}</Descriptions.Item>}
                    {this.isInContainer() && <Descriptions.Item label="PGID">{this.state.pgid || ""}</Descriptions.Item>}
                    {this.isInContainer() && <Descriptions.Item label="UMASK">{this.state.umask || ""}</Descriptions.Item>}
                    <Descriptions.Item label="启动器模式">
                        {this.state.isLauncherManaged ? (
                            <Tag color="purple">由启动器管理</Tag>
                        ) : (
                            <Tag>独立运行</Tag>
                        )}
                    </Descriptions.Item>
                    {this.state.isLauncherManaged && (
                        <Descriptions.Item label="Launcher PID">{this.state.launcherPid || "-"}</Descriptions.Item>
                    )}
                    {this.state.isLauncherManaged && (
                        <Descriptions.Item label="Launcher Path">{this.state.launcherExePath || "-"}</Descriptions.Item>
                    )}
                </Descriptions>
                <Button
                    type="default"
                    style={{
                        marginTop: 16,
                    }}
                    onClick={() => {
                        const text = this.getTextForCopy();
                        const result = copy(text);
                        if (result) {
                            alert("复制成功:" + text);
                        } else {
                            alert(`复制失败`);
                        }
                    }}
                >
                    复制到剪贴板
                </Button>
            </div >
        )
    }
}

export default LiveInfo;

