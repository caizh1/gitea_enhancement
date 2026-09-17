#!/usr/bin/env bash
# 构建包含最新模板、静态资源及版本号的 Linux amd64 导航 Demo。
set -euo pipefail
cd "$(dirname "$0")/.."
version="${1:-1.27.3+governance.demo.nav7}"
image="${2:-gitea-governance-navigation:review-20260916}"
go_binary="${GO:-go}"
make frontend
# Makefile 的程序目标不依赖 bindata.dat；强制重编译，避免只更新资源而沿用旧程序。
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 make -W main.go backend GO="$go_binary" TAGS=bindata GITEA_VERSION="$version"
docker build --platform linux/amd64 -f Dockerfile.governance -t "$image" .
docker run --rm --platform linux/amd64 --entrypoint /usr/local/bin/gitea "$image" --version | grep -F "$version"
