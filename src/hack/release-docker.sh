#!/bin/sh

set -o errexit
set -o nounset

DOCKERHUB_IMAGE=chigusa/bililive-go
GHCR_IMAGE=ghcr.io/bililive-go/bililive-go
VERSION=$(git describe --tags --always)

add_tags() {
  image="$1"
  tags="-t $image:$VERSION"
  if ! echo "$VERSION" | grep "rc" >/dev/null; then
    tags="$tags -t $image:latest"
  fi
  echo "$tags"
}

docker buildx build \
  --platform=linux/amd64,linux/arm64/v8,linux/arm/v7 \
  $(add_tags $DOCKERHUB_IMAGE) \
  $(add_tags $GHCR_IMAGE) \
  --build-arg "tag=${VERSION}" \
  --progress plain \
  --push \
  ./
