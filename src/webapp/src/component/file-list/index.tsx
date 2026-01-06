import React from "react";
import API from "../../utils/api";
import { Breadcrumb, Divider, Icon, Table, Popconfirm, message, Modal, Input, Button } from "antd";
import { Link, RouteComponentProps } from "react-router-dom";
import Utils from "../../utils/common";
import './file-list.css';
import { PaginationConfig } from "antd/lib/pagination";
import { SorterResult } from "antd/lib/table";
import Artplayer from "artplayer";
import mpegtsjs from "mpegts.js";

const api = new API();

interface MatchParams {
    path: string | undefined;
}

interface Props extends RouteComponentProps<MatchParams> {
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
    sortedInfo: Partial<SorterResult<CurrentFolderFile>>;
    isPlayerVisible: boolean;
    selectedRowKeys: string[];
    renameModalVisible: boolean;
    renameType: 'single' | 'batch';
    singleRenameRecord: CurrentFolderFile | null;
    singleNewName: string;
    batchSearch: string;
    batchReplace: string;
    singleExtension: string;
}

class FileList extends React.Component<Props, IState> {
    constructor(props: Props) {
        super(props);
        this.state = {
            parentFolders: [props.match.params.path ?? ""],
            currentFolderFiles: [],
            sortedInfo: {},
            isPlayerVisible: false,
            selectedRowKeys: [],
            renameModalVisible: false,
            renameType: 'single',
            singleRenameRecord: null,
            singleNewName: '',
            batchSearch: '',
            batchReplace: '',
            singleExtension: '',
        };
    }

    componentDidMount(): void {
        this.requestFileList(this.props.match.params.path);
    }

    componentWillReceiveProps(nextProps: Props) {
        this.requestFileList(nextProps.match.params.path);
        this.setState({ selectedRowKeys: [] });
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

    handleChange = (pagination: PaginationConfig, filtetrs: Partial<Record<keyof CurrentFolderFile, string[]>>, sorter: SorterResult<CurrentFolderFile>) => {
        this.setState({
            sortedInfo: sorter,
        });
    };

    onDelete = (record: CurrentFolderFile, e: React.MouseEvent) => {
        e.stopPropagation();
        let path = encodeURIComponent(record.name);
        if (this.props.match.params.path) {
            path = this.props.match.params.path + "/" + path;
        }
        api.deleteFile(path)
            .then((rsp: any) => {
                if (rsp.data === "OK") {
                    message.success("删除成功");
                    this.requestFileList(this.props.match.params.path);
                    this.setState({ selectedRowKeys: [] });
                } else {
                    message.error(rsp.err_msg || "删除失败");
                }
            })
            .catch(err => {
                message.error("删除请求失败");
            });
    };

    onBatchDelete = () => {
        const { selectedRowKeys } = this.state;
        const paths = selectedRowKeys.map(name => {
            let p = name;
            if (this.props.match.params.path) {
                p = this.props.match.params.path + "/" + p;
            }
            return p;
        });

        api.deleteFilesBatch(paths)
            .then((rsp: any) => {
                if (rsp.data === "OK") {
                    message.success("批量删除成功");
                    this.requestFileList(this.props.match.params.path);
                    this.setState({ selectedRowKeys: [] });
                } else {
                    // 批量删除可能部分失败（如文件占用）
                    Modal.warning({
                        title: '部分删除失败',
                        content: rsp.err_msg || "未知错误",
                    });
                    this.requestFileList(this.props.match.params.path);
                    this.setState({ selectedRowKeys: [] });
                }
            })
            .catch(err => {
                message.error("批量删除请求失败");
            });
    }

    onSingleRenameClick = (record: CurrentFolderFile, e: React.MouseEvent) => {
        e.stopPropagation();
        let nameToEdit = record.name;
        let extension = '';
        if (!record.is_folder) {
            const lastDotIndex = record.name.lastIndexOf('.');
            if (lastDotIndex > 0) {
                nameToEdit = record.name.substring(0, lastDotIndex);
                extension = record.name.substring(lastDotIndex);
            }
        }
        this.setState({
            renameType: 'single',
            singleRenameRecord: record,
            singleNewName: nameToEdit,
            singleExtension: extension,
            renameModalVisible: true,
        });
    }

    onBatchRenameClick = () => {
        this.setState({
            renameType: 'batch',
            batchSearch: '',
            batchReplace: '',
            renameModalVisible: true,
        });
    }

    handleRenameSubmit = () => {
        const { renameType, singleRenameRecord, singleNewName, singleExtension, batchSearch, batchReplace, selectedRowKeys } = this.state;
        let actions: any[] = [];

        if (renameType === 'single' && singleRenameRecord) {
            const finalName = singleNewName + singleExtension;
            if (!singleNewName || finalName === singleRenameRecord.name) {
                this.setState({ renameModalVisible: false });
                return;
            }
            let oldPath = singleRenameRecord.name;
            if (this.props.match.params.path) {
                oldPath = this.props.match.params.path + "/" + oldPath;
            }
            actions.push({ old_path: oldPath, new_name: finalName });
        } else if (renameType === 'batch') {
            if (!batchSearch) {
                message.warning("请输入查找字符串");
                return;
            }

            selectedRowKeys.forEach(name => {
                const record = this.state.currentFolderFiles.find(f => f.name === name);
                let baseName = name;
                let extension = "";

                // 分离文件名和后缀，保护后缀不被批量替换修改
                if (record && !record.is_folder) {
                    const lastDotIndex = name.lastIndexOf('.');
                    if (lastDotIndex > 0) {
                        baseName = name.substring(0, lastDotIndex);
                        extension = name.substring(lastDotIndex);
                    }
                }

                if (baseName.includes(batchSearch)) {
                    let oldPath = name;
                    if (this.props.match.params.path) {
                        oldPath = this.props.match.params.path + "/" + oldPath;
                    }
                    const newBaseName = baseName.split(batchSearch).join(batchReplace);
                    const newName = newBaseName + extension;
                    if (newName !== name) {
                        actions.push({ old_path: oldPath, new_name: newName });
                    }
                }
            });
        }

        if (actions.length === 0) {
            this.setState({ renameModalVisible: false });
            return;
        }

        api.renameFiles(actions)
            .then((rsp: any) => {
                if (rsp.data === "OK") {
                    message.success("重命名成功");
                    this.requestFileList(this.props.match.params.path);
                    this.setState({ renameModalVisible: false, selectedRowKeys: [] });
                } else {
                    message.error(rsp.err_msg || "重命名失败");
                }
            })
            .catch(err => {
                message.error("重命名请求失败");
            });
    }

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
                                    type: "mpegts",
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
        const items = folders.map(v => {
            currentPath += "/" + v;
            return <Breadcrumb.Item key={v}>
                <Link to={`${currentPath}`} onClick={this.hidePlayer}>{v}</Link>
            </Breadcrumb.Item>
        });
        return (
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
                <Breadcrumb>
                    {rootBreadcrumbItem}
                    {items}
                </Breadcrumb>
                {this.state.selectedRowKeys.length > 0 && (
                    <span>
                        <Button type="primary" size="small" icon="edit" onClick={this.onBatchRenameClick} style={{ marginRight: '8px' }}>
                            批量重命名 ({this.state.selectedRowKeys.length})
                        </Button>
                        <Popconfirm
                            title={`确定要删除选中的 ${this.state.selectedRowKeys.length} 个项目吗？`}
                            onConfirm={this.onBatchDelete}
                            okText="确定"
                            cancelText="取消"
                            placement="bottomRight"
                        >
                            <Button type="danger" size="small" icon="delete">
                                批量删除 ({this.state.selectedRowKeys.length})
                            </Button>
                        </Popconfirm>
                    </span>
                )}
            </div>
        );
    }

    renderCurrentFolderFileList(): JSX.Element {
        let { sortedInfo, selectedRowKeys } = this.state;
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
                    record.is_folder ? <Icon key="icon" type="folder" theme="filled" /> : <Icon key="icon" type="file" />,
                    <Divider key="divider" type="vertical" />,
                    record.name,
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
        }, {
            title: "操作",
            key: "action",
            render: (text: string, record: CurrentFolderFile) => (
                <span>
                    <Button
                        size="small"
                        icon="edit"
                        onClick={(e) => this.onSingleRenameClick(record, e)}
                        style={{ marginRight: '8px' }}
                    >
                        重命名
                    </Button>
                    <Popconfirm
                        title={`确定要删除${record.is_folder ? '文件夹' : '文件'} "${record.name}" 吗？`}
                        onConfirm={(e) => this.onDelete(record, e as any)}
                        onCancel={(e) => e?.stopPropagation()}
                        okText="确定"
                        cancelText="取消"
                    >
                        <Button
                            size="small"
                            type="danger"
                            icon="delete"
                            onClick={(e) => e.stopPropagation()}
                        >
                            删除
                        </Button>
                    </Popconfirm>
                </span>
            ),
        }];

        const rowSelection = {
            selectedRowKeys,
            onChange: (selectedRowKeys: any) => this.setState({ selectedRowKeys }),
        };

        return (<Table
            rowSelection={rowSelection}
            columns={columns}
            dataSource={this.state.currentFolderFiles}
            rowKey="name"
            onChange={this.handleChange}
            pagination={{ pageSize: 50 }}
            onRowClick={this.onRowClick}
            scroll={{ x: 'max-content' }}
        />);
    }

    renderRenameModal() {
        const { renameModalVisible, renameType, singleNewName, singleExtension, batchSearch, batchReplace } = this.state;
        return (
            <Modal
                title={renameType === 'single' ? "重命名" : "批量重命名 (查找替换)"}
                visible={renameModalVisible}
                onOk={this.handleRenameSubmit}
                onCancel={() => this.setState({ renameModalVisible: false })}
                okText="确定"
                cancelText="取消"
            >
                {renameType === 'single' ? (
                    <div>
                        <div style={{ marginBottom: '8px' }}>请输入新名称：</div>
                        <Input
                            value={singleNewName}
                            addonAfter={singleExtension || null}
                            onChange={e => this.setState({ singleNewName: e.target.value })}
                            onPressEnter={this.handleRenameSubmit}
                            autoFocus
                        />
                    </div>
                ) : (
                    <div>
                        <div style={{ marginBottom: '16px' }}>将在选中的 {this.state.selectedRowKeys.length} 个项中执行查找替换：</div>
                        <div style={{ marginBottom: '8px' }}>查找：</div>
                        <Input
                            placeholder="请输入要查找的字符串"
                            value={batchSearch}
                            onChange={e => this.setState({ batchSearch: e.target.value })}
                            style={{ marginBottom: '16px' }}
                        />
                        <div style={{ marginBottom: '8px' }}>替换为：</div>
                        <Input
                            placeholder="请输入替换后的字符串"
                            value={batchReplace}
                            onChange={e => this.setState({ batchReplace: e.target.value })}
                        />
                    </div>
                )}
            </Modal>
        );
    }

    renderArtPlayer() {
        return <div id="art-container"></div>;
    }

    render(): JSX.Element {
        return (<div style={{ height: "100%" }}>
            {this.renderParentFolderBar()}
            {this.state.isPlayerVisible ? this.renderArtPlayer() : this.renderCurrentFolderFileList()}
            {this.renderRenameModal()}
        </div>);
    }
}

export default FileList;
