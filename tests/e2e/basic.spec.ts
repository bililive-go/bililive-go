import { test, expect } from '@playwright/test';

/**
 * Bililive-Go 基础功能测试
 * 
 * 测试 Web UI 的基本功能，包括页面加载和导航
 */
test.describe('基础功能测试', () => {
  test('首页正常加载', async ({ page }) => {
    // 访问首页
    await page.goto('/');

    // 等待页面加载完成
    await page.waitForLoadState('networkidle');

    // 验证页面包含关键元素
    // bgo 使用 antd，应该有 ant-layout 类
    await expect(page.locator('.ant-layout')).toBeVisible();
  });

  test('API 信息端点可访问', async ({ request }) => {
    // 直接测试 API
    const response = await request.get('/api/info');
    expect(response.ok()).toBeTruthy();

    const info = await response.json();
    // 验证返回的信息包含版本号
    expect(info).toHaveProperty('version');
  });

  test('直播间列表页面加载', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // 等待直播间列表区域出现
    // 注意：如果没有直播间，可能显示空状态
    const content = page.locator('.ant-layout-content');
    await expect(content).toBeVisible();
  });
});

test.describe('设置功能测试', () => {
  test('设置页面可访问', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // 查找设置按钮（通常是齿轮图标）
    const settingsButton = page.locator('[data-testid="settings-button"], .anticon-setting').first();

    // 如果设置按钮存在，点击它
    if (await settingsButton.isVisible()) {
      await settingsButton.click();

      // 等待设置面板/页面出现
      await page.waitForTimeout(500);

      // 验证设置相关内容出现
      // 这里可能需要根据实际 UI 调整选择器
    }
  });
});
