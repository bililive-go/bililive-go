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
      max_duration: (values.video_split_strategies?.max_duration ?? 0) * 1e9,
    } : null,
  };
}

