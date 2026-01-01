import React from "react";
import API from "../../utils/api";
import { Breadcrumb, Divider, Table } from "antd";
import { FolderFilled, FileOutlined } from "@ant-design/icons";
import { Link, RouteComponentProps } from "react-router-dom";
import Utils from "../../utils/common";
import './file-list.css';
import type { TablePaginationConfig } from "antd";

import Artplayer from "artplayer";
import mpegtsjs from "mpegts.js";

const api = new API();

interface MatchParams {
    path: string | undefined;
}

interface Props extends RouteComponentProps<any> {
    children?: React.ReactNode;
}

type CurrentFolderFile = {
    is_folder: boolean;
    name: string;
    last_modified: number;
    size: number;
}

interface IState {
    parentFolders: string[];
    currentFolderFiles: CurrentFolderFile[];
    sortedInfo: any;
    isPlayerVisible: boolean;
}

class FileList extends React.Component<Props, IState> {
    constructor(props: Props) {
        super(props);
        this.state = {
            parentFolders: [props.match.params.path ?? ""],
            currentFolderFiles: [],
            sortedInfo: {},
            isPlayerVisible: false,
        };
    }

    componentDidMount(): void {
        this.requestFileList(this.props.match.params.path);
    }

    componentWillReceiveProps(nextProps: Props) {
        this.requestFileList(nextProps.match.params.path);
    }

    setPath(path: string) {
        const folders = path.split("/");
        this.setState({ parentFolders: folders });
    }

    requestFileList(path: string = ""): void {
        api.getFileList(path)
            .then((rsp: any) => {
                if (rsp?.files) {
                    this.setState({
                        currentFolderFiles: rsp.files,
                        sortedInfo: path ? {
                            order: "descend",
                            columnKey: "last_modified",
                        } : {
                            order: "ascend",
                            columnKey: "name"
                        },
                    })
                }
            });
    }

    showPlayer = () => {
        this.setState({
            isPlayerVisible: true,
        });
    };

    hidePlayer = () => {
        this.setState({
            isPlayerVisible: false,
        });
    };

    handleChange = (pagination: TablePaginationConfig, filtetrs: Partial<Record<keyof CurrentFolderFile, string[]>>, sorter: any) => {
        this.setState({
            sortedInfo: sorter,
        });
    };

    onRowClick = (record: CurrentFolderFile) => {
        let path = encodeURIComponent(record.name);
        if (this.props.match.params.path) {
            path = this.props.match.params.path + "/" + path;
        }
        if (record.is_folder) {
            this.props.history.push("/fileList/" + path);
        } else {
            this.setState({
                isPlayerVisible: true,
            }, () => {
                const art = new Artplayer({
                    pip: true,
                    setting: true,
                    playbackRate: true,
                    aspectRatio: true,
                    flip: true,
                    autoSize: true,
                    autoMini: true,
                    mutex: true,
                    miniProgressBar: true,
                    backdrop: false,
                    fullscreen: true,
                    fullscreenWeb: true,
                    lang: 'zh-cn',
                    container: '#art-container',
                    url: `files/${path}`,
                    customType: {
                        flv: function (video, url) {
                            if (mpegtsjs.isSupported()) {
                                const flvPlayer = mpegtsjs.createPlayer({
                                    type: "flv",
                                    url: url,
                                    hasVideo: true,
                                    hasAudio: true,
                                }, {});
                                flvPlayer.attachMediaElement(video);
                                flvPlayer.load();
                            } else {
                                if (art) {
                                    art.notice.show = "不支持播放格式: flv";
                                }
                            }
                        },
                        ts: function (video, url) {
                            if (mpegtsjs.isSupported()) {
                                const tsPlayer = mpegtsjs.createPlayer({
                                    type: "mpegts", // could also be mpegts, m2ts, flv,mse
                                    url: url,
                                    hasVideo: true,
                                    hasAudio: true,
                                }, {});
                                tsPlayer.attachMediaElement(video);
                                tsPlayer.load();
                            } else {
                                if (art) {
                                    art.notice.show = "不支持播放格式: mpegts";
                                }
                            }
                        },
                    },
                });
            });
        }
    };

    renderParentFolderBar(): JSX.Element {
        const rootFolderName = "输出文件路径";
        let currentPath = "/fileList";
        const rootBreadcrumbItem = <Breadcrumb.Item key={rootFolderName}>
            <Link to={currentPath} onClick={this.hidePlayer}>{rootFolderName}</Link>
        </Breadcrumb.Item>;
        const folders = this.props.match.params.path?.split("/") || [];
        const items = folders.map((v: string) => {
            currentPath += "/" + v;
            return <Breadcrumb.Item key={v}>
                <Link to={`${currentPath}`} onClick={this.hidePlayer}>{v}</Link>
            </Breadcrumb.Item>
        });
        return <Breadcrumb>
            {rootBreadcrumbItem}
            {items}
        </Breadcrumb>;
    }

    renderCurrentFolderFileList(): JSX.Element {
        let { sortedInfo } = this.state;
        sortedInfo = sortedInfo || {};
        const columns = [{
            title: "文件名",
            dataIndex: "name",
            key: "name",
            sorter: (a: CurrentFolderFile, b: CurrentFolderFile) => {
                if (a.is_folder === b.is_folder) {
                    return a.name.localeCompare(b.name);
                } else {
                    return a.is_folder ? -1 : 1;
                }
            },
            sortOrder: sortedInfo.columnKey === "name" && sortedInfo.order,
            render: (text: string, record: CurrentFolderFile, index: number) => {
                return [
                    record.is_folder ? <FolderFilled key="icon" style={{ color: '#1890ff' }} /> : <FileOutlined key="icon" />,
                    <Divider key="divider" type="vertical" />,
                    <span key="name">{record.name}</span>,
                ];
            }
        }, {
            title: "文件大小",
            dataIndex: "size",
            key: "size",
            sorter: (a: CurrentFolderFile, b: CurrentFolderFile) => a.size - b.size,
            sortOrder: sortedInfo.columnKey === "size" && sortedInfo.order,
            render: (text: string, record: CurrentFolderFile, index: number) => {
                if (record.is_folder) {
                    return "";
                } else {
                    return Utils.byteSizeToHumanReadableFileSize(record.size);
                }
            },
        }, {
            title: "最后修改时间",
            dataIndex: "last_modified",
            key: "last_modified",
            sorter: (a: CurrentFolderFile, b: CurrentFolderFile) => a.last_modified - b.last_modified,
            sortOrder: sortedInfo.columnKey === "last_modified" && sortedInfo.order,
            render: (text: string, record: CurrentFolderFile, index: number) => Utils.timestampToHumanReadable(record.last_modified),
        }];

        return (<Table
            columns={columns}
            dataSource={this.state.currentFolderFiles}
            onChange={this.handleChange}
            pagination={{ pageSize: 50 }}
            onRow={(record) => ({
                onClick: () => this.onRowClick(record)
            })}
            scroll={{ x: 'max-content' }}
        />);
    }

    renderArtPlayer() {
        return <div id="art-container"></div>;
    }

    render(): JSX.Element {
        return (<div style={{ height: "100%" }}>
            {this.renderParentFolderBar()}
            {this.state.isPlayerVisible ? this.renderArtPlayer() : this.renderCurrentFolderFileList()}
        </div>);
    }
}

export default FileList;
