#!/usr/bin/env bash
# docker-prepare-tools.sh
# 在原生环境中为 Docker 构建预下载内置工具
# 用法: ./scripts/docker-prepare-tools.sh <arch> <output_dir>
# 示例: ./scripts/docker-prepare-tools.sh arm64 docker-context/tools
#
# 支持的架构: amd64, arm64, arm (armv7)
# 此脚本在 CI 的原生 x86/ARM runner 上运行，避免在 QEMU 中解压大文件
#
# 重要: 解压逻辑必须与 remotetools 的 extractDownloadedFile() 行为一致：
#   1. 先解压到临时目录
#   2. 如果解压后顶层只有一个目录，则自动提升该子目录（strip 顶层）
#   3. 最终移动到目标目录
set -euo pipefail

# 全局临时文件追踪，用于 trap 清理
_CLEANUP_DIRS=()
_CLEANUP_FILES=()
trap '_cleanup' EXIT
_cleanup() {
    for f in "${_CLEANUP_FILES[@]}"; do
        rm -f "$f" 2>/dev/null || true
    done
    for d in "${_CLEANUP_DIRS[@]}"; do
        rm -rf "$d" 2>/dev/null || true
    done
}

ARCH="${1:?用法: $0 <arch> <output_dir>}"
OUTPUT_DIR="${2:?用法: $0 <arch> <output_dir>}"

mkdir -p "$OUTPUT_DIR"

# ===========================================================================
# 辅助函数
# 模拟 remotetools 的 extractDownloadedFile() 行为：
# - 解压到临时目录
# - 如果顶层只有一个子目录，自动 strip 该目录层
# ===========================================================================
download_and_extract() {
    local url="$1"
    local dest="$2"  # 最终目标目录（例如 tools/ffmpeg/n8.0-latest）
    local filename
    # 清理文件名：移除路径遍历字符和特殊字符，仅保留安全字符
    filename=$(basename "$url" | tr -cd 'a-zA-Z0-9._-')

    # 确保目标目录的父目录存在
    mkdir -p "$(dirname "$dest")"

    # 使用临时目录解压（与 remotetools 行为一致）
    local tmp_dir="${dest}.tmp_extract"
    rm -rf "$tmp_dir"
    mkdir -p "$tmp_dir"

    # 使用 mktemp 创建唯一临时文件，避免并发构建时文件冲突
    local tmp_file
    tmp_file=$(mktemp "/tmp/docker-prepare-tools-XXXXXX")
    _CLEANUP_FILES+=("$tmp_file")
    _CLEANUP_DIRS+=("$tmp_dir")

    echo ">>> 下载: $url"
    curl -sSL -o "$tmp_file" "$url"

    echo "    解压到临时目录..."
    case "$filename" in
        *.tar.xz)
            tar xf "$tmp_file" -C "$tmp_dir"
            ;;
        *.tar.gz)
            tar xzf "$tmp_file" -C "$tmp_dir"
            ;;
        *.zip)
            unzip -qo "$tmp_file" -d "$tmp_dir"
            ;;
        *)
            echo "    错误: 不支持的格式: $filename" >&2
            # trap 会自动清理临时文件和目录
            exit 1
            ;;
    esac

    # 模拟 remotetools 的 "单目录提升" 逻辑：
    # 如果解压后顶层只有一个子目录，将其内容提升为目标目录
    # 使用 ls -1A 以包含隐藏文件（与 remotetools 的 extractDownloadedFile 行为一致）
    local entries
    entries=$(ls -1A "$tmp_dir" | wc -l)
    local first_entry
    first_entry=$(ls -1A "$tmp_dir" | head -1)

    if [ "$entries" -eq 1 ] && [ -d "$tmp_dir/$first_entry" ]; then
        echo "    检测到单一顶层目录 '$first_entry'，自动提升"
        rm -rf "$dest"
        mv "$tmp_dir/$first_entry" "$dest"
        rm -rf "$tmp_dir"  # 顶层目录已空，清理残留
    else
        echo "    直接使用解压内容"
        rm -rf "$dest"
        mv "$tmp_dir" "$dest"  # tmp_dir 被 rename，不再需要清理
    fi

    # 下载和解压成功，清理临时文件并从 trap 追踪中移除
    rm -f "$tmp_file" 2>/dev/null || true
    _CLEANUP_FILES=("${_CLEANUP_FILES[@]/$tmp_file}")
    _CLEANUP_DIRS=("${_CLEANUP_DIRS[@]/$tmp_dir}")

    echo "    完成: $dest"
}

# ===========================================================================
# 工具版本定义
# 重要: 以下版本号需要与 src/tools/remote-tools-config.json 保持同步。
# 修改版本时，请同步更新对应的配置文件，以避免版本不一致。
# ===========================================================================

echo "=== 为 linux/$ARCH 预下载 Docker 内置工具 ==="

# --- ffmpeg ---
# 注意: arm/armv7 使用 apt 安装的 ffmpeg，不需要下载
if [ "$ARCH" = "amd64" ]; then
    download_and_extract \
        "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n8.0-latest-linux64-gpl-8.0.tar.xz" \
        "$OUTPUT_DIR/ffmpeg/n8.0-latest"
elif [ "$ARCH" = "arm64" ]; then
    download_and_extract \
        "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n8.0-latest-linuxarm64-gpl-8.0.tar.xz" \
        "$OUTPUT_DIR/ffmpeg/n8.0-latest"
fi

# --- dotnet ---
if [ "$ARCH" = "amd64" ]; then
    download_and_extract \
        "https://builds.dotnet.microsoft.com/dotnet/aspnetcore/Runtime/8.0.20/aspnetcore-runtime-8.0.20-linux-x64.tar.gz" \
        "$OUTPUT_DIR/dotnet/8.0.20"
elif [ "$ARCH" = "arm64" ]; then
    download_and_extract \
        "https://builds.dotnet.microsoft.com/dotnet/aspnetcore/Runtime/8.0.20/aspnetcore-runtime-8.0.20-linux-arm64.tar.gz" \
        "$OUTPUT_DIR/dotnet/8.0.20"
elif [ "$ARCH" = "arm" ]; then
    download_and_extract \
        "https://builds.dotnet.microsoft.com/dotnet/aspnetcore/Runtime/8.0.20/aspnetcore-runtime-8.0.20-linux-arm.tar.gz" \
        "$OUTPUT_DIR/dotnet/8.0.20"
fi

# --- bililive-recorder ---
# 跨平台 .NET 程序，所有架构相同
download_and_extract \
    "https://github.com/BililiveRecorder/BililiveRecorder/releases/download/v2.17.3/BililiveRecorder-CLI-any.zip" \
    "$OUTPUT_DIR/bililive-recorder/v2.17.3"

# --- node ---
if [ "$ARCH" = "amd64" ]; then
    download_and_extract \
        "https://nodejs.org/dist/v20.10.0/node-v20.10.0-linux-x64.tar.xz" \
        "$OUTPUT_DIR/node/v20.10.0"
elif [ "$ARCH" = "arm64" ]; then
    download_and_extract \
        "https://nodejs.org/dist/v20.10.0/node-v20.10.0-linux-arm64.tar.xz" \
        "$OUTPUT_DIR/node/v20.10.0"
elif [ "$ARCH" = "arm" ]; then
    download_and_extract \
        "https://nodejs.org/dist/v20.10.0/node-v20.10.0-linux-armv7l.tar.xz" \
        "$OUTPUT_DIR/node/v20.10.0"
fi

# --- biliLive-tools ---
if [ "$ARCH" = "amd64" ]; then
    download_and_extract \
        "https://github.com/kira1928/biliLive-tools/releases/download/3.1.2-bgo.2/bililive-cli-lib-ubuntu-x64.zip" \
        "$OUTPUT_DIR/biliLive-tools/3.1.2-bgo.2"
elif [ "$ARCH" = "arm64" ]; then
    download_and_extract \
        "https://github.com/kira1928/biliLive-tools/releases/download/3.1.2-bgo.2/bililive-cli-lib-ubuntu-arm64.zip" \
        "$OUTPUT_DIR/biliLive-tools/3.1.2-bgo.2"
elif [ "$ARCH" = "arm" ]; then
    download_and_extract \
        "https://github.com/kira1928/biliLive-tools/releases/download/3.1.2-bgo.2/bililive-cli-lib-ubuntu-armv7.zip" \
        "$OUTPUT_DIR/biliLive-tools/3.1.2-bgo.2"
fi

echo "=== 所有工具下载完成 ==="
du -sh "$OUTPUT_DIR"/* 2>/dev/null || true
