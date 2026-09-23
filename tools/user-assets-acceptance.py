#!/usr/bin/env python3
"""隔离验证私有子模块与 LFS；所有账号和凭据仅保存在临时目录。"""
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
WORK = Path('/tmp/gitea-user-acceptance-20260918')
ASSET_WORK = WORK / 'assets'
OUT = ROOT / 'docs/evidence/user-permission-20260918/assets-results.jsonl'
BASE = 'http://127.0.0.1:3538'
STATE_FILE = WORK / 'assets-state.json'
SECRETS = json.loads((WORK / 'secrets.json').read_text())
ADMIN = {'name': 'acceptance-admin', 'password': SECRETS['管理员密码']}
S = json.loads(STATE_FILE.read_text()) if STATE_FILE.exists() else {'用户': {}, '组织': None, '仓库': {}}
RUN_TAG = secrets.token_hex(3)


def run(args, cwd=None, env=None):
    return subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True, timeout=90)


def save():
    STATE_FILE.write_text(json.dumps(S, ensure_ascii=False, indent=2))
    STATE_FILE.chmod(0o600)


def request(method, path, data=None, actor=None, api=True, headers=None):
    actor = actor or ADMIN
    secret = actor.get('token', actor['password'])
    auth = 'Basic ' + base64.b64encode(f'{actor["name"]}:{secret}'.encode()).decode()
    body = json.dumps(data).encode() if data is not None else None
    req_headers = {'Authorization': auth, **(headers or {})}
    if data is not None:
        req_headers.setdefault('Content-Type', 'application/json')
    url = BASE + ('/api/v1' if api else '') + path
    req = urllib.request.Request(url, data=body, headers=req_headers, method=method)
    try:
        res = urllib.request.urlopen(req, timeout=40)
    except urllib.error.HTTPError as exc:
        res = exc
    raw = res.read()
    try:
        parsed = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        parsed = raw
    return res.status, parsed, dict(res.headers)


def ok(method, path, data=None, actor=None):
    status, body, _ = request(method, path, data, actor)
    if not 200 <= status < 300:
        raise RuntimeError(f'准备失败：{method} {path}，状态 {status}，响应 {body!r}')
    return body


def record(check, passed, **evidence):
    row = {'场景': 'D10', '检查': check, '结果': '通过' if passed else '失败',
           '时间': datetime.datetime.now(datetime.timezone.utc).isoformat(), **evidence}
    with OUT.open('a') as f:
        f.write(json.dumps(row, ensure_ascii=False) + '\n')
    print(check, row['结果'], flush=True)


def lfs_server_setting():
    result = run(['docker', 'exec', 'user-permission-20260918-gitea-1', 'sh', '-lc',
                  "sed -n 's/^[[:space:]]*LFS_START_SERVER[[:space:]]*=[[:space:]]*//p' /data/gitea/conf/app.ini"])
    return result.stdout.strip().lower() if result.returncode == 0 else '读取失败'


def user():
    if S['用户']:
        return S['用户']
    name = 'asset-user-' + secrets.token_hex(4)
    password = secrets.token_urlsafe(24)
    created = ok('POST', '/admin/users', {'username': name, 'password': password,
        'email': name + '@example.invalid', 'must_change_password': False})
    actor = {'name': name, 'password': password, 'id': created['id']}
    token = ok('POST', f'/users/{name}/tokens', {'name': '资产验收', 'scopes': ['all']}, actor)
    actor['token'] = token['sha1']
    key = ASSET_WORK / 'user-key'
    r = run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(key)])
    assert r.returncode == 0, r.stderr
    ok('POST', '/user/keys', {'title': '资产验收', 'key': Path(str(key) + '.pub').read_text()}, actor)
    S['用户'] = actor
    save()
    return actor


def env_for(actor):
    env = os.environ.copy()
    for key in list(env):
        if key.startswith('GIT_'):
            del env[key]
    secret = actor.get('token', actor['password'])
    header = 'Authorization: Basic ' + base64.b64encode(f'{actor["name"]}:{secret}'.encode()).decode()
    env.update({'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null',
        'GIT_CONFIG_COUNT': '2', 'GIT_CONFIG_KEY_0': 'http.extraHeader',
        'GIT_CONFIG_VALUE_0': header, 'GIT_CONFIG_KEY_1': 'credential.helper',
        'GIT_CONFIG_VALUE_1': '', 'GIT_TERMINAL_PROMPT': '0',
        'GIT_SSH_COMMAND': f'ssh -i {ASSET_WORK}/user-key -o IdentitiesOnly=yes -o BatchMode=yes '
                           f'-o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={ASSET_WORK}/known-hosts'})
    return env


def repo_url(repo, protocol):
    return repo['clone_url'] if protocol == 'HTTP' else repo['ssh_url']


def collaborator(repo, actor, enabled):
    path = f'/repos/{repo["full_name"]}/collaborators/{actor["name"]}'
    if enabled:
        ok('PUT', path, {'permission': 'write'})
    else:
        status, body, _ = request('DELETE', path)
        if status not in (204, 404):
            raise RuntimeError(f'撤销协作者失败：{status} {body!r}')


def setup():
    ASSET_WORK.mkdir(mode=0o700, parents=True, exist_ok=True)
    ASSET_WORK.chmod(0o700)
    if not S['组织']:
        name = 'asset-space-' + secrets.token_hex(4)
        S['组织'] = ok('POST', '/orgs', {'username': name, 'full_name': '资产隔离验收'})
        save()
    org = S['组织']['username']
    for short in ('main', 'child', 'lfs'):
        if short not in S['仓库']:
            S['仓库'][short] = ok('POST', f'/orgs/{org}/repos', {
                'name': 'private-' + short, 'private': True, 'auto_init': True, 'default_branch': 'main'})
            save()
    actor = user()
    for repo in S['仓库'].values():
        collaborator(repo, actor, True)
    return actor


def git_config(folder):
    for key, value in [('user.name', '资产验收'), ('user.email', 'assets@example.invalid')]:
        r = run(['git', 'config', key, value], folder)
        assert r.returncode == 0, r.stderr


def seed_submodule():
    child, main = S['仓库']['child'], S['仓库']['main']
    child_dir = ASSET_WORK / 'seed-child'
    if not child_dir.exists():
        r = run(['git', 'clone', child['clone_url'], str(child_dir)], env=env_for(ADMIN)); assert r.returncode == 0, r.stderr
        git_config(child_dir)
        (child_dir / 'private-child.txt').write_text('私有子仓库初始内容\n')
        for args in (['git', 'add', '.'], ['git', 'commit', '-m', 'test: 准备私有子仓库'], ['git', 'push', 'origin', 'main']):
            r = run(args, child_dir, env_for(ADMIN)); assert r.returncode == 0, r.stderr
    main_dir = ASSET_WORK / 'seed-main'
    if not main_dir.exists():
        r = run(['git', 'clone', main['clone_url'], str(main_dir)], env=env_for(ADMIN)); assert r.returncode == 0, r.stderr
        git_config(main_dir)
        r = run(['git', '-c', 'protocol.file.allow=never', 'submodule', 'add', child['clone_url'], 'modules/private-child'], main_dir, env_for(ADMIN)); assert r.returncode == 0, r.stderr
        r = run(['git', 'commit', '-m', 'test: 添加私有子模块'], main_dir, env_for(ADMIN)); assert r.returncode == 0, r.stderr
        r = run(['git', 'push', 'origin', 'main'], main_dir, env_for(ADMIN)); assert r.returncode == 0, r.stderr


def add_child_content(label):
    folder = ASSET_WORK / ('admin-child-' + label)
    r = run(['git', 'clone', S['仓库']['child']['clone_url'], str(folder)], env=env_for(ADMIN)); assert r.returncode == 0, r.stderr
    git_config(folder)
    filename = 'new-' + label + '.txt'
    (folder / filename).write_text('撤权后新增的私有子仓库内容\n')
    for args in (['git', 'add', filename], ['git', 'commit', '-m', 'test: 增加撤权后私有内容'], ['git', 'push', 'origin', 'main']):
        r = run(args, folder, env_for(ADMIN)); assert r.returncode == 0, r.stderr
    return run(['git', 'rev-parse', 'HEAD'], folder).stdout.strip()


def submodules(actor):
    seed_submodule()
    for protocol in ('HTTP', 'SSH'):
        collaborator(S['仓库']['main'], actor, True); collaborator(S['仓库']['child'], actor, True)
        folder = ASSET_WORK / ('recursive-' + protocol.lower() + '-' + RUN_TAG)
        r = run(['git', 'clone', '--recurse-submodules', repo_url(S['仓库']['main'], protocol), str(folder)], env=env_for(actor))
        cloned = r.returncode == 0 and (folder / 'modules/private-child/private-child.txt').exists()
        record('私有子模块递归克隆', cloned, 协议=protocol, 预期='主仓库和子仓库均允许', 退出码=r.returncode, 输出=r.stderr[-1800:])
        if cloned:
            git_config(folder); git_config(folder / 'modules/private-child')
            (folder / 'main-change.txt').write_text('主仓库独立推送\n')
            run(['git', 'add', 'main-change.txt'], folder); run(['git', 'commit', '-m', 'test: 主仓库独立推送'], folder)
            branch = 'assets-main-' + protocol.lower() + '-' + RUN_TAG
            push_main = run(['git', 'push', repo_url(S['仓库']['main'], protocol), f'HEAD:refs/heads/{branch}'], folder, env_for(actor))
            child_folder = folder / 'modules/private-child'
            (child_folder / ('child-' + protocol.lower() + '-' + RUN_TAG + '.txt')).write_text('子仓库独立推送\n')
            run(['git', 'add', '.'], child_folder); run(['git', 'commit', '-m', 'test: 子仓库独立推送'], child_folder)
            child_branch = 'assets-child-' + protocol.lower() + '-' + RUN_TAG
            push_child = run(['git', 'push', repo_url(S['仓库']['child'], protocol), f'HEAD:refs/heads/{child_branch}'], child_folder, env_for(actor))
            record('主仓库与子仓库独立推送', push_main.returncode == 0 and push_child.returncode == 0,
                   协议=protocol, 主仓库退出码=push_main.returncode, 子仓库退出码=push_child.returncode,
                   输出=(push_main.stderr + push_child.stderr)[-1800:])

        collaborator(S['仓库']['main'], actor, False)
        denied = run(['git', 'clone', '--recurse-submodules', repo_url(S['仓库']['main'], protocol), str(ASSET_WORK / ('main-revoked-' + protocol.lower() + '-' + RUN_TAG))], env=env_for(actor))
        child_access = run(['git', 'ls-remote', repo_url(S['仓库']['child'], protocol)], env=env_for(actor))
        record('仅撤销主仓库权限', denied.returncode != 0 and child_access.returncode == 0, 协议=protocol,
               主仓库退出码=denied.returncode, 子仓库退出码=child_access.returncode, 输出=denied.stderr[-1400:])

        collaborator(S['仓库']['main'], actor, True); collaborator(S['仓库']['child'], actor, False)
        new_sha = add_child_content('revoked-' + protocol.lower() + '-' + RUN_TAG)
        plain = ASSET_WORK / ('child-revoked-plain-' + protocol.lower() + '-' + RUN_TAG)
        plain_result = run(['git', 'clone', repo_url(S['仓库']['main'], protocol), str(plain)], env=env_for(actor))
        recursive = ASSET_WORK / ('child-revoked-recursive-' + protocol.lower() + '-' + RUN_TAG)
        recursive_result = run(['git', 'clone', '--recurse-submodules', repo_url(S['仓库']['main'], protocol), str(recursive)], env=env_for(actor))
        object_absent = not recursive.exists() or run(['git', 'cat-file', '-e', new_sha], recursive).returncode != 0
        record('仅撤销子仓库权限', plain_result.returncode == 0 and recursive_result.returncode != 0 and object_absent,
               协议=protocol, 主仓库退出码=plain_result.returncode, 递归克隆退出码=recursive_result.returncode,
               未授权新对象SHA=new_sha, 新对象未取得=object_absent, 输出=recursive_result.stderr[-1800:])
        collaborator(S['仓库']['child'], actor, True)


def lfs_batch(repo, actor, operation, oid, size):
    path = f'/{repo["full_name"]}.git/info/lfs/objects/batch'
    return request('POST', path, {'operation': operation, 'transfers': ['basic'],
        'objects': [{'oid': oid, 'size': size}]}, actor, api=False,
        headers={'Accept': 'application/vnd.git-lfs+json', 'Content-Type': 'application/vnd.git-lfs+json'})


def action_request(action, actor, payload=None):
    headers = dict(action.get('header', {}))
    # urllib 不会在显式复制 batch 返回的 chunked 标头时替调用方编码 bytes。
    headers.pop('Transfer-Encoding', None)
    headers.pop('transfer-encoding', None)
    if payload is not None:
        headers['Content-Length'] = str(len(payload))
    secret = actor.get('token', actor['password'])
    headers.setdefault('Authorization', 'Basic ' + base64.b64encode(f'{actor["name"]}:{secret}'.encode()).decode())
    req = urllib.request.Request(action['href'], data=payload, headers=headers, method='PUT' if payload is not None else 'GET')
    try:
        res = urllib.request.urlopen(req, timeout=40)
    except urllib.error.HTTPError as exc:
        res = exc
    return res.status, res.read()


def lfs(actor):
    repo = S['仓库']['lfs']
    collaborator(repo, actor, True)
    content = ('Gitea LFS acceptance object private ' + RUN_TAG + '\n').encode()
    oid = hashlib.sha256(content).hexdigest()
    status, body, _ = lfs_batch(repo, actor, 'upload', oid, len(content))
    upload = body.get('objects', [{}])[0].get('actions', {}).get('upload') if isinstance(body, dict) else None
    up_status, _ = action_request(upload, actor, content) if upload else (0, b'')
    dstatus, dbody, _ = lfs_batch(repo, actor, 'download', oid, len(content))
    download = dbody.get('objects', [{}])[0].get('actions', {}).get('download') if isinstance(dbody, dict) else None
    get_status, got = action_request(download, actor) if download else (0, b'')
    record('LFS batch/object 上传下载', status == 200 and up_status in (200, 201, 202) and dstatus == 200 and get_status == 200 and got == content,
           协议='LFS HTTP', OID=oid, batch上传状态=status, 对象上传状态=up_status,
           batch下载状态=dstatus, 对象下载状态=get_status, 内容SHA256=hashlib.sha256(got).hexdigest() if got else '')

    pointer = f'version https://git-lfs.github.com/spec/v1\noid sha256:{oid}\nsize {len(content)}\n'
    for protocol in ('HTTP', 'SSH'):
        folder = ASSET_WORK / ('lfs-pointer-' + protocol.lower() + '-' + RUN_TAG)
        r = run(['git', 'clone', repo_url(repo, protocol), str(folder)], env=env_for(actor)); assert r.returncode == 0, r.stderr
        git_config(folder)
        (folder / 'asset.bin').write_text(pointer)
        (folder / '.gitattributes').write_text('asset.bin filter=lfs diff=lfs merge=lfs -text\n')
        run(['git', 'add', 'asset.bin', '.gitattributes'], folder); run(['git', 'commit', '-m', 'test: 提交 LFS 指针'], folder)
        branch = 'lfs-pointer-' + protocol.lower() + '-' + RUN_TAG
        pushed = run(['git', 'push', repo_url(repo, protocol), f'HEAD:refs/heads/{branch}'], folder, env_for(actor))
        verify = ASSET_WORK / ('lfs-pointer-verify-' + protocol.lower() + '-' + RUN_TAG)
        cloned = run(['git', 'clone', '--branch', branch, repo_url(repo, protocol), str(verify)], env=env_for(actor))
        record('LFS 指针真实 Git 推送', pushed.returncode == 0 and cloned.returncode == 0 and (verify / 'asset.bin').read_text() == pointer,
               协议=protocol, 推送退出码=pushed.returncode, 克隆退出码=cloned.returncode, OID=oid)

    collaborator(repo, actor, False)
    denied_status, denied_body, _ = lfs_batch(repo, actor, 'download', oid, len(content))
    new_content = b'new object after permission revocation\n'
    new_oid = hashlib.sha256(new_content).hexdigest()
    new_status, new_body, _ = lfs_batch(repo, actor, 'upload', new_oid, len(new_content))
    record('LFS 撤权后 batch 拒绝', denied_status in (401, 403, 404) and new_status in (401, 403, 404),
           协议='LFS HTTP', 已有OID下载状态=denied_status, 新OID上传状态=new_status,
           已有OID响应=str(denied_body)[:800], 新OID响应=str(new_body)[:800])
    collaborator(repo, actor, True)


def main():
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.touch(exist_ok=True)
    actor = setup()
    setting = lfs_server_setting()
    record('Git LFS 客户端与服务配置', setting == 'true', 宿主机='未安装', 服务容器客户端='未安装',
           启用前LFS_START_SERVER=S.get('LFS启用前配置', '未记录'), 服务端LFS_START_SERVER=setting,
           验收边界='使用真实 LFS HTTP batch/object 协议和真实 Git pointer 推送，不等同于 Git LFS 客户端 smudge/clean 闭环')
    submodules(actor)
    lfs(actor)
    rows = [json.loads(line) for line in OUT.read_text().splitlines()]
    print(json.dumps({'总数': len(rows), '通过': sum(r['结果'] == '通过' for r in rows),
                      '失败': sum(r['结果'] == '失败' for r in rows)}, ensure_ascii=False))


if __name__ == '__main__':
    main()
