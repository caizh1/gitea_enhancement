#!/usr/bin/env bash
# 共用安装器由相邻 Manager 仓库维护，产出独立可用的增强版安装包。
set -euo pipefail
MANAGER_ROOT="${MANAGER_SOURCE_DIR:-$(cd "$(dirname "$0")/../.." && pwd)/gitea-manager}"
[[ -f "$MANAGER_ROOT/deployment/build-installers.py" ]] || { echo '找不到安装包构建器，请设置 MANAGER_SOURCE_DIR。' >&2; exit 1; }
exec python3 "$MANAGER_ROOT/deployment/build-installers.py" --product gitea "$@"
