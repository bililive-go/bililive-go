import React, { useState, useEffect, useCallback, useRef } from "react";
import API from "../../utils/api";
import { Breadcrumb, Table, Button, Modal, Input, Popconfirm, message, Space, Tooltip, Switch, Popover, Slider, Select, Checkbox } from "antd";
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

// ==================== ASS 弹幕解析 ====================

/** 解析 ASS 时间格式 H:MM:SS.CC 为秒数 */
function parseAssTime(s: string): number {
    const p = s.trim().split(':');
    if (p.length !== 3) return 0;
    const sp = p[2].split('.');
    return (parseInt(p[0]) || 0) * 3600 + (parseInt(p[1]) || 0) * 60 + (parseInt(sp[0]) || 0) + (parseInt(sp[1] || '0') || 0) / 100;
}

/** ASS &HAABBGGRR& → CSS rgba */
function parseAssColor(c: string): string {
    const h = c.replace(/[&H]/gi, '').padStart(8, '0');
    const b = parseInt(h.substring(2, 4), 16);
    const g = parseInt(h.substring(4, 6), 16);
    const r = parseInt(h.substring(6, 8), 16);
    const a = 1 - parseInt(h.substring(0, 2), 16) / 255;
    return `rgba(${r},${g},${b},${a.toFixed(2)})`;
}

type DanmakuEntry = { start: number; end: number; color: string; text: string; style: string; align: number; bgColor: string; marginV: number };

/** 解析 ASS 文件，提取所有弹幕条目 */
function parseAss(content: string): { items: DanmakuEntry[]; scrollTime: number; resY: number } {
    const lines = content.split('\n');
    const items: DanmakuEntry[] = [];
    let inStyles = false;
    let inEvents = false;
    let resX = 1920;
    let resY = 1080;
    let bannerSpeed = 80; // ms per pixel
    // 样式名 → PrimaryColour / BackColour CSS 颜色
    const styleColors: Record<string, string> = {};
    const styleBackColors: Record<string, string> = {};
    // 样式名 → MarginV
    const styleMarginV: Record<string, number> = {};

    for (const line of lines) {
        const mr = line.match(/^PlayResX:\s*(\d+)/);
        if (mr) resX = parseInt(mr[1]);
        const my = line.match(/^PlayResY:\s*(\d+)/);
        if (my) resY = parseInt(my[1]);

        // 解析样式定义中的颜色（忽略大小写）
        const trimmed = line.trim().toLowerCase();
        if (trimmed === '[v4+ styles]' || trimmed === '[v4 styles]') { inStyles = true; inEvents = false; continue; }
        if (trimmed === '[events]') { inEvents = true; inStyles = false; continue; }
        if (line.startsWith('[')) { inStyles = false; inEvents = false; continue; }

        if (inStyles && line.startsWith('Style:')) {
            // Style: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
            const sp = line.substring('Style:'.length).split(',');
            if (sp.length >= 23) {
                const name = sp[0].trim();
                styleColors[name] = parseAssColor(sp[3].trim());
                styleBackColors[name] = parseAssColor(sp[6].trim());
                styleMarginV[name] = parseInt(sp[21].trim()) || 0;
            }
            continue;
        }

        if (!inEvents) continue;
        if (!line.startsWith('Dialogue:')) continue;

        // Dialogue: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
        const parts = line.substring('Dialogue:'.length).split(',');
        if (parts.length < 10) continue;

        const style = parts[3].trim();
        const effect = parts[8].trim();

        // 提取 Banner 速度（仅滚动弹幕用）
        if (effect.startsWith('Banner;')) {
            const ep = effect.split(';');
            if (ep.length >= 2) bannerSpeed = parseInt(ep[1]) || 80;
        }

        const start = parseAssTime(parts[1]);
        const end = parseAssTime(parts[2]);
        // MarginV: Dialogue 行的第 8 个字段（0-indexed: 7）
        const mv = parseInt(parts[7].trim(), 10);
        const marginV = Number.isNaN(mv) ? (styleMarginV[style] ?? 0) : mv;
        const raw = parts.slice(9).join(',');
        // 优先使用 Dialogue 行中的 {\c} 覆盖色，否则回退到样式定义的 PrimaryColour
        let color = styleColors[style] || 'rgba(255,255,255,1)';
        const cm = raw.match(/\\c(&H[0-9A-Fa-f]+&)/);
        if (cm) color = parseAssColor(cm[1]);
        // 提取 {\an} 对齐覆盖（用于定位 SC/上舰消息）
        let align = 0;
        const am = raw.match(/\\an(\d+)/);
        if (am) align = parseInt(am[1]);
        const text = raw.replace(/\{[^}]*\}/g, '');
        const bgColor = styleBackColors[style] || '';
        if (end > start && text) items.push({ start, end, color, text, style, align, bgColor, marginV });
    }

    return { items, scrollTime: (bannerSpeed * resX) / 1000, resY };
}

// ==================== 弹幕设置 ====================

export type DanmakuArea = 'full' | 'top' | 'bottom' | 'quarter' | 'three-quarter';

export interface DanmakuSettings {
    area: DanmakuArea;       // 渲染区域
    fontSize: number;        // 字体大小 (12-48)
    opacity: number;         // 透明度 (0.2-1.0)
    scrollTime: number;      // 滚动时间 (3-20秒)
}

const DEFAULT_DANMAKU_SETTINGS: DanmakuSettings = {
    area: 'full',
    fontSize: 22,
    opacity: 1.0,
    scrollTime: 10,
};

// ==================== 弹幕渲染器 ====================

const DANMAKU_FONT_SIZE = 22;

/**
 * 弹幕渲染器 — 直接使用 ASS 文件中的时间轴和位置信息
 * ASS 已包含防重叠逻辑，Web 端无需重复计算
 * 使用 requestAnimationFrame 手动控制 left 位置实现滚动动画
 */
export interface DanmakuFilter {
    danmaku: boolean;
    gift: boolean;
    guard: boolean;
    sc: boolean;
}

class DanmakuRenderer {
    private overlay: HTMLDivElement;
    private video: HTMLVideoElement;
    private items: DanmakuEntry[];
    private filter: DanmakuFilter;
    private settings: DanmakuSettings;
    private rafId = 0;
    private running = false;
    private nextIdx = 0;
    // 滚动弹幕（按结束时间排序，用于清理）
    private scrollEls: { el: HTMLDivElement; spawnTime: number; endTime: number }[] = [];
    // 固定位置消息（按位置分组堆叠）
    private fixedEls: { el: HTMLDivElement; endTime: number; fadeOutTime: number; posKey: string; fadingOut: boolean }[] = [];
    // 滚动样式白名单
    private static SCROLL_STYLES = new Set(['Danmaku', 'Gift']);
    // ASS 原始分辨率（用于 marginV 缩放）
    private resY = 1080;

    constructor(overlay: HTMLDivElement, video: HTMLVideoElement, items: DanmakuEntry[], filter: DanmakuFilter, resY: number, settings: DanmakuSettings) {
        this.overlay = overlay;
        this.video = video;
        this.items = items.slice().sort((a, b) => a.start - b.start);
        this.filter = filter;
        this.resY = resY > 0 ? resY : 1080;
        this.settings = settings;
        this.overlay.style.opacity = String(settings.opacity);
    }

    /** 判断弹幕是否在指定区域内 */
    private isInArea(marginV: number): boolean {
        const ratio = marginV / this.resY;
        switch (this.settings.area) {
            case 'top': return ratio <= 0.5;
            case 'bottom': return ratio > 0.5;
            case 'quarter': return ratio <= 0.25;
            case 'three-quarter': return ratio <= 0.75;
            default: return true; // full
        }
    }

    private matchesFilter(style: string): boolean {
        if (style === 'Danmaku') return this.filter.danmaku;
        if (style === 'Gift') return this.filter.gift;
        if (style === 'Guard') return this.filter.guard;
        if (style.startsWith('SC')) return this.filter.sc;
        return true;
    }

    getCurrentTime(): number {
        return this.video.currentTime;
    }

    start() {
        if (this.running) return;
        this.running = true;
        this.nextIdx = 0;
        this.scrollEls = [];
        this.rafId = requestAnimationFrame(this.tick);
    }

    stop() {
        this.running = false;
        if (this.rafId) cancelAnimationFrame(this.rafId);
        this.rafId = 0;
        // 移除所有滚动弹幕
        for (const d of this.scrollEls) d.el.remove();
        this.scrollEls = [];
        // 移除固定消息
        for (const f of this.fixedEls) f.el.remove();
        this.fixedEls = [];
        // 移除 overlay 元素本身
        if (this.overlay.parentNode) {
            this.overlay.parentNode.removeChild(this.overlay);
        }
    }

    /** 跳转到指定时间：清理已有弹幕，找到正确起始索引 */
    seek(time: number) {
        // 清理 DOM
        for (const d of this.scrollEls) d.el.remove();
        this.scrollEls = [];
        for (const f of this.fixedEls) f.el.remove();
        this.fixedEls = [];

        // 二分查找：找到第一个 start > time 的索引
        let lo = 0, hi = this.items.length;
        while (lo < hi) {
            const mid = (lo + hi) >>> 1;
            if (this.items[mid].start <= time) lo = mid + 1;
            else hi = mid;
        }
        this.nextIdx = lo;

        // 向前寻找仍在显示期的固定消息并直接渲染
        for (let i = lo - 1; i >= 0; i--) {
            const item = this.items[i];
            if (time - item.start > 10) break; // 超过 10 秒的消息肯定已结束
            if (item.end > time && !DanmakuRenderer.SCROLL_STYLES.has(item.style)) {
                if (this.matchesFilter(item.style)) {
                    this.spawnFixed(item, time);
                }
            }
        }
    }

    /** 动态更新设置，无需销毁重建 */
    update(filter: DanmakuFilter, settings: DanmakuSettings) {
        this.filter = filter;
        // 检测是否需要重绘：区域、字体大小、滚动时间变化会影响已上屏弹幕
        const needReset = settings.area !== this.settings.area
            || settings.fontSize !== this.settings.fontSize
            || settings.scrollTime !== this.settings.scrollTime;
        this.settings = settings;
        // 透明度直接应用到 overlay
        this.overlay.style.opacity = String(settings.opacity);
        // 属性变化时重绘，避免新旧弹幕视觉不一致
        if (needReset) this.seek(this.video.currentTime);
    }

    private tick = () => {
        if (!this.running) return;
        this.rafId = requestAnimationFrame(this.tick);
        if (this.video.paused || this.video.seeking) return;

        const t = this.video.currentTime;
        const cw = this.overlay.clientWidth || 1;
        const ch = this.overlay.clientHeight || 1;

        // 1. 清理已结束的滚动弹幕（endTime 大致递增，从队头移除）
        let scrollRemoved = 0;
        while (scrollRemoved < this.scrollEls.length && this.scrollEls[scrollRemoved].endTime <= t) {
            this.scrollEls[scrollRemoved].el.remove();
            scrollRemoved++;
        }
        if (scrollRemoved > 0) this.scrollEls.splice(0, scrollRemoved);

        // 2. 清理过期的固定位置消息 + 淡出动画
        let fixedChanged = false;
        for (let i = this.fixedEls.length - 1; i >= 0; i--) {
            const f = this.fixedEls[i];
            if (f.endTime <= t) {
                f.el.remove();
                this.fixedEls.splice(i, 1);
                fixedChanged = true;
            } else if (!f.fadingOut && t >= f.fadeOutTime) {
                f.el.style.opacity = '0';
                f.fadingOut = true;
            }
        }
        // 有消息过期后重新布局，消除空隙
        if (fixedChanged) this.relayoutFixed();

        // 3. 发射新弹幕
        while (this.nextIdx < this.items.length && this.items[this.nextIdx].start <= t) {
            const item = this.items[this.nextIdx];
            if (!this.matchesFilter(item.style)) {
                this.nextIdx++;
                continue;
            }
            if (DanmakuRenderer.SCROLL_STYLES.has(item.style)) {
                this.spawnScroll(item, t, cw, ch);
            } else {
                this.spawnFixed(item, t);
            }
            this.nextIdx++;
        }

        // 4. 更新所有滚动弹幕位置（使用 settings 中的 scrollTime）
        const scrollTime = this.settings.scrollTime;
        for (const d of this.scrollEls) {
            const elapsed = t - d.spawnTime;
            const progress = elapsed / scrollTime;
            const el = d.el;
            const twPct = parseFloat(el.dataset.tw || '20');
            const leftPct = 100 - progress * (100 + twPct);
            el.style.left = leftPct + '%';
        }
    };

    /** 估算文本宽度（px），处理 surrogate pair 和粗体 */
    private estimateTextWidth(text: string): number {
        const fontSize = this.settings.fontSize;
        let px = 0;
        for (const ch of text) {
            const code = ch.codePointAt(0)!;
            if (code > 0xFFFF) {
                // emoji / 扩展 CJK，算 2 个字符宽
                px += fontSize * 2;
            } else if (code > 0x7F) {
                // CJK / 全角
                px += fontSize;
            } else {
                // ASCII（粗体下约 0.65x）
                px += fontSize * 0.65;
            }
        }
        return px;
    }

    private spawnScroll(item: DanmakuEntry, t: number, cw: number, ch: number) {
        // 区域过滤：跳过不在指定区域的弹幕
        if (!this.isInArea(item.marginV)) return;

        const fontSize = this.settings.fontSize;
        const scrollTime = this.settings.scrollTime;
        const textPx = this.estimateTextWidth(item.text);
        const twPct = (textPx / cw) * 100;

        // 计算结束时间：基于实际 spawn 时间 t，确保弹幕完整滚出屏幕
        const endTime = t + scrollTime + (textPx / cw) * scrollTime;

        // 创建 DOM
        const el = document.createElement('div');
        el.className = 'danmaku-item';
        el.textContent = item.text;
        el.style.color = item.color;
        el.style.fontSize = fontSize + 'px';
        el.style.lineHeight = (fontSize + 4) + 'px';
        el.style.textShadow = '1px 1px 2px rgba(0,0,0,0.8),-1px -1px 2px rgba(0,0,0,0.8),1px -1px 2px rgba(0,0,0,0.8),-1px 1px 2px rgba(0,0,0,0.8)';

        // 直接使用 ASS 的 marginV，按比例缩放到 overlay 高度
        const scaledTop = (item.marginV / this.resY) * ch;
        el.style.top = scaledTop + 'px';
        el.style.left = '100%';
        el.dataset.tw = twPct.toString();

        this.overlay.appendChild(el);
        this.scrollEls.push({ el, spawnTime: t, endTime });
    }

    /** 重新布局固定位置消息，消除过期后留下的空隙 */
    private relayoutFixed() {
        // 按位置分组，每组内重新计算堆叠偏移
        const groups: Record<string, { el: HTMLDivElement }[]> = {};
        for (const f of this.fixedEls) {
            if (!groups[f.posKey]) groups[f.posKey] = [];
            groups[f.posKey].push(f);
        }
        for (const posKey in groups) {
            const isTop = posKey.startsWith('top');
            let offset = 0;
            for (const item of groups[posKey]) {
                if (isTop) {
                    item.el.style.top = (10 + offset) + 'px';
                    item.el.style.bottom = '';
                } else {
                    item.el.style.bottom = (10 + offset) + 'px';
                    item.el.style.top = '';
                }
                offset += item.el.offsetHeight + 4;
            }
        }
    }

    /** 生成固定位置消息（Guard/SC），支持同位置堆叠 */
    private spawnFixed(item: DanmakuEntry, t: number) {
        const el = document.createElement('div');
        el.style.position = 'absolute';
        el.style.whiteSpace = 'normal';
        el.style.wordBreak = 'break-all';
        el.style.fontWeight = 'bold';
        el.style.padding = '4px 12px';
        el.style.borderRadius = '4px';
        el.style.fontSize = (DANMAKU_FONT_SIZE - 2) + 'px';
        el.style.lineHeight = (DANMAKU_FONT_SIZE + 2) + 'px';
        el.style.textShadow = '1px 1px 2px rgba(0,0,0,0.6)';
        el.style.pointerEvents = 'none';
        el.style.transition = 'opacity 0.3s';
        el.style.opacity = '1';
        el.textContent = item.text;

        // 根据 {\an} 值确定位置
        // ASS numpad: 7=左上 8=上中 9=右上 / 4=左中 5=居中 6=右中 / 1=左下 2=下中 3=右下
        const isTop = item.align >= 7;
        const isRight = item.align === 3 || item.align === 6 || item.align === 9;
        const isCenter = item.align === 2 || item.align === 5 || item.align === 8;
        const posKey = (isTop ? 'top' : 'bottom') + '-' + (isCenter ? 'center' : isRight ? 'right' : 'left');

        // 计算同位置已有消息的总高度（用于堆叠偏移）
        let stackOffset = 0;
        for (const f of this.fixedEls) {
            if (f.posKey === posKey) {
                stackOffset += f.el.offsetHeight + 4; // 4px 间距
            }
        }

        // 设置位置
        if (isTop) {
            el.style.top = (10 + stackOffset) + 'px';
        } else {
            el.style.bottom = (10 + stackOffset) + 'px';
        }
        if (isCenter) {
            el.style.left = '50%';
            el.style.transform = 'translateX(-50%)';
        } else if (isRight) {
            el.style.right = '10px';
        } else {
            el.style.left = '10px';
        }

        // 背景色：优先使用 ASS 文件中样式定义的颜色
        if (item.bgColor) {
            el.style.background = item.bgColor;
        } else if (item.style === 'Guard') {
            el.style.background = 'rgba(255,128,0,0.50)';
        } else {
            el.style.background = 'rgba(20,165,0,0.37)';
        }
        el.style.color = item.color;
        el.style.maxWidth = '60%';

        this.overlay.appendChild(el);
        this.fixedEls.push({ el, endTime: item.end, fadeOutTime: Math.max(item.start, item.end - 0.5), posKey, fadingOut: false });
    }
}

// ==================== 组件 ====================

type CurrentFolderFile = {
    is_folder: boolean;
    name: string;
    last_modified: number;
    size: number;
    subtitle_file?: string;
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
    const danmakuRef = useRef<DanmakuRenderer | null>(null);
    const [danmakuStats, setDanmakuStats] = useState<{ danmaku: number; gift: number; giftAmount: number; guard: number; guardAmount: number; sc: number; scAmount: number; totalIncome: number } | null>(null);
    const playerInitRef = useRef(false); // 跟踪播放器是否应该激活
    const [loadDanmaku, setLoadDanmaku] = useState(true); // 是否加载 ASS 弹幕
    const [filterDanmaku, setFilterDanmaku] = useState(true);
    const [filterGift, setFilterGift] = useState(true);
    const [filterGuard, setFilterGuard] = useState(true);
    const [filterSC, setFilterSC] = useState(true);
    const [danmakuSettings, setDanmakuSettings] = useState<DanmakuSettings>(DEFAULT_DANMAKU_SETTINGS);
    const parsedItemsRef = useRef<{ items: DanmakuEntry[]; scrollTime: number; resY: number } | null>(null);
    const currentPlayingRef = useRef<{ record: CurrentFolderFile; fullPath: string } | null>(null);

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
    const [isConcatModalVisible, setIsConcatModalVisible] = useState(false);
    const [concatOutputName, setConcatOutputName] = useState("");
    const [concatSubmitting, setConcatSubmitting] = useState(false);
    const [batchBurning, setBatchBurning] = useState(false);

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
        playerInitRef.current = false;
        if (danmakuRef.current) {
            danmakuRef.current.stop();
            danmakuRef.current = null;
        }
        parsedItemsRef.current = null;
        if (artRef.current) {
            artRef.current.destroy(true);
            artRef.current = null;
        }
        setIsPlayerVisible(false);
        setCurrentPlayingName("");
        setDanmakuStats(null);
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

    // 组件卸载时清理播放器和弹幕渲染器
    useEffect(() => {
        return () => {
            if (danmakuRef.current) {
                danmakuRef.current.stop();
                danmakuRef.current = null;
            }
            if (artRef.current) {
                artRef.current.destroy(true);
                artRef.current = null;
            }
        };
    }, []);

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

    // 切换 ASS 弹幕加载状态
    const toggleDanmaku = useCallback((checked: boolean) => {
        setLoadDanmaku(checked);

        if (!checked) {
            // 关闭弹幕：停止渲染器并移除覆盖层
            if (danmakuRef.current) {
                danmakuRef.current.stop();
                danmakuRef.current = null;
            }
            setDanmakuStats(null);
            return;
        }

        // 开启弹幕：加载 ASS 并渲染
        const playing = currentPlayingRef.current;
        if (!playing || !playing.record.subtitle_file || !artRef.current) return;

        const { record, fullPath } = playing;
        const dirPath = fullPath.includes('/') ? fullPath.substring(0, fullPath.lastIndexOf('/') + 1) : '';
        const assUrl = `files/${encodePath(dirPath + record.subtitle_file)}`;

        fetch(assUrl)
            .then(r => r.ok ? r.text() : Promise.reject())
            .then(text => {
                const { items, scrollTime, resY } = parseAss(text);
                if (items.length === 0) return;
                parsedItemsRef.current = { items, scrollTime, resY };

                // 统计弹幕类型
                let danmakuCount = 0, giftCount = 0, giftAmount = 0, guardCount = 0, guardAmount = 0, scCount = 0, scAmount = 0;
                for (const item of items) {
                    if (item.style === 'Danmaku') danmakuCount++;
                    else if (item.style === 'Gift') {
                        giftCount++;
                        const m = item.text.match(/\[礼物 ¥([\d.]+)\]/);
                        if (m) giftAmount += parseFloat(m[1]);
                    }
                    else if (item.style === 'Guard') {
                        guardCount++;
                        const m = item.text.match(/\[.+¥(\d+)\]( .+ 开通了)? \S+/);
                        if (m) guardAmount += parseInt(m[1]);
                    }
                    else if (item.style.startsWith('SC')) {
                        scCount++;
                        const m = item.text.match(/\[SC ¥(\d+)\]/);
                        if (m) scAmount += parseInt(m[1]);
                    }
                }
                const totalIncome = Math.round((giftAmount + guardAmount + scAmount) * 10) / 10;
                setDanmakuStats({ danmaku: danmakuCount, gift: giftCount, giftAmount, guard: guardCount, guardAmount, sc: scCount, scAmount, totalIncome });

                const artContainer = document.getElementById('art-container');
                if (!artContainer) return;
                const artInner = artContainer.querySelector('.art-video-player') as HTMLElement || artContainer;
                const overlay = document.createElement('div');
                overlay.className = 'danmaku-overlay';
                artInner.appendChild(overlay);

                // 使用 ASS 解析的 scrollTime 作为初始值
                const initialSettings = { ...danmakuSettings, scrollTime };
                setDanmakuSettings(initialSettings);
                const renderer = new DanmakuRenderer(overlay, artRef.current!.video, items, { danmaku: filterDanmaku, gift: filterGift, guard: filterGuard, sc: filterSC }, resY, initialSettings);
                danmakuRef.current = renderer;
                renderer.start();
                if (artRef.current!.video.currentTime > 0) {
                    renderer.seek(artRef.current!.video.currentTime);
                }
            })
            .catch(() => { /* 没有 ASS 文件或加载失败，静默忽略 */ });
    }, [filterDanmaku, filterGift, filterGuard, filterSC, danmakuSettings]); // eslint-disable-line react-hooks/exhaustive-deps

    // 过滤开关或设置变化时更新弹幕渲染器
    useEffect(() => {
        if (!parsedItemsRef.current || !artRef.current) return;

        const filter = { danmaku: filterDanmaku, gift: filterGift, guard: filterGuard, sc: filterSC };

        // 如果渲染器已存在，直接动态更新，不销毁重建
        if (danmakuRef.current) {
            danmakuRef.current.update(filter, danmakuSettings);
            return;
        }

        const artContainer = document.getElementById('art-container');
        if (!artContainer) return;
        const artInner = artContainer.querySelector('.art-video-player') as HTMLElement || artContainer;
        const overlay = document.createElement('div');
        overlay.className = 'danmaku-overlay';
        artInner.appendChild(overlay);

        const { items, resY } = parsedItemsRef.current;
        const renderer = new DanmakuRenderer(overlay, artRef.current.video, items, filter, resY, danmakuSettings);
        danmakuRef.current = renderer;
        renderer.start();
        if (artRef.current.video.currentTime > 0) {
            renderer.seek(artRef.current.video.currentTime);
        }
    }, [filterDanmaku, filterGift, filterGuard, filterSC, danmakuSettings]); // eslint-disable-line react-hooks/exhaustive-deps

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

    // 批量烧录相关
    const videoExtensions = ['.mp4', '.flv', '.ts', '.mkv', '.avi', '.mov', '.wmv', '.webm'];
    const concatVideoExtensions = ['.mp4', '.flv', '.ts', '.mkv', '.mov', '.m4v'];

    const isVideoFileName = (name: string): boolean => {
        const lower = name.toLowerCase();
        return videoExtensions.some(ext => lower.endsWith(ext));
    };

    const isConcatSupportedVideoFileName = (name: string): boolean => {
        const lower = name.toLowerCase();
        return concatVideoExtensions.some(ext => lower.endsWith(ext));
    };

    const handleBatchBurn = () => {
        if (selectedRowKeys.length === 0 || batchBurning) return;

        // 从 selectedRowKeys 中筛选视频文件
        const selectedFiles = currentFolderFiles.filter(f =>
            selectedRowKeys.includes(f.name) && !f.is_folder && isVideoFileName(f.name)
        );

        // 分类：有 ASS 的和无 ASS 的
        const withAss = selectedFiles.filter(f => f.subtitle_file);
        const withoutAss = selectedFiles.filter(f => !f.subtitle_file);

        if (withAss.length === 0) {
            message.info("选中的文件中没有可烧录的视频文件（需要有同名 ASS 字幕文件）");
            return;
        }

        // 构建确认信息
        const confirmContent = withoutAss.length > 0
            ? `可烧录 ${withAss.length} 个文件，${withoutAss.length} 个文件无 ASS 字幕将跳过。是否继续？`
            : `将对 ${withAss.length} 个文件进行弹幕字幕烧录，是否继续？`;

        Modal.confirm({
            title: '批量烧录弹幕字幕',
            content: (
                <div>
                    <div>{confirmContent}</div>
                    {withAss.length > 0 && (
                        <div style={{ marginTop: 8, fontSize: '12px', color: '#8c8c8c' }}>
                            可烧录: {withAss.map(f => f.name).join('、')}
                        </div>
                    )}
                </div>
            ),
            okText: '开始烧录',
            cancelText: '取消',
            onOk: async () => {
                setBatchBurning(true);
                try {
                    const paths = withAss.map(f => pathParam ? `${pathParam}/${f.name}` : f.name);
                    const rsp: any = await api.batchBurnFiles(paths);

                    if (rsp.enqueued > 0) {
                        message.success(`已成功入队 ${rsp.enqueued} 个烧录任务`);
                    }
                    if (rsp.skipped && rsp.skipped.length > 0) {
                        Modal.info({
                            title: '部分文件已跳过',
                            content: (
                                <div style={{ maxHeight: 300, overflow: 'auto' }}>
                                    {rsp.skipped.map((s: string, i: number) => (
                                        <div key={i} style={{ fontSize: '12px', color: '#8c8c8c' }}>{s}</div>
                                    ))}
                                </div>
                            ),
                        });
                    }
                    setSelectedRowKeys([]);
                } catch (err: any) {
                    message.error("批量烧录请求失败: " + (err?.message || err));
                } finally {
                    setBatchBurning(false);
                }
            },
        });
    };

    const getSelectedVideoFiles = (): CurrentFolderFile[] => {
        return currentFolderFiles
            .filter(f => selectedRowKeys.includes(f.name) && !f.is_folder && isConcatSupportedVideoFileName(f.name))
            .sort((a, b) => {
                if (a.last_modified === b.last_modified) {
                    return a.name.localeCompare(b.name);
                }
                return a.last_modified - b.last_modified;
            });
    };

    const showConcatModal = () => {
        const selectedFiles = getSelectedVideoFiles();
        if (selectedFiles.length < 2) {
            message.info("至少选择两个视频文件才能拼接");
            return;
        }
        const firstName = selectedFiles[0].name;
        const extIndex = firstName.lastIndexOf('.');
        const baseName = extIndex > 0 ? firstName.substring(0, extIndex) : firstName;
        setConcatOutputName(`${baseName}_concat`);
        setIsConcatModalVisible(true);
    };

    const handleBatchConcat = async () => {
        const selectedFiles = getSelectedVideoFiles();
        if (selectedFiles.length < 2 || concatSubmitting) return;

        const paths = selectedFiles.map(f => pathParam ? `${pathParam}/${f.name}` : f.name);
        setConcatSubmitting(true);
        try {
            const rsp: any = await api.batchConcatFiles(paths, concatOutputName.trim());
            if (rsp?.data?.output_name) {
                message.success(`已按时间顺序拼接 ${rsp.data.count} 个视频：${rsp.data.output_name}`);
            } else {
                message.success("视频拼接成功");
            }
            setIsConcatModalVisible(false);
            setSelectedRowKeys([]);
            requestFileList(pathParam);
        } catch (err: any) {
            message.error("视频拼接失败: " + (err?.message || err));
        } finally {
            setConcatSubmitting(false);
        }
    };

    // 计算选中文件中可烧录的数量
    const getBurnableCount = (): { total: number; withAss: number } => {
        const selectedFiles = getSelectedVideoFiles();
        return {
            total: selectedFiles.length,
            withAss: selectedFiles.filter(f => f.subtitle_file).length,
        };
    };

    const getConcatableInfo = (): { total: number; extensions: string[] } => {
        const selectedFiles = getSelectedVideoFiles();
        const extensions = Array.from(new Set(selectedFiles.map(f => f.name.includes('.') ? f.name.substring(f.name.lastIndexOf('.')).toLowerCase() : '')));
        return { total: selectedFiles.length, extensions };
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
            currentPlayingRef.current = { record, fullPath };
            playerInitRef.current = true;
            // 使用 setTimeout 确保 DOM 已更新
            setTimeout(() => {
                // 检查是否已被 hidePlayer 取消
                if (!playerInitRef.current) return;
                if (danmakuRef.current) {
                    danmakuRef.current.stop();
                    danmakuRef.current = null;
                }
                parsedItemsRef.current = null;
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

                // 弹幕渲染集成（仅在 loadDanmaku 为 true 时加载）
                if (loadDanmaku && record.subtitle_file) {
                    // 使用 API 返回的 subtitle_file 字段构造 URL，而非推导
                    const dirPath = fullPath.includes('/') ? fullPath.substring(0, fullPath.lastIndexOf('/') + 1) : '';
                    const assUrl = `files/${encodePath(dirPath + record.subtitle_file)}`;
                    art.on('ready', () => {
                        fetch(assUrl)
                            .then(r => r.ok ? r.text() : Promise.reject())
                            .then(text => {
                                const { items, scrollTime, resY } = parseAss(text);
                                if (items.length === 0) return;
                                parsedItemsRef.current = { items, scrollTime, resY };
                                let danmakuCount = 0, giftCount = 0, giftAmount = 0, guardCount = 0, guardAmount = 0, scCount = 0, scAmount = 0;
                                for (const item of items) {
                                    if (item.style === 'Danmaku') danmakuCount++;
                                    else if (item.style === 'Gift') {
                                        giftCount++;
                                        const m = item.text.match(/\[礼物 ¥([\d.]+)\]/);
                                        if (m) giftAmount += parseFloat(m[1]);
                                    }
                                    else if (item.style === 'Guard') {
                                        guardCount++;
                                        const m = item.text.match(/\[.+¥(\d+)\]( .+ 开通了)? \S+/);
                                        if (m) guardAmount += parseInt(m[1]);
                                    }
                                    else if (item.style.startsWith('SC')) {
                                        scCount++;
                                        // 从文本中提取金额: [SC ¥100] ...
                                        const m = item.text.match(/\[SC ¥(\d+)\]/);
                                        if (m) scAmount += parseInt(m[1]);
                                    }
                                }
                                const totalIncome = Math.round((giftAmount + guardAmount + scAmount) * 10) / 10;
                                setDanmakuStats({ danmaku: danmakuCount, gift: giftCount, giftAmount, guard: guardCount, guardAmount, sc: scCount, scAmount, totalIncome });

                                // 找到 Artplayer 内部容器，将覆盖层插入其中（和 video 同级）
                                const artContainer = document.getElementById('art-container');
                                if (!artContainer) return;
                                // Artplayer 在 #art-container 内创建 .art-video-player
                                const artInner = artContainer.querySelector('.art-video-player') as HTMLElement || artContainer;
                                artInner.style.position = 'relative';
                                const overlay = document.createElement('div');
                                overlay.className = 'danmaku-overlay';
                                artInner.appendChild(overlay);

                                // 使用 ASS 解析的 scrollTime 作为初始值
                                const initialSettings = { ...danmakuSettings, scrollTime };
                                setDanmakuSettings(initialSettings);
                                const renderer = new DanmakuRenderer(overlay, art.video, items, { danmaku: filterDanmaku, gift: filterGift, guard: filterGuard, sc: filterSC }, resY, initialSettings);
                                danmakuRef.current = renderer;
                                renderer.start();
                                // 如果浏览器恢复了播放位置，跳转到当前时间
                                if (art.video.currentTime > 0) {
                                    renderer.seek(art.video.currentTime);
                                }
                            })
                            .catch(() => { /* 没有 ASS 文件或加载失败，静默忽略 */ });
                    });
                }

                // 拖拽进度条时跳转弹幕（始终注册，供 toggle 动态加载后使用）
                art.on('video:seeked', () => {
                    if (danmakuRef.current) {
                        danmakuRef.current.seek(art.video.currentTime);
                    }
                });

                art.on('destroy', () => {
                    if (danmakuRef.current) {
                        danmakuRef.current.stop();
                        danmakuRef.current = null;
                    }
                });
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
                        {record.subtitle_file && (
                            <Tooltip title={`弹幕字幕: ${record.subtitle_file}`}>
                                <span style={{ marginLeft: 6, fontSize: 11, color: '#1890ff', background: '#e6f4ff', padding: '1px 6px', borderRadius: 4, cursor: 'default' }}>
                                    弹幕
                                </span>
                            </Tooltip>
                        )}
                        {!record.is_folder && !record.subtitle_file && isVideoFileName(record.name) && (
                            <span style={{ marginLeft: 6, fontSize: 11, color: '#8c8c8c', background: '#f5f5f5', padding: '1px 6px', borderRadius: 4, cursor: 'default' }}>
                                无字幕
                            </span>
                        )}
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
        const hasAss = currentPlayingRef.current?.record?.subtitle_file;
        return (
            <div className="player-wrapper">
                <div className="player-header">
                    <div className="playing-title" title={currentPlayingName}>
                        正在播放: {currentPlayingName}
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                        {hasAss && (
                            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, fontWeight: 400 }}>
                                <span style={{ color: loadDanmaku ? '#1890ff' : '#ffffff73' }}>弹幕</span>
                                <Switch
                                    size="small"
                                    checked={loadDanmaku}
                                    onChange={toggleDanmaku}
                                />
                                {loadDanmaku && (
                                    <>
                                        <span style={{ color: '#ffffff40' }}>|</span>
                                        <Checkbox checked={filterDanmaku} onChange={(e) => setFilterDanmaku(e.target.checked)}>
                                            <span style={{ fontSize: 12 }}>文字</span>
                                        </Checkbox>
                                        <Checkbox checked={filterGift} onChange={(e) => setFilterGift(e.target.checked)}>
                                            <span style={{ fontSize: 12 }}>礼物</span>
                                        </Checkbox>
                                        <Checkbox checked={filterGuard} onChange={(e) => setFilterGuard(e.target.checked)}>
                                            <span style={{ fontSize: 12 }}>上舰</span>
                                        </Checkbox>
                                        <Checkbox checked={filterSC} onChange={(e) => setFilterSC(e.target.checked)}>
                                            <span style={{ fontSize: 12 }}>SC</span>
                                        </Checkbox>
                                    </>
                                )}
                            </div>
                        )}
                        {hasAss && loadDanmaku && (
                            <Popover
                                content={
                                    <div style={{ width: 220 }}>
                                        <div style={{ marginBottom: 12 }}>
                                            <div style={{ marginBottom: 4, fontSize: 12, color: '#999' }}>显示区域</div>
                                            <Select
                                                value={danmakuSettings.area}
                                                onChange={(v) => setDanmakuSettings(s => ({ ...s, area: v }))}
                                                style={{ width: '100%' }}
                                                size="small"
                                                options={[
                                                    { label: '全屏', value: 'full' },
                                                    { label: '顶部半屏', value: 'top' },
                                                    { label: '底部半屏', value: 'bottom' },
                                                    { label: '顶部1/4屏', value: 'quarter' },
                                                    { label: '顶部3/4屏', value: 'three-quarter' },
                                                ]}
                                            />
                                        </div>
                                        <div style={{ marginBottom: 12 }}>
                                            <div style={{ marginBottom: 4, fontSize: 12, color: '#999' }}>字体大小: {danmakuSettings.fontSize}px</div>
                                            <Slider
                                                min={12}
                                                max={48}
                                                value={danmakuSettings.fontSize}
                                                onChange={(v) => setDanmakuSettings(s => ({ ...s, fontSize: v }))}
                                            />
                                        </div>
                                        <div style={{ marginBottom: 12 }}>
                                            <div style={{ marginBottom: 4, fontSize: 12, color: '#999' }}>透明度: {Math.round(danmakuSettings.opacity * 100)}%</div>
                                            <Slider
                                                min={20}
                                                max={100}
                                                value={Math.round(danmakuSettings.opacity * 100)}
                                                onChange={(v) => setDanmakuSettings(s => ({ ...s, opacity: v / 100 }))}
                                            />
                                        </div>
                                        <div>
                                            <div style={{ marginBottom: 4, fontSize: 12, color: '#999' }}>滚动速度: {danmakuSettings.scrollTime}秒</div>
                                            <Slider
                                                min={3}
                                                max={20}
                                                value={danmakuSettings.scrollTime}
                                                onChange={(v) => setDanmakuSettings(s => ({ ...s, scrollTime: v }))}
                                            />
                                        </div>
                                        <div style={{ marginTop: 8, textAlign: 'right' }}>
                                            <Button size="small" onClick={() => setDanmakuSettings(DEFAULT_DANMAKU_SETTINGS)}>恢复默认</Button>
                                        </div>
                                    </div>
                                }
                                trigger="click"
                                placement="bottomRight"
                            >
                                <span style={{ color: '#1890ff', cursor: 'pointer', fontSize: 13, fontWeight: 600, background: '#e6f4ff', padding: '2px 8px', borderRadius: 4 }}>弹</span>
                            </Popover>
                        )}
                        <div className="close-btn" onClick={hidePlayer} title="退出播放 (Esc)">
                            <CloseOutlined />
                        </div>
                    </div>
                </div>
                <div id="art-container"></div>
                {danmakuStats && (
                    <div className="danmaku-stats">
                        <span className="stat-item" style={{ opacity: filterDanmaku ? 1 : 0.4 }}>
                            <span className="stat-icon" style={{ color: '#1890ff' }}>💬</span>
                            弹幕 <b>{danmakuStats.danmaku}</b>
                        </span>
                        <span className="stat-item" style={{ opacity: filterGift ? 1 : 0.4 }}>
                            <span className="stat-icon" style={{ color: '#faad14' }}>🎁</span>
                            礼物 <b>{danmakuStats.gift}</b>
                            {danmakuStats.giftAmount > 0 && <span className="stat-amount"> ¥{danmakuStats.giftAmount % 1 === 0 ? danmakuStats.giftAmount : danmakuStats.giftAmount.toFixed(1)}</span>}
                        </span>
                        <span className="stat-item" style={{ opacity: filterSC ? 1 : 0.4 }}>
                            <span className="stat-icon" style={{ color: '#ff6a39' }}>💰</span>
                            SC <b>{danmakuStats.sc}</b>
                            {danmakuStats.scAmount > 0 && <span className="stat-amount"> ¥{danmakuStats.scAmount}</span>}
                        </span>
                        <span className="stat-item" style={{ opacity: filterGuard ? 1 : 0.4 }}>
                            <span className="stat-icon" style={{ color: '#ff8c00' }}>⚓</span>
                            上舰 <b>{danmakuStats.guard}</b>
                            {danmakuStats.guardAmount > 0 && <span className="stat-amount"> ¥{danmakuStats.guardAmount}</span>}
                        </span>
                        {danmakuStats.totalIncome > 0 && (
                            <span className="stat-item" style={{ fontWeight: 600 }}>
                                <span className="stat-icon" style={{ color: '#52c41a' }}>💵</span>
                                总收入 <b>¥{danmakuStats.totalIncome % 1 === 0 ? danmakuStats.totalIncome : danmakuStats.totalIncome.toFixed(1)}</b>
                            </span>
                        )}
                    </div>
                )}
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
                        {(() => {
                            const { total, extensions } = getConcatableInfo();
                            const isSameFormat = extensions.length <= 1;
                            const canConcat = total >= 2 && isSameFormat;
                            const tooltip = total < 2
                                ? '至少选择两个受支持的视频文件才能拼接'
                                : !isSameFormat
                                    ? '当前仅支持拼接相同格式的视频文件'
                                    : `将按修改时间从早到晚拼接 ${total} 个视频`;
                            return (
                                <Tooltip title={tooltip}>
                                    <Button size="small" type="default" disabled={!canConcat} loading={concatSubmitting} onClick={showConcatModal}>
                                        拼接视频{total >= 2 ? ` (${total})` : ''}
                                    </Button>
                                </Tooltip>
                            );
                        })()}
                        {(() => {
                            const { withAss } = getBurnableCount();
                            return (
                                <Tooltip title={withAss > 0 ? `可烧录 ${withAss} 个文件` : '选中的文件中无可烧录的视频（需有同名 ASS 字幕）'}>
                                    <Button
                                        size="small"
                                        type="default"
                                        loading={batchBurning}
                                        disabled={withAss === 0}
                                        onClick={handleBatchBurn}
                                    >
                                        批量烧录{withAss > 0 ? ` (${withAss})` : ''}
                                    </Button>
                                </Tooltip>
                            );
                        })()}
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
            {/* @ts-ignore */}
            <Modal
                title="按时间顺序拼接视频"
                open={isConcatModalVisible}
                onOk={handleBatchConcat}
                confirmLoading={concatSubmitting}
                onCancel={() => setIsConcatModalVisible(false)}
                okText="开始拼接"
                cancelText="取消"
                destroyOnClose
            >
                <div>
                    <div style={{ marginBottom: 12 }}>
                        将按文件最后修改时间从早到晚拼接选中的视频文件。
                    </div>
                    <div style={{ marginBottom: 8 }}>输出文件名（后缀自动保留）：</div>
                    <Input value={concatOutputName} onChange={(e) => setConcatOutputName(e.target.value)} placeholder="请输入输出文件名" onPressEnter={handleBatchConcat} autoComplete="off" />
                    <div style={{ marginTop: 12, color: '#8c8c8c', fontSize: '12px', lineHeight: 1.8 }}>
                        * 仅支持拼接同一目录下、相同格式的视频文件。
                        <br />* 当前使用 FFmpeg 直接拼接，不重新编码，适合同一场录播分段合并。
                    </div>
                </div>
            </Modal>
        </div>
    );
};

export default FileList;
