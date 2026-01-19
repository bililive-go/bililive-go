import React, { useState, useEffect, useCallback, useRef } from "react";
import API from "../../utils/api";
import { Breadcrumb, Divider, Table } from "antd";
import { FolderOutlined, FileOutlined, CloseOutlined } from "@ant-design/icons";
import { Link, useNavigate, useParams } from "react-router-dom";
import Utils from "../../utils/common";
import './file-list.css';
import type { TablePaginationConfig } from "antd";
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
    const pathParam = params["*"] || "";

    const [currentFolderFiles, setCurrentFolderFiles] = useState<CurrentFolderFile[]>([]);
    const [sortedInfo, setSortedInfo] = useState<any>({});
    const [isPlayerVisible, setIsPlayerVisible] = useState(false);
    const [currentPlayingName, setCurrentPlayingName] = useState("");
    const artRef = useRef<Artplayer | null>(null);

    const requestFileList = useCallback((path: string = "") => {
        api.getFileList(path)
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
        const handleEsc = (event: KeyboardEvent) => {
            if (event.key === "Escape") {
                hidePlayer();
            }
        };
        window.addEventListener("keydown", handleEsc);
        return () => {
            window.removeEventListener("keydown", handleEsc);
        };
    }, [hidePlayer]);

    const handleChange = (pagination: TablePaginationConfig, filters: any, sorter: any) => {
        setSortedInfo(sorter);
    };

    const onRowClick = (record: CurrentFolderFile) => {
        let path = encodeURIComponent(record.name);
        if (pathParam) {
            path = pathParam + "/" + path;
        }
        if (record.is_folder) {
            navigate("/fileList/" + path);
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
                    url: `files/${path}`,
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
                currentPath += "/" + v;
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
            sortOrder: currentSortedInfo.columnKey === "name" && currentSortedInfo.order,
            render: (text: string, record: CurrentFolderFile) => {
                return [
                    record.is_folder ? <FolderOutlined key="icon" style={{ color: '#1890ff' }} /> : <FileOutlined key="icon" />,
                    <Divider key="divider" type="vertical" />,
                    <span key="name">{record.name}</span>,
                ];
            }
        }, {
            title: "文件大小",
            dataIndex: "size",
            key: "size",
            sorter: (a: CurrentFolderFile, b: CurrentFolderFile) => a.size - b.size,
            sortOrder: currentSortedInfo.columnKey === "size" && currentSortedInfo.order,
            render: (text: string, record: CurrentFolderFile) => {
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
            sortOrder: currentSortedInfo.columnKey === "last_modified" && currentSortedInfo.order,
            render: (text: string, record: CurrentFolderFile) => Utils.timestampToHumanReadable(record.last_modified),
        }];

        return (<Table
            columns={columns}
            dataSource={currentFolderFiles}
            onChange={handleChange}
            pagination={{ pageSize: 50 }}
            onRow={(record) => ({
                onClick: () => onRowClick(record)
            })}
            scroll={{ x: 'max-content' }}
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
            <div style={{ marginBottom: 12 }}>
                {renderParentFolderBar()}
            </div>
            <div style={{ flex: 1, minHeight: 0 }}>
                {isPlayerVisible ? renderArtPlayer() : renderCurrentFolderFileList()}
            </div>
        </div>
    );
};

export default FileList;
