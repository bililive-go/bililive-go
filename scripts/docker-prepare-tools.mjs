#!/usr/bin/env node
/**
 * docker-prepare-tools.mjs
 * 为 Docker 构建预下载内置工具（跨平台 Node.js 实现）
 *
 * 用法: node scripts/docker-prepare-tools.mjs <arch> <output_dir> [--dry-run]
 * 示例: node scripts/docker-prepare-tools.mjs arm64 docker-context/tools
 *       node scripts/docker-prepare-tools.mjs amd64 docker-context/tools --dry-run
 *
 * 支持的架构: amd64, arm64, arm (armv7)
 * 此脚本在 CI 的原生 x86/ARM runner 上运行，避免在 QEMU 中解压大文件
 *
 * 版本号和下载 URL 自动从 src/tools/remote-tools-config.json 读取，
 * 无需手动维护版本号的一致性。
 *
 * 重要: 解压逻辑必须与 remotetools 的 extractDownloadedFile() 行为一致：
 *   1. 先解压到临时目录
 *   2. 如果解压后顶层只有一个目录，则自动提升该子目录（strip 顶层）
 *   3. 最终移动到目标目录
 */

import { readFileSync, mkdirSync, rmSync, readdirSync, renameSync, statSync, existsSync, createWriteStream } from 'fs';
import { join, dirname, resolve, basename } from 'path';
import { execSync } from 'child_process';
import { tmpdir } from 'os';
import { randomBytes } from 'crypto';
import { pipeline } from 'stream/promises';
import https from 'https';
import http from 'http';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

// ===========================================================================
// 配置
// ===========================================================================

/** Docker 内置工具列表（与 SyncBuiltInTools() 中的列表一致） */
const DOCKER_BUILTIN_TOOLS = ['ffmpeg', 'dotnet', 'bililive-recorder', 'node', 'biliLive-tools'];

/**
 * ffmpeg 在 arm/armv7 上通过 apt 安装，不需要预下载。
 * 这里定义需要跳过下载的工具+架构组合。
 */
const SKIP_RULES = [
  { tool: 'ffmpeg', arch: 'arm' },
];

// ===========================================================================
// 工具函数
// ===========================================================================

/**
 * 解析 remote-tools-config.json，获取指定工具在 linux/<arch> 上的下载 URL 和最新版本
 * @param {object} config - 完整配置对象
 * @param {string} toolName - 工具名称
 * @param {string} arch - 目标架构 (amd64, arm64, arm)
 * @returns {{ version: string, url: string } | null}
 */
function resolveToolDownload(config, toolName, arch) {
  const toolConfig = config[toolName];
  if (!toolConfig) {
    console.error(`  警告: 配置中未找到工具 "${toolName}"，跳过`);
    return null;
  }

  // 获取最新版本（按语义版本排序）
  const versions = Object.keys(toolConfig);
  if (versions.length === 0) {
    console.error(`  警告: 工具 "${toolName}" 无可用版本`);
    return null;
  }
  const latestVersion = getLatestVersion(versions);

  const versionConfig = toolConfig[latestVersion];
  const downloadUrl = versionConfig.downloadUrl;

  // downloadUrl 的结构有两种形式：
  // 1. 数组形式（跨平台通用）: ["url1", "url2"]
  //    如 bililive-recorder
  // 2. 按平台/架构分层的对象: { "linux": { "amd64": [...], "arm64": [...] } }
  //    如 ffmpeg, dotnet, node, biliLive-tools
  let url = resolveUrl(downloadUrl, arch);
  if (!url) {
    console.error(`  警告: 工具 "${toolName}" v${latestVersion} 无 linux/${arch} 的下载链接`);
    return null;
  }

  return { version: latestVersion, url };
}

/**
 * 从 downloadUrl 结构中提取 linux/<arch> 的实际下载 URL（第一个，即主源）
 * @param {string|string[]|object} downloadUrl
 * @param {string} arch
 * @returns {string|null}
 */
function resolveUrl(downloadUrl, arch) {
  // 情况1: 直接是字符串
  if (typeof downloadUrl === 'string') {
    return downloadUrl;
  }

  // 情况2: 数组 → 取第一个（主源）
  if (Array.isArray(downloadUrl)) {
    return downloadUrl[0] || null;
  }

  // 情况3: 按平台分层的对象 → 取 linux/<arch>
  if (typeof downloadUrl === 'object' && downloadUrl !== null) {
    const linuxUrls = downloadUrl['linux'];
    if (!linuxUrls) return null;

    const archUrls = linuxUrls[arch];
    if (!archUrls) return null;

    // archUrls 可能是字符串或数组
    if (typeof archUrls === 'string') return archUrls;
    if (Array.isArray(archUrls)) return archUrls[0] || null;
  }

  return null;
}

/**
 * 版本排序：与 remotetools 的 GetLatestVersion 行为一致
 * 使用语义版本比较，返回最新（最大）的版本
 * @param {string[]} versions
 * @returns {string}
 */
function getLatestVersion(versions) {
  if (versions.length === 1) return versions[0];

  return versions.sort((a, b) => compareVersions(b, a))[0];
}

/**
 * 版本比较（简化的语义版本比较）
 * @param {string} a
 * @param {string} b
 * @returns {number} >0 if a > b, <0 if a < b, 0 if equal
 */
function compareVersions(a, b) {
  // 移除前缀 'v' 或 'n' 等非数字前缀
  const cleanA = a.replace(/^[^0-9]*/, '');
  const cleanB = b.replace(/^[^0-9]*/, '');

  const partsA = cleanA.split(/[.\-]/);
  const partsB = cleanB.split(/[.\-]/);

  const maxLen = Math.max(partsA.length, partsB.length);
  for (let i = 0; i < maxLen; i++) {
    const pa = partsA[i] || '0';
    const pb = partsB[i] || '0';

    // 尝试数字比较
    const na = parseInt(pa, 10);
    const nb = parseInt(pb, 10);

    if (!isNaN(na) && !isNaN(nb)) {
      if (na !== nb) return na - nb;
    } else {
      // 字符串比较
      if (pa < pb) return -1;
      if (pa > pb) return 1;
    }
  }
  return 0;
}

/**
 * 使用 HTTPS 下载文件
 * @param {string} url
 * @param {string} destPath
 * @returns {Promise<void>}
 */
function downloadFile(url, destPath) {
  return new Promise((resolve, reject) => {
    const doRequest = (currentUrl, redirectCount = 0) => {
      if (redirectCount > 10) {
        return reject(new Error(`重定向次数过多: ${url}`));
      }

      const client = currentUrl.startsWith('https') ? https : http;
      client.get(currentUrl, (res) => {
        // 处理重定向
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          return doRequest(res.headers.location, redirectCount + 1);
        }

        if (res.statusCode !== 200) {
          return reject(new Error(`下载失败: HTTP ${res.statusCode} - ${currentUrl}`));
        }

        const fileStream = createWriteStream(destPath);
        pipeline(res, fileStream).then(resolve).catch(reject);
      }).on('error', reject);
    };

    doRequest(url);
  });
}

/**
 * 下载并解压工具到目标目录
 * 模拟 remotetools 的 extractDownloadedFile() 行为
 * @param {string} url
 * @param {string} dest - 最终目标目录（例如 tools/ffmpeg/n8.0-latest）
 */
async function downloadAndExtract(url, dest) {
  const filename = basename(new URL(url).pathname);

  // 确保目标目录的父目录存在
  mkdirSync(dirname(dest), { recursive: true });

  // 使用临时目录解压（与 remotetools 行为一致）
  const tmpDir = `${dest}.tmp_extract`;
  rmSync(tmpDir, { recursive: true, force: true });
  mkdirSync(tmpDir, { recursive: true });

  // 创建唯一临时文件
  const tmpFile = join(tmpdir(), `docker-prepare-tools-${randomBytes(6).toString('hex')}`);

  try {
    console.log(`>>> 下载: ${url}`);
    await downloadFile(url, tmpFile);

    console.log(`    解压到临时目录...`);
    if (filename.endsWith('.tar.xz')) {
      execSync(`tar xf "${tmpFile}" -C "${tmpDir}"`, { stdio: 'inherit' });
    } else if (filename.endsWith('.tar.gz')) {
      execSync(`tar xzf "${tmpFile}" -C "${tmpDir}"`, { stdio: 'inherit' });
    } else if (filename.endsWith('.zip')) {
      // 跨平台解压 zip
      if (process.platform === 'win32') {
        execSync(`powershell -Command "Expand-Archive -Path '${tmpFile}' -DestinationPath '${tmpDir}' -Force"`, { stdio: 'inherit' });
      } else {
        execSync(`unzip -qo "${tmpFile}" -d "${tmpDir}"`, { stdio: 'inherit' });
      }
    } else {
      throw new Error(`不支持的格式: ${filename}`);
    }

    // 模拟 remotetools 的 "单目录提升" 逻辑：
    // 如果解压后顶层只有一个子目录，将其内容提升为目标目录
    const entries = readdirSync(tmpDir);
    if (entries.length === 1) {
      const firstEntry = entries[0];
      const firstEntryPath = join(tmpDir, firstEntry);
      if (statSync(firstEntryPath).isDirectory()) {
        console.log(`    检测到单一顶层目录 '${firstEntry}'，自动提升`);
        rmSync(dest, { recursive: true, force: true });
        renameSync(firstEntryPath, dest);
        rmSync(tmpDir, { recursive: true, force: true });
      } else {
        rmSync(dest, { recursive: true, force: true });
        renameSync(tmpDir, dest);
      }
    } else {
      console.log(`    直接使用解压内容`);
      rmSync(dest, { recursive: true, force: true });
      renameSync(tmpDir, dest);
    }

    console.log(`    完成: ${dest}`);
  } finally {
    // 清理临时文件
    try { rmSync(tmpFile, { force: true }); } catch { /* ignore */ }
    try { rmSync(tmpDir, { recursive: true, force: true }); } catch { /* ignore */ }
  }
}

// ===========================================================================
// 主流程
// ===========================================================================

async function main() {
  const args = process.argv.slice(2);
  if (args.length < 2) {
    console.error(`用法: node ${basename(__filename)} <arch> <output_dir> [--dry-run]`);
    console.error(`示例: node ${basename(__filename)} arm64 docker-context/tools`);
    console.error(`       node ${basename(__filename)} amd64 docker-context/tools --dry-run`);
    process.exit(1);
  }

  const arch = args[0];
  const outputDir = args[1];
  const dryRun = args.includes('--dry-run');

  if (!['amd64', 'arm64', 'arm'].includes(arch)) {
    console.error(`错误: 不支持的架构 "${arch}"，支持: amd64, arm64, arm`);
    process.exit(1);
  }

  // 加载 remote-tools-config.json
  const configPath = resolve(__dirname, '..', 'src', 'tools', 'remote-tools-config.json');
  let config;
  try {
    config = JSON.parse(readFileSync(configPath, 'utf-8'));
  } catch (err) {
    console.error(`错误: 无法加载配置文件 ${configPath}: ${err.message}`);
    process.exit(1);
  }

  mkdirSync(outputDir, { recursive: true });

  console.log(`=== 为 linux/${arch} 预下载 Docker 内置工具${dryRun ? ' (dry-run)' : ''} ===`);
  console.log(`配置来源: ${configPath}`);

  let downloadCount = 0;
  let skipCount = 0;

  for (const toolName of DOCKER_BUILTIN_TOOLS) {
    // 检查是否需要跳过
    if (SKIP_RULES.some(r => r.tool === toolName && r.arch === arch)) {
      console.log(`\n--- ${toolName} --- (跳过: linux/${arch} 使用 apt 安装)`);
      skipCount++;
      continue;
    }

    console.log(`\n--- ${toolName} ---`);

    const result = resolveToolDownload(config, toolName, arch);
    if (!result) {
      console.log(`  跳过 (无可用下载)`);
      skipCount++;
      continue;
    }

    console.log(`  版本: ${result.version}`);
    console.log(`  URL: ${result.url}`);
    const destDir = join(outputDir, toolName, result.version);
    console.log(`  目标: ${destDir}`);

    if (dryRun) {
      downloadCount++;
      continue;
    }

    await downloadAndExtract(result.url, destDir);
    downloadCount++;
  }

  console.log(`\n=== 所有工具下载完成 (下载: ${downloadCount}, 跳过: ${skipCount}) ===`);

  // 列出下载结果
  if (existsSync(outputDir)) {
    const toolDirs = readdirSync(outputDir);
    for (const dir of toolDirs) {
      const dirPath = join(outputDir, dir);
      if (statSync(dirPath).isDirectory()) {
        console.log(`  ${dir}/`);
      }
    }
  }
}

main().catch(err => {
  console.error(`致命错误: ${err.message}`);
  process.exit(1);
});
