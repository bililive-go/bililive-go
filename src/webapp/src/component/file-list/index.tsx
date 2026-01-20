import React, { useState, useEffect, useCallback, useRef } from "react";
import API from "../../utils/api";
import { Breadcrumb, Table, Button, Modal, Input, Popconfirm, message, Space } from "antd";
import {
    // @ts-ignore
    FolderOutlined,
    // @ts-ignore
    FileOutlined,
    // @ts-ignore
    CloseOutlined,
    // @ts-ignore
    EditOutlined,
    // @ts-ignore
    DeleteOutlined
} from "@ant-design/icons";
import { Link, useNavigate, useParams } from "react-router-dom";
import Utils from "../../utils/common";
import './file-list.css';
import Artplayer from "artplayer";
import mpegtsjs from "mpegts.js";

const api = new API();

type CurrentFolderFile = {
    is_folder: boolean;
    name: string;
    last_modified: number;
    size: number;
}

const FileList: React.FC = () => {
    const navigate = useNavigate();
    // 使用 "*" 通配符捕获的路径参数
    const params = useParams();
    // 确保从 URL 获取的路径参数是解码后的原始字符串
    const pathParam = decodeURIComponent(params["*"] || "");

    const [currentFolderFiles, setCurrentFolderFiles] = useState<CurrentFolderFile[]>([]);
    const [sortedInfo, setSortedInfo] = useState<any>({});
    const [isPlayerVisible, setIsPlayerVisible] = useState(false);
    const [currentPlayingName, setCurrentPlayingName] = useState("");
    const artRef = useRef<Artplayer | null>(null);

    // 重命名相关状态
    const [isRenameModalVisible, setIsRenameModalVisible] = useState(false);
    const [renameTarget, setRenameTarget] = useState<CurrentFolderFile | null>(null);
    const [newName, setNewName] = useState("");
    const inputRef = useRef<any>(null);

    // 批量操作相关状态
    const [selectedRowKeys, setSelectedRowKeys] = useState<any[]>([]);
    const [isBatchRenameModalVisible, setIsBatchRenameModalVisible] = useState(false);
    const [batchFind, setBatchFind] = useState("");
    const [batchReplace, setBatchReplace] = useState("");

    // 当弹窗打开时，自动聚焦到输入框
    useEffect(() => {
        if (isRenameModalVisible) {
            setTimeout(() => {
                inputRef.current?.focus?.({
                    cursor: 'end',
                });
            }, 100);
        }
    }, [isRenameModalVisible]);

    // 清空选择
    useEffect(() => {
        setSelectedRowKeys([]);
    }, [pathParam]);

    const requestFileList = useCallback((path: string = "") => {
        api.getFileList(encodePath(path))
            .then((rsp: any) => {
                if (rsp?.files) {
                    setCurrentFolderFiles(rsp.files);
                    setSortedInfo(path ? {
                        order: "descend",
                        columnKey: "last_modified",
                    } : {
                        order: "ascend",
                        columnKey: "name"
                    });
                }
            });
    }, []);

    useEffect(() => {
        requestFileList(pathParam);
    }, [pathParam, requestFileList]);

    const hidePlayer = useCallback(() => {
        if (artRef.current) {
            artRef.current.destroy(true);
            artRef.current = null;
        }
        setIsPlayerVisible(false);
        setCurrentPlayingName("");
    }, []);

    // 监听 ESC 键退出播放
    useEffect(() => {
        if (!isPlayerVisible) return;

        const handleEsc = (event: KeyboardEvent) => {
            if (event.key === "Escape") {
                hidePlayer();
            }
        };
        window.addEventListener("keydown", handleEsc);
        return () => {
            window.removeEventListener("keydown", handleEsc);
        };
    }, [isPlayerVisible, hidePlayer]);

    const handleChange = (pagination: any, filters: any, sorter: any) => {
        setSortedInfo(sorter);
    };

    /**
     * 对路径进行 URL 编码，用于 API 请求和资源定位。
     */
    const encodePath = (path: string): string => {
        if (!path) return "";
        return path.split("/").map(p => encodeURIComponent(p)).join("/");
    };

    /**
     * 对路径进行双重 URL 编码，专门用于 HashRouter 导航。
     * 因为 HashRouter 会将路径中的第一个 # 视为路由分隔符，
     * 双重编码可以将 # 转义为 %2523，避免冲突。
     */
    const encodePathForNav = (path: string): string => {
        if (!path) return "";
        return path.split("/").map(p => encodeURIComponent(encodeURIComponent(p))).join("/");
    };

    const showBatchRenameModal = () => {
        setBatchFind("");
        setBatchReplace("");
        setIsBatchRenameModalVisible(true);
    };

    const showRenameModal = (record: CurrentFolderFile, e: React.MouseEvent) => {
        e.stopPropagation();
        setRenameTarget(record);
        // 如果是文件，提取不含后缀的文件名
        let baseName = record.name;
        if (!record.is_folder) {
            const lastDotIndex = record.name.lastIndexOf('.');
            if (lastDotIndex !== -1) {
                baseName = record.name.substring(0, lastDotIndex);
            }
        }
        setNewName(baseName);
        setIsRenameModalVisible(true);
    };

    const handleRename = () => {
        if (!renameTarget || !newName.trim()) return;
        let fullOldPath = renameTarget.name;
        if (pathParam) {
            fullOldPath = pathParam + "/" + renameTarget.name;
        }

        api.renameFile(encodePath(fullOldPath), newName.trim())
            .then((rsp: any) => {
                if (rsp.data === "OK") {
                    message.success("重命名成功");
                    setIsRenameModalVisible(false);
                    requestFileList(pathParam);
                } else {
                    message.error(rsp.err_msg || "重命名失败");
                }
            })
            .catch(err => message.error("重命名失败: " + err));
    };

    const handleDelete = (record: CurrentFolderFile) => {
        let fullPath = record.name;
        if (pathParam) {
            fullPath = pathParam + "/" + record.name;
        }

        api.deleteFile(encodePath(fullPath))
            .then((rsp: any) => {
                if (rsp.data === "OK") {
                    message.success("删除成功");
                    requestFileList(pathParam);
                } else {
                    message.error(rsp.err_msg || "删除失败");
                }
            })
            .catch(err => message.error("删除失败: " + err));
    };

    const handleBatchDelete = () => {
        if (selectedRowKeys.length === 0) return;
        const paths = selectedRowKeys.map(key => {
            const fileName = key.toString();
            return pathParam ? `${pathParam}/${fileName}` : fileName;
        });

        api.batchDeleteFiles(paths)
            .then((rsp: any) => {
                const results = rsp.data as any[];
                const successCount = results.filter(r => r.success).length;
                const failCount = results.length - successCount;
                if (failCount === 0) {
                    message.success(`成功删除 ${successCount} 个项目`);
                } else {
                    message.warning(`操作完成。成功: ${successCount}, 失败: ${failCount}`);
                    // 打印详细错误到控制台或通知
                    results.filter(r => !r.success).forEach(r => console.error(`删除失败 [${r.path}]: ${r.message}`));
                }
                setSelectedRowKeys([]);
                requestFileList(pathParam);
            })
            .catch(err => message.error("批量删除请求失败: " + err));
    };

    const handleBatchRename = () => {
        if (selectedRowKeys.length === 0 || !batchFind.trim()) return;
        const paths = selectedRowKeys.map(key => {
            const fileName = key.toString();
            return pathParam ? `${pathParam}/${fileName}` : fileName;
        });

        api.batchRenameFiles(paths, batchFind, batchReplace)
            .then((rsp: any) => {
                const results = rsp.data as any[];
                let successCount = 0;
                let skipCount = 0;
                let failCount = 0;
                let failMessages: string[] = [];

                results.forEach(r => {
                    if (r.success) {
                        if (r.message === "无需更改") skipCount++;
                        else successCount++;
                    } else {
                        failCount++;
                        failMessages.push(`${r.path}: ${r.message}`);
                    }
                });

                if (failCount === 0) {
                    message.success(`重命名完成。成功: ${successCount}, 无需更改: ${skipCount}`);
                } else {
                    message.warning(`重命名部分完成。成功: ${successCount}, 失败: ${failCount}`);
                    Modal.error({
                        title: '批量重命名部分失败',
                        content: (
                            <div style={{ maxHeight: '300px', overflow: 'auto' }}>
                                {failMessages.map((msg, i) => <div key={i} style={{ color: 'red', fontSize: '12px' }}>{msg}</div>)}
                            </div>
                        ),
                    });
                }
                setIsBatchRenameModalVisible(false);
                setSelectedRowKeys([]);
                requestFileList(pathParam);
            })
            .catch(err => message.error("批量重命名请求失败: " + err));
    };

    const onRowClick = (record: CurrentFolderFile) => {
        // 保持使用原始字符串进行拼接
        let fullPath = record.name;
        if (pathParam) {
            fullPath = pathParam + "/" + record.name;
        }

        if (record.is_folder) {
            // 仅在跳转时进行编码
            navigate("/fileList/" + encodePathForNav(fullPath));
        } else {
            setCurrentPlayingName(record.name);
            setIsPlayerVisible(true);
            // 使用 setTimeout 确保 DOM 已更新
            setTimeout(() => {
                if (artRef.current) {
                    artRef.current.destroy(true);
                }

                const art = new Artplayer({
                    container: '#art-container',
                    url: `files/${encodePath(fullPath)}`,
                    title: record.name,
                    volume: 0.7,
                    autoplay: true,
                    pip: true,
                    setting: true,
                    playbackRate: true,
                    aspectRatio: true,
                    flip: true,
                    autoSize: true,
                    autoMini: true,
                    mutex: true,
                    miniProgressBar: true,
                    backdrop: true,
                    fullscreen: true,
                    fullscreenWeb: true,
                    lang: 'zh-cn',
                    customType: {
                        flv: function (video, url, art) {
                            if (mpegtsjs.isSupported()) {
                                const flvPlayer = mpegtsjs.createPlayer({
                                    type: "flv",
                                    url: url,
                                    hasVideo: true,
                                    hasAudio: true,
                                }, {});
                                flvPlayer.attachMediaElement(video);
                                flvPlayer.load();
                                art.on('destroy', () => {
                                    flvPlayer.destroy();
                                });
                            } else {
                                art.notice.show = "不支持播放格式: flv";
                            }
                        },
                        ts: function (video, url, art) {
                            if (mpegtsjs.isSupported()) {
                                const tsPlayer = mpegtsjs.createPlayer({
                                    type: "mpegts",
                                    url: url,
                                    hasVideo: true,
                                    hasAudio: true,
                                }, {});
                                tsPlayer.attachMediaElement(video);
                                tsPlayer.load();
                                art.on('destroy', () => {
                                    tsPlayer.destroy();
                                });
                            } else {
                                art.notice.show = "不支持播放格式: mpegts";
                            }
                        },
                    },
                });
                artRef.current = art;
            }, 0);
        }
    };

    const renderParentFolderBar = (): JSX.Element => {
        const rootFolderName = "输出文件路径";
        let currentPath = "/fileList";
        const folders = pathParam?.split("/").filter(Boolean) || [];

        const breadcrumbItems = [
            {
                key: 'root',
                title: <Link to={currentPath} onClick={hidePlayer}>{rootFolderName}</Link>
            },
            ...folders.map((v: string) => {
                currentPath += "/" + encodeURIComponent(encodeURIComponent(v));
                return {
                    key: v,
                    title: <Link to={currentPath} onClick={hidePlayer}>{v}</Link>
                };
            })
        ];

        // @ts-ignore
        return <Breadcrumb items={breadcrumbItems} />;
    };

    const renderCurrentFolderFileList = (): JSX.Element => {
        const currentSortedInfo = sortedInfo || {};
        const columns: any[] = [{
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
            sortOrder: currentSortedInfo.columnKey === "name" && currentSortedInfo.order,
            render: (text: string, record: CurrentFolderFile) => {
                return (
                    <div className="file-name-cell">
                        {record.is_folder ? <FolderOutlined style={{ color: '#1890ff', fontSize: '16px' }} /> : <FileOutlined style={{ fontSize: '16px' }} />}
                        <span className="name-text">{record.name}</span>
                    </div>
                );
            }
        }, {
            title: "文件大小",
            dataIndex: "size",
            key: "size",
            width: 120,
            sorter: (a: CurrentFolderFile, b: CurrentFolderFile) => a.size - b.size,
            sortOrder: currentSortedInfo.columnKey === "size" && currentSortedInfo.order,
            render: (text: number, record: CurrentFolderFile) => {
                if (record.is_folder) {
                    return "-";
                } else {
                    return Utils.byteSizeToHumanReadableFileSize(record.size);
                }
            },
        }, {
            title: "最后修改时间",
            dataIndex: "last_modified",
            key: "last_modified",
            width: 180,
            sorter: (a: CurrentFolderFile, b: CurrentFolderFile) => a.last_modified - b.last_modified,
            sortOrder: currentSortedInfo.columnKey === "last_modified" && currentSortedInfo.order,
            render: (text: number) => Utils.timestampToHumanReadable(text),
        }, {
            title: "操作",
            key: "action",
            width: 200,
            render: (text: any, record: CurrentFolderFile) => (
                <Space size="small" onClick={(e) => e.stopPropagation()}>
                    <Button
                        type="link"
                        size="small"
                        // @ts-ignore
                        icon={<EditOutlined />}
                        onClick={(e) => showRenameModal(record, e)}
                        className="action-btn"
                    >
                        重命名
                    </Button>
                    <Popconfirm
                        title={`确定要删除${record.is_folder ? '文件夹' : '文件'} "${record.name}" 吗？`}
                        onConfirm={() => handleDelete(record)}
                        okText="确定"
                        cancelText="取消"
                        // @ts-ignore
                        okButtonProps={{ danger: true }}
                    >
                        <Button
                            type="link"
                            size="small"
                            danger
                            // @ts-ignore
                            icon={<DeleteOutlined />}
                            onClick={(e) => e.stopPropagation()}
                            className="action-btn danger"
                        >
                            删除
                        </Button>
                    </Popconfirm>
                </Space>
            )
        }];

        const onSelectChange = (newSelectedRowKeys: any[]) => {
            setSelectedRowKeys(newSelectedRowKeys);
        };

        const rowSelection = {
            selectedRowKeys,
            onChange: onSelectChange,
        };

        return (<Table
            rowSelection={rowSelection}
            columns={columns}
            dataSource={currentFolderFiles}
            rowKey="name"
            onChange={handleChange}
            pagination={{ pageSize: 50 }}
            onRow={(record) => ({
                onClick: () => onRowClick(record)
            })}
            scroll={{ x: 'max-content' }}
            rowClassName={() => "file-table-row"}
        />);
    };

    const renderArtPlayer = () => {
        return (
            <div className="player-wrapper">
                <div className="player-header">
                    <div className="playing-title" title={currentPlayingName}>
                        正在播放: {currentPlayingName}
                    </div>
                    <div className="close-btn" onClick={hidePlayer} title="退出播放 (Esc)">
                        <CloseOutlined />
                    </div>
                </div>
                <div id="art-container"></div>
            </div>
        );
    };

    return (
        <div style={{ height: "100%", display: "flex", flexDirection: "column" }}>
            <div style={{ marginBottom: 12, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <div>{renderParentFolderBar()}</div>
                {selectedRowKeys.length > 0 && (
                    <Space>
                        <span style={{ fontSize: '14px', color: '#8c8c8c' }}>已选择 {selectedRowKeys.length} 项</span>
                        <Button type="primary" size="small" onClick={showBatchRenameModal}>
                            批量重命名
                        </Button>
                        <Popconfirm
                            title={`确定要删除选中的 ${selectedRowKeys.length} 个项目吗？`}
                            onConfirm={handleBatchDelete}
                            okText="确定"
                            cancelText="取消"
                            // @ts-ignore
                            okButtonProps={{ danger: true }}
                        >
                            {/* @ts-ignore */}
                            <Button danger size="small">
                                批量删除
                            </Button>
                        </Popconfirm>
                        <Button size="small" onClick={() => setSelectedRowKeys([])}>取消选择</Button>
                    </Space>
                )}
            </div>
            <div style={{ flex: 1, minHeight: 0 }}>
                {isPlayerVisible ? renderArtPlayer() : renderCurrentFolderFileList()}
            </div>

            {/* @ts-ignore */}
            <Modal
                title={`重命名 ${renameTarget?.is_folder ? '文件夹' : '文件'}`}
                open={isRenameModalVisible}
                onOk={handleRename}
                onCancel={() => setIsRenameModalVisible(false)}
                okText="确定"
                cancelText="取消"
                destroyOnClose
            >
                <div>
                    <div style={{ marginBottom: 8 }}>请输入新名称（后缀会自动保留）：</div>
                    <Input
                        ref={inputRef}
                        value={newName}
                        onChange={(e) => setNewName(e.target.value)}
                        placeholder="请输入新名称"
                        onPressEnter={handleRename}
                        autoFocus
                    />
                    {!renameTarget?.is_folder && renameTarget?.name.includes('.') && (
                        <div style={{ marginTop: 8, color: '#8c8c8c', fontSize: '12px' }}>
                            当前后缀: {renameTarget.name.substring(renameTarget.name.lastIndexOf('.'))}
                        </div>
                    )}
                </div>
            </Modal>
            {/* @ts-ignore */}
            <Modal
                title="批量重命名 (查找替换)"
                open={isBatchRenameModalVisible}
                onOk={handleBatchRename}
                onCancel={() => setIsBatchRenameModalVisible(false)}
                okText="开始替换"
                cancelText="取消"
                destroyOnClose
            >
                <div>
                    <div style={{ marginBottom: 16 }}>
                        <div style={{ marginBottom: 8 }}>查找内容:</div>
                        <Input
                            value={batchFind}
                            onChange={(e) => setBatchFind(e.target.value)}
                            placeholder="输入要查找的字符串"
                            autoComplete="off"
                        />
                    </div>
                    <div>
                        <div style={{ marginBottom: 8 }}>替换为:</div>
                        <Input
                            value={batchReplace}
                            onChange={(e) => setBatchReplace(e.target.value)}
                            placeholder="输入替换后的字符串"
                            autoComplete="off"
                        />
                    </div>
                    <div style={{ marginTop: 16, color: '#8c8c8c', fontSize: '12px' }}>
                        * 此操作将对所有选中的文件执行查找替换。文件后缀将被自动保护。
                        <br />* 被其他程序占用的文件将被自动跳过。
                    </div>
                </div>
            </Modal>
        </div>
    );
};

export default FileList;
