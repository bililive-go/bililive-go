import { comparePinnedForTable } from './pinning';

describe('comparePinnedForTable', () => {
    const pinned = { pinned: true };
    const normal = { pinned: false };

    test('升序时直接将置顶项排在前面', () => {
        expect(comparePinnedForTable(pinned, normal, 'ascend', () => 0)).toBeLessThan(0);
        expect(comparePinnedForTable(normal, pinned, 'ascend', () => 0)).toBeGreaterThan(0);
    });

    test('降序时预先反转结果以抵消 Table 的反转', () => {
        expect(comparePinnedForTable(pinned, normal, 'descend', () => 0)).toBeGreaterThan(0);
        expect(comparePinnedForTable(normal, pinned, 'descend', () => 0)).toBeLessThan(0);
    });

    test('置顶状态相同时使用原字段比较结果', () => {
        expect(comparePinnedForTable(pinned, pinned, 'ascend', () => 3)).toBe(3);
        expect(comparePinnedForTable(normal, normal, 'descend', () => -2)).toBe(-2);
    });
});
