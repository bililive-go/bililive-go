#!/usr/bin/env python3
"""
bgo 本地升级测试服务器

用途：在开发机起一个 HTTP 服务器，模拟 bililive-go.com 的版本检测 API，
让 NAS 上的 bgo 容器通过环境变量 VERSION_API_URL 指向本服务器，
从而测试自动升级到本地打包的版本。

配置：默认读取脚本同目录下的 update-server.env；系统环境变量会覆盖变量文件，
命令行参数又会覆盖系统环境变量。可复制 update-server.env.example 后填写实际值。

工作原理：
  - bgo 启动后会请求 GET {VERSION_API_URL}?current=...&platform=...&prerelease=...
  - 本服务器固定返回 update_available=true，并把下载链接指向本机提供的升级包
  - bgo (remotetools) 下载 tar.gz 后按后缀解压，定位顶层 bililive-linux-amd64
  - 用户在前端点"立即更新"后，bgo 同进程热切换到新版本

端点：
  GET  /api/versions            返回版本检测 JSON（始终 update_available=true）
  HEAD /api/versions            同上（仅头）
  GET  /<package>               返回升级包，支持 Range 断点续传
  HEAD /<package>               返回包元信息（Content-Length / Accept-Ranges）

用法：
  cp scripts/update-server.env.example scripts/update-server.env
  # 编辑 scripts/update-server.env 后运行：
  python3 scripts/update-server.py

  # 也可以临时用系统环境变量或命令行覆盖：
  BGO_UPDATE_VERSION=0.8.0-local.2 python3 scripts/update-server.py --port 8100
"""

import argparse
import json
import os
import sys
from datetime import date
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

API_PATH = "/api/versions"
DEFAULT_ENV_FILE = Path(__file__).with_name("update-server.env")


def load_env_file(path):
    """读取简单的 KEY=VALUE 变量文件，不修改当前进程的系统环境变量。"""
    values = {}
    env_path = Path(path).expanduser()
    if not env_path.is_file():
        return values

    with env_path.open(encoding="utf-8") as f:
        for line_number, raw_line in enumerate(f, 1):
            line = raw_line.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("export "):
                line = line[len("export ") :].lstrip()
            key, separator, value = line.partition("=")
            key = key.strip()
            if not separator or not key:
                raise ValueError(f"{env_path}:{line_number}: 应使用 KEY=VALUE 格式")
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in ('"', "'"):
                value = value[1:-1]
            values[key] = value
    return values


def parse_args():
    """按“命令行 > 系统环境变量 > 变量文件 > 安全默认值”的顺序解析配置。"""
    bootstrap = argparse.ArgumentParser(add_help=False)
    bootstrap.add_argument(
        "--env-file",
        default=os.environ.get("BGO_UPDATE_ENV_FILE", str(DEFAULT_ENV_FILE)),
    )
    initial_args, _ = bootstrap.parse_known_args()

    try:
        file_values = load_env_file(initial_args.env_file)
    except (OSError, ValueError) as e:
        bootstrap.error(str(e))

    def configured(name, fallback):
        return os.environ.get(name, file_values.get(name, fallback))

    parser = argparse.ArgumentParser(description="bgo 本地升级测试服务器")
    parser.add_argument(
        "--env-file",
        default=initial_args.env_file,
        help=f"变量文件路径（默认 {DEFAULT_ENV_FILE}，也可用 BGO_UPDATE_ENV_FILE 指定）",
    )
    parser.add_argument(
        "--host",
        default=configured("BGO_UPDATE_BIND_HOST", "127.0.0.1"),
        help="监听地址（变量 BGO_UPDATE_BIND_HOST；安全默认值 127.0.0.1）",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=configured("BGO_UPDATE_PORT", "8099"),
        help="监听端口（变量 BGO_UPDATE_PORT；默认 8099）",
    )
    parser.add_argument(
        "--public-host",
        default=configured("BGO_UPDATE_PUBLIC_HOST", "127.0.0.1"),
        help="写入下载 URL、供容器或外部设备访问的主机名/IP（变量 BGO_UPDATE_PUBLIC_HOST）",
    )
    parser.add_argument(
        "--pkg",
        default=configured(
            "BGO_UPDATE_PACKAGE", "bin/bililive-linux-amd64.tar.gz"
        ),
        help="升级包路径（变量 BGO_UPDATE_PACKAGE）",
    )
    parser.add_argument(
        "--version",
        default=configured("BGO_UPDATE_VERSION", "0.0.0-local.1"),
        help="目标版本号（变量 BGO_UPDATE_VERSION）",
    )
    return parser.parse_args()


class Handler(BaseHTTPRequestHandler):
    server_version = "bgo-update-server/1.0"

    # ------------------------------------------------------------------ utils
    def _send_json(self, obj, status=200):
        data = json.dumps(obj).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)

    def _version_response(self):
        port = self.server.server_address[1]
        pkg_name = os.path.basename(self.server.pkg_path)
        url = f"http://{self.server.public_host}:{port}/{pkg_name}"
        return {
            "latest_version": self.server.version,
            "release_date": date.today().isoformat(),
            "changelog": "本地升级测试版本 (local upgrade test build)",
            "prerelease": True,
            "update_available": True,
            "update_required": False,
            "download": {
                "urls": [url],
                "filename": pkg_name,
                "sha256": "",
            },
            "release_page": "",
        }

    # ---------------------------------------------------------------- routing
    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == API_PATH:
            self._send_json(self._version_response())
            return
        if path in ("", "/"):
            self._send_json({"service": "bgo-update-server", "api": API_PATH})
            return
        self._serve_pkg(path)

    def do_HEAD(self):
        path = self.path.split("?", 1)[0]
        if path == API_PATH:
            self._send_json(self._version_response())
            return
        self._serve_pkg(path)

    # ------------------------------------------------------------- file serving
    def _serve_pkg(self, path):
        pkg_name = os.path.basename(self.server.pkg_path)
        if path.lstrip("/") != pkg_name:
            self.send_error(404, "Not Found")
            return
        try:
            size = os.path.getsize(self.server.pkg_path)
        except OSError as e:
            self.send_error(500, str(e))
            return

        range_header = self.headers.get("Range")
        if range_header:
            start, end = self._parse_range(range_header, size)
            if start is None:
                self.send_response(416)  # Requested Range Not Satisfiable
                self.send_header("Content-Range", f"bytes */{size}")
                self.end_headers()
                return
            length = end - start + 1
            self.send_response(206)  # Partial Content
            self.send_header("Content-Type", "application/gzip")
            self.send_header("Content-Length", str(length))
            self.send_header("Content-Range", f"bytes {start}-{end}/{size}")
            self.send_header("Accept-Ranges", "bytes")
            self.end_headers()
            if self.command != "HEAD":
                self._write_range(start, end)
        else:
            self.send_response(200)
            self.send_header("Content-Type", "application/gzip")
            self.send_header("Content-Length", str(size))
            self.send_header("Accept-Ranges", "bytes")
            self.end_headers()
            if self.command != "HEAD":
                with open(self.server.pkg_path, "rb") as f:
                    while True:
                        chunk = f.read(64 * 1024)
                        if not chunk:
                            break
                        self.wfile.write(chunk)

    def _parse_range(self, header, size):
        """解析 bytes=start-end / bytes=start- / bytes=-suffix，失败返回 (None, None)。"""
        try:
            unit, spec = header.split("=", 1)
            if unit.strip() != "bytes":
                return None, None
            spec = spec.strip()
            if spec.startswith("-"):  # 后缀 bytes=-N
                suffix = int(spec[1:])
                if suffix <= 0:
                    return None, None
                start = max(0, size - suffix)
                return start, size - 1
            start_s, _, end_s = spec.partition("-")
            start = int(start_s)
            end = int(end_s) if end_s else size - 1
            if start < 0 or start >= size or end >= size or start > end:
                return None, None
            return start, end
        except (ValueError, AttributeError):
            return None, None

    def _write_range(self, start, end):
        remaining = end - start + 1
        with open(self.server.pkg_path, "rb") as f:
            f.seek(start)
            while remaining > 0:
                chunk = f.read(min(64 * 1024, remaining))
                if not chunk:
                    break
                self.wfile.write(chunk)
                remaining -= len(chunk)

    def log_message(self, fmt, *args):
        sys.stderr.write("[%s] %s\n" % (self.address_string(), fmt % args))


def main():
    args = parse_args()

    if not os.path.isfile(args.pkg):
        sys.exit(f"错误：升级包不存在: {args.pkg}")

    httpd = ThreadingHTTPServer((args.host, args.port), Handler)
    httpd.pkg_path = os.path.abspath(args.pkg)
    httpd.public_host = args.public_host
    httpd.version = args.version

    pkg_name = os.path.basename(httpd.pkg_path)
    size = os.path.getsize(httpd.pkg_path)
    api_url = f"http://{args.public_host}:{args.port}{API_PATH}"
    print("bgo 本地升级测试服务器已启动")
    print(f"  监听:          http://{args.host}:{args.port}")
    print(f"  版本检测 API:  {api_url}")
    print(f"  升级包:        http://{args.public_host}:{args.port}/{pkg_name} ({size} bytes)")
    print(f"  目标版本:      {args.version}")
    print()
    print("在 NAS 容器重建时添加环境变量：")
    print(f"  VERSION_API_URL={api_url}")
    print()
    print("按 Ctrl+C 停止。访问日志将打印到 stderr。\n")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n已停止")
        httpd.shutdown()


if __name__ == "__main__":
    main()
