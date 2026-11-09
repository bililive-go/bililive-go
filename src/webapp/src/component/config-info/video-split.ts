// 与后端一致，三个字段作为一组覆盖；表单使用秒，API 使用纳秒。
export function videoSplitFormValues(own: any, inherited: any) {
  const value = own ?? inherited;
  return {
    video_split_override: own != null,
    video_split_strategies: {
      on_room_name_changed: value?.on_room_name_changed ?? false,
      max_duration: (value?.max_duration ?? 0) / 1e9,
      max_file_size: value?.max_file_size ?? '0',
    },
  };
}

export function videoSplitUpdates(values: any) {
  const { video_split_override, ...updates } = values;
  return {
    ...updates,
    video_split_strategies: video_split_override ? {
      ...values.video_split_strategies,
      max_duration: Math.round((values.video_split_strategies?.max_duration ?? 0) * 1e9),
    } : null,
  };
}


// 裸字节数必须为整数，带单位时允许小数，与 ParseByteSize 保持一致。
export const videoSplitFileSizePattern = /^\s*(?:\d+|\d+(?:\.\d+)?\s*(?:B|KB|MB|GB|TB))\s*$/i;

export function validateVideoSplitDuration(_: unknown, value: number | null) {
  if (value != null && (value < 0 || (value > 0 && value < 60))) {
    return Promise.reject(new Error('时长应为 0（不限）或至少 60 秒'));
  }
  return Promise.resolve();
}

export const videoSplitFileSizeRules = [
  { required: true, message: '请输入有效的文件大小' },
  { pattern: videoSplitFileSizePattern, message: '请输入有效的文件大小' },
];
