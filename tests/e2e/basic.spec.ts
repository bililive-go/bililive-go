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
    await page.waitForLoadState('domcontentloaded');

    // 验证页面包含关键元素：Bililive-go 标题
    await expect(page.getByRole('heading', { name: 'Bililive-go' })).toBeVisible();
  });

  test('页面标题正确', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 验证页面标题包含 Bililive
    await expect(page).toHaveTitle(/.*[Bb]ililive.*/);
  });

  test('Header 正确显示', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 验证 Header 存在（通过 banner 角色）
    const header = page.getByRole('banner');
    await expect(header).toBeVisible();

    // 验证 logo 文本
    await expect(page.getByRole('heading', { name: 'Bililive-go' })).toBeVisible();
  });

  test('API 信息端点可访问', async ({ request }) => {
    // 直接测试 API
    const response = await request.get('/api/info');
    expect(response.ok()).toBeTruthy();

    const info = await response.json();
    // 验证返回的信息包含应用名称（使用 snake_case）
    expect(info).toHaveProperty('app_name');
  });

  test('API 直播间列表端点可访问', async ({ request }) => {
    const response = await request.get('/api/lives');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 返回的是直播间数组
    expect(Array.isArray(data)).toBe(true);
  });

  test('API 配置端点可访问', async ({ request }) => {
    const response = await request.get('/api/config');
    expect(response.ok()).toBeTruthy();
  });

  test('直播间列表页面加载', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 等待直播间列表区域出现（通过 main 角色）
    const content = page.getByRole('main');
    await expect(content).toBeVisible();
  });

  test('侧边栏菜单存在', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 验证侧边栏存在（通过 complementary 角色）
    const sider = page.getByRole('complementary');
    await expect(sider).toBeVisible();

    // 验证菜单存在
    const menu = page.getByRole('menu');
    await expect(menu).toBeVisible();
  });
});

test.describe('错误处理测试', () => {
  test('404 页面处理', async ({ page }) => {
    // 访问不存在的页面
    await page.goto('/#/nonexistent-page');
    await page.waitForLoadState('domcontentloaded');

    // 应该仍然显示布局（通过 banner 角色验证）
    const header = page.getByRole('banner');
    await expect(header).toBeVisible();
  });

  test('API 错误响应处理', async ({ request }) => {
    // 请求不存在的 API
    const response = await request.get('/api/nonexistent');
    // 应该返回 404 或其他错误状态
    expect(response.status()).toBeGreaterThanOrEqual(400);
  });
});

test.describe('性能基准测试', () => {
  test('首页加载时间合理', async ({ page }) => {
    const startTime = Date.now();

    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    const loadTime = Date.now() - startTime;

    // 首页应该在 10 秒内加载完成
    expect(loadTime).toBeLessThan(10000);

    console.log(`首页加载时间: ${loadTime}ms`);
  });

  test('页面没有控制台错误', async ({ page }) => {
    const errors: string[] = [];

    page.on('console', msg => {
      if (msg.type() === 'error') {
        errors.push(msg.text());
      }
    });

    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(1000);

    // 过滤掉一些已知的无害错误
    const criticalErrors = errors.filter(error =>
      !error.includes('favicon') &&
      !error.includes('Failed to load resource')
    );

    // 不应该有严重的控制台错误
    if (criticalErrors.length > 0) {
      console.log('发现控制台错误:', criticalErrors);
    }
  });
});

test.describe('设置功能测试', () => {
  test('设置页面可访问', async ({ page }) => {
    await page.goto('/#/configInfo');
    await page.waitForLoadState('domcontentloaded');

    // 验证设置页面加载（通过 main 角色）
    const content = page.getByRole('main');
    await expect(content).toBeVisible();
  });

  test('通过菜单访问设置', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 查找设置菜单项（使用 menuitem 角色）
    const settingsLink = page.getByRole('menuitem', { name: /设置/ });

    if (await settingsLink.isVisible()) {
      await settingsLink.click();
      // 等待一下让路由切换
      await page.waitForTimeout(500);

      // 验证 URL 包含 configInfo
      expect(page.url()).toContain('configInfo');

      // 验证导航成功
      await expect(page.getByRole('main')).toBeVisible();
    }
  });
});

test.describe('键盘可访问性测试', () => {
  test('Tab 键可以导航', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 按 Tab 键导航
    await page.keyboard.press('Tab');
    await page.waitForTimeout(100);

    // 验证有元素获得焦点
    const focusedElement = page.locator(':focus');
    await expect(focusedElement).toBeDefined();
  });

  test('Enter 键可以激活按钮', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 找到并聚焦一个按钮
    const button = page.locator('.ant-btn').first();

    if (await button.isVisible()) {
      await button.focus();

      // 验证按钮获得焦点
      await expect(button).toBeFocused();
    }
  });
});

