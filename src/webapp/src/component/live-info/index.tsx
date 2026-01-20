import React from "react";
import API from '../../utils/api';
import {
    Descriptions,
    Button
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
            umask: ""
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
                    umask: rsp.umask
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
        return `
App Name: ${this.state.appName}
App Version: ${this.state.appVersion}
Build Time: ${this.state.buildTime}
Pid: ${this.state.pid}
Platform: ${this.state.platform}
Go Version: ${this.state.goVersion}
Git Hash: ${this.state.gitHash}
Is In Container: ${inContainer ? "是" : "否"}${extra}
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
                    <Descriptions.Item label="Pid">{this.state.pid}</Descriptions.Item>
                    <Descriptions.Item label="Platform">{this.state.platform}</Descriptions.Item>
                    <Descriptions.Item label="Go Version">{this.state.goVersion}</Descriptions.Item>
                    <Descriptions.Item label="Git Hash">{this.state.gitHash}</Descriptions.Item>
                    <Descriptions.Item label="Is In Container">{this.isInContainer() ? "是" : "否"}</Descriptions.Item>
                    {this.isInContainer() && <Descriptions.Item label="PUID">{this.state.puid || ""}</Descriptions.Item>}
                    {this.isInContainer() && <Descriptions.Item label="PGID">{this.state.pgid || ""}</Descriptions.Item>}
                    {this.isInContainer() && <Descriptions.Item label="UMASK">{this.state.umask || ""}</Descriptions.Item>}
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
