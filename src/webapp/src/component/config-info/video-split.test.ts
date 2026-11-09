import Schema from '@rc-component/async-validator';
import { videoSplitFormValues, videoSplitUpdates, videoSplitFileSizePattern, videoSplitFileSizeRules, validateVideoSplitDuration } from './video-split';

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


test('小数秒转换为整数纳秒并可往返', () => {
  const values = videoSplitFormValues(inherited, undefined);
  values.video_split_strategies.max_duration = 64.001;
  const updates = videoSplitUpdates(values);
  expect(updates.video_split_strategies.max_duration).toBe(64001000000);
  expect(Number.isInteger(updates.video_split_strategies.max_duration)).toBe(true);
  expect(videoSplitFormValues(updates.video_split_strategies, undefined).video_split_strategies.max_duration).toBe(64.001);
});

test.each(['0', '123', '1.5GB', ' 500 MB ', '1.5B'])('文件大小接受后端支持的格式 %s', value => {
  expect(videoSplitFileSizePattern.test(value)).toBe(true);
});

test.each(['1.5', '1GiB', '-1', '1.5 GBs', ''])('文件大小拒绝无效格式 %s', value => {
  expect(videoSplitFileSizePattern.test(value)).toBe(false);
});


test.each([0, 60, 64.001])('时长允许不限或至少 60 秒：%s', async value => {
  await expect(validateVideoSplitDuration(undefined, value)).resolves.toBeUndefined();
});

test.each([-1, 0.1, 59])('时长拒绝负数或小于 60 秒：%s', async value => {
  await expect(validateVideoSplitDuration(undefined, value)).rejects.toThrow();
});

test('表单实际校验拒绝空串及小数字节数，允许带单位的小数', async () => {
  const validator = new Schema({ max_file_size: videoSplitFileSizeRules });
  await expect(validator.validate({ max_file_size: '' })).rejects.toBeDefined();
  await expect(validator.validate({ max_file_size: '1.5' })).rejects.toBeDefined();
  await expect(validator.validate({ max_file_size: '1.5GB' })).resolves.toBeDefined();
});
