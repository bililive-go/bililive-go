#!/bin/sh

set -o errexit
set -o nounset

readonly OUTPUT_PATH=bin
readonly CONSTS_PATH="github.com/bililive-go/bililive-go/src/consts"

_build() {
  target=$1
  bin_name=$2
  ld_flags=$3
  go build \
    -tags ${TAGS:-"release"} \
    -gcflags="${GCFLAGS:-""}" \
    -o ${OUTPUT_PATH}/${bin_name} \
    -ldflags="${ld_flags}" \
    ./src/cmd/${target}/
}

build() {
  target=$1

  if [ ${target} = 'bililive' ]; then
    now=$(date '+%Y-%m-%d_%H:%M:%S')
    rev=$(echo "${rev:-$(git rev-parse HEAD)}")
    ver=$(git describe --tags --always)
    debug_build_flags=""
    if [ ${TAGS} = 'release' ]; then
      debug_build_flags=" -s -w "
    fi
    ld_flags="${debug_build_flags} -X ${CONSTS_PATH}.BuildTime=${now} -X ${CONSTS_PATH}.AppVersion=${ver} -X ${CONSTS_PATH}.GitHash=${rev}"
  fi

  if [ $(go env GOOS) = "windows" ]; then
    ext=".exe"
  fi

  if [ $(go env GOARCH) = "mips" ]; then
    bin_name="${target}-$(go env GOOS)-$(go env GOARCH)-softfloat${ext:-}"

    export GOMIPS=softfloat
    _build "${target}" "${bin_name}" "${ld_flags:-}"
    unset GOMIPS
  fi

  bin_name="${target}-$(go env GOOS)-$(go env GOARCH)${ext:-}"

  _build "${target}" "${bin_name}" "${ld_flags:-}"

  if [ ${UPX_ENABLE:-"0"} = "1" ]; then
    case "${bin_name}" in
    *-aix-* | *bsd-* | *-mips64* | *-riscv64 | *-s390x | *-plan9-* | *-windows-arm*) ;;
    *)
      upx --no-progress ${OUTPUT_PATH}/"${bin_name}"
      ;;
    esac
  fi
}

main() {
  if [ ! -d src/cmd/$1 ]; then
    echo 'Target not exist in src/cmd/'
    exit 1
  fi
  build $1
}

main $@
