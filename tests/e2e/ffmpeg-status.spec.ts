/**
 * ffmpeg-status.spec.ts
 *
 * 测试 FFmpeg 状态 Banner 的 UI 行为。
 * 使用 /api/debug/ffmpeg/force-state 端点（dev 构建专有）注入各种状态，
 * 验证 Banner 的显示、内容和消失逻辑是否正确。
 *
 * 依赖：正常 playwright.config.ts（bgo 在 8080 端口运行，带 -tags dev）
 */
import { test, expect, Page, APIRequestContext } from '@playwright/test';

const BASE_URL = 'http://127.0.0.1:8080';

/**
 * 通过 debug 端点强制设置 FFmpeg 状态，并等待 UI 响应
 */
async function forceFFmpegState(
  request: APIRequestContext,
  state: 'checking' | 'downloading' | 'ready' | 'not_found' | 'error',
  message?: string,
  source?: string,
) {
  const body: Record<string, string> = { state };
  if (message) body['message'] = message;
  if (source) body['source'] = source;

  const resp = await request.post(`${BASE_URL}/api/debug/ffmpeg/force-state`, { data: body });
  expect(resp.status()).toBe(200);
}

/**
 * 等待并返回 FFmpeg Banner 元素（可见状态下）
 */
function ffmpegBanner(page: Page) {
  return page.locator('.ffmpeg-banner');
}

test.describe('FFmpeg 状态 Banner', () => {
  test.beforeEach(async ({ page, request }) => {
    // 先将状态重置为 ready，确保页面初始加载时 Banner 不可见
    await forceFFmpegState(request, 'ready');
    await page.goto('/');
    // 等待页面基本加载完成
    await page.waitForLoadState('networkidle');
    // Banner 应为隐藏
    await expect(ffmpegBanner(page)).not.toBeVisible();
  });

  test.afterEach(async ({ request }) => {
    // 测试后恢复 ready 状态，不影响其他测试
    await forceFFmpegState(request, 'ready');
  });

  test('ready 状态时 Banner 不显示', async ({ page }) => {
    await expect(ffmpegBanner(page)).not.toBeVisible();
  });

  test('checking 状态时显示正在检测提示', async ({ page, request }) => {
    await forceFFmpegState(request, 'checking');
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 5000 });
    await expect(ffmpegBanner(page)).toContainText('正在检测 FFmpeg');
  });

  test('downloading 状态时显示下载进度提示', async ({ page, request }) => {
    await forceFFmpegState(
      request,
      'downloading',
      '正在从 GitHub 下载 FFmpeg...',
      'remotetools',
    );
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 5000 });
    await expect(ffmpegBanner(page)).toContainText('正在下载 FFmpeg');
    await expect(ffmpegBanner(page)).toContainText('正在从 GitHub 下载 FFmpeg...');
  });

  test('not_found 状态时显示警告提示', async ({ page, request }) => {
    await forceFFmpegState(request, 'not_found', 'FFmpeg 未找到');
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 5000 });
    await expect(ffmpegBanner(page)).toContainText('未找到 FFmpeg');
  });

  test('error 状态时显示错误提示', async ({ page, request }) => {
    await forceFFmpegState(request, 'error', '下载超时');
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 5000 });
    await expect(ffmpegBanner(page)).toContainText('FFmpeg 下载失败');
    await expect(ffmpegBanner(page)).toContainText('下载超时');
  });

  test('从 downloading 切换到 ready 后 Banner 消失', async ({ page, request }) => {
    // 先注入 downloading 状态
    await forceFFmpegState(request, 'downloading', '模拟下载中...');
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 5000 });

    // 再切换到 ready — Banner 应消失
    await forceFFmpegState(request, 'ready');
    await expect(ffmpegBanner(page)).not.toBeVisible({ timeout: 5000 });
  });

  test('从 error 切换到 ready 后 Banner 消失', async ({ page, request }) => {
    await forceFFmpegState(request, 'error', '模拟下载失败');
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 5000 });

    await forceFFmpegState(request, 'ready');
    await expect(ffmpegBanner(page)).not.toBeVisible({ timeout: 5000 });
  });

  test('GET /api/ffmpeg/status 返回正确格式', async ({ request }) => {
    await forceFFmpegState(request, 'downloading', '测试中', 'remotetools');

    const resp = await request.get(`${BASE_URL}/api/ffmpeg/status`);
    expect(resp.status()).toBe(200);

    const body = await resp.json();
    expect(body).toHaveProperty('state', 'downloading');
    expect(body).toHaveProperty('message', '测试中');
    expect(body).toHaveProperty('source', 'remotetools');
  });

  test('Banner 类型：downloading/checking 为 info，not_found 为 warning，error 为 error', async ({
    page,
    request,
  }) => {
    // downloading → info（蓝色横幅）
    await forceFFmpegState(request, 'downloading');
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 5000 });
    await expect(ffmpegBanner(page)).toHaveClass(/ant-alert-info/);

    // not_found → warning（黄色横幅）
    await forceFFmpegState(request, 'not_found');
    await expect(ffmpegBanner(page)).toHaveClass(/ant-alert-warning/, { timeout: 5000 });

    // error → error（红色横幅）
    await forceFFmpegState(request, 'error', '测试失败');
    await expect(ffmpegBanner(page)).toHaveClass(/ant-alert-error/, { timeout: 5000 });
  });
});
