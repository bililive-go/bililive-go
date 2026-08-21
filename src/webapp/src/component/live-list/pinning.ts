export type TableSortOrder = 'ascend' | 'descend' | null | undefined;

interface Pinnable {
    pinned: boolean;
}

/**
 * 为 Ant Design Table 的比较器增加“置顶优先”规则。
 *
 * Table 会在降序时反转比较器的返回值，因此这里也要预先反转置顶结果，
 * 才能保证无论用户选择升序还是降序，置顶项始终位于列表顶部。
 */
export function comparePinnedForTable<T extends Pinnable>(
    a: T,
    b: T,
    sortOrder: TableSortOrder,
    compareValues: () => number
): number {
    if (a.pinned !== b.pinned) {
        const pinnedFirst = a.pinned ? -1 : 1;
        return sortOrder === 'descend' ? -pinnedFirst : pinnedFirst;
    }
    return compareValues();
}
