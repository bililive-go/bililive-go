import React, { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { Tooltip, message, Input } from 'antd';
import {
  VerticalAlignBottomOutlined,
  PlayCircleOutlined,
  PauseCircleOutlined,
  DeleteOutlined,
  SaveOutlined,
  CloseCircleOutlined,
  UpOutlined,
  DownOutlined
} from '@ant-design/icons';
import './log-panel.css';

// 日志显示上限
const MAX_LOG_LINES = 500;
// 每行的估算高度
const ITEM_HEIGHT = 24;
// 可视区域上下额外渲染的行数
const OVERSCAN = 5;
// 搜索防抖延迟(ms)
const SEARCH_DEBOUNCE = 150;

interface LogPanelProps {
  logs: string[];
  onLogsChange?: (logs: string[]) => void;
  roomName?: string;
}

interface MatchInfo {
  logIndex: number;
  startPos: number;
}

// 防抖 hook
function useDebounce<T>(value: T, delay: number): T {
  const [debouncedValue, setDebouncedValue] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedValue(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return debouncedValue;
}

const LogPanel: React.FC<LogPanelProps> = ({ logs, onLogsChange, roomName }) => {
  const [isPaused, setIsPaused] = useState(false);
  const [autoScroll, setAutoScroll] = useState(true);
  const [displayLogs, setDisplayLogs] = useState<string[]>([]);
  const [pendingCount, setPendingCount] = useState(0);

  // 搜索相关状态
  const [searchInput, setSearchInput] = useState(''); // 即时输入值
  const debouncedSearch = useDebounce(searchInput, SEARCH_DEBOUNCE); // 防抖后的搜索值
  const [currentMatchIndex, setCurrentMatchIndex] = useState(0);
  const [matchCountChanged, setMatchCountChanged] = useState(false);

  // 虚拟滚动状态
  const [scrollTop, setScrollTop] = useState(0);
  const [containerHeight, setContainerHeight] = useState(240);

  const listContainerRef = useRef<HTMLDivElement>(null);
  const lastLogsLengthRef = useRef(0);
  const prevMatchCountRef = useRef(0);
  const autoScrollEnabledRef = useRef(true); // 用于追踪自动滚动是否应该生效

  // 计算所有匹配项 - 使用防抖后的搜索值
  const matches = useMemo((): MatchInfo[] => {
    if (!debouncedSearch.trim()) return [];

    const result: MatchInfo[] = [];
    const searchLower = debouncedSearch.toLowerCase();

    displayLogs.forEach((log, logIndex) => {
      const logLower = log.toLowerCase();
      let pos = 0;
      while ((pos = logLower.indexOf(searchLower, pos)) !== -1) {
        result.push({ logIndex, startPos: pos });
        pos += debouncedSearch.length;
      }
    });

    return result;
  }, [displayLogs, debouncedSearch]);

  // 为每行创建匹配位置的快速查找映射
  const matchesByLine = useMemo(() => {
    const map = new Map<number, Map<number, number>>(); // logIndex -> (startPos -> globalMatchIndex)
    matches.forEach((match, globalIndex) => {
      if (!map.has(match.logIndex)) {
        map.set(match.logIndex, new Map());
      }
      map.get(match.logIndex)!.set(match.startPos, globalIndex);
    });
    return map;
  }, [matches]);

  // 监听匹配数量变化，触发动画
  useEffect(() => {
    if (matches.length !== prevMatchCountRef.current && debouncedSearch.trim()) {
      setMatchCountChanged(true);
      const timer = setTimeout(() => setMatchCountChanged(false), 600);

      if (matches.length < prevMatchCountRef.current && currentMatchIndex >= matches.length) {
        setCurrentMatchIndex(Math.max(0, matches.length - 1));
      }

      prevMatchCountRef.current = matches.length;
      return () => clearTimeout(timer);
    }
  }, [matches.length, debouncedSearch, currentMatchIndex]);

  // 当外部 logs 更新时，根据暂停状态决定是否更新显示
  useEffect(() => {
    const newLogsCount = logs.length - lastLogsLengthRef.current;

    if (isPaused) {
      if (newLogsCount > 0) {
        setPendingCount(prev => prev + newLogsCount);
      }
    } else {
      const trimmedLogs = logs.slice(-MAX_LOG_LINES);
      setDisplayLogs(trimmedLogs);
      setPendingCount(0);
    }

    lastLogsLengthRef.current = logs.length;
  }, [logs, isPaused]);

  // 自动滚动到底部
  useEffect(() => {
    if (autoScroll && autoScrollEnabledRef.current && !debouncedSearch.trim() && listContainerRef.current) {
      const container = listContainerRef.current;
      container.scrollTop = container.scrollHeight;
    }
  }, [displayLogs, autoScroll, debouncedSearch]);

  // 滚动到当前匹配项
  useEffect(() => {
    if (matches.length > 0 && currentMatchIndex < matches.length && listContainerRef.current) {
      const match = matches[currentMatchIndex];
      const targetScrollTop = match.logIndex * ITEM_HEIGHT - containerHeight / 2 + ITEM_HEIGHT / 2;
      listContainerRef.current.scrollTop = Math.max(0, targetScrollTop);
    }
  }, [currentMatchIndex, matches, containerHeight]);

  // 初始化容器高度
  useEffect(() => {
    if (listContainerRef.current) {
      setContainerHeight(listContainerRef.current.clientHeight);
    }
  }, []);

  const handleScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    const container = e.currentTarget;
    setScrollTop(container.scrollTop);

    // 检查是否在底部
    const isAtBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 50;
    autoScrollEnabledRef.current = isAtBottom;
  }, []);

  // 切换自动滚动
  const toggleAutoScroll = useCallback(() => {
    const newAutoScroll = !autoScroll;
    setAutoScroll(newAutoScroll);
    if (newAutoScroll) {
      autoScrollEnabledRef.current = true;
      if (searchInput.trim()) {
        setSearchInput('');
        setCurrentMatchIndex(0);
      }
      if (listContainerRef.current) {
        listContainerRef.current.scrollTop = listContainerRef.current.scrollHeight;
      }
    }
  }, [autoScroll, searchInput]);

  const togglePause = useCallback(() => {
    if (isPaused) {
      const trimmedLogs = logs.slice(-MAX_LOG_LINES);
      setDisplayLogs(trimmedLogs);
      setPendingCount(0);
      lastLogsLengthRef.current = logs.length;
    }
    setIsPaused(!isPaused);
  }, [isPaused, logs]);

  const clearLogs = useCallback(() => {
    setDisplayLogs([]);
    setPendingCount(0);
    lastLogsLengthRef.current = 0;
    setSearchInput('');
    setCurrentMatchIndex(0);
    if (onLogsChange) {
      onLogsChange([]);
    }
    message.success('日志已清空');
  }, [onLogsChange]);

  const saveLogs = useCallback(() => {
    if (displayLogs.length === 0) {
      message.warning('暂无日志可保存');
      return;
    }

    const content = displayLogs.join('\n');
    const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    const timestamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
    const filename = roomName ? `${roomName}_logs_${timestamp}.txt` : `logs_${timestamp}.txt`;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    URL.revokeObjectURL(url);
    message.success('日志已保存');
  }, [displayLogs, roomName]);

  const handleSearchChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setSearchInput(value);
    setCurrentMatchIndex(0);
    if (value.trim()) {
      setAutoScroll(false);
    }
  }, []);

  const goToPrevMatch = useCallback(() => {
    if (matches.length === 0) return;
    setCurrentMatchIndex(prev => (prev - 1 + matches.length) % matches.length);
  }, [matches.length]);

  const goToNextMatch = useCallback(() => {
    if (matches.length === 0) return;
    setCurrentMatchIndex(prev => (prev + 1) % matches.length);
  }, [matches.length]);

  const clearSearch = useCallback(() => {
    setSearchInput('');
    setCurrentMatchIndex(0);
  }, []);

  // 虚拟列表计算
  const totalHeight = displayLogs.length * ITEM_HEIGHT;
  const startIndex = Math.max(0, Math.floor(scrollTop / ITEM_HEIGHT) - OVERSCAN);
  const endIndex = Math.min(
    displayLogs.length,
    Math.ceil((scrollTop + containerHeight) / ITEM_HEIGHT) + OVERSCAN
  );
  const visibleLogs = displayLogs.slice(startIndex, endIndex);
  const offsetY = startIndex * ITEM_HEIGHT;

  // 渲染单行日志（带高亮）
  const renderLogLine = useCallback((text: string, logIndex: number) => {
    const searchTerm = debouncedSearch.trim();
    if (!searchTerm) {
      return <span>{text}</span>;
    }

    const lineMatches = matchesByLine.get(logIndex);

    if (!lineMatches || lineMatches.size === 0) {
      return <span>{text}</span>;
    }

    const parts: React.ReactNode[] = [];
    let lastIndex = 0;
    let keyIndex = 0;

    // 找出所有匹配位置并排序
    const positions = Array.from(lineMatches.keys()).sort((a, b) => a - b);

    for (const pos of positions) {
      if (pos > lastIndex) {
        parts.push(<span key={`t${keyIndex++}`}>{text.substring(lastIndex, pos)}</span>);
      }

      const globalMatchIndex = lineMatches.get(pos)!;
      const isCurrentMatch = globalMatchIndex === currentMatchIndex;

      parts.push(
        <span
          key={`h${keyIndex++}`}
          className={`log-highlight ${isCurrentMatch ? 'log-highlight-current' : ''}`}
        >
          {text.substring(pos, pos + searchTerm.length)}
        </span>
      );

      lastIndex = pos + searchTerm.length;
    }

    if (lastIndex < text.length) {
      parts.push(<span key={`t${keyIndex++}`}>{text.substring(lastIndex)}</span>);
    }

    return <>{parts}</>;
  }, [debouncedSearch, matchesByLine, currentMatchIndex]);

  const isSearching = searchInput.trim().length > 0;

  return (
    <div className="log-panel">
      {/* 工具栏 */}
      <div className="log-toolbar">
        <Tooltip title={autoScroll ? '自动滚动已开启（点击关闭）' : '点击开启自动滚动'}>
          <div
            className={`log-toolbar-btn ${autoScroll ? 'active' : ''}`}
            onClick={toggleAutoScroll}
          >
            <VerticalAlignBottomOutlined />
          </div>
        </Tooltip>
        <Tooltip title={isPaused ? '继续接收日志' : '暂停接收日志'}>
          <div
            className={`log-toolbar-btn ${isPaused ? 'paused' : ''}`}
            onClick={togglePause}
          >
            {isPaused ? <PlayCircleOutlined /> : <PauseCircleOutlined />}
          </div>
        </Tooltip>
        <Tooltip title="清空日志">
          <div className="log-toolbar-btn" onClick={clearLogs}>
            <DeleteOutlined />
          </div>
        </Tooltip>
        <Tooltip title="保存日志">
          <div className="log-toolbar-btn" onClick={saveLogs}>
            <SaveOutlined />
          </div>
        </Tooltip>

        {/* 搜索框 */}
        <div className="log-search-box">
          <Input
            placeholder="搜索日志..."
            value={searchInput}
            onChange={handleSearchChange}
            size="small"
            style={{ width: 120 }}
            suffix={
              searchInput ? (
                <CloseCircleOutlined
                  onClick={clearSearch}
                  style={{ cursor: 'pointer', color: '#999' }}
                />
              ) : null
            }
          />
          {isSearching && (
            <>
              <span className={`log-search-count ${matchCountChanged ? 'count-changed' : ''}`}>
                {matches.length > 0
                  ? `${currentMatchIndex + 1}/${matches.length}`
                  : '0/0'
                }
              </span>
              <div
                className={`log-toolbar-btn log-search-nav ${matches.length === 0 ? 'disabled' : ''}`}
                onClick={goToPrevMatch}
              >
                <UpOutlined />
              </div>
              <div
                className={`log-toolbar-btn log-search-nav ${matches.length === 0 ? 'disabled' : ''}`}
                onClick={goToNextMatch}
              >
                <DownOutlined />
              </div>
            </>
          )}
        </div>

        {isPaused && pendingCount > 0 && (
          <span className="log-paused-hint">
            +{pendingCount} 条新日志
          </span>
        )}
      </div>

      {/* 虚拟滚动日志列表 */}
      <div
        ref={listContainerRef}
        className="log-list-container"
        onScroll={handleScroll}
      >
        <div style={{ height: totalHeight, position: 'relative' }}>
          <div style={{ transform: `translateY(${offsetY}px)` }}>
            {visibleLogs.map((log, idx) => {
              const actualIndex = startIndex + idx;
              return (
                <div
                  key={actualIndex}
                  className="log-item"
                  style={{ height: ITEM_HEIGHT }}
                >
                  <code className="log-item-text">
                    {renderLogLine(log, actualIndex)}
                  </code>
                </div>
              );
            })}
          </div>
        </div>
        {displayLogs.length === 0 && (
          <div className="log-empty">暂无日志</div>
        )}
      </div>

      {/* 日志数量指示 */}
      <div className="log-status-bar">
        <span>共 {displayLogs.length} 条日志</span>
        {displayLogs.length >= MAX_LOG_LINES && (
          <span className="log-limit-hint">（已达上限，旧日志已被移除）</span>
        )}
        {autoScroll && !isSearching && (
          <span className="log-autoscroll-hint">自动滚动中</span>
        )}
      </div>
    </div>
  );
};

export default LogPanel;
