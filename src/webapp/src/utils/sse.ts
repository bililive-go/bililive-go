/**
 * SSE 连接管理器
 * 所有页面共享一个 SSE 连接，使用发布-订阅模式分发事件
 */

// SSE 事件类型
export type SSEEventType = 'live_update' | 'log' | 'conn_stats' | 'recorder_status' | 'connected' | 'list_change' | 'rate_limit_update';

// SSE 消息结构
export interface SSEMessage {
  type: SSEEventType;
  room_id: string;
  data: any;
}

// 事件回调类型
export type SSECallback = (message: SSEMessage) => void;

// 订阅信息
interface Subscription {
  roomId: string;
  eventType: SSEEventType | '*'; // '*' 表示订阅所有事件类型
  callback: SSECallback;
}

class SSEManager {
  private static instance: SSEManager;
  private eventSource: EventSource | null = null;
  private subscriptions: Map<string, Subscription> = new Map();
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 10;
  private reconnectDelay = 1000; // 初始重连延迟 1 秒
  private isConnecting = false;
  private baseUrl: string;

  private constructor() {
    // 根据当前页面 URL 构建 API 地址
    this.baseUrl = window.location.origin;
  }

  public static getInstance(): SSEManager {
    if (!SSEManager.instance) {
      SSEManager.instance = new SSEManager();
    }
    return SSEManager.instance;
  }

  /**
   * 连接到 SSE 端点
   */
  private connect(): void {
    if (this.eventSource || this.isConnecting) {
      return;
    }

    this.isConnecting = true;
    console.log('[SSE] Connecting...');

    try {
      this.eventSource = new EventSource(`${this.baseUrl}/api/sse`);

      this.eventSource.onopen = () => {
        console.log('[SSE] Connected');
        this.reconnectAttempts = 0;
        this.reconnectDelay = 1000;
        this.isConnecting = false;
      };

      this.eventSource.onerror = (error) => {
        console.error('[SSE] Connection error:', error);
        this.handleDisconnect();
      };

      // 监听 connected 事件
      this.eventSource.addEventListener('connected', (event: MessageEvent) => {
        console.log('[SSE] Received connected event:', event.data);
      });

      // 监听 live_update 事件
      this.eventSource.addEventListener('live_update', (event: MessageEvent) => {
        this.handleMessage('live_update', event.data);
      });

      // 监听 log 事件
      this.eventSource.addEventListener('log', (event: MessageEvent) => {
        this.handleMessage('log', event.data);
      });

      // 监听 conn_stats 事件
      this.eventSource.addEventListener('conn_stats', (event: MessageEvent) => {
        this.handleMessage('conn_stats', event.data);
      });

      // 监听 recorder_status 事件
      this.eventSource.addEventListener('recorder_status', (event: MessageEvent) => {
        this.handleMessage('recorder_status', event.data);
      });

      // 监听 list_change 事件（直播间增删、监控开关等）
      this.eventSource.addEventListener('list_change', (event: MessageEvent) => {
        this.handleMessage('list_change', event.data);
      });

      // 监听 rate_limit_update 事件（强制刷新后更新频率限制信息）
      this.eventSource.addEventListener('rate_limit_update', (event: MessageEvent) => {
        this.handleMessage('rate_limit_update', event.data);
      });

    } catch (error) {
      console.error('[SSE] Failed to create EventSource:', error);
      this.isConnecting = false;
      this.scheduleReconnect();
    }
  }

  /**
   * 处理收到的消息
   */
  private handleMessage(type: SSEEventType, data: string): void {
    try {
      const message: SSEMessage = JSON.parse(data);
      // 分发给所有匹配的订阅者
      this.subscriptions.forEach((sub) => {
        // 检查房间 ID 是否匹配（'*' 表示订阅所有房间）
        const roomMatches = sub.roomId === '*' || sub.roomId === message.room_id;
        // 检查事件类型是否匹配（'*' 表示订阅所有事件类型）
        const typeMatches = sub.eventType === '*' || sub.eventType === type;

        if (roomMatches && typeMatches) {
          try {
            sub.callback(message);
          } catch (err) {
            console.error('[SSE] Error in callback:', err);
          }
        }
      });
    } catch (error) {
      console.error('[SSE] Failed to parse message:', error, data);
    }
  }

  /**
   * 处理断开连接
   */
  private handleDisconnect(): void {
    this.isConnecting = false;
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }

    // 如果还有订阅者，尝试重连
    if (this.subscriptions.size > 0) {
      this.scheduleReconnect();
    }
  }

  /**
   * 安排重连
   */
  private scheduleReconnect(): void {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      console.error('[SSE] Max reconnect attempts reached');
      return;
    }

    this.reconnectAttempts++;
    const delay = Math.min(this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1), 30000);
    console.log(`[SSE] Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts}/${this.maxReconnectAttempts})`);

    setTimeout(() => {
      if (this.subscriptions.size > 0) {
        this.connect();
      }
    }, delay);
  }

  /**
   * 订阅事件
   * @param roomId 房间 ID，'*' 表示订阅所有房间
   * @param eventType 事件类型，'*' 表示订阅所有事件类型
   * @param callback 回调函数
   * @returns 订阅 ID，用于取消订阅
   */
  public subscribe(
    roomId: string,
    eventType: SSEEventType | '*',
    callback: SSECallback
  ): string {
    const subscriptionId = `${roomId}_${eventType}_${Date.now()}_${Math.random().toString(36).substring(2, 11)}`;

    this.subscriptions.set(subscriptionId, {
      roomId,
      eventType,
      callback,
    });

    // 如果这是第一个订阅者，建立连接
    if (this.subscriptions.size === 1 && !this.eventSource && !this.isConnecting) {
      this.connect();
    }

    console.log(`[SSE] Subscribed: ${subscriptionId}, total subscribers: ${this.subscriptions.size}`);
    return subscriptionId;
  }

  /**
   * 取消订阅
   * @param subscriptionId 订阅 ID
   */
  public unsubscribe(subscriptionId: string): void {
    this.subscriptions.delete(subscriptionId);
    console.log(`[SSE] Unsubscribed: ${subscriptionId}, total subscribers: ${this.subscriptions.size}`);

    // 如果没有订阅者了，关闭连接
    if (this.subscriptions.size === 0 && this.eventSource) {
      console.log('[SSE] No subscribers, closing connection');
      this.eventSource.close();
      this.eventSource = null;
      this.reconnectAttempts = 0;
    }
  }

  /**
   * 获取连接状态
   */
  public isConnected(): boolean {
    return this.eventSource !== null && this.eventSource.readyState === EventSource.OPEN;
  }

  /**
   * 获取订阅者数量
   */
  public getSubscriberCount(): number {
    return this.subscriptions.size;
  }

  /**
   * 强制重连
   */
  public reconnect(): void {
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
    this.reconnectAttempts = 0;
    if (this.subscriptions.size > 0) {
      this.connect();
    }
  }
}

// 导出单例实例
export const sseManager = SSEManager.getInstance();

// 导出便捷方法
export const subscribeSSE = (
  roomId: string,
  eventType: SSEEventType | '*',
  callback: SSECallback
): string => sseManager.subscribe(roomId, eventType, callback);

export const unsubscribeSSE = (subscriptionId: string): void => sseManager.unsubscribe(subscriptionId);
