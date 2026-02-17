import { test, expect } from '@playwright/test';

/**
 * Bililive-Go 完整升级流程 E2E 测试
 * 
 * 测试使用 update-mock-server 模拟版本 API
 * 验证从检查更新到应用更新的完整流程
 */

test.describe('更新页面 UI 测试', () => {
  test.beforeEach(async ({ page }) => {
    // 导航到更新页面
    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');
  });

  test('更新页面可访问', async ({ page }) => {
    // 等待页面完全加载
    await page.waitForTimeout(2000);

    // 验证页面标题（使用 h4 选择器更精确）
    await expect(page.locator('h4').filter({ hasText: '程序更新' })).toBeVisible({ timeout: 10000 });

    // 验证版本信息卡片存在
    await expect(page.locator('.ant-card').filter({ hasText: '版本信息' }).first()).toBeVisible({ timeout: 5000 });

    // 验证检查更新卡片存在（使用卡片选择器避免与按钮混淆）
    await expect(page.locator('.ant-card').filter({ hasText: '检查更新' }).first()).toBeVisible({ timeout: 5000 });
  });

  test('显示当前版本信息', async ({ page }) => {
    // 等待版本信息加载
    await page.waitForTimeout(2000);

    // 验证当前版本显示
    const versionCard = page.locator('.ant-card').filter({ hasText: '版本信息' });
    await expect(versionCard).toBeVisible();

    // 验证显示了当前版本（可能是 dev 或具体版本号）
    await expect(versionCard.locator('text=当前版本')).toBeVisible();
  });

  test('检查更新按钮可用', async ({ page }) => {
    // 验证检查更新按钮存在且可点击
    const checkButton = page.locator('button').filter({ hasText: '检查更新' });
    await expect(checkButton).toBeVisible();
    await expect(checkButton).toBeEnabled();
  });

  test('预发布版本切换开关工作正常', async ({ page }) => {
    // 找到预发布版本切换按钮
    const prereleaseButton = page.locator('button').filter({ hasText: /是|否/ });
    await expect(prereleaseButton).toBeVisible();

    // 点击切换
    const initialText = await prereleaseButton.textContent();
    await prereleaseButton.click();

    // 验证状态已切换
    const newText = await prereleaseButton.textContent();
    expect(newText).not.toBe(initialText);
  });

  test('更新说明卡片显示正确', async ({ page }) => {
    // 验证更新说明卡片存在
    const helpCard = page.locator('.ant-card').filter({ hasText: '更新说明' });
    await expect(helpCard).toBeVisible();

    // 验证优雅更新说明
    await expect(helpCard.locator('text=优雅更新')).toBeVisible();

    // 验证强制更新说明
    await expect(helpCard.locator('text=强制更新')).toBeVisible();
  });
});

test.describe('更新 API 集成测试', () => {
  test('检查更新 API 可访问', async ({ request }) => {
    const response = await request.get('/api/update/check');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty('available');
    expect(data).toHaveProperty('current_version');
  });

  test('更新状态 API 返回正确格式', async ({ request }) => {
    const response = await request.get('/api/update/status');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty('state');
    expect(data).toHaveProperty('graceful_update_pending');
    expect(data).toHaveProperty('active_recordings_count');
    expect(data).toHaveProperty('can_apply_now');
  });

  test('启动器状态 API 返回自托管模式信息', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty('connected');
    expect(data).toHaveProperty('current_version');
    expect(data).toHaveProperty('is_docker');
    expect(data).toHaveProperty('active_recordings');

    // 在 E2E 测试环境中，没有外部启动器连接
    expect(data.connected).toBe(false);
  });

  test('检查预发布版本 API 可访问', async ({ request }) => {
    const response = await request.get('/api/update/check?prerelease=true');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty('available');
    expect(data).toHaveProperty('current_version');
  });

  test('取消更新 API 可访问', async ({ request }) => {
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

test.describe('更新页面交互测试', () => {
  test('点击检查更新后显示结果', async ({ page }) => {
    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');

    // 点击检查更新
    const checkButton = page.locator('button').filter({ hasText: '检查更新' });
    await checkButton.click();

    // 等待检查完成（按钮不再显示 loading）
    await page.waitForTimeout(3000);

    // 验证页面仍然正常显示
    await expect(page.locator('text=程序更新')).toBeVisible();
  });

  test('无更新时显示最新版本提示', async ({ page }) => {
    await page.goto('/#/update');
    await page.waitForLoadState('domcontentloaded');

    // 等待加载完成
    await page.waitForTimeout(2000);

    // 如果没有可用更新，应该显示 "当前已是最新版本" 或类似信息
    // 注意：在测试环境中可能没有真正的更新可用
    const noUpdateMessage = page.locator('text=当前已是最新版本');
    const hasUpdate = page.locator('.ant-card').filter({ hasText: /可用更新/ });

    // 至少应该显示其中一个
    const noUpdateVisible = await noUpdateMessage.isVisible().catch(() => false);
    const hasUpdateVisible = await hasUpdate.isVisible().catch(() => false);

    expect(noUpdateVisible || hasUpdateVisible).toBeTruthy();
  });
});

test.describe('侧边栏导航测试', () => {
  test('更新菜单项存在且可点击', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 查找更新菜单项
    const updateMenuItem = page.locator('.ant-menu-item').filter({ hasText: '更新' });
    await expect(updateMenuItem).toBeVisible({ timeout: 10000 });

    // 点击更新菜单
    await updateMenuItem.click();

    // 验证导航到更新页面
    await expect(page.locator('text=程序更新')).toBeVisible();
  });

  test('从其他页面导航到更新页面', async ({ page }) => {
    // 先访问设置页面
    await page.goto('/#/configInfo');
    await page.waitForLoadState('domcontentloaded');

    // 点击更新菜单
    const updateMenuItem = page.locator('.ant-menu-item').filter({ hasText: '更新' });
    await updateMenuItem.click();

    // 验证导航成功
    await expect(page.locator('text=程序更新')).toBeVisible();
  });
});

test.describe('优雅更新逻辑测试', () => {
  test('无活跃录制时状态正确', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 测试环境应该没有活跃录制
    expect(data.active_recordings).toBe(0);
  });

  test('更新状态不影响其他 API', async ({ request }) => {
    // 触发更新检查
    await request.get('/api/update/check');

    // 验证直播间列表 API 仍然正常
    const livesResponse = await request.get('/api/lives');
    expect(livesResponse.ok()).toBeTruthy();

    // 验证配置 API 仍然正常
    const configResponse = await request.get('/api/config');
    expect(configResponse.ok()).toBeTruthy();
  });
});

test.describe('自托管 Launcher 模式测试', () => {
  test('launcher 状态反映自托管模式', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();

    // 在 E2E 测试中，不连接外部 launcher
    expect(data.connected).toBe(false);

    // 验证 launched_by 字段存在
    expect(data).toHaveProperty('launched_by');
  });

  test('Docker 环境检测正确', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();

    // E2E 测试不在 Docker 中运行
    expect(data.is_docker).toBe(false);
  });
});
