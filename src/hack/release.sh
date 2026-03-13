#!/bin/sh

set -o errexit
set -o nounset

readonly BIN_PATH=bin

package() {
  last_dir=$(pwd)
  cd $BIN_PATH
  file=$1
  type=$2
  case $type in
  zip)
    res=${file%.exe}.zip
    zip $res ${file} -j ../config.yml >/dev/null 2>&1
    ;;
  tar)
    res=${file}.tar.gz
    tar zcvf $res ${file} -C ../ config.yml >/dev/null 2>&1
    ;;
  7z)
    res=${file}.7z
    7z a $res ${file} ../config.yml >/dev/null 2>&1
    ;;
  *) ;;

  esac
  cd "$last_dir"
  echo $BIN_PATH/$res
}

# 串行构建，支持在 CI 通过“分片”并行（多个 Runner/Job 共同完成）
# 可选环境变量：
#   SHARD_TOTAL 分片总数（默认 1）
#   SHARD_INDEX 当前分片索引（默认 0，范围 [0, SHARD_TOTAL)）
SHARD_TOTAL=${SHARD_TOTAL:-1}
SHARD_INDEX=${SHARD_INDEX:-0}
# 基本健壮性：非数字/越界回退到安全值
case "$SHARD_TOTAL" in ''|*[!0-9]*) SHARD_TOTAL=1;; esac
case "$SHARD_INDEX" in ''|*[!0-9]*) SHARD_INDEX=0;; esac
if [ "$SHARD_TOTAL" -lt 1 ]; then SHARD_TOTAL=1; fi
if [ "$SHARD_INDEX" -ge "$SHARD_TOTAL" ]; then SHARD_INDEX=$((SHARD_INDEX % SHARD_TOTAL)); fi

# 预热依赖缓存，减少后续下载
echo "[deps] Warming Go module cache..."
go mod download >/dev/null 2>&1 || true

# 明确支持的目标平台列表（白名单）
# 避免使用 go tool dist list 全量列表，因为很多冷门平台的第三方库（如 modernc.org/sqlite、gopsutil）不支持
TARGETS="
linux/amd64
linux/arm64
linux/arm
linux/386
linux/ppc64le
linux/riscv64
linux/s390x
darwin/amd64
darwin/arm64
windows/amd64
windows/arm64
windows/386
freebsd/amd64
freebsd/arm64
"
# 去除空行
TARGETS=$(echo "$TARGETS" | sed '/^$/d')
echo "[shard] TOTAL=${SHARD_TOTAL} INDEX=${SHARD_INDEX}"

i=0
printf "%s\n" "$TARGETS" | while IFS= read -r dist; do
  mod=$(( i % SHARD_TOTAL ))
  i=$((i+1))
  # 非本分片的目标直接跳过
  if [ "$mod" -ne "$SHARD_INDEX" ]; then
    continue
  fi
  platform="${dist%/*}"
  arch="${dist#*/}"
  echo "[build] PLATFORM=$platform ARCH=$arch"
  make PLATFORM="$platform" ARCH="$arch" bililive
done

for file in $(ls $BIN_PATH); do
  case $file in
  *.tar.gz | *.zip | *.7z | *.yml | *.yaml)
    continue
    ;;
  *windows*)
    package_type=zip
    ;;
  *)
    package_type=tar
    ;;
  esac
  res=$(package $file $package_type)
  rm -f $BIN_PATH/$file
done