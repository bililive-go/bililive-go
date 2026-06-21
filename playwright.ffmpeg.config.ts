/**
 * playwright.ffmpeg.config.ts
 *
 * FFmpeg 异步下载流程 e2e 测试专用配置。
 *
 * 与普通 playwright.config.ts 的区别：
 * - bgo 运行在 8085 端口（避免端口冲突）
 * - bgo 配置 ffmpeg_path 指向不存在路径，强制走 remotetools 下载流程
 * - 通过 REMOTETOOLS_CONFIG 环境变量将 remotetools 重定向到本地 mock server
 * - 额外启动 ffmpeg-mock-server 在 8890 端口，提供可控的 FFmpeg 下载
 * - 只运行 ffmpeg-download.spec.ts（状态注入测试在普通配置中运行）
 */
import { defineConfig, devices } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const configTemplate = path.join(
  __dirname,
  'tests/e2e/fixtures/ffmpeg-test-config.template.yml',
);
const configRuntime = path.join(__dirname, 'test-output/ffmpeg-test-config.yml');
const toolsConfigPath = path.join(
  __dirname,
  'tests/e2e/fixtures/test-tools-config.json',
).replace(/\\/g, '/');

// 确保 test-output 目录存在并准备配置文件
fs.mkdirSync(path.dirname(configRuntime), { recursive: true });
fs.copyFileSync(configTemplate, configRuntime);

// 清空 FFmpeg e2e 测试专用的 appdata 目录，避免上次测试缓存的 fake-ffmpeg 影响结果
const ffmpegAppData = path.join(__dirname, 'test-output/.appdata-ffmpeg-e2e/external_tools');
try {
  fs.rmSync(ffmpegAppData, { recursive: true, force: true });
} catch (_) {
  // 目录不存在时忽略
}

// 构建启动 bgo 的命令（需设置 REMOTETOOLS_CONFIG 环境变量）
function makeBgoCommand(): string {
  const configArg = `test-output/ffmpeg-test-config.yml`;
  if (process.platform === 'win32') {
    // PowerShell 语法设置环境变量
    return (
      `powershell -Command "$env:REMOTETOOLS_CONFIG='${toolsConfigPath}'; ` +
      `go run -tags dev ./src/cmd/bililive --config ${configArg}"`
    );
  }
  // bash/sh 语法
  return `REMOTETOOLS_CONFIG='${toolsConfigPath}' go run -tags dev ./src/cmd/bililive --config ${configArg}`;
}

export default defineConfig({
  // 只运行 FFmpeg 下载流程专用的测试文件
  testDir: './tests/e2e',
  testMatch: ['**/ffmpeg-download.spec.ts'],

  outputDir: 'test-results-ffmpeg',
  timeout: 120 * 1000, // 下载测试需要更长超时
  expect: { timeout: 10 * 1000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,

  reporter: [
    ['list'],
    ['html', { outputFolder: 'playwright-report-ffmpeg', open: 'never' }],
  ],

  use: {
    baseURL: 'http://127.0.0.1:8085',
    screenshot: 'only-on-failure',
    trace: { mode: 'on', sources: true, snapshots: true, screenshots: true },
    video: 'retain-on-failure',
    viewport: { width: 1280, height: 720 },
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  webServer: [
    // ffmpeg-mock-server — 提供可控的 FFmpeg zip 下载
    {
      command: 'go run ./test/ffmpeg-mock-server -port 8890',
      url: 'http://127.0.0.1:8890/health',
      reuseExistingServer: false,
      timeout: 120 * 1000, // 等待 fake-ffmpeg 编译完成
      stdout: 'pipe',
      stderr: 'pipe',
    },
    // bgo — 无可用 FFmpeg，remotetools 指向 mock server
    {
      command: makeBgoCommand(),
      url: 'http://127.0.0.1:8085/api/info',
      reuseExistingServer: false,
      timeout: 60 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
    },
  ],
});
