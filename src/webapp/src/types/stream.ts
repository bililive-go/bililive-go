// 流属性类型
export interface StreamAttributes {
  [key: string]: string;
}

// 可用流信息（扩展现有类型）
export interface AvailableStream {
  format: string;
  quality: string;
  quality_name?: string;
  description?: string;
  width?: number;
  height?: number;
  bitrate?: number;
  frame_rate?: number;
  codec?: string;
  audio_codec?: string;
  attributes_for_stream_select?: StreamAttributes; // 用于流选择的属性
}

// 流偏好（用于 API 请求）
export interface StreamPreferenceV2 {
  quality?: string;
  attributes?: StreamAttributes;
}
