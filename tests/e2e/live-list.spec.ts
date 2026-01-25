import { test, expect } from '@playwright/test';

/**
 * 监控列表页面测试
 * 
 * 测试主页的直播间列表功能
 */
test.describe('监控列表页面测试', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
  });

  test('页面标题和头部正确显示', async ({ page }) => {
    // 验证 logo 文本
    await expect(page.getByText('Bililive-go')).toBeVisible();

    // 验证页面布局
    await expect(page.getByRole('banner')).toBeVisible();
    await expect(page.getByRole('banner')).toBeVisible();
  });

  test('直播间列表表格存在', async ({ page }) => {
    // 验证表格存在
    const table = page.locator('.ant-table');
    await expect(table).toBeVisible();

    // 验证表格头
    const tableHeader = page.locator('.ant-table-thead');
    await expect(tableHeader).toBeVisible();
  });

  test('表格包含必要的列', async ({ page }) => {
    // 等待表格加载
    await page.waitForSelector('.ant-table-thead');

    // 验证列标题（可能在大屏幕或小屏幕模式下不同）
    const headerCells = page.locator('.ant-table-thead th');
    const headerCount = await headerCells.count();
    expect(headerCount).toBeGreaterThanOrEqual(3);
  });

  test('Tabs 标签页切换', async ({ page }) => {
    // 如果有标签页，测试切换
    const tabs = page.locator('.ant-tabs');

    if (await tabs.isVisible()) {
      const tabItems = page.locator('.ant-tabs-tab');
      const tabCount = await tabItems.count();

      if (tabCount > 1) {
        // 点击第二个标签
        await tabItems.nth(1).click();
        await page.waitForTimeout(300);

        // 验证标签激活状态改变
        await expect(tabItems.nth(1)).toHaveClass(/ant-tabs-tab-active/);
      }
    }
  });

  test('添加直播间按钮存在', async ({ page }) => {
    // 查找添加按钮（可能在不同位置）
    const addButton = page.locator('button').filter({ hasText: /添加|新增|Add/i });

    // 如果找到添加按钮，验证其可见性
    if (await addButton.first().isVisible()) {
      await expect(addButton.first()).toBeEnabled();
    }
  });

  test('表格支持排序', async ({ page }) => {
    // 等待表格加载
    await page.waitForSelector('.ant-table-thead');

    // 找到可排序的列标题
    const sortableHeader = page.locator('.ant-table-column-sorters').first();

    if (await sortableHeader.isVisible()) {
      // 点击排序
      await sortableHeader.click();
      await page.waitForTimeout(300);

      // 验证排序图标出现
      const sorterIcon = page.locator('.ant-table-column-sorter-up, .ant-table-column-sorter-down');
      await expect(sorterIcon.first()).toBeVisible();
    }
  });
});

test.describe('添加直播间对话框测试', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
  });

  test('点击添加按钮显示对话框', async ({ page }) => {
    // 查找添加按钮
    const addButton = page.locator('button').filter({ hasText: /添加|新增/i }).first();

    if (await addButton.isVisible()) {
      await addButton.click();

      // 等待对话框出现
      await page.waitForTimeout(500);

      // 验证对话框存在
      const modal = page.locator('.ant-modal');
      await expect(modal).toBeVisible();

      // 验证对话框标题
      await expect(page.getByText('添加直播间')).toBeVisible();

      // 验证输入框存在
      const input = modal.locator('input');
      await expect(input).toBeVisible();

      // 关闭对话框
      await page.locator('.ant-modal-close').click();
      await page.waitForTimeout(300);
    }
  });

  test('对话框取消按钮可以关闭', async ({ page }) => {
    const addButton = page.locator('button').filter({ hasText: /添加|新增/i }).first();

    if (await addButton.isVisible()) {
      await addButton.click();
      await page.waitForTimeout(500);

      // 点击取消按钮
      await page.getByRole('button', { name: /取消|Cancel/i }).click();
      await page.waitForTimeout(300);

      // 验证对话框关闭
      await expect(page.locator('.ant-modal')).not.toBeVisible();
    }
  });

  test('输入框可以输入URL', async ({ page }) => {
    const addButton = page.locator('button').filter({ hasText: /添加|新增/i }).first();

    if (await addButton.isVisible()) {
      await addButton.click();
      await page.waitForTimeout(500);

      const input = page.locator('.ant-modal input');
      await input.fill('https://live.bilibili.com/123456');

      // 验证输入值
      await expect(input).toHaveValue('https://live.bilibili.com/123456');

      // 关闭对话框
      await page.locator('.ant-modal-close').click();
    }
  });
});

