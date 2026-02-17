import { defineConfig, devices } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import * as dotenv from 'dotenv';

// 加载本地环境变量配置（.env.local 不会被 git 跟踪）
dotenv.config({ path: path.join(__dirname, '.env.local') });

// 在测试前复制配置模板到 test-output 目录
// 这样配置文件的变化不会被 git 跟踪
const configTemplate = path.join(__dirname, 'tests/e2e/fixtures/test-config.template.yml');
const configRuntime = path.join(__dirname, 'test-output/test-config.yml');

// 确保 test-output 目录存在并复制配置
fs.mkdirSync(path.dirname(configRuntime), { recursive: true });
fs.copyFileSync(configTemplate, configRuntime);

/**
 * Bililive-Go E2E 测试配置
 * 
 * 使用 osrp-stream-tester 作为模拟直播流服务器
 * 测试 Web UI 的各种功能
 */
export default defineConfig({
  testDir: './tests/e2e',

  // 测试输出目录（被 .gitignore 忽略）
  outputDir: 'test-results',

  // 全局超时：30 秒（快速失败，节省 CI 时间）
  timeout: 30 * 1000,

  // 期望超时
  expect: {
    timeout: 5 * 1000,
  },

  // 由于需要共享服务器，不使用完全并行
  fullyParallel: false,

  // CI 环境检测
  forbidOnly: !!process.env.CI,

  // 重试策略（CI 中只重试 1 次）
  retries: process.env.CI ? 1 : 0,

  // 并发控制：由于需要共享服务器，使用单线程
  workers: 1,

  // 测试报告
  reporter: [
    ['list'],
    ['html', { outputFolder: 'playwright-report', open: 'never' }],
  ],

  // 共享配置
  use: {
    // 基础 URL（bgo Web UI）
    baseURL: 'http://127.0.0.1:8080',

    // 仅在失败时截图
    screenshot: 'only-on-failure',

    // 始终录制 trace，便于调试（可在 Trace Viewer 中查看源代码和调用栈）
    trace: {
      mode: 'on',
      sources: true,      // 在 trace 中包含源代码
      snapshots: true,    // 包含 DOM 快照
      screenshots: true,  // 包含截图用于时间线预览
    },

    // 仅在失败时录制视频
    video: 'on-first-retry',

    // 浏览器视口
    viewport: { width: 1280, height: 720 },
  },

  // 只测试 Chromium（减少测试时间）
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  // Web 服务器配置
  webServer: [
    // update-mock-server 模拟版本 API 服务器
    {
      command: 'go run ./test/update-mock-server -version 99.0.0 -port 8889',
      url: 'http://127.0.0.1:8889/health',
      reuseExistingServer: !process.env.CI,
      timeout: 60 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
    },
    // osrp-stream-tester 测试流服务器
    {
      // 本地开发可设置 OSRP_STREAM_TESTER_PATH 环境变量指向本地目录
      // 例如: set OSRP_STREAM_TESTER_PATH=C:/path/to/osrp-stream-tester
      // 如未设置（包括 CI），则从远程拉取最新版本
      command: process.env.OSRP_STREAM_TESTER_PATH
        ? `powershell -Command "cd ${process.env.OSRP_STREAM_TESTER_PATH}; go run ./cmd/stream-tester serve --port 8888"`
        : 'go run github.com/kira1928/osrp-stream-tester/cmd/stream-tester@latest serve --port 8888',
      url: 'http://127.0.0.1:8888/health',
      reuseExistingServer: !process.env.CI,
      timeout: 30 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
    },
    // bililive-go 主程序（使用 dev 构建标签）
    {
      // 在 CI 中使用预构建的二进制，本地使用 go run
      // 配置文件从模板复制到 test-output 目录（避免 git 跟踪动态变化）
      command: process.env.CI
        ? './bin/bililive-dev --config test-output/test-config.yml'
        : 'go run -tags dev ./src/cmd/bililive --config test-output/test-config.yml',
      url: 'http://127.0.0.1:8080/api/info',
      reuseExistingServer: !process.env.CI,
      timeout: 60 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
    },
  ],
});
