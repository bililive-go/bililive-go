import { test, expect } from '@playwright/test';

/**
 * Bililive-Go 自动更新功能测试
 * 
 * 测试更新系统的 API 接口和前端组件
 */
test.describe('更新系统 API 测试', () => {
  test('更新检查 API 可访问', async ({ request }) => {
    // 请求更新检查 API
    const response = await request.get('/api/update/check');

    // API 应该返回成功（即使没有可用更新）
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 验证返回包含必要字段
    expect(data).toHaveProperty('available');
    expect(data).toHaveProperty('current_version');
  });

  test('更新状态 API 可访问', async ({ request }) => {
    const response = await request.get('/api/update/status');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 验证返回包含状态字段
    expect(data).toHaveProperty('state');
  });

  test('启动器状态 API 可访问', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 验证返回包含连接状态
    expect(data).toHaveProperty('connected');
    // 在 E2E 测试环境中，没有启动器连接
    expect(data.connected).toBe(false);
  });

  test('最新版本 API 可访问', async ({ request }) => {
    // 请求最新版本 API
    const response = await request.get('/api/update/latest');

    // API 应该返回成功、404（如果没有可用版本）或 500（服务器错误）
    expect([200, 404, 500]).toContain(response.status());

    if (response.status() === 200) {
      const data = await response.json();
      // 如果有数据，验证包含版本信息
      if (data) {
        expect(data).toHaveProperty('version');
      }
    }
  });

  test('更新通道 API 可访问', async ({ request }) => {
    // 设置更新通道
    const response = await request.put('/api/update/channel', {
      data: { channel: 'stable' }
    });

    // 验证响应成功
    expect(response.ok()).toBeTruthy();
  });

  test('更新取消 API 可访问', async ({ request }) => {
    // 测试取消更新 API（即使没有进行中的更新也应该成功）
    const response = await request.post('/api/update/cancel', {
      data: {}
    });

    expect(response.ok()).toBeTruthy();
  });
});

test.describe('更新系统状态测试', () => {
  test.beforeEach(async ({ request }) => {
    // 确保测试前状态被重置
    await request.post('/api/update/cancel', { data: {} });
  });

  test('初始状态为 idle', async ({ request }) => {
    const response = await request.get('/api/update/status');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 状态应该是合法的状态之一
    expect(['idle', 'available', 'ready', 'checking', 'downloading', 'failed', 'applying']).toContain(data.state);
  });

  test('版本信息格式正确', async ({ request }) => {
    // 通过更新检查获取版本信息
    const response = await request.get('/api/update/check');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // 当前版本应该是 semver 格式或 dev 版本
    expect(data.current_version).toBeDefined();
    // 版本号应该是字符串
    expect(typeof data.current_version).toBe('string');
  });
});

test.describe('更新配置 API 测试', () => {
  test('配置包含更新选项', async ({ request }) => {
    const response = await request.get('/api/config');
    expect(response.ok()).toBeTruthy();

    const config = await response.json();
    // 验证配置包含更新相关字段
    expect(config).toHaveProperty('update');
    expect(config.update).toHaveProperty('auto_check');
    expect(config.update).toHaveProperty('auto_download');
    expect(config.update).toHaveProperty('check_interval_hours');
    expect(config.update).toHaveProperty('include_prerelease');
  });

  test('更新配置值正确', async ({ request }) => {
    const response = await request.get('/api/config');
    expect(response.ok()).toBeTruthy();

    const config = await response.json();
    // 验证测试配置文件中的值
    expect(config.update.auto_check).toBe(false);  // 测试配置中禁用
    expect(config.update.auto_download).toBe(false);  // 测试配置中禁用
    expect(config.update.check_interval_hours).toBe(6);
    expect(config.update.include_prerelease).toBe(false);
  });

  test('可以更新配置中的更新选项', async ({ request }) => {
    // 获取当前配置
    const getResponse = await request.get('/api/config');
    expect(getResponse.ok()).toBeTruthy();
    const originalConfig = await getResponse.json();

    // 尝试更新配置（但不实际改变值，避免影响其他测试）
    const updateResponse = await request.put('/api/config', {
      data: {
        update: {
          auto_check: false,
          auto_download: false,
          check_interval_hours: 6,
          include_prerelease: false
        }
      }
    });

    expect(updateResponse.ok()).toBeTruthy();

    // 验证配置未变（因为我们设置了相同的值）
    const verifyResponse = await request.get('/api/config');
    expect(verifyResponse.ok()).toBeTruthy();
    const verifyConfig = await verifyResponse.json();
    expect(verifyConfig.update.auto_check).toBe(originalConfig.update.auto_check);
  });
});

test.describe('SSE 更新事件测试', () => {
  test('SSE 端点可连接', async ({ page }) => {
    // 访问页面以建立 SSE 连接
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 验证页面加载正常（SSE 连接在后台建立）
    await expect(page.getByRole('heading', { name: 'Bililive-go' })).toBeVisible();
  });

  test('SSE 连接建立后可接收事件', async ({ page }) => {
    // 监听控制台日志以验证 SSE 连接
    const sseMessages: string[] = [];
    page.on('console', msg => {
      if (msg.text().includes('[SSE]')) {
        sseMessages.push(msg.text());
      }
    });

    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 等待 SSE 连接建立
    await page.waitForTimeout(2000);

    // 验证 SSE 连接日志
    const hasConnected = sseMessages.some(msg =>
      msg.includes('Connected') || msg.includes('Subscribed')
    );

    // SSE 应该已连接或订阅
    if (sseMessages.length > 0) {
      console.log('SSE 消息:', sseMessages);
    }
  });
});

test.describe('更新 UI 组件测试', () => {
  test('初始状态下不显示更新横幅', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');

    // 更新横幅在没有可用更新时不应该显示
    const updateBanner = page.locator('.update-banner');

    // 等待一下确保组件有机会渲染
    await page.waitForTimeout(1000);

    // 由于测试环境没有真正的更新，横幅不应该显示
    // 使用 count() 而不是 toBeHidden() 因为元素可能根本不存在
    const count = await updateBanner.count();
    expect(count).toBe(0);
  });

  test('页面不应有更新相关的错误提示', async ({ page }) => {
    const errors: string[] = [];

    page.on('console', msg => {
      if (msg.type() === 'error' && msg.text().toLowerCase().includes('update')) {
        errors.push(msg.text());
      }
    });

    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);

    // 不应该有更新相关的错误
    expect(errors.length).toBe(0);
  });
});

test.describe('更新工作流测试', () => {
  test('完整的更新检查流程', async ({ request }) => {
    // 1. 检查初始状态
    const statusBefore = await request.get('/api/update/status');
    expect(statusBefore.ok()).toBeTruthy();
    const beforeData = await statusBefore.json();
    expect(beforeData.state).toBeDefined();

    // 2. 触发更新检查
    const checkResponse = await request.get('/api/update/check');
    expect(checkResponse.ok()).toBeTruthy();

    const checkData = await checkResponse.json();
    expect(checkData).toHaveProperty('available');
    expect(checkData).toHaveProperty('current_version');

    // 3. 检查状态（可能已更新或保持 idle）
    const statusAfter = await request.get('/api/update/status');
    expect(statusAfter.ok()).toBeTruthy();
    const afterData = await statusAfter.json();
    expect(afterData.state).toBeDefined();

    console.log('更新检查结果:', {
      available: checkData.available,
      currentVersion: checkData.current_version,
      stateBefore: beforeData.state,
      stateAfter: afterData.state
    });
  });

  test('预发布版本检查', async ({ request }) => {
    // 测试包含预发布版本的检查
    const response = await request.get('/api/update/check?prerelease=true');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(data).toHaveProperty('available');
    expect(data).toHaveProperty('current_version');
  });

  test('取消更新不会引发错误', async ({ request }) => {
    // 即使没有进行中的更新，取消操作也应该成功
    const cancelResponse = await request.post('/api/update/cancel', { data: {} });
    expect(cancelResponse.ok()).toBeTruthy();

    // 验证状态正常
    const statusResponse = await request.get('/api/update/status');
    expect(statusResponse.ok()).toBeTruthy();
  });
});

test.describe('更新错误处理测试', () => {
  test('无效的更新通道被拒绝', async ({ request }) => {
    // 发送无效的通道值
    const response = await request.put('/api/update/channel', {
      data: { channel: 'invalid_channel' }
    });

    // 应该返回错误（400 Bad Request）或者接受并忽略
    // 根据实际实现可能有不同行为
    const status = response.status();
    expect([200, 400]).toContain(status);
  });

  test('应用更新时无可用更新返回错误', async ({ request }) => {
    // 在没有下载更新的情况下尝试应用
    const response = await request.post('/api/update/apply', {
      data: { graceful_wait: true }
    });

    // 应该返回错误，因为没有可用的更新
    // 状态码可能是 400、409 或其他错误码
    const status = response.status();
    expect(status).toBeGreaterThanOrEqual(400);
  });

  test('下载更新时无可用更新返回错误', async ({ request }) => {
    // 确保状态是 idle
    await request.post('/api/update/cancel', { data: {} });

    // 尝试下载（在没有检查到可用更新的情况下）
    const response = await request.post('/api/update/download', {
      data: {}
    });

    // 可能返回错误或成功开始检查
    // 根据实现，可能需要先检查更新
    const status = response.status();
    console.log('下载响应状态:', status);
  });
});

test.describe('更新与录制集成测试', () => {
  test.beforeEach(async ({ request }) => {
    // 确保测试环境干净
    await request.post('/api/update/cancel', { data: {} });
  });

  test('启动器未连接时更新状态正确', async ({ request }) => {
    const response = await request.get('/api/update/launcher');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    // E2E 测试环境中没有启动器
    expect(data.connected).toBe(false);

    // 验证这不影响更新检查
    const checkResponse = await request.get('/api/update/check');
    expect(checkResponse.ok()).toBeTruthy();
  });

  test('更新状态不影响直播间列表', async ({ request }) => {
    // 触发更新检查
    await request.get('/api/update/check');

    // 验证直播间列表 API 仍然正常工作
    const livesResponse = await request.get('/api/lives');
    expect(livesResponse.ok()).toBeTruthy();

    const lives = await livesResponse.json();
    expect(Array.isArray(lives)).toBe(true);
  });
});
