import React, { useState, useEffect, useCallback } from "react";
import API from "../../utils/api";
import { Breadcrumb, Divider, Table } from "antd";
import { FolderFilled, FileOutlined } from "@ant-design/icons";
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

    const hidePlayer = () => {
        setIsPlayerVisible(false);
    };

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
            setIsPlayerVisible(true);
            // 使用 setTimeout 确保 DOM 已更新
            setTimeout(() => {
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
            }, 0);
        }
    };

    const renderParentFolderBar = (): JSX.Element => {
        const rootFolderName = "输出文件路径";
        let currentPath = "/fileList";
        const folders = pathParam?.split("/").filter(Boolean) || [];

        // 使用 Ant Design v5 的 items API
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
        return <div id="art-container"></div>;
    };

    return (
        <div style={{ height: "100%" }}>
            {renderParentFolderBar()}
            {isPlayerVisible ? renderArtPlayer() : renderCurrentFolderFileList()}
        </div>
    );
};

export default FileList;
