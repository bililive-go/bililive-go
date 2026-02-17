import { defineConfig, devices } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

/**
 * Bililive-Go 完整升级流程测试配置
 * 
 * 此配置文件用于运行需要 update-mock-server 的完整升级测试。
 * 
 * 使用方法:
 * 1. 首先构建开发版本: make dev-incremental
 * 2. 运行测试: npx playwright test --config=playwright.update.config.ts
 */

const configTemplate = path.join(__dirname, 'tests/e2e/fixtures/test-config.template.yml');
const configRuntime = path.join(__dirname, 'test-output/test-config-update.yml');

// 确保 test-output 目录存在
fs.mkdirSync(path.dirname(configRuntime), { recursive: true });

// 复制配置模板并修改更新服务器地址
if (fs.existsSync(configTemplate)) {
  let config = fs.readFileSync(configTemplate, 'utf-8');
  // 如果配置中有 update_server 字段，替换为 mock server 地址
  // 否则保持原样
  fs.writeFileSync(configRuntime, config);
}

export default defineConfig({
  testDir: './tests/e2e',

  // 只运行升级相关测试
  testMatch: ['update-workflow.spec.ts', 'update-full.spec.ts'],

  // 测试输出目录
  outputDir: 'test-results-update',

  // 全局超时：60 秒（升级测试可能需要更长时间）
  timeout: 60 * 1000,

  // 期望超时
  expect: {
    timeout: 10 * 1000,
  },

  // 不并行运行
  fullyParallel: false,

  // CI 环境检测
  forbidOnly: !!process.env.CI,

  // 重试策略
  retries: process.env.CI ? 1 : 0,

  // 单线程运行
  workers: 1,

  // 测试报告
  reporter: [
    ['list'],
    ['html', { outputFolder: 'playwright-report-update', open: 'never' }],
  ],

  // 共享配置
  use: {
    baseURL: 'http://127.0.0.1:8080',
    screenshot: 'only-on-failure',
    trace: {
      mode: 'on',
      sources: true,
      snapshots: true,
      screenshots: true,
    },
    video: 'on-first-retry',
    viewport: { width: 1280, height: 720 },
  },

  // 只测试 Chromium
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  // Web 服务器配置
  webServer: [
    // update-mock-server
    {
      command: 'go run ./test/update-mock-server -version 99.0.0 -port 8889',
      url: 'http://127.0.0.1:8889/health',
      reuseExistingServer: !process.env.CI,
      timeout: 30 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
    },
    // osrp-stream-tester
    {
      command: process.env.OSRP_STREAM_TESTER_PATH
        ? `powershell -Command "cd ${process.env.OSRP_STREAM_TESTER_PATH}; go run ./cmd/stream-tester serve --port 8888"`
        : 'go run github.com/kira1928/osrp-stream-tester/cmd/stream-tester@latest serve --port 8888',
      url: 'http://127.0.0.1:8888/health',
      reuseExistingServer: !process.env.CI,
      timeout: 30 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
    },
    // bililive-go 主程序
    {
      command: process.env.CI
        ? './bin/bililive-dev --config test-output/test-config-update.yml'
        : 'go run -tags dev ./src/cmd/bililive --config test-output/test-config-update.yml',
      url: 'http://127.0.0.1:8080/api/info',
      reuseExistingServer: !process.env.CI,
      timeout: 60 * 1000,
      stdout: 'pipe',
      stderr: 'pipe',
    },
  ],
});
