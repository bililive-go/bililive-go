import { test, expect, Page } from '@playwright/test';

/**
 * 导航功能测试
 * 
 * 测试侧边栏导航和页面切换
 */
test.describe('导航功能测试', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
  });

  test('侧边栏包含所有菜单项', async ({ page }) => {
    // 验证侧边栏存在
    const sider = page.getByRole('complementary');
    await expect(sider).toBeVisible();

    // 验证菜单项（使用 menuitem 角色）
    const menuItems = page.getByRole('menuitem');
    await expect(menuItems).toHaveCount(8); // 8个菜单项

    // 验证各个菜单项文本
    await expect(page.getByRole('menuitem', { name: /监控列表/ })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: /系统状态/ })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: /设置/ })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: /文件/ })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: /工具/ })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: /任务队列/ })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: /IO 统计/ })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: /更新/ })).toBeVisible();
  });

  test('点击导航到系统状态页面', async ({ page }) => {
    await page.getByRole('menuitem', { name: /系统状态/ }).click();
    await page.waitForTimeout(500);

    // 验证 URL 包含 liveInfo
    expect(page.url()).toContain('liveInfo');

    // 验证页面内容加载
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('点击导航到设置页面', async ({ page }) => {
    await page.getByRole('menuitem', { name: /设置/ }).click();
    await page.waitForTimeout(500);

    // 验证 URL 包含 configInfo
    expect(page.url()).toContain('configInfo');

    // 验证页面内容加载
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('点击导航到文件页面', async ({ page }) => {
    await page.getByRole('menuitem', { name: /文件/ }).click();
    await page.waitForTimeout(500);

    // 验证 URL 包含 fileList
    expect(page.url()).toContain('fileList');

    // 验证页面内容加载
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('点击导航到任务队列页面', async ({ page }) => {
    await page.getByRole('menuitem', { name: /任务队列/ }).click();
    await page.waitForTimeout(500);

    // 验证 URL 包含 tasks
    expect(page.url()).toContain('tasks');

    // 验证页面内容加载
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('点击导航到IO统计页面', async ({ page }) => {
    await page.getByRole('menuitem', { name: /IO 统计/ }).click();
    await page.waitForTimeout(500);

    // 验证 URL 包含 iostats
    expect(page.url()).toContain('iostats');

    // 验证页面内容加载
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('点击导航到更新页面', async ({ page }) => {
    await page.getByRole('menuitem', { name: /更新/ }).click();
    await page.waitForTimeout(500);

    // 验证 URL 包含 update
    expect(page.url()).toContain('update');

    // 验证页面内容加载
    await expect(page.getByRole('main')).toBeVisible();
  });

  test('侧边栏折叠/展开功能', async ({ page }) => {
    // 找到折叠按钮（通过 button 角色和名称）
    const collapseButton = page.getByRole('button', { name: /收起菜单|menu-fold/ });

    // 获取初始宽度
    const sider = page.getByRole('complementary');
    const initialWidth = await sider.evaluate(el => el.getBoundingClientRect().width);

    // 点击折叠
    await collapseButton.click();
    await page.waitForTimeout(300); // 等待动画

    // 验证宽度变小
    const collapsedWidth = await sider.evaluate(el => el.getBoundingClientRect().width);
    expect(collapsedWidth).toBeLessThan(initialWidth);

    // 找到展开按钮
    const expandButton = page.getByRole('button', { name: /menu-unfold/ });
    await expandButton.click();
    await page.waitForTimeout(300);

    // 验证宽度恢复
    const expandedWidth = await sider.evaluate(el => el.getBoundingClientRect().width);
    expect(expandedWidth).toBeGreaterThan(collapsedWidth);
  });
});
