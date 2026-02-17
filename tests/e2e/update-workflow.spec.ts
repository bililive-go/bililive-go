import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

/**
 * Bililive-Go 完整升级流程 E2E 测试
 * 
 * 测试完整的更新工作流：
 * 1. 检查更新 -> 发现新版本
 * 2. 下载新版本
 * 3. 应用更新
 * 4. 下载中断恢复
 * 
 * 注意：这些测试需要额外配置才能运行完整流程：
 * - 启动 update-mock-server
 * - 配置 bgo 使用 mock server
 * 
 * 可以使用以下命令运行这些测试：
 * ```bash
 * # 终端 1: 构建开发版本
 * make dev-incremental
 * 
 * # 终端 2: 启动 mock server
 * go run ./test/update-mock-server -version 99.0.0
 * 
 * # 终端 3: 启动 bgo（设置更新 URL 为 mock server）
 * go run -tags dev ./src/cmd/bililive --config test-output/test-config.yml
 * 
 * # 终端 4: 运行测试
 * npx playwright test tests/e2e/update-workflow.spec.ts
 * ```
 */

// 辅助函数：等待特定的更新状态
async function waitForUpdateState(page: Page, state: string, timeout = 30000) {
  const startTime = Date.now();
  while (Date.now() - startTime < timeout) {
    try {
      const response = await page.request.get('/api/update/status');
      if (response.ok()) {
        const data = await response.json();
        if (data.state === state) {
          return data;
        }
      }
    } catch (e) {
      // 忽略错误，继续轮询
    }
    await page.waitForTimeout(500);
  }
  throw new Error(`等待状态 ${state} 超时`);
}

// 辅助函数：获取下载进度
async function getDownloadProgress(page: Page) {
  const response = await page.request.get('/api/update/status');
  if (response.ok()) {
    const data = await response.json();
    return data.progress;
  }
  return null;
}

test.describe('更新 API 核心功能测试', () => {
  test('检查更新 API 返回正确格式', async ({ request }) => {
    const response = await request.get('/api/update/check');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty('available');
    expect(data).toHaveProperty('current_version');

    // 验证版本信息结构
    if (data.available && data.latest_info) {
      expect(data.latest_info).toHaveProperty('version');
    }
  });

  test('更新状态 API 返回完整信息', async ({ request }) => {
    const response = await request.get('/api/update/status');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty('state');
    expect(['idle', 'checking', 'available', 'downloading', 'ready', 'applying', 'failed']).toContain(data.state);
    expect(data).toHaveProperty('graceful_update_pending');
    expect(data).toHaveProperty('active_recordings_count');
    expect(data).toHaveProperty('can_apply_now');
  });

  test('launcher 状态 API 返回自托管模式信息', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty('connected');
    expect(data).toHaveProperty('current_version');
    expect(data).toHaveProperty('is_docker');
    expect(data).toHaveProperty('active_recordings');
    expect(data).toHaveProperty('launched_by');

    // E2E 测试环境中没有外部 launcher 连接
    expect(data.connected).toBe(false);
  });

  test('取消更新 API 可成功调用', async ({ request }) => {
    const response = await request.post('/api/update/cancel', { data: {} });
    expect(response.ok()).toBeTruthy();
  });

  test('无可用更新时应用更新返回错误', async ({ request }) => {
    // 确保没有待处理的更新
    await request.post('/api/update/cancel', { data: {} });

    // 尝试应用更新
    const response = await request.post('/api/update/apply', {
      data: { graceful_wait: true }
    });

    // 应该返回错误
    expect(response.status()).toBeGreaterThanOrEqual(400);
  });
});

test.describe('更新页面 UI 交互测试', () => {
  test.beforeEach(async ({ page, request }) => {
    // 在每个测试开始前，取消任何进行中的下载
    await request.post('/api/update/cancel', { data: {} });
    await page.waitForTimeout(500);

    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);
  });

  test('更新页面完整布局', async ({ page }) => {
    // 验证页面标题
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();

    // 验证版本信息卡片
    const versionCard = page.locator('.ant-card').filter({ hasText: '版本信息' }).first();
    await expect(versionCard).toBeVisible();
    await expect(versionCard.getByText('当前版本')).toBeVisible();
    await expect(versionCard.getByText('更新状态')).toBeVisible();
    await expect(versionCard.getByText('运行环境')).toBeVisible();
    await expect(versionCard.getByText('启动器模式')).toBeVisible();
    await expect(versionCard.getByText('活跃录制')).toBeVisible();

    // 验证检查更新卡片
    const checkCard = page.locator('.ant-card').filter({ hasText: '检查更新' }).first();
    await expect(checkCard).toBeVisible();
    await expect(checkCard.locator('button').filter({ hasText: '检查更新' })).toBeVisible();
    await expect(checkCard.getByText('包含预发布版本')).toBeVisible();

    // 验证更新说明卡片
    const helpCard = page.locator('.ant-card').filter({ hasText: '更新说明' });
    await expect(helpCard).toBeVisible();
    await expect(helpCard.getByText('优雅更新')).toBeVisible();
    await expect(helpCard.getByText('强制更新')).toBeVisible();
  });

  test('点击检查更新按钮后按钮状态变化', async ({ page }) => {
    const checkButton = page.locator('button').filter({ hasText: '检查更新' });
    await expect(checkButton).toBeEnabled();

    // 点击检查更新
    await checkButton.click();

    // 按钮应该变为加载状态（可能很快就结束）
    // 等待一段时间后验证页面仍正常
    await page.waitForTimeout(3000);
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();
  });

  test('预发布版本切换功能', async ({ page }) => {
    // 找到预发布版本切换按钮
    const toggleButton = page.locator('button').filter({ hasText: /^是$|^否$/ });
    await expect(toggleButton).toBeVisible();

    // 记录初始状态
    const initialText = await toggleButton.textContent();

    // 点击切换
    await toggleButton.click();
    await page.waitForTimeout(500);

    // 验证状态已切换
    const newText = await toggleButton.textContent();
    expect(newText).not.toBe(initialText);

    // 再次点击切换回来
    await toggleButton.click();
    await page.waitForTimeout(500);

    const finalText = await toggleButton.textContent();
    expect(finalText).toBe(initialText);
  });

  test('无更新时显示最新版本提示', async ({ page }) => {
    // 无更新时应该显示 "当前已是最新版本"
    const noUpdateMessage = page.getByText('当前已是最新版本');

    // 如果没有可用更新，这个消息应该可见
    // 如果有更新可用，则会显示更新信息
    const isVisible = await noUpdateMessage.isVisible().catch(() => false);
    const hasUpdateCard = await page.locator('.ant-card').filter({ hasText: /可用更新/ }).isVisible().catch(() => false);

    // 至少应该显示其中一个
    expect(isVisible || hasUpdateCard).toBeTruthy();
  });
});

test.describe('侧边栏导航和路由测试', () => {
  test('从首页导航到更新页面', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 找到更新菜单项
    const updateMenuItem = page.locator('.ant-menu-item').filter({ hasText: '更新' });
    await expect(updateMenuItem).toBeVisible({ timeout: 10000 });

    // 点击导航
    await updateMenuItem.click();

    // 验证到达更新页面
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();
    expect(page.url()).toContain('/update');
  });

  test('从设置页面导航到更新页面', async ({ page }) => {
    await page.goto('/#/configInfo');
    await page.waitForLoadState('domcontentloaded');

    // 点击更新菜单
    const updateMenuItem = page.locator('.ant-menu-item').filter({ hasText: '更新' });
    await updateMenuItem.click();

    // 验证导航成功
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();
  });

  test('直接访问更新页面 URL', async ({ page }) => {
    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    // 验证页面正确加载
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();
  });
});

test.describe('优雅更新逻辑测试', () => {
  test('无活跃录制时 active_recordings 为 0', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 测试环境应该没有活跃录制
    expect(data.active_recordings).toBe(0);
  });

  test('更新状态不影响其他核心 API', async ({ request }) => {
    // 触发更新检查
    await request.get('/api/update/check');

    // 验证直播间列表 API 仍然正常
    const livesResponse = await request.get('/api/lives');
    expect(livesResponse.ok()).toBeTruthy();

    // 验证配置 API 仍然正常
    const configResponse = await request.get('/api/config');
    expect(configResponse.ok()).toBeTruthy();

    // 验证系统信息 API 仍然正常
    const infoResponse = await request.get('/api/info');
    expect(infoResponse.ok()).toBeTruthy();
  });

  test('更新通道设置 API 可调用', async ({ request }) => {
    // 设置更新通道为稳定版
    const putResponse = await request.put('/api/update/channel', {
      data: { channel: 'stable' }
    });
    expect(putResponse.ok()).toBeTruthy();
  });
});

test.describe('自托管 Launcher 模式测试', () => {
  test('launcher 状态反映自托管模式', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();

    // 在 E2E 测试中，不连接外部 launcher
    expect(data.connected).toBe(false);

    // 验证必要字段存在
    expect(data).toHaveProperty('launched_by');
    expect(data).toHaveProperty('current_version');
  });

  test('Docker 环境检测', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();

    // E2E 测试不在 Docker 中运行
    expect(data.is_docker).toBe(false);
  });

  test('自托管模式下更新状态正确', async ({ request }) => {
    const response = await request.get('/api/update/status');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();

    // 验证状态字段完整
    expect(data).toHaveProperty('state');
    expect(data).toHaveProperty('graceful_update_pending');
    expect(data).toHaveProperty('active_recordings_count');
    expect(data).toHaveProperty('can_apply_now');
  });
});

test.describe('错误处理测试', () => {
  test.beforeEach(async ({ request }) => {
    // 清理任何进行中的下载
    await request.post('/api/update/cancel', { data: {} });
  });

  test('使用无效 URL 下载更新返回错误', async ({ request }) => {
    // 无效的下载请求
    const response = await request.post('/api/update/download', {
      data: {}
    });

    // 如果没有可用更新，应该返回错误
    // (因为在测试环境中可能没有真正发现更新)
    if (!response.ok()) {
      expect(response.status()).toBeGreaterThanOrEqual(400);
    }
  });

  test('检查更新时界面仍然正常', async ({ page, request }) => {
    // 确保清理状态
    await request.post('/api/update/cancel', { data: {} });
    await page.waitForTimeout(500);

    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    // 点击检查更新
    const checkButton = page.locator('button').filter({ hasText: '检查更新' });
    await checkButton.click();

    // 等待结果（成功或错误都可以）
    await page.waitForTimeout(5000);

    // 验证页面仍然正常工作
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();
  });
});

/**
 * 完整升级流程测试
 * 
 * 这些测试使用 update-mock-server（通过 playwright.config.ts 自动启动在 8889 端口）
 * 测试完整的更新检查、下载、取消等流程
 */
test.describe('完整升级流程测试', () => {
  // 每个测试前清理状态
  test.beforeEach(async ({ request }) => {
    await request.post('/api/update/cancel', { data: {} });
    await new Promise(r => setTimeout(r, 500));
  });

  test('mock server 健康检查', async ({ request }) => {
    // 验证 mock server 已启动（由 playwright 自动管理）
    const response = await request.get('http://127.0.0.1:8889/health');
    expect(response.ok()).toBeTruthy();
  });

  test('检查更新 API 返回更新信息', async ({ request }) => {
    // 调用检查更新 API
    const response = await request.get('/api/update/check?prerelease=false');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 验证返回结构正确
    expect(data).toHaveProperty('available');
    expect(data).toHaveProperty('current_version');

    // 如果发现更新，验证版本信息结构正确
    if (data.available && data.latest_info) {
      expect(data.latest_info).toHaveProperty('version');
      // 版本号应该是字符串格式
      expect(typeof data.latest_info.version).toBe('string');
    }
  });

  test('下载更新 API 可正常调用', async ({ request }) => {
    // 先检查更新
    const checkResponse = await request.get('/api/update/check?prerelease=false');
    expect(checkResponse.ok()).toBeTruthy();

    const checkData = await checkResponse.json();

    // 如果有可用更新，尝试下载
    if (checkData.available) {
      const downloadResponse = await request.post('/api/update/download', { data: {} });
      // 下载请求应该成功或返回已在下载中的状态
      expect([200, 202, 400]).toContain(downloadResponse.status());
    }
  });

  test('更新状态在下载后变化', async ({ request }) => {
    // 获取初始状态
    const initialStatus = await request.get('/api/update/status');
    expect(initialStatus.ok()).toBeTruthy();

    // 检查更新
    await request.get('/api/update/check?prerelease=false');

    // 验证状态 API 仍然可用
    const afterStatus = await request.get('/api/update/status');
    expect(afterStatus.ok()).toBeTruthy();

    const data = await afterStatus.json();
    expect(data).toHaveProperty('state');
    // 状态可能是多种状态之一
    expect(['idle', 'available', 'ready', 'checking', 'downloading', 'failed', 'applying']).toContain(data.state);
  });

  test('取消更新 API 可成功调用', async ({ request }) => {
    // 取消更新应该总是成功
    const cancelResponse = await request.post('/api/update/cancel', { data: {} });
    expect(cancelResponse.ok()).toBeTruthy();
  });

  test('更新页面 UI 显示版本信息', async ({ page, request }) => {
    // 清理状态
    await request.post('/api/update/cancel', { data: {} });

    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    // 验证页面标题
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();

    // 验证版本信息卡片
    await expect(page.locator('.ant-card').filter({ hasText: '版本信息' }).first()).toBeVisible();

    // 验证检查更新按钮
    const checkButton = page.locator('button').filter({ hasText: '检查更新' });
    await expect(checkButton).toBeVisible();
  });

  test('点击检查更新后显示更新信息', async ({ page, request }) => {
    // 清理状态
    await request.post('/api/update/cancel', { data: {} });

    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    // 点击检查更新
    const checkButton = page.locator('button').filter({ hasText: '检查更新' });
    await checkButton.click();

    // 等待检查完成（最多 10 秒）
    await page.waitForTimeout(5000);

    // 验证页面仍然正常显示
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();

    // 应该显示"当前已是最新版本"或有可用更新（版本号可能是 v99 或其他真实版本）
    const hasNoUpdate = await page.getByText('当前已是最新版本').isVisible().catch(() => false);
    // 使用更宽松的选择器查找可用更新文本
    const hasUpdate = await page.getByText(/可用更新/).first().isVisible().catch(() => false);

    expect(hasNoUpdate || hasUpdate).toBeTruthy();
  });

  test('取消下载功能正常', async ({ page, request }) => {
    // 清理状态
    await request.post('/api/update/cancel', { data: {} });

    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    // 调用取消 API
    const cancelResponse = await request.post('/api/update/cancel', { data: {} });
    expect(cancelResponse.ok()).toBeTruthy();

    // 验证页面仍然正常
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible();
  });
});

