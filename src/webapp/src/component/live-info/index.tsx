import React from "react";
import API from '../../utils/api';
import {
    PageHeader,
    Descriptions,
    Button
} from 'antd';
import copy from 'copy-to-clipboard';

const api = new API();

interface Props {

}

interface IState {
    appName: string
    appVersion: string
    buildTime: string
    gitHash: string
    pid: string
    platform: string
    goVersion: string
    isInContainer: boolean
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
            isInContainer: false
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
                    isInContainer: rsp.is_in_container
                })
            })
            .catch(err => {
                alert("请求服务器失败");
            })
    }

    getTextForCopy(): string {
        return `
App Name: ${this.state.appVersion}
App Version: ${this.state.appVersion}
Build Time: ${this.state.buildTime}
Pid: ${this.state.pid}
Platform: ${this.state.platform}
Go Version: ${this.state.goVersion}
Git Hash: ${this.state.gitHash}
Is Container: ${this.state.isInContainer}
`;
    }

    render() {
        return (
            <div>
                <div style={{ backgroundColor: '#F5F5F5', }}>
                    <PageHeader
                        ghost={false}
                        title="系统状态"
                        subTitle="System Info">
                    </PageHeader>
                </div>
                <Descriptions bordered>
                    <Descriptions.Item label="App Name">{this.state.appName}</Descriptions.Item>
                    <Descriptions.Item label="App Version">{this.state.appVersion}</Descriptions.Item>
                    <Descriptions.Item label="Build Time">{this.state.buildTime}</Descriptions.Item>
                    <Descriptions.Item label="Pid">{this.state.pid}</Descriptions.Item>
                    <Descriptions.Item label="Platform">{this.state.platform}</Descriptions.Item>
                    <Descriptions.Item label="Go Version">{this.state.goVersion}</Descriptions.Item>
                    <Descriptions.Item label="Git Hash">{this.state.gitHash}</Descriptions.Item>
                    <Descriptions.Item label="Is In Container">{this.state.isInContainer ? "是" : "否"}</Descriptions.Item>
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
