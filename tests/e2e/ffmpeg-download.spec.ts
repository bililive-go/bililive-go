/**
 * ffmpeg-download.spec.ts
 *
 * 测试 FFmpeg 异步下载流程的完整 UI 行为。
 *
 * 需要 playwright.ffmpeg.config.ts 配置（bgo 在 8085 端口运行，没有 FFmpeg 可用，
 * remotetools 配置指向本地 ffmpeg-mock-server）。
 *
 * 关键特点：
 * - bgo 启动时 ffmpeg_path 指向不存在的路径，触发 remotetools 下载
 * - ffmpeg-mock-server 在 8890 端口运行，提供可控下载速度和失败模拟
 * - 测试验证 Banner 在 downloading → ready 整个流程中的正确行为
 */
import { test, expect, Page } from '@playwright/test';

const BGO_BASE = 'http://127.0.0.1:8085';
const MOCK_SERVER = 'http://127.0.0.1:8890';

function ffmpegBanner(page: Page) {
  return page.locator('.ffmpeg-banner');
}

/** 设置 mock server 的行为 */
async function setMockServerControl(
  request: import('@playwright/test').APIRequestContext,
  opts: { speed?: number; fail?: boolean },
) {
  const resp = await request.post(`${MOCK_SERVER}/control`, { data: opts });
  expect(resp.status()).toBe(200);
}

/** 轮询 /api/ffmpeg/status 直到匹配目标状态，返回最终状态对象 */
async function pollFFmpegStatus(
  request: import('@playwright/test').APIRequestContext,
  targetState: string,
  timeoutMs = 60_000,
): Promise<{ state: string; message?: string; source?: string }> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const resp = await request.get(`${BGO_BASE}/api/ffmpeg/status`);
    if (resp.ok()) {
      const body = await resp.json();
      if (body.state === targetState) {
        return body;
      }
      // 如果状态跳过了预期状态（如直接到 ready），也终止
      if (body.state === 'error' && targetState !== 'error') {
        throw new Error(`FFmpeg 进入 error 状态（预期 ${targetState}）: ${body.message}`);
      }
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  const last = await (await request.get(`${BGO_BASE}/api/ffmpeg/status`)).json();
  throw new Error(
    `等待 FFmpeg 状态 "${targetState}" 超时（${timeoutMs}ms），当前状态: ${JSON.stringify(last)}`,
  );
}

test.describe('FFmpeg 异步下载流程', () => {
  test('下载中时 Banner 显示 downloading 状态，下载完成后 Banner 消失', async ({
    page,
    request,
  }) => {
    // 设置 mock server 为慢速（1KB/s），让下载过程持续足够长以供观察
    await setMockServerControl(request, { speed: 1024, fail: false });

    await page.goto(BGO_BASE + '/');
    await page.waitForLoadState('networkidle');

    // 等待出现 downloading 状态（下载启动后 UI 应显示进度横幅）
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 30_000 });
    await expect(ffmpegBanner(page)).toContainText('正在下载 FFmpeg');

    // 验证 API 也返回 downloading 状态
    const dlStatus = await pollFFmpegStatus(request, 'downloading', 30_000);
    expect(dlStatus.state).toBe('downloading');

    // 切换为不限速让下载快速完成
    await setMockServerControl(request, { speed: 0 });

    // 等待下载完成，Banner 应消失（状态变为 ready）
    await expect(ffmpegBanner(page)).not.toBeVisible({ timeout: 60_000 });

    // 验证 API 状态也为 ready
    const readyStatus = await pollFFmpegStatus(request, 'ready', 10_000);
    expect(readyStatus.state).toBe('ready');
  });

  test('下载失败时 Banner 显示 error 状态', async ({ page, request }) => {
    // 设置 mock server 为失败模式
    await setMockServerControl(request, { fail: true, speed: 0 });

    await page.goto(BGO_BASE + '/');
    await page.waitForLoadState('networkidle');

    // 等待 error 状态出现
    await expect(ffmpegBanner(page)).toBeVisible({ timeout: 30_000 });
    await expect(ffmpegBanner(page)).toContainText('FFmpeg 下载失败');

    // 验证 API 也返回 error 状态
    const status = await pollFFmpegStatus(request, 'error', 30_000);
    expect(status.state).toBe('error');
    expect(status.message).toBeTruthy();
  });

  test('页面刷新后能正确恢复当前 FFmpeg 状态', async ({ page, request }) => {
    // 先让 bgo 处于任意非 ready 状态（如果可以查到）
    const initialStatus = await request.get(`${BGO_BASE}/api/ffmpeg/status`);
    expect(initialStatus.ok()).toBeTruthy();
    const status = await initialStatus.json();

    // 刷新页面
    await page.goto(BGO_BASE + '/');
    await page.waitForLoadState('networkidle');

    if (status.state === 'ready') {
      // ready 状态：Banner 不应显示
      await expect(ffmpegBanner(page)).not.toBeVisible({ timeout: 3000 });
    } else {
      // 非 ready 状态：Banner 应显示对应内容
      await expect(ffmpegBanner(page)).toBeVisible({ timeout: 5000 });
    }
  });
});
