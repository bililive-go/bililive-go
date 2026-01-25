import { defineConfig, devices } from '@playwright/test';

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
    // osrp-stream-tester 测试流服务器
    {
      // 在 CI 中使用预安装的二进制，本地使用 go run
      command: process.env.CI
        ? 'stream-tester serve --port 8888'
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
      command: process.env.CI
        ? './bililive-go --config tests/e2e/fixtures/test-config.yml'
        : 'go run -tags dev ./src/cmd/bililive --config tests/e2e/fixtures/test-config.yml',
      url: 'http://127.0.0.1:8080/api/info',
      reuseExistingServer: !process.env.CI,
      timeout: 60 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
    },
  ],
});
