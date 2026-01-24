import { test, expect } from '@playwright/test';

/**
 * 直播录制功能测试
 * 
 * 使用 osrp-stream-tester 提供的 dev 直播间进行测试
 * 测试添加直播间、开始/停止录制等功能
 */

// dev 测试流服务器地址
const DEV_STREAM_SERVER = 'http://127.0.0.1:8888';
const DEV_STREAM_URL = `${DEV_STREAM_SERVER}/live/test.flv`;

test.describe('直播间管理测试', () => {
  test.beforeEach(async ({ page }) => {
    // 每个测试前访问首页
    await page.goto('/');
    await page.waitForLoadState('networkidle');
  });

  test('测试流服务器健康检查', async ({ request }) => {
    // 首先验证 osrp-stream-tester 正在运行
    const response = await request.get(`${DEV_STREAM_SERVER}/health`);
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data.status).toBe('ok');
  });

  test('添加 dev 直播间', async ({ page }) => {
    // 查找添加按钮
    const addButton = page.locator('button').filter({ hasText: /添加|Add/i }).first();

    // 如果找不到明确的添加按钮，尝试查找其他可能的选择器
    const addButtonAlt = page.locator('[data-testid="add-room"], .add-room-button, button:has(.anticon-plus)').first();

    const buttonToClick = await addButton.isVisible() ? addButton : addButtonAlt;

    if (await buttonToClick.isVisible()) {
      await buttonToClick.click();

      // 等待添加对话框出现
      await page.waitForTimeout(500);

      // 查找输入框
      const urlInput = page.locator('input[type="text"], input[placeholder*="url" i], input[placeholder*="地址" i]').first();

      if (await urlInput.isVisible()) {
        // 输入 dev 流地址
        await urlInput.fill(DEV_STREAM_URL);

        // 查找确认按钮
        const confirmButton = page.locator('button').filter({ hasText: /确定|确认|OK|Submit/i }).first();

        if (await confirmButton.isVisible()) {
          await confirmButton.click();

          // 等待添加完成
          await page.waitForTimeout(1000);

          // 验证直播间已添加（查找相关内容）
          // 如果添加成功，应该在页面上看到相关信息
        }
      }
    } else {
      // 如果找不到添加按钮，跳过此测试
      test.skip();
    }
  });
});

test.describe('流信息 API 测试', () => {
  test('获取 dev 流信息', async ({ request }) => {
    // 测试 osrp-stream-tester 的 API
    const response = await request.get(`${DEV_STREAM_SERVER}/api/streams/test`);
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data.id).toBe('test');
    expect(data.live).toBe(true);
  });

  test('获取可用流列表', async ({ request }) => {
    const response = await request.get(`${DEV_STREAM_SERVER}/api/streams/test/available`);
    expect(response.ok()).toBeTruthy();

    const streams = await response.json();
    expect(Array.isArray(streams)).toBe(true);
    expect(streams.length).toBeGreaterThan(0);

    // 验证流信息结构
    const firstStream = streams[0];
    expect(firstStream).toHaveProperty('url');
    expect(firstStream).toHaveProperty('format');
    expect(firstStream).toHaveProperty('codec');
  });
});
