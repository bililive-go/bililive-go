// IO 统计相关的共享类型定义

export interface IOStat {
  id?: number;
  timestamp: number;
  stat_type: string;
  live_id?: string;
  platform?: string;
  speed: number;
  total_bytes?: number;
}

export interface RequestStatusSegment {
  start_time: number;
  end_time: number;
  success: boolean;
  count: number;
}

export interface RequestStatusResponse {
  segments: RequestStatusSegment[];
  grouped_segments?: Record<string, RequestStatusSegment[]>;
}
