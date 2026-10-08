import React, { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { Tooltip } from 'antd';
import {
  VerticalAlignBottomOutlined
} from '@ant-design/icons';
import './danmaku-panel.css';

const MAX_MESSAGES = 5000;
const ITEM_HEIGHT = 32;
const OVERSCAN = 5;

export interface DanmakuMessage {
  type: 'danmaku' | 'gift' | 'super_chat' | 'guard';
  username: string;
  content: string;
  color?: number;
  timestamp: number;
  gift_name?: string;
  num?: number;
  price?: number;
  coin_type?: string;
}

interface DanmakuPanelProps {
  messages: DanmakuMessage[];
}

// 消息类型过滤配置
const FILTER_TYPES: { key: DanmakuMessage['type']; label: string; color: string }[] = [
  { key: 'danmaku', label: '弹幕', color: '#69b1ff' },
  { key: 'gift', label: '礼物', color: '#ffc53d' },
  { key: 'super_chat', label: 'SC', color: '#ffa940' },
  { key: 'guard', label: '舰长', color: '#b37feb' },
];

const formatTime = (timestamp: number): string => new Date(timestamp * 1000).toLocaleTimeString();

const formatGiftPrice = (msg: DanmakuMessage): string => {
  if (msg.coin_type === 'gold' && msg.price && msg.price > 0) {
    return `¥${(msg.price * (msg.num || 1) / 1000).toFixed(1)}`;
  }
  return '';
};

const formatGuardPrice = (msg: DanmakuMessage): string => {
  if (msg.price && msg.price > 0) {
    return `¥${(msg.price / 1000).toFixed(0)}`;
  }
  return '';
};

const DanmakuPanel: React.FC<DanmakuPanelProps> = ({ messages }) => {
  const [autoScroll, setAutoScroll] = useState(true);
  const [activeFilters, setActiveFilters] = useState<Set<DanmakuMessage['type']>>(
    () => new Set(FILTER_TYPES.map(f => f.key))
  );

  const [scrollTop, setScrollTop] = useState(0);
  const [containerHeight, setContainerHeight] = useState(480);

  const listContainerRef = useRef<HTMLDivElement>(null);
  const autoScrollEnabledRef = useRef(true);
  const prevFilteredCountRef = useRef(0);
  const prevActiveFiltersRef = useRef(activeFilters);
  const prevFilteredLenRef = useRef(0);
  const [newMessageCount, setNewMessageCount] = useState(0);

  const displayMessages = useMemo(() => messages.slice(-MAX_MESSAGES), [messages]);

  const filteredMessages = useMemo(() => {
    if (activeFilters.size === FILTER_TYPES.length) return displayMessages;
    return displayMessages.filter(msg => activeFilters.has(msg.type));
  }, [displayMessages, activeFilters]);

  // 统计：筛选后的条数 + 礼物总金额
  const stats = useMemo(() => {
    let totalCount = 0;
    let totalAmount = 0;
    for (const msg of filteredMessages) {
      totalCount++;
      if (msg.type === 'gift' && msg.coin_type === 'gold' && msg.price && msg.price > 0) {
        totalAmount += msg.price * (msg.num || 1) / 1000;
      } else if (msg.type === 'super_chat' && msg.price && msg.price > 0) {
        totalAmount += msg.price;
      } else if (msg.type === 'guard' && msg.price && msg.price > 0) {
        totalAmount += msg.price / 1000;
      }
    }
    return { totalCount, totalAmount };
  }, [filteredMessages]);

  const toggleFilter = useCallback((type: DanmakuMessage['type']) => {
    setActiveFilters(prev => {
      const next = new Set(prev);
      if (next.has(type)) {
        if (next.size <= 1) return prev;
        next.delete(type);
      } else {
        next.add(type);
      }
      return next;
    });
  }, []);

  // 暂停滚动时，统计新消息数量（过滤器变化时重置）
  useEffect(() => {
    const prevCount = prevFilteredCountRef.current;
    const newCount = filteredMessages.length;
    if (newCount > prevCount && !autoScrollEnabledRef.current) {
      setNewMessageCount(prev => prev + (newCount - prevCount));
    }
    prevFilteredCountRef.current = newCount;
  }, [filteredMessages.length]);

  // 过滤器变化时重置新消息计数（避免过滤器切换导致误判）
  useEffect(() => {
    if (prevActiveFiltersRef.current !== activeFilters) {
      // 过滤器变化：重置计数
      prevActiveFiltersRef.current = activeFilters;
      prevFilteredLenRef.current = filteredMessages.length;
      setNewMessageCount(0);
    } else {
      // 仅新消息：更新长度 ref
      prevFilteredLenRef.current = filteredMessages.length;
    }
  }, [activeFilters, filteredMessages.length]);

  useEffect(() => {
    if (autoScroll && autoScrollEnabledRef.current && listContainerRef.current) {
      // 双重 rAF 确保浏览器完成 paint 后再滚动，避免 scrollHeight 未更新
      const id = requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          if (listContainerRef.current && autoScrollEnabledRef.current) {
            listContainerRef.current.scrollTop = listContainerRef.current.scrollHeight;
          }
        });
      });
      return () => cancelAnimationFrame(id);
    }
  }, [filteredMessages, autoScroll, containerHeight]);

  useEffect(() => {
    const el = listContainerRef.current;
    if (!el) return;
    setContainerHeight(el.clientHeight || 300);
    const ro = new ResizeObserver(entries => {
      for (const entry of entries) {
        const h = entry.contentRect.height;
        if (h > 0) setContainerHeight(h);
      }
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const handleScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    const container = e.currentTarget;
    setScrollTop(container.scrollTop);
    const atBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 50;
    autoScrollEnabledRef.current = atBottom;
    if (atBottom) setNewMessageCount(0);
  }, []);

  const toggleAutoScroll = useCallback(() => {
    const next = !autoScroll;
    setAutoScroll(next);
    if (next) {
      autoScrollEnabledRef.current = true;
      setNewMessageCount(0);
      if (listContainerRef.current) listContainerRef.current.scrollTop = listContainerRef.current.scrollHeight;
    } else {
      autoScrollEnabledRef.current = false;
    }
  }, [autoScroll]);

  const totalHeight = filteredMessages.length * ITEM_HEIGHT;
  const startIndex = Math.max(0, Math.floor(scrollTop / ITEM_HEIGHT) - OVERSCAN);
  const endIndex = Math.min(filteredMessages.length, Math.ceil((scrollTop + containerHeight) / ITEM_HEIGHT) + OVERSCAN);
  const visibleMessages = filteredMessages.slice(startIndex, endIndex);
  const offsetY = startIndex * ITEM_HEIGHT;

  const renderMessage = useCallback((msg: DanmakuMessage) => {
    const timeStr = formatTime(msg.timestamp);

    switch (msg.type) {
      case 'danmaku': {
        const contentEl = (
          <span className="dm-content" style={msg.color ? { color: `#${msg.color.toString(16).padStart(6, '0')}` } : undefined}>
            {msg.content}
          </span>
        );
        return (
          <span className="dm-line dm-danmaku">
            <span className="dm-time">{timeStr}</span>
            {msg.username && (
              <>
                <span className="dm-username">{msg.username}</span>
                <span className="dm-colon">: </span>
              </>
            )}
            {msg.content.length > 20 ? (
              <Tooltip title={msg.content} placement="topLeft" overlayClassName="dm-tooltip">
                {contentEl}
              </Tooltip>
            ) : contentEl}
          </span>
        );
      }
      case 'gift': {
        const priceText = formatGiftPrice(msg);
        return (
          <span className="dm-line dm-gift">
            <span className="dm-time">{timeStr}</span>
            {msg.username && <span className="dm-username">{msg.username}</span>}
            <span> 赠送 </span>
            <span className="dm-price-badge gift-price">{priceText ? `${priceText} ` : ''}{msg.gift_name || ''} x{msg.num}</span>
          </span>
        );
      }
      case 'super_chat':
        return (
          <span className="dm-line dm-super-chat">
            <span className="dm-time">{timeStr}</span>
            {msg.username && <span className="dm-username">{msg.username}</span>}
            <span> </span>
            <span className="dm-price-badge sc-price">SC ¥{msg.price} {msg.content}</span>
          </span>
        );
      case 'guard': {
        const guardPrice = formatGuardPrice(msg);
        return (
          <span className="dm-line dm-guard">
            <span className="dm-time">{timeStr}</span>
            {msg.username && <span className="dm-username">{msg.username}</span>}
            <span> 开通了</span>
            <span className="dm-price-badge guard-price">{msg.gift_name} {guardPrice}</span>
          </span>
        );
      }
      default:
        return (
          <span className="dm-line">
            <span className="dm-time">{timeStr}</span>
            <span className="dm-username">{msg.username}</span>
            <span>: {msg.content}</span>
          </span>
        );
    }
  }, []);

  const isAllFiltered = activeFilters.size === FILTER_TYPES.length;

  return (
    <div className="dm-panel">
      {/* 顶部栏：筛选 + 统计 */}
      <div className="dm-top-bar">
        {/* 筛选按钮组 */}
        <div className="dm-filter-group">
          {FILTER_TYPES.map(f => {
            const isActive = activeFilters.has(f.key);
            return (
              <div
                key={f.key}
                className={`dm-filter-chip ${isActive ? 'active' : ''}`}
                style={isActive ? { '--chip-color': f.color } as React.CSSProperties : undefined}
                onClick={() => toggleFilter(f.key)}
              >
                <span className="dm-filter-checkbox">
                  {isActive && (
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
                  )}
                </span>
                <span className="dm-filter-label">{f.label}</span>
              </div>
            );
          })}
        </div>

        {/* 统计 + 操作 */}
        <div className="dm-actions">
          <span className="dm-stat-count">
            {isAllFiltered ? '共' : '显示'} <strong>{stats.totalCount}</strong> 条
          </span>
          {stats.totalAmount > 0 && (
            <span className="dm-stat-amount">¥{stats.totalAmount.toFixed(1)}</span>
          )}
          <div className={`dm-scroll-toggle ${autoScroll ? 'on' : 'off'}`} onClick={toggleAutoScroll}>
            <VerticalAlignBottomOutlined />
            <span>{autoScroll ? '滚动中' : '已暂停'}</span>
          </div>
        </div>
      </div>

      {/* 弹幕列表 */}
      <div ref={listContainerRef} className="dm-list-container" onScroll={handleScroll}>
        <div style={{ height: totalHeight, position: 'relative' }}>
          <div style={{ transform: `translateY(${offsetY}px)` }}>
            {visibleMessages.map((msg, idx) => (
              <div key={startIndex + idx} className="dm-item" style={{ height: ITEM_HEIGHT }}>
                {renderMessage(msg)}
              </div>
            ))}
          </div>
        </div>
        {filteredMessages.length === 0 && (
          <div className="dm-empty">
            {displayMessages.length === 0 ? '暂无弹幕' : '当前筛选无匹配弹幕'}
          </div>
        )}
        {newMessageCount > 0 && (
          <div className="dm-new-message-hint" onClick={() => {
            setNewMessageCount(0);
            setAutoScroll(true);
            autoScrollEnabledRef.current = true;
            if (listContainerRef.current) listContainerRef.current.scrollTop = listContainerRef.current.scrollHeight;
          }}>
            ↓ {newMessageCount > 99 ? '99+' : newMessageCount} 条新消息
          </div>
        )}
      </div>

      {/* 状态栏 */}
      <div className="dm-status-bar">
        <span>
          {isAllFiltered ? `共 ${displayMessages.length} 条` : `已筛选 ${filteredMessages.length}/${displayMessages.length} 条`}
        </span>
        {autoScroll && (
          <span className="dm-autoscroll-hint">自动滚动中</span>
        )}
      </div>
    </div>
  );
};

export default DanmakuPanel;
