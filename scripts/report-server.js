/**
 * Playwright 报告服务器 (增强版)
 * 
 * 使用方法：
 *   node scripts/report-server.js                    # 本地模式，从文件系统读取源码
 *   node scripts/report-server.js --commit abc123   # CI 模式，从 GitHub 获取源码
 *   npm run report                                   # 本地模式
 * 
 * 特性：
 * - 本地模式：直接从文件系统读取源码（和 npx playwright show-report 一样）
 * - CI 模式：从 GitHub 获取指定 commit 的源码
 */

const http = require('http');
const https = require('https');
const fs = require('fs');
const path = require('path');
const { exec } = require('child_process');

const PORT = 9323;
const REPORT_DIR = path.join(__dirname, '..', 'playwright-report');
const PROJECT_ROOT = path.join(__dirname, '..');

// 解析命令行参数
const args = process.argv.slice(2);
let commitSha = null;
let repo = 'bililive-go/bililive-go';

for (let i = 0; i < args.length; i++) {
  if (args[i] === '--commit' && args[i + 1]) {
    commitSha = args[i + 1];
    i++;
  } else if (args[i] === '--repo' && args[i + 1]) {
    repo = args[i + 1];
    i++;
  }
}

// MIME 类型映射
const MIME_TYPES = {
  '.html': 'text/html',
  '.css': 'text/css',
  '.js': 'application/javascript',
  '.json': 'application/json',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
  '.zip': 'application/zip',
  '.webmanifest': 'application/manifest+json',
  '.webm': 'video/webm',
  '.mp4': 'video/mp4',
  '.ts': 'text/plain',
  '.tsx': 'text/plain',
  '.go': 'text/plain',
  '.yaml': 'text/plain',
  '.yml': 'text/plain',
  '.md': 'text/plain',
};

function getMimeType(filePath) {
  const ext = path.extname(filePath).toLowerCase();
  return MIME_TYPES[ext] || 'application/octet-stream';
}

/**
 * 从 GitHub 获取文件内容
 */
function fetchFromGitHub(filePath, commit) {
  return new Promise((resolve, reject) => {
    // 将 Windows 路径转换为相对路径
    let relativePath = filePath;

    // 移除驱动器前缀和项目根路径
    if (relativePath.includes('bililive-go')) {
      const match = relativePath.match(/bililive-go[\\\/](.+)/);
      if (match) {
        relativePath = match[1];
      }
    }

    // 标准化路径分隔符
    relativePath = relativePath.replace(/\\/g, '/');

    const url = `https://raw.githubusercontent.com/${repo}/${commit}/${relativePath}`;
    console.log(`[GitHub] Fetching: ${url}`);

    https.get(url, (res) => {
      if (res.statusCode === 404) {
        resolve(null);
        return;
      }

      if (res.statusCode !== 200) {
        reject(new Error(`GitHub returned ${res.statusCode}`));
        return;
      }

      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => resolve(data));
    }).on('error', reject);
  });
}

/**
 * 从本地文件系统读取源码
 */
function readLocalSource(filePath) {
  try {
    // 尝试直接读取
    if (fs.existsSync(filePath)) {
      return fs.readFileSync(filePath, 'utf-8');
    }

    // 尝试相对于项目根目录
    const relativePath = path.join(PROJECT_ROOT, filePath);
    if (fs.existsSync(relativePath)) {
      return fs.readFileSync(relativePath, 'utf-8');
    }

    return null;
  } catch (e) {
    console.error(`[Local] Failed to read: ${filePath}`, e.message);
    return null;
  }
}

// 创建 HTTP 服务器
const server = http.createServer(async (req, res) => {
  // 解析请求路径
  let reqPath = decodeURIComponent(req.url.split('?')[0]);
  const queryString = req.url.includes('?') ? req.url.split('?')[1] : '';
  const params = new URLSearchParams(queryString);

  // 处理根路径
  if (reqPath === '/') reqPath = '/index.html';

  // 源码文件请求（Playwright Trace Viewer 使用的格式）
  // 格式: /file?path=C:/path/to/file.ts
  if (reqPath === '/file' && params.has('path')) {
    const filePath = params.get('path');
    console.log(`[Source] Request: ${filePath}`);

    let content = null;

    // 优先使用本地文件
    content = readLocalSource(filePath);

    // 如果本地没有，且指定了 commit，从 GitHub 获取
    if (!content && commitSha) {
      try {
        content = await fetchFromGitHub(filePath, commitSha);
      } catch (e) {
        console.error(`[GitHub] Error: ${e.message}`);
      }
    }

    if (content) {
      res.writeHead(200, {
        'Content-Type': getMimeType(filePath),
        'Cache-Control': 'no-cache'
      });
      res.end(content);
    } else {
      res.writeHead(404);
      res.end(`// Source file not found: ${filePath}`);
    }
    return;
  }

  // 报告文件请求
  const filePath = path.join(REPORT_DIR, reqPath);

  // 安全检查：确保不能访问 REPORT_DIR 之外的文件
  if (!filePath.startsWith(REPORT_DIR)) {
    res.writeHead(403);
    res.end('Forbidden');
    return;
  }

  fs.stat(filePath, (err, stats) => {
    if (err) {
      res.writeHead(404);
      res.end(`File not found: ${reqPath}`);
      return;
    }

    if (stats.isDirectory()) {
      // 如果是目录，尝试返回 index.html
      const indexPath = path.join(filePath, 'index.html');
      fs.readFile(indexPath, (err, data) => {
        if (err) {
          res.writeHead(404);
          res.end('Directory index not found');
          return;
        }
        res.writeHead(200, { 'Content-Type': 'text/html' });
        res.end(data);
      });
      return;
    }

    // 读取并返回文件
    fs.readFile(filePath, (err, data) => {
      if (err) {
        res.writeHead(500);
        res.end('Error reading file');
        return;
      }

      res.writeHead(200, {
        'Content-Type': getMimeType(filePath),
        'Cache-Control': 'no-cache'
      });
      res.end(data);
    });
  });
});

// 检查报告目录是否存在
if (!fs.existsSync(REPORT_DIR)) {
  console.error('❌ playwright-report 目录不存在！');
  console.error('   请先运行测试生成报告：npm run test:e2e');
  process.exit(1);
}

// 启动服务器
server.listen(PORT, () => {
  const url = `http://localhost:${PORT}`;
  console.log('');
  console.log('🎭 Playwright 报告服务器已启动');
  console.log('');
  console.log(`   📊 报告地址: ${url}`);
  if (commitSha) {
    console.log(`   📦 源码来源: GitHub (${repo}@${commitSha.substring(0, 7)})`);
  } else {
    console.log(`   📁 源码来源: 本地文件系统`);
  }
  console.log('');
  console.log('   按 Ctrl+C 停止服务器');
  console.log('');

  // 自动在浏览器中打开
  const command = process.platform === 'win32'
    ? `start ${url}`
    : process.platform === 'darwin'
      ? `open ${url}`
      : `xdg-open ${url}`;

  exec(command, (err) => {
    if (err) {
      console.log(`   提示: 请手动在浏览器中打开 ${url}`);
    }
  });
});

// 优雅关闭
process.on('SIGINT', () => {
  console.log('\n\n👋 服务器已停止');
  process.exit(0);
});
