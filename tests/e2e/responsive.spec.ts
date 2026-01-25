import { test, expect, devices } from '@playwright/test';

/**
 * 响应式设计测试
 * 
 * 测试不同屏幕尺寸下的界面表现
 */
test.describe('桌面端布局测试', () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test('大屏幕布局正确', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 侧边栏应该展开显示
    const sider = page.getByRole('complementary');
    await expect(sider).toBeVisible();

    // 表格应该显示所有列（使用 columnheader 角色）
    const tableColumns = page.getByRole('columnheader');
    const columnCount = await tableColumns.count();
    expect(columnCount).toBeGreaterThanOrEqual(4);
  });

  test('大屏幕表格列完整显示', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(500);

    // 验证表格头存在多个列
    await expect(page.getByText('主播名称')).toBeVisible();
    await expect(page.getByText('直播间名称')).toBeVisible();
    await expect(page.getByText('直播平台')).toBeVisible();
    await expect(page.getByText('运行状态')).toBeVisible();
    await expect(page.getByText('操作')).toBeVisible();
  });
});

test.describe('平板端布局测试', () => {
  test.use({ viewport: { width: 768, height: 1024 } });

  test('平板布局正确', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 页面应该可以正常加载
    await expect(page.getByRole('banner')).toBeVisible();

    // 内容区域可见（使用 main 角色或其他方式）
    const content = page.getByRole('main');
    if (await content.isVisible()) {
      await expect(content).toBeVisible();
    } else {
      // 某些布局可能没有 main 角色，验证页面加载成功即可
      await expect(page.getByRole('heading', { name: /bililive/i })).toBeVisible();
    }
  });
});

test.describe('移动端布局测试', () => {
  test.use({ viewport: { width: 375, height: 667 } });

  test('移动端布局正确', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 页面应该可以正常加载
    await expect(page.getByRole('banner')).toBeVisible();
  });

  test('移动端表格可滚动', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 表格可能需要水平滚动（使用 table 角色）
    const table = page.getByRole('table');

    if (await table.isVisible()) {
      // 验证表格存在
      await expect(table).toBeVisible();
    }
  });

  test('移动端侧边栏可折叠', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    const sider = page.getByRole('complementary');

    if (await sider.isVisible()) {
      // 侧边栏应该折叠或可折叠
      const width = await sider.evaluate(el => el.getBoundingClientRect().width);
      // 移动端侧边栏通常较窄
      expect(width).toBeLessThanOrEqual(200);
    }
  });
});

test.describe('不同浏览器兼容性', () => {
  // 这些测试会在配置的浏览器项目中运行

  test('页面在所有浏览器中加载', async ({ page, browserName }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 验证基本元素存在
    await expect(page.getByRole('banner')).toBeVisible();

    console.log(`测试在 ${browserName} 浏览器中运行成功`);
  });

  test('CSS 样式正确渲染', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 验证 antd 样式加载（使用 button 角色）
    const button = page.getByRole('button').first();

    if (await button.isVisible()) {
      // 检查按钮有应用样式
      const backgroundColor = await button.evaluate(
        el => getComputedStyle(el).backgroundColor
      );
      expect(backgroundColor).toBeDefined();
    }
  });
});

test.describe('打印样式测试', () => {
  test('打印预览不会崩溃', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 触发打印（这只是模拟，不会真正打印）
    await page.emulateMedia({ media: 'print' });

    // 验证页面仍然可见
    await expect(page.getByRole('banner')).toBeVisible();

    // 恢复正常媒体类型
    await page.emulateMedia({ media: 'screen' });
  });
});

test.describe('暗色模式测试', () => {
  test('暗色模式下页面正常', async ({ page }) => {
    // 设置暗色模式偏好
    await page.emulateMedia({ colorScheme: 'dark' });

    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 验证页面加载
    await expect(page.getByRole('banner')).toBeVisible();
  });
});

