#!/usr/bin/env bash
set -euo pipefail

source_script="${1:-outputs/gitea-native-governance-demo-offline-linux-amd64/安装并启动.sh}"
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/bin"

cat >"$root/bin/uname" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == -s ]] && echo Linux || echo x86_64
EOF
cat >"$root/bin/sha256sum" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == -c ]]; then exit 0; fi
shasum -a 256
EOF
cat >"$root/bin/docker" <<'EOF'
#!/usr/bin/env bash
echo "${POSTGRES_PASSWORD-unset}|$*" >>"$TEST_LOG"
if [[ "$*" == *"exec -T gitea wget"* ]]; then
  count=0; [[ -f "$TEST_STATE" ]] && count="$(cat "$TEST_STATE")"
  count=$((count + 1)); echo "$count" >"$TEST_STATE"
  if (( count <= MOCK_HEALTH_FAILURES )); then
    echo "wget: cannot connect to remote host 127.0.0.1: Connection refused" >&2
    exit 1
  fi
fi
exit 0
EOF
chmod +x "$root/bin/"*

new_case() {
  case_dir="$root/$1"
  mkdir -p "$case_dir/images"
  cp "$source_script" "$case_dir/安装并启动.sh"
  : >"$case_dir/SHA256SUMS"
  : >"$case_dir/images/gitea-native-governance-demo-1.27.3-linux-amd64.tar"
  : >"$case_dir/images/postgres-17.11-bookworm-linux-amd64.tar"
  export TEST_LOG="$case_dir/调用.log" TEST_STATE="$case_dir/状态" MOCK_HEALTH_FAILURES="${2:-0}"
}

new_case 局域网 2
(cd "$case_dir" && PATH="$root/bin:$PATH" GITEA_DOMAIN=192.168.8.20 GITEA_HTTP_PORT=3505 bash 安装并启动.sh >输出.log 2>&1)
grep -qx 'GITEA_ROOT_URL=http://192.168.8.20:3505/' "$case_dir/.env"
grep -qx 'GITEA_HTTP_BIND=0.0.0.0' "$case_dir/.env"
! grep -q 'Connection refused' "$case_dir/输出.log"

new_case 本机
(cd "$case_dir" && PATH="$root/bin:$PATH" GITEA_DOMAIN=127.0.0.1 POSTGRES_PASSWORD=错误临时密码 bash 安装并启动.sh >输出.log 2>&1)
grep -qx 'GITEA_HTTP_BIND=127.0.0.1' "$case_dir/.env"
grep -q '^POSTGRES_PASSWORD=.' "$case_dir/.env"
! grep -q '^POSTGRES_PASSWORD=错误临时密码$' "$case_dir/.env"
grep -q '^unset|compose -p .* up -d' "$case_dir/调用.log"

new_case 已有配置
printf '%s\n' 'GITEA_DOMAIN=10.0.0.8' 'GITEA_ROOT_URL=http://10.0.0.8:3300/' 'GITEA_HTTP_PORT=3300' 'GITEA_SSH_PORT=2322' 'GITEA_HTTP_BIND=0.0.0.0' 'GITEA_SSH_BIND=0.0.0.0' 'GITEA_ALLOWED_HOST_LIST=external' 'POSTGRES_PASSWORD=已保存密码' >"$case_dir/.env"
before="$(shasum -a 256 "$case_dir/.env")"
(cd "$case_dir" && PATH="$root/bin:$PATH" POSTGRES_PASSWORD=错误临时密码 GITEA_DOMAIN=bad.example bash 安装并启动.sh >输出.log 2>&1)
[[ "$before" == "$(shasum -a 256 "$case_dir/.env")" ]]
grep -q '^unset|compose -p .* up -d' "$case_dir/调用.log"

new_case 持续失败 999
sed -i.bak 's/deadline=$((SECONDS + 180))/deadline=$((SECONDS + 1))/' "$case_dir/安装并启动.sh"
if (cd "$case_dir" && PATH="$root/bin:$PATH" GITEA_DOMAIN=demo.example bash 安装并启动.sh >输出.log 2>&1); then
  echo "持续失败场景应返回非零。" >&2
  exit 1
fi
grep -q '未就绪' "$case_dir/输出.log"
grep -q 'compose -p .* ps' "$case_dir/调用.log"
grep -q 'logs --tail 100 gitea database' "$case_dir/调用.log"

for invalid in 'http://demo.example' 'demo.example:3300' 'demo/example' 'demo example' 'demo$example' '2001:db8::1' '999.1.1.1'; do
  new_case 非法地址
  if (cd "$case_dir" && PATH="$root/bin:$PATH" GITEA_DOMAIN="$invalid" bash 安装并启动.sh >输出.log 2>&1); then
    echo "非法地址未拒绝：$invalid" >&2
    exit 1
  fi
  grep -q '只接受 IPv4 或 ASCII 域名' "$case_dir/输出.log"
done

echo "部署脚本 mock 回归通过。"
