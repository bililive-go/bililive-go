import { videoSplitFormValues, videoSplitUpdates } from './video-split';

const inherited = { on_room_name_changed: true, max_duration: 600e9, max_file_size: '1GB' };

test('未配置时显示上级策略，保存为 null 恢复继承', () => {
  const values = videoSplitFormValues(undefined, inherited);
  expect(values.video_split_override).toBe(false);
  expect(values.video_split_strategies.max_duration).toBe(600);
  expect(videoSplitUpdates(values)).toEqual({ video_split_strategies: null });
});

test('房间覆盖可显式禁用所有策略，秒数能往返转换', () => {
  const disabled = { on_room_name_changed: false, max_duration: 0, max_file_size: '0' };
  expect(videoSplitUpdates(videoSplitFormValues(disabled, inherited)))
    .toEqual({ video_split_strategies: disabled });
  expect(videoSplitUpdates(videoSplitFormValues(inherited, disabled)))
    .toEqual({ video_split_strategies: inherited });
});

test('清除覆盖保留其它房间设置，不发送表单辅助字段', () => {
  const values = { ...videoSplitFormValues(inherited, undefined), video_split_override: false, nick_name: '主播' };
  expect(videoSplitUpdates(values)).toEqual({ video_split_strategies: null, nick_name: '主播' });
});
