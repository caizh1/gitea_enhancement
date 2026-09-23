#!/usr/bin/env python3
"""对本次专用隔离实例执行真实 Git/API 验收；凭据仅写入权限为 600 的临时目录。"""
import base64
from concurrent.futures import ThreadPoolExecutor
import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
WORK = Path('/tmp/gitea-user-acceptance-20260918')
OUT = ROOT / 'docs/evidence/user-permission-20260918'
BASE = 'http://127.0.0.1:3538'
CONTAINER = 'user-permission-20260918-gitea-1'
OUT.mkdir(parents=True, exist_ok=True)
SECRETS = json.loads((WORK / 'secrets.json').read_text())
STATE = WORK / 'fixtures.json'
S = json.loads(STATE.read_text()) if STATE.exists() else {'用户': {}, '组织': {}, '仓库': {}}
ADMIN = {'name': 'acceptance-admin', 'password': SECRETS['管理员密码']}
ROWS = []


def save():
    STATE.write_text(json.dumps(S, ensure_ascii=False, indent=2))
    STATE.chmod(0o600)


def api(method, path, data=None, user=None):
    user = user or ADMIN
    auth = 'token ' + user['token'] if 'token' in user else 'Basic ' + base64.b64encode((user['name'] + ':' + user['password']).encode()).decode()
    req = urllib.request.Request(BASE + '/api/v1' + path, data=json.dumps(data).encode() if data is not None else None, headers={'Authorization': auth, 'Content-Type': 'application/json'}, method=method)
    try:
        r = urllib.request.urlopen(req, timeout=40)
    except urllib.error.HTTPError as e:
        r = e
    raw = r.read()
    try:
        body = json.loads(raw)
    except ValueError:
        body = raw.decode(errors='replace')
    return r.status, body


def ok(method, path, data=None, user=None):
    status, body = api(method, path, data, user)
    if not 200 <= status < 300:
        raise RuntimeError(f'准备操作失败：{method} {path}，状态 {status}，响应 {body}')
    return body


def run(args, env=None, cwd=None):
    return subprocess.run(args, env=env, cwd=cwd, capture_output=True, text=True, timeout=90)


def record(case, action, passed, **evidence):
    row = {'场景': case, '检查': action, '结果': '通过' if passed else '失败', '时间': datetime.datetime.now(datetime.timezone.utc).isoformat(), **evidence}
    ROWS.append(row)
    with (OUT / (sys.argv[1] + '-results.jsonl')).open('a') as f:
        f.write(json.dumps(row, ensure_ascii=False) + '\n')
    print(case, action, row['结果'], flush=True)


def user(name):
    if name not in S['用户']:
        pwd = secrets.token_urlsafe(24)
        u = ok('POST', '/admin/users', {'username': name, 'password': pwd, 'email': name + '@example.invalid', 'must_change_password': False})
        S['用户'][name] = {'name': name, 'password': pwd, 'id': u['id']}
        save()
    u = S['用户'][name]
    if 'token' not in u:
        t = ok('POST', '/users/' + name + '/tokens', {'name': '隔离验收', 'scopes': ['all']}, u)
        u['token'] = t['sha1']
        save()
    key = WORK / (name + '-key')
    if not key.exists():
        r = run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(key)])
        assert r.returncode == 0, r.stderr
        k = ok('POST', '/user/keys', {'title': '隔离验收', 'key': Path(str(key) + '.pub').read_text()}, u)
        u['key_id'] = k['id']
        save()
    return u


def member(group, u, role=None, **extra):
    gid = S['组织'][group]['id']
    state = ok('GET', f'/governance/groups/{gid}')
    p = f'/governance/groups/{gid}/members/{u["id"]}'
    if role is None:
        return ok('DELETE', p + '?revision=' + str(state['revision']))
    return ok('PUT', p, {'role': role, 'revision': state['revision'], **extra})


def direct(repo, u, role=None):
    rid = S['仓库'][repo]['id']
    state = ok('GET', f'/governance/repositories/{rid}/members')
    p = f'/governance/repositories/{rid}/members/{u["id"]}'
    return ok('DELETE', p + '?revision=' + str(state['revision'])) if role is None else ok('PUT', p, {'role': role, 'revision': state['revision']})


def env_for(u):
    env = os.environ.copy()
    for k in list(env):
        if k.startswith('GIT_'):
            del env[k]
    secret = u.get('token', u.get('password', ''))
    header = 'Authorization: Basic ' + base64.b64encode((u['name'] + ':' + secret).encode()).decode()
    env.update({'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_CONFIG_COUNT': '2', 'GIT_CONFIG_KEY_0': 'http.extraHeader', 'GIT_CONFIG_VALUE_0': header, 'GIT_CONFIG_KEY_1': 'credential.helper', 'GIT_CONFIG_VALUE_1': '', 'GIT_TERMINAL_PROMPT': '0', 'GIT_SSH_COMMAND': f'ssh -i {WORK}/{u["name"]}-key -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={WORK}/known-hosts'})
    return env


def url(repo, protocol):
    r = S['仓库'][repo]
    return r['clone_url'] if protocol == 'HTTP' else r['ssh_url']


def refs(repo):
    r = run(['git', 'ls-remote', url(repo, 'HTTP')], env_for(ADMIN))
    assert r.returncode == 0, r.stderr
    return dict(line.split()[::-1] for line in r.stdout.splitlines())


def local(repo, u, protocol, suffix):
    folder = WORK / 'clones' / (u['name'] + '-' + protocol + '-' + suffix)
    folder.parent.mkdir(exist_ok=True)
    r = run(['git', 'clone', url(repo, protocol), str(folder)], env_for(u))
    return folder, r


def commit(folder, label):
    (folder / 'acceptance.txt').write_text('真实权限验收：' + label + '\n')
    for args in [['git', 'config', 'user.name', '验收用户'], ['git', 'config', 'user.email', 'acceptance@example.invalid'], ['git', 'add', 'acceptance.txt'], ['git', 'commit', '-m', 'test: 验证真实开发权限']]:
        r = run(args, env_for(ADMIN), folder)
        assert r.returncode == 0, r.stderr
    return run(['git', 'rev-parse', 'HEAD'], cwd=folder).stdout.strip()


def probe(case, repo, u, readable, writable, label='default'):
    for protocol in ['HTTP', 'SSH']:
        suffix = case.lower() + '-' + repo + '-' + label
        folder, r = local(repo, u, protocol, suffix)
        read_ok = r.returncode == 0 and (folder / 'README.md').exists()
        record(case, '克隆', read_ok if readable else r.returncode != 0 and not (folder / 'README.md').exists(), 协议=protocol, 用户=u['name'], 仓库=repo, 变体=label, 预期='允许' if readable else '拒绝', 退出码=r.returncode, 输出=r.stderr[-1800:])
        if not read_ok:
            folder = WORK / 'clones' / (u['name'] + '-' + protocol + '-' + suffix + '-seed')
            r = run(['git', 'clone', url(repo, 'HTTP'), str(folder)], env_for(ADMIN))
            assert r.returncode == 0, r.stderr
        sha = commit(folder, suffix + '-' + protocol)
        branch = 'feature/' + u['name'] + '-' + suffix + '-' + protocol.lower()
        before = refs(repo)
        r = run(['git', 'push', url(repo, protocol), 'HEAD:refs/heads/' + branch], env_for(u), folder)
        after = refs(repo)
        success = r.returncode == 0 and after.get('refs/heads/' + branch) == sha
        denied = r.returncode != 0 and before == after
        record(case, '推送', success if writable else denied, 协议=protocol, 用户=u['name'], 仓库=repo, 变体=label, 预期='允许' if writable else '拒绝', 退出码=r.returncode, 本地SHA=sha, 操作前=before, 操作后=after, 输出=r.stderr[-1800:])
        if writable and success:
            check = WORK / 'clones' / (u['name'] + '-' + protocol + '-' + suffix + '-verify')
            r = run(['git', 'clone', '--branch', branch, '--single-branch', url(repo, protocol), str(check)], env_for(u))
            record(case, '全新克隆内容核对', r.returncode == 0 and (check / 'acceptance.txt').read_bytes() == (folder / 'acceptance.txt').read_bytes(), 协议=protocol, 用户=u['name'], 仓库=repo, 本地SHA=sha)


def setup():
    status, _ = api('GET', '/user')
    if status != 200:
        r = run(['docker', 'exec', '-u', 'git', CONTAINER, 'gitea', 'admin', 'user', 'create', '--username', ADMIN['name'], '--password', ADMIN['password'], '--email', 'acceptance-admin@example.invalid', '--admin', '--must-change-password=false'])
        assert r.returncode == 0, '隔离管理员创建失败'
    for path, parent in [('alpha', ''), ('alpha/platform', 'alpha'), ('alpha/platform/sdk', 'alpha/platform'), ('alpha/business', 'alpha'), ('beta', '')]:
        if path not in S['组织']:
            S['组织'][path] = ok('POST', '/governance/groups', {'path': path.split('/')[-1], 'parent_id': S['组织'][parent]['id'] if parent else 0, 'name': '验收组织', 'visibility': 2})
            save()
    for short, path in [('root', 'alpha'), ('platform', 'alpha/platform'), ('sdk', 'alpha/platform/sdk'), ('business', 'alpha/business'), ('beta', 'beta'), ('public', 'alpha')]:
        if short not in S['仓库']:
            S['仓库'][short] = ok('POST', '/orgs/' + path + '/repos', {'name': 'dev-' + short, 'private': short != 'public', 'auto_init': True, 'default_branch': 'main'})
            save()
    for role in [5, 10, 15, 20, 30, 40, 50]:
        u = user('role-' + str(role))
        member('alpha', u, role)
    for name in ['outsider', 'approver-one', 'approver-two', 'check-bot']:
        u = user(name)
        if name != 'outsider':
            member('alpha', u, 30)
    info = {'源码': run(['git', 'rev-parse', 'HEAD'], cwd=ROOT).stdout.strip(), '程序': ok('GET', '/version'), '数据库版本': run(['docker', 'exec', 'user-permission-20260918-database-1', 'psql', '-U', 'gitea', '-d', 'gitea', '-tAc', 'select version from version']).stdout.strip(), '二进制SHA256': hashlib.sha256((WORK / 'gitea').read_bytes()).hexdigest(), '环境': '独立 PostgreSQL，Linux amd64 服务端，macOS Git 客户端', '地址': BASE, '静态资源': '复用已有前端产物；web_src、package.json、锁文件、vite 配置与对应源码逐文件相同；模板和 options 来自当前提交'}
    (OUT / 'baseline.json').write_text(json.dumps(info, ensure_ascii=False, indent=2))
    print('隔离数据准备完成', flush=True)


def roles():
    for role, case in [(30, 'A06'), (5, 'A02'), (10, 'A03'), (15, 'A04'), (20, 'A05'), (40, 'A07'), (50, 'A07')]:
        u = user('role-' + str(role))
        probe(case, 'sdk', u, role >= 20, role >= 30)
        status, data = api('GET', '/user/repos', user=u)
        present = status == 200 and any(x['id'] == S['仓库']['sdk']['id'] for x in data)
        record(case, '个人仓库列表', status == 200 and present == (role >= 10), 用户=u['name'], HTTP状态=status, 包含目标=present)
    probe('A01', 'sdk', user('outsider'), False, False)
    probe('A10', 'public', user('outsider'), True, False)


def inheritance():
    u = user('inherit-only')
    member('alpha/platform', u, 30)
    for repo in ['platform', 'sdk']:
        probe('B02', repo, u, True, True)
    for repo in ['root', 'business', 'beta']:
        probe('B03', repo, u, False, False)
    member('alpha/platform', u)
    member('alpha', u, 30)
    for repo in ['platform', 'sdk']:
        probe('B01', repo, u, True, True)
    member('alpha/platform', u, 20)
    probe('B06', 'sdk', u, True, True)
    member('alpha', u, 20)
    member('alpha/platform', u, 30)
    probe('B05', 'sdk', u, True, True)
    probe('B05', 'business', u, True, False)
    member('alpha', u)
    member('alpha/platform', u)
    direct('sdk', u, 30)
    probe('B07', 'sdk', u, True, True)
    probe('B07', 'platform', u, False, False)
    member('alpha', u, 30)
    member('alpha', u)
    probe('B08', 'sdk', u, True, True, 'one-remaining')
    direct('sdk', u)
    probe('B08', 'sdk', u, False, False, 'all-revoked')


def protected():
    repo = S['仓库']['sdk']
    path = '/repos/' + repo['full_name']
    protection = ok('POST', path + '/branch_protections', {'rule_name': 'main', 'enable_push': False, 'enable_force_push': False, 'required_approvals': 2, 'enable_status_check': True, 'status_check_contexts': ['acceptance/check'], 'dismiss_stale_approvals': True, 'block_admin_merge_override': True})
    ok('PUT', f'/governance/repositories/{repo["id"]}/approval-settings', {'prevent_author': True, 'prevent_overrides': True, 'reset_on_change': True, 'revision': 0})
    (OUT / 'branch-protection.json').write_text(json.dumps(protection, ensure_ascii=False, indent=2))
    for name in ['role-30', 'role-40', 'role-50']:
        u = user(name)
        for protocol in ['HTTP', 'SSH']:
            folder, r = local('sdk', u, protocol, 'protected')
            assert r.returncode == 0, r.stderr
            commit(folder, '保护分支验证')
            for label, args in [('普通推送', ['HEAD:refs/heads/main']), ('强推', ['--force', 'HEAD:refs/heads/main']), ('删除', [':refs/heads/main']), ('删除失败后重建', ['HEAD:refs/heads/main'])]:
                before = refs('sdk')
                r = run(['git', 'push', url('sdk', protocol), *args], env_for(u), folder)
                after = refs('sdk')
                record('D06' if name == 'role-30' else 'A07', label, r.returncode != 0 and before == after, 协议=protocol, 用户=name, 退出码=r.returncode, 操作前=before, 操作后=after, 输出=r.stderr[-1800:])


def pulls():
    u = user('role-30')
    repo = S['仓库']['sdk']
    path = '/repos/' + repo['full_name']
    for protocol in ['HTTP', 'SSH']:
        folder, r = local('sdk', u, protocol, 'pulls')
        assert r.returncode == 0, r.stderr
        branch = 'feature/approval-' + protocol.lower()
        sha = commit(folder, '审批闭环-' + protocol)
        r = run(['git', 'push', url('sdk', protocol), 'HEAD:refs/heads/' + branch], env_for(u), folder)
        assert r.returncode == 0, r.stderr
        pr = ok('POST', path + '/pulls', {'title': '真实用户审批验收-' + protocol, 'head': branch, 'base': 'main', 'body': '两人审批与最新提交检查验收'}, u)
        pp = path + '/pulls/' + str(pr['number'])
        record('A06', '推送后创建PR', pr['head']['sha'] == sha, 协议=protocol, PR编号=pr['number'], 提交SHA=sha)
        before = refs('sdk')
        status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': sha}, u)
        record('E02', '审批与检查缺失拒绝合并', status >= 400 and refs('sdk') == before, 协议=protocol, HTTP状态=status, 响应=body, 操作前=before, 操作后=refs('sdk'))
        for name in ['approver-one', 'approver-two']:
            status, body = api('POST', pp + '/reviews', {'event': 'APPROVED', 'body': '已核对本次提交'}, user(name))
            record('E01', '独立用户审批-' + name, status in [200, 201], 协议=protocol, HTTP状态=status, PR编号=pr['number'])
        status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': sha}, u)
        record('E02', '两人审批后缺少状态检查仍拒绝', status >= 400 and refs('sdk') == before, 协议=protocol, HTTP状态=status, 响应=body)
        ok('POST', path + '/statuses/' + sha, {'state': 'success', 'context': 'acceptance/check', 'description': '独立检查账号核对通过'}, user('check-bot'))
        status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': sha}, u)
        after = refs('sdk')
        state = ok('GET', pp, user=u)
        record('E01', '两人审批及检查通过后合并', status in [200, 201] and state['merged'] and before['refs/heads/main'] != after['refs/heads/main'], 协议=protocol, HTTP状态=status, 响应=body, PR编号=pr['number'], 操作前=before, 操作后=after)
        if state['merged']:
            r = run(['git', 'fetch', url('sdk', protocol), 'main'], env_for(u), folder)
            content = run(['git', 'show', 'FETCH_HEAD:acceptance.txt'], cwd=folder)
            ancestor = run(['git', 'merge-base', '--is-ancestor', sha, 'FETCH_HEAD'], cwd=folder)
            record('E01', '拉取合并内容及祖先关系', r.returncode == 0 and content.stdout == (folder / 'acceptance.txt').read_text() and ancestor.returncode == 0, 协议=protocol, 提交SHA=sha, 目标SHA=after['refs/heads/main'])


def corrected():
    for role, case in [(10, 'A03'), (15, 'A04')]:
        u = user('role-' + str(role))
        status, data = api('GET', '/user/repos', user=u)
        present = status == 200 and any(x['id'] == S['仓库']['sdk']['id'] for x in data)
        record(case, '个人仓库列表预期纠正', present, 用户=u['name'], HTTP状态=status, 包含目标=present, 原因='源码允许有事项或PR读取能力的仓库出现在列表；代码读写另行拒绝，原脚本预期错误')
    if 'public-space' not in S['组织']:
        S['组织']['public-space'] = ok('POST', '/governance/groups', {'path': 'public-space', 'visibility': 0})
        S['仓库']['public'] = ok('POST', '/orgs/public-space/repos', {'name': 'dev-public', 'private': False, 'auto_init': True, 'default_branch': 'main'})
        save()
    assert not S['仓库']['public']['private'], '公开样本实际不是公开仓库'
    record('A10', '公开样本前置条件纠正', True, 原因='私有父组将初始样本强制为私有；重新在公开组织准备公开仓库，保留原始失败证据')
    probe('A10', 'public', user('outsider'), True, False, 'public-parent')


def share(repo, group, role=None, **extra):
    rid, gid = S['仓库'][repo]['id'], S['组织'][group]['id']
    state = ok('GET', f'/governance/repositories/{rid}/shares')
    path = f'/governance/repositories/{rid}/shares/{gid}'
    return ok('DELETE', path + '?revision=' + str(state['revision'])) if role is None else ok('PUT', path, {'max_role': role, 'revision': state['revision'], **extra})


def sharing():
    u = user('shared-user')
    # 邀请组独立于其他测试账号，防止共享污染原有角色矩阵。
    if 'partner' not in S['组织']:
        S['组织']['partner'] = ok('POST', '/governance/groups', {'path': 'partner', 'visibility': 2})
        save()
    for case, role, ceiling, readable, writable in [('C01', 30, 20, True, False), ('C02', 20, 30, True, False), ('C03', 30, 30, True, True), ('C04', 15, 20, False, False), ('C04', 20, 15, False, False), ('C04', 15, 30, False, False)]:
        member('partner', u, role)
        share('sdk', 'partner', ceiling)
        probe(case, 'sdk', u, readable, writable, f'{role}-{ceiling}')
    member('partner', u, 30)
    share('sdk', 'partner', 30)
    probe('C05', 'platform', u, False, False)
    share('sdk', 'partner')
    probe('C06', 'sdk', u, False, False, 'share-revoked')


def revocation():
    u = user('revocation-user')
    member('alpha', u, 30)
    folders = {}
    for protocol in ['HTTP', 'SSH']:
        folder, r = local('sdk', u, protocol, 'old-clone')
        assert r.returncode == 0, r.stderr
        sha = commit(folder, '离线提交-' + protocol)
        folders[protocol] = (folder, sha)
        r = run(['git', 'push', url('sdk', protocol), 'HEAD:refs/heads/feature/revoke-control-' + protocol.lower()], env_for(u), folder)
        record('F06', '撤权前对照推送', r.returncode == 0, 协议=protocol)
    member('alpha', u)
    # 创建撤权后才存在的新对象，排除旧克隆已有对象对读取结果的干扰。
    control, r = local('platform', user('role-30'), 'HTTP', 'new-object')
    assert r.returncode == 0, r.stderr
    newsha = commit(control, '撤权后新增私有内容')
    r = run(['git', 'push', url('sdk', 'HTTP'), 'HEAD:refs/heads/feature/after-revocation'], env_for(user('role-30')), control)
    assert r.returncode == 0, r.stderr
    for protocol, (folder, sha) in folders.items():
        r = run(['git', 'fetch', url('sdk', protocol), 'refs/heads/feature/after-revocation'], env_for(u), folder)
        absent = run(['git', 'cat-file', '-e', newsha], cwd=folder).returncode != 0
        record('C06', '旧克隆拒绝读取撤权后新对象', r.returncode != 0 and absent, 协议=protocol, 新对象SHA=newsha, 退出码=r.returncode, 输出=r.stderr[-1600:])
        before = refs('sdk')
        r = run(['git', 'push', url('sdk', protocol), 'HEAD:refs/heads/feature/offline-' + protocol.lower()], env_for(u), folder)
        record('F06', '离线提交保留但撤权拒绝推送', r.returncode != 0 and refs('sdk') == before and run(['git', 'rev-parse', 'HEAD'], cwd=folder).stdout.strip() == sha, 协议=protocol, 本地SHA=sha, 操作前=before, 操作后=refs('sdk'), 输出=r.stderr[-1600:])
    member('alpha', u, 30)
    for protocol, (folder, sha) in folders.items():
        r = run(['git', 'push', url('sdk', protocol), 'HEAD:refs/heads/feature/offline-' + protocol.lower()], env_for(u), folder)
        record('F06', '恢复授权后原提交交付', r.returncode == 0 and refs('sdk').get('refs/heads/feature/offline-' + protocol.lower()) == sha, 协议=protocol)


def credentials():
    u = user('credential-user')
    member('alpha', u, 30)
    folder, r = local('sdk', u, 'HTTP', 'credential')
    assert r.returncode == 0, r.stderr
    commit(folder, '凭据边界')
    basic = {'name': u['name'], 'password': u['password']}
    t = ok('POST', '/users/' + u['name'] + '/tokens', {'name': '只读验收', 'scopes': ['read:repository']}, basic)
    limited = {**u, 'token': t['sha1']}
    for case, actor, label in [('D02', limited, '只读Token'), ('D01', user('role-20'), '有Token无写权限')]:
        before = refs('sdk')
        r = run(['git', 'push', url('sdk', 'HTTP'), 'HEAD:refs/heads/feature/token-denied'], env_for(actor), folder)
        record(case, label, r.returncode != 0 and refs('sdk') == before, 退出码=r.returncode, 输出=r.stderr[-1600:])
    r = run(['git', 'push', url('sdk', 'HTTP'), 'HEAD:refs/heads/feature/token-control'], env_for(u), folder)
    record('D02', '足够范围Token对照', r.returncode == 0, 退出码=r.returncode)
    ok('DELETE', '/users/' + u['name'] + '/tokens/' + str(t['id']), user=basic)
    for protocol, actor in [('HTTP', limited), ('SSH', u)]:
        if protocol == 'SSH':
            ok('DELETE', '/user/keys/' + str(u['key_id']), user=u)
        before = refs('sdk')
        for action, args in [('读取', ['ls-remote', url('sdk', protocol)]), ('推送', ['push', url('sdk', protocol), 'HEAD:refs/heads/feature/revoked-credential'])]:
            r = run(['git', *args], env_for(actor), folder)
            record('D03', '撤销凭据后' + action, r.returncode != 0 and refs('sdk') == before, 协议=protocol, 退出码=r.returncode, 输出=r.stderr[-1600:])
    for protocol in ['HTTP', 'SSH']:
        actor = user('role-20')
        for key, value in [('user.name', 'acceptance-admin'), ('user.email', 'acceptance-admin@example.invalid')]:
            assert run(['git', 'config', key, value], cwd=folder).returncode == 0
        r = run(['git', 'commit', '--allow-empty', '-m', 'test: 验证作者身份不授予权限'], env_for(actor), folder)
        before = refs('sdk')
        r = run(['git', 'push', url('sdk', protocol), 'HEAD:refs/heads/feature/forged-author'], env_for(actor), folder)
        record('D04', '低权限账号伪装提交作者', r.returncode != 0 and refs('sdk') == before, 协议=protocol, 实际认证身份=actor['name'], 提交作者='acceptance-admin', 输出=r.stderr[-1600:])


def boundaries():
    path = '/repos/' + S['仓库']['sdk']['full_name']
    u = user('role-30')
    tagrule = ok('POST', path + '/tag_protections', {'name_pattern': 'release-*', 'whitelist_usernames': ['role-50']})
    for protocol in ['HTTP', 'SSH']:
        folder, r = local('sdk', u, protocol, 'boundaries')
        assert r.returncode == 0, r.stderr
        sha = commit(folder, '多引用与标签-' + protocol)
        for atomic in [False, True]:
            before = refs('sdk')
            branch = 'feature/multi-' + protocol.lower() + str(atomic).lower()
            r = run(['git', 'push', *(['--atomic'] if atomic else []), url('sdk', protocol), 'HEAD:refs/heads/' + branch, 'HEAD:refs/heads/main'], env_for(u), folder)
            after = refs('sdk')
            passed = r.returncode != 0 and before['refs/heads/main'] == after['refs/heads/main'] and (not atomic or before == after)
            record('D08', '原子多引用' if atomic else '普通多引用', passed, 协议=protocol, 操作前=before, 操作后=after, 输出=r.stderr[-1800:])
        for name, allowed in [('role-30', False), ('role-50', True)]:
            actor = user(name)
            tag = 'release-' + protocol.lower() + '-' + name
            for action, refspec in [('创建', 'HEAD:refs/tags/' + tag), ('删除', ':refs/tags/' + tag)]:
                before = refs('sdk')
                r = run(['git', 'push', url('sdk', protocol), refspec], env_for(actor), folder)
                after = refs('sdk')
                valid = (r.returncode == 0 and (after.get('refs/tags/' + tag) == sha if action == '创建' else 'refs/tags/' + tag not in after)) if allowed else r.returncode != 0 and before == after
                record('D07', '保护标签' + action, valid, 协议=protocol, 用户=name, 操作前=before, 操作后=after, 输出=r.stderr[-1600:])
    for name in ['role-20', 'role-30']:
        actor = user(name)
        for branch in ['main', 'feature/file-api-' + name]:
            before = refs('sdk')
            data = {'content': base64.b64encode('文件接口验收'.encode()).decode(), 'message': 'test: 文件接口权限', 'branch': 'main'}
            if branch != 'main':
                data['new_branch'] = branch
            status, body = api('POST', path + '/contents/api-' + name + '.txt', data, actor)
            allowed = name == 'role-30' and branch != 'main'
            record('D09', '文件API写入', status == 201 if allowed else status >= 400 and refs('sdk') == before, 用户=name, 分支=branch, HTTP状态=status, 响应=body)
    # 账号禁用与仓库归档分别验证，使用独立对象以免污染其他用例。
    actor = user('disabled-user')
    member('alpha', actor, 30)
    ok('PATCH', '/admin/users/' + actor['name'], {'login_name': actor['name'], 'prohibit_login': True})
    probe('D03', 'sdk', actor, False, False, 'account-disabled')
    ok('PATCH', '/admin/users/' + actor['name'], {'login_name': actor['name'], 'prohibit_login': False})
    archive = ok('POST', '/orgs/alpha/repos', {'name': 'dev-archive', 'private': True, 'auto_init': True, 'default_branch': 'main'})
    S['仓库']['archive'] = archive
    save()
    ap = '/repos/' + archive['full_name']
    ok('PATCH', ap, {'archived': True})
    probe('C10', 'archive', u, True, False, 'archived')
    ok('PATCH', ap, {'archived': False})
    probe('C10', 'archive', u, True, True, 'restored')


def lifecycle():
    u = user('lifecycle-user')
    member('alpha/platform/sdk', u, 30)
    probe('B04', 'sdk', u, True, True)
    probe('B04', 'platform', u, False, False)
    member('alpha/platform/sdk', u)
    # 原生协作者与治理来源分别保留一次。
    path = '/repos/' + S['仓库']['sdk']['full_name']
    member('alpha', u, 30)
    ok('PUT', path + '/collaborators/' + u['name'], {'permission': 'write'})
    member('alpha', u)
    probe('B09', 'sdk', u, True, True, 'native-only')
    member('alpha', u, 30)
    ok('DELETE', path + '/collaborators/' + u['name'])
    probe('B09', 'sdk', u, True, True, 'governance-only')
    member('alpha', u)
    probe('B09', 'sdk', u, False, False, 'none')
    gid = S['组织']['alpha']['id']
    state = ok('GET', f'/governance/groups/{gid}')
    role = ok('POST', f'/governance/groups/{gid}/roles', {'name': '事项管理专用', 'base_role': 5, 'abilities': ['write_issues'], 'revision': state['revision']})
    member('alpha', u, 5, custom_role_id=role['id'])
    probe('B10', 'sdk', u, False, False)
    state = ok('GET', f'/governance/groups/{gid}')
    ok('PUT', f'/governance/groups/{gid}/roles/{role["id"]}', {'name': '窄代码能力', 'base_role': 5, 'abilities': ['read_code', 'push_code'], 'revision': state['revision']})
    probe('C11', 'sdk', u, True, True, 'before')
    state = ok('GET', f'/governance/groups/{gid}')
    ok('PUT', f'/governance/groups/{gid}/roles/{role["id"]}', {'name': '窄代码能力', 'base_role': 5, 'abilities': ['read_code'], 'revision': state['revision']})
    probe('C11', 'sdk', u, True, False, 'after')
    member('alpha', u)
    member('alpha/platform', u, 50)
    for target in ['alpha', 'alpha/business']:
        groupid = S['组织'][target]['id']
        old = ok('GET', f'/governance/groups/{groupid}')
        status, body = api('PUT', f'/governance/groups/{groupid}/members/{user("outsider")["id"]}', {'role': 50, 'revision': old['revision']}, u)
        new = ok('GET', f'/governance/groups/{groupid}')
        record('B11', '子组Owner越界授权', status >= 400 and old['revision'] == new['revision'], 目标=target, HTTP状态=status, 响应=body)
    member('alpha/platform', u)
    # 到期依靠真实服务端时钟，等待只用于跨越明确到期点。
    expiry = int(time.time()) + 5
    member('alpha', u, 30, expires_unix=expiry)
    r = run(['git', 'ls-remote', url('sdk', 'HTTP')], env_for(u))
    record('C06', '成员到期前读取', r.returncode == 0, 到期时间=expiry)
    time.sleep(max(0, expiry + 1 - time.time()))
    probe('C06', 'sdk', u, False, False, 'membership-expired')
    # 修正保护标签删除的负向样本：目标必须真实存在。
    for protocol in ['HTTP', 'SSH']:
        folder, r = local('sdk', user('role-50'), protocol, 'tag-delete-control')
        assert r.returncode == 0, r.stderr
        tag = 'refs/tags/release-existing-' + protocol.lower()
        r = run(['git', 'push', url('sdk', protocol), 'HEAD:' + tag], env_for(user('role-50')), folder)
        assert r.returncode == 0, r.stderr
        before = refs('sdk')
        r = run(['git', 'push', url('sdk', protocol), ':' + tag], env_for(user('role-30')), folder)
        record('D07', '拒绝删除实际存在的保护标签', r.returncode != 0 and refs('sdk') == before, 协议=protocol, 操作前=before, 操作后=refs('sdk'), 输出=r.stderr[-1600:])


def races():
    u = user('race-user')
    if 'race' not in S['仓库']:
        S['仓库']['race'] = ok('POST', '/orgs/alpha/repos', {'name': 'dev-race', 'private': True, 'auto_init': True, 'default_branch': 'main'})
        save()
    hook = '/data/git/repositories/alpha/dev-race.git/hooks/reference-transaction'
    original = run(['docker', 'exec', CONTAINER, 'cat', hook]).stdout
    assert 'Gitea 原生治理引用事务入口' in original

    def install(text):
        p = subprocess.run(['docker', 'exec', '-i', CONTAINER, 'sh', '-c', 'cat > "$1" && chmod 755 "$1"', 'sh', hook], input=text, text=True, capture_output=True)
        assert p.returncode == 0, p.stderr

    for protocol in ['HTTP', 'SSH']:
        member('alpha', u, 30)
        folder, r = local('race', u, protocol, 'race')
        assert r.returncode == 0, r.stderr
        sha = commit(folder, '引用写入前撤权-' + protocol)
        gate = '/tmp/acceptance-gate-' + protocol.lower()
        # 仅在本次隔离仓库拦截原生引用准备入口，保留实际鉴权及写入实现。
        pause = f'if [ "$1" = prepared ]; then\n  touch {gate}.reached\n  for n in $(seq 1 25); do [ -f {gate}.release ] && break; sleep 1; done\nfi\n'
        install(original.replace('exec /usr/local/bin/gitea', pause + 'exec /usr/local/bin/gitea'))
        before = refs('race')
        proc = subprocess.Popen(['git', 'push', url('race', protocol), 'HEAD:refs/heads/feature/race-' + protocol.lower()], cwd=folder, env=env_for(u), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            reached = False
            for _ in range(40):
                if run(['docker', 'exec', CONTAINER, 'test', '-f', gate + '.reached']).returncode == 0:
                    reached = True
                    break
                if proc.poll() is not None:
                    break
                time.sleep(0.2)
            if not reached:
                raise RuntimeError('未到达受控暂停点，不能认定并发验收完成')
            revoked_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
            member('alpha', u)
            run(['docker', 'exec', CONTAINER, 'touch', gate + '.release'])
            stdout, stderr = proc.communicate(timeout=40)
            after = refs('race')
            record('F01', '引用准备前撤权先完成', proc.returncode != 0 and before == after, 协议=protocol, 暂停点='原生reference-transaction prepared处理前，pre-receive之后', 撤权时间=revoked_at, 本地SHA=sha, 操作前=before, 操作后=after, 输出=stderr[-2200:])
        finally:
            run(['docker', 'exec', CONTAINER, 'touch', gate + '.release'])
            if proc.poll() is None:
                proc.communicate(timeout=40)
            install(original)
        member('alpha', u, 30)
        before = refs('race')
        # 中断发生在原生 prepared 已成功后，模拟引用准备完成但最终写入失败。
        wrapped = original.replace('exec /usr/local/bin/gitea', '/usr/local/bin/gitea') + '\nresult=$?\n[ "$result" -eq 0 ] || exit "$result"\nif [ "$1" = prepared ]; then exit 42; fi\n'
        install(wrapped)
        try:
            r = run(['git', 'push', url('race', protocol), 'HEAD:refs/heads/feature/interrupted-' + protocol.lower()], env_for(u), folder)
            after = refs('race')
            record('F03', '原生引用准备成功后注入失败', r.returncode != 0 and before == after, 协议=protocol, 操作前=before, 操作后=after, 输出=r.stderr[-2200:])
        finally:
            install(original)
        r = run(['git', 'push', url('race', protocol), 'HEAD:refs/heads/feature/interrupted-' + protocol.lower()], env_for(u), folder)
        record('F03', '恢复原Hook后原提交可交付', r.returncode == 0 and refs('race').get('refs/heads/feature/interrupted-' + protocol.lower()) == sha, 协议=protocol, 输出=r.stderr[-1600:])
    # 两个工作副本从同一主分支并发更新同一引用，只允许一个成功。
    copies = []
    for n in [1, 2]:
        folder, r = local('race', u, 'HTTP', 'concurrent-' + str(n))
        assert r.returncode == 0, r.stderr
        sha = commit(folder, '并发提交-' + str(n))
        copies.append((folder, sha))
    processes = [subprocess.Popen(['git', 'push', url('race', 'HTTP'), 'HEAD:refs/heads/main'], cwd=f, env=env_for(u), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) for f, _ in copies]
    outputs = [p.communicate(timeout=60) for p in processes]
    passed = sum(p.returncode == 0 for p in processes) == 1
    winner = next((copies[i][1] for i, p in enumerate(processes) if p.returncode == 0), None)
    record('F04', '两个真实客户端并发推送同一引用', passed and refs('race')['refs/heads/main'] == winner, 协议='HTTP', 退出码=[p.returncode for p in processes], 远端SHA=refs('race')['refs/heads/main'], 输出=[x[1][-1600:] for x in outputs])


def race_confirmation():
    u = user('race-confirm-user')
    direct('race', u, 30)
    folder, r = local('race', u, 'HTTP', 'confirmation')
    assert r.returncode == 0, r.stderr
    sha = commit(folder, '独立账号直接授权撤销反证')
    hook = '/data/git/repositories/alpha/dev-race.git/hooks/reference-transaction'
    original = run(['docker', 'exec', CONTAINER, 'cat', hook]).stdout
    marker = '/tmp/acceptance-confirm-' + secrets.token_hex(5)
    pause = f'if [ "$1" = prepared ]; then\n touch {marker}.reached\n for n in $(seq 1 40); do [ -f {marker}.release ] && break; sleep 1; done\nfi\n'
    wrapped = original.replace('exec /usr/local/bin/gitea', pause + 'exec /usr/local/bin/gitea')
    def install(content):
        p = subprocess.run(['docker', 'exec', '-i', CONTAINER, 'sh', '-c', 'cat > "$1" && chmod 755 "$1"', 'sh', hook], input=content, capture_output=True, text=True)
        assert p.returncode == 0, p.stderr
    install(wrapped)
    before = refs('race')
    proc = subprocess.Popen(['git', 'push', url('race', 'HTTP'), 'HEAD:refs/heads/feature/confirmed-revocation'], cwd=folder, env=env_for(u), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        reached = False
        for _ in range(50):
            if run(['docker', 'exec', CONTAINER, 'test', '-f', marker + '.reached']).returncode == 0:
                reached = True
                break
            time.sleep(0.2)
        assert reached, '反证暂停点未到达'
        direct('race', u)
        removed_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
        status, body = api('GET', '/repos/' + S['仓库']['race']['full_name'], user=u)
        fresh = run(['git', 'ls-remote', url('race', 'HTTP')], env_for(u))
        me = ok('GET', '/user', user=u)
        members = ok('GET', f'/governance/repositories/{S["仓库"]["race"]["id"]}/members')
        record('F01', '反证：撤权已完成且新请求已拒绝', status == 404 and fresh.returncode != 0 and not me['is_admin'], 用户=u['name'], HTTP状态=status, 新Git请求退出码=fresh.returncode, 是否管理员=me['is_admin'], 撤权完成时间=removed_at, 成员状态=members)
        run(['docker', 'exec', CONTAINER, 'touch', marker + '.release'])
        stdout, stderr = proc.communicate(timeout=50)
        after = refs('race')
        record('F01', '反证后恢复在途请求', proc.returncode != 0 and before == after, 用户=u['name'], 协议='HTTP', 本地SHA=sha, 退出码=proc.returncode, 撤权完成时间=removed_at, 操作前=before, 操作后=after, 输出=stderr[-2200:])
    finally:
        run(['docker', 'exec', CONTAINER, 'touch', marker + '.release'])
        if proc.poll() is None:
            proc.communicate(timeout=50)
        install(original)


def approval_edges():
    repo = S['仓库']['sdk']
    path = '/repos/' + repo['full_name']
    actor = user('role-30')
    folder, r = local('sdk', actor, 'HTTP', 'approval-edges')
    assert r.returncode == 0, r.stderr
    sha = commit(folder, '审批边界第一版')
    branch = 'feature/approval-edges'
    r = run(['git', 'push', url('sdk', 'HTTP'), 'HEAD:refs/heads/' + branch], env_for(actor), folder)
    assert r.returncode == 0, r.stderr
    pr = ok('POST', path + '/pulls', {'title': '审批边界真实验收', 'head': branch, 'base': 'main'}, actor)
    pp = path + '/pulls/' + str(pr['number'])
    status, body = api('POST', pp + '/reviews', {'event': 'APPROVED', 'body': '作者自批反证'}, actor)
    record('E08', '禁止作者自批', status >= 400, HTTP状态=status, 响应=body)
    before = refs('sdk')
    for name in ['approver-one', 'approver-two']:
        ok('POST', pp + '/reviews', {'event': 'APPROVED', 'body': '第一版已核对'}, user(name))
    ok('POST', path + '/statuses/' + sha, {'state': 'failure', 'context': 'acceptance/check', 'description': '确定性失败检查'}, user('check-bot'))
    status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': sha}, actor)
    record('E02', '两票齐全但状态检查失败', status >= 400 and refs('sdk') == before, HTTP状态=status, 响应=body)
    ok('POST', path + '/statuses/' + sha, {'state': 'success', 'context': 'acceptance/check'}, user('check-bot'))
    nextsha = commit(folder, '审批边界追加提交')
    r = run(['git', 'push', url('sdk', 'HTTP'), 'HEAD:refs/heads/' + branch], env_for(actor), folder)
    assert r.returncode == 0, r.stderr
    ok('POST', path + '/statuses/' + nextsha, {'state': 'success', 'context': 'acceptance/check'}, user('check-bot'))
    before = refs('sdk')
    status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': nextsha}, actor)
    record('E05', '新提交检查已通过但旧审批不能放行', status >= 400 and refs('sdk') == before, HTTP状态=status, 响应=body, 原SHA=sha, 新SHA=nextsha)
    for name in ['approver-one', 'approver-two']:
        ok('POST', pp + '/reviews', {'event': 'APPROVED', 'body': '新版本重新核对'}, user(name))
    member('alpha', user('approver-one'))
    status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': nextsha}, actor)
    record('E07', '审批人撤权后拒绝合并', status >= 400 and refs('sdk') == before, HTTP状态=status, 响应=body)
    member('alpha', user('approver-one'), 30)
    # 恢复后明确重批，不能靠恢复成员关系复活旧批准。
    ok('POST', pp + '/reviews', {'event': 'APPROVED', 'body': '恢复后重新核对'}, user('approver-one'))
    member('alpha', actor, 20)
    status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': nextsha}, actor)
    record('E07', '合并人降为Reporter后拒绝合并', status >= 400 and refs('sdk') == before, HTTP状态=status, 响应=body)
    member('alpha', actor, 30)
    status, body = api('PATCH', path + '/branch_protections/main', {'required_approvals': 0}, actor)
    current = ok('GET', path + '/branch_protections/main')
    record('E08', 'Developer不能降低审批门禁', status >= 400 and current['required_approvals'] == 2, HTTP状态=status, 响应=body)
    status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': nextsha}, actor)
    state = ok('GET', pp, user=actor)
    record('E05', '恢复有效审批与合并资格后交付', status in [200, 201] and state['merged'], HTTP状态=status, 响应=body, PR编号=pr['number'])


def merge_race():
    actor = user('role-30')
    repo = S['仓库']['sdk']
    path = '/repos/' + repo['full_name']
    folder, r = local('sdk', actor, 'HTTP', 'merge-race')
    assert r.returncode == 0, r.stderr
    sha = commit(folder, '最终合并授权与撤权时序')
    branch = 'feature/merge-race'
    r = run(['git', 'push', url('sdk', 'HTTP'), 'HEAD:refs/heads/' + branch], env_for(actor), folder)
    assert r.returncode == 0, r.stderr
    pr = ok('POST', path + '/pulls', {'title': '最终合并并发验收', 'head': branch, 'base': 'main'}, actor)
    pp = path + '/pulls/' + str(pr['number'])
    for name in ['approver-one', 'approver-two']:
        ok('POST', pp + '/reviews', {'event': 'APPROVED', 'body': '并发前有效审批'}, user(name))
    ok('POST', path + '/statuses/' + sha, {'state': 'success', 'context': 'acceptance/check'}, user('check-bot'))
    paths = run(['docker', 'exec', CONTAINER, 'find', '/data/git/repositories', '-maxdepth', '3', '-name', 'dev-sdk.git']).stdout.splitlines()
    assert len(paths) == 1
    hook = paths[0] + '/hooks/reference-transaction'
    original = run(['docker', 'exec', CONTAINER, 'cat', hook]).stdout
    marker = '/tmp/acceptance-merge-' + secrets.token_hex(5)
    pause = f'if [ "$1" = prepared ]; then\n touch {marker}.reached\n for n in $(seq 1 30); do [ -f {marker}.release ] && break; sleep 1; done\nfi\n'
    def install(content):
        r = subprocess.run(['docker', 'exec', '-i', CONTAINER, 'sh', '-c', 'cat > "$1" && chmod 755 "$1"', 'sh', hook], input=content, text=True, capture_output=True)
        assert r.returncode == 0, r.stderr
    install(original.replace('exec /usr/local/bin/gitea', pause + 'exec /usr/local/bin/gitea'))
    before = refs('sdk')
    with ThreadPoolExecutor(max_workers=1) as pool:
        pending = pool.submit(api, 'POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': sha}, actor)
        try:
            reached = False
            for _ in range(100):
                if run(['docker', 'exec', CONTAINER, 'test', '-f', marker + '.reached']).returncode == 0:
                    reached = True
                    break
                if pending.done():
                    break
                time.sleep(0.2)
            if not reached:
                raise RuntimeError('未到达合并最终写入暂停点：' + str(pending.result() if pending.done() else '仍在执行'))
            gid = S['组织']['alpha']['id']
            state = ok('GET', f'/governance/groups/{gid}')
            revoked_status, revoked_body = api('DELETE', f'/governance/groups/{gid}/members/{user("approver-one")["id"]}?revision={state["revision"]}')
            run(['docker', 'exec', CONTAINER, 'touch', marker + '.release'])
            status, body = pending.result(timeout=40)
            merged = ok('GET', pp, user=actor)['merged']
            safe = (revoked_status == 409 and merged and status in [200, 201]) or (revoked_status == 204 and not merged and before == refs('sdk'))
            record('F02', '最终授权后撤销审批人成员资格', safe, 撤权HTTP状态=revoked_status, 撤权响应=revoked_body, 合并HTTP状态=status, 合并响应=body, 是否已合并=merged, 操作前=before, 操作后=refs('sdk'), 暂停点='最终授权已生成，目标引用prepared处理前')
            if revoked_status == 204:
                member('alpha', user('approver-one'), 30)
        finally:
            run(['docker', 'exec', CONTAINER, 'touch', marker + '.release'])
            install(original)


def native_hook_confirmation():
    actor = user('native-hook-user')
    direct('race', actor, 30)
    folder, r = local('race', actor, 'HTTP', 'native-hook')
    assert r.returncode == 0, r.stderr
    sha = commit(folder, '保持原生入口不变的时序反证')
    root = '/data/git/repositories/alpha/dev-race.git/hooks/'
    original = run(['docker', 'exec', CONTAINER, 'cat', root + 'reference-transaction']).stdout
    marker = '/tmp/acceptance-native-' + secrets.token_hex(5)
    custom = root + 'pre-receive.d/zz-acceptance-pause'
    pause = f'#!/bin/sh\n# 仅限本次隔离验收，在原生前置检查后暂停。\ncat >/dev/null\ntouch {marker}.reached\nfor n in $(seq 1 40); do [ -f {marker}.release ] && exit 0; sleep 1; done\nexit 1\n'
    r = subprocess.run(['docker', 'exec', '-i', CONTAINER, 'sh', '-c', 'cat > "$1" && chmod 755 "$1"', 'sh', custom], input=pause, capture_output=True, text=True)
    assert r.returncode == 0, r.stderr
    before = refs('race')
    proc = subprocess.Popen(['git', 'push', url('race', 'HTTP'), 'HEAD:refs/heads/feature/native-hook-confirm'], cwd=folder, env=env_for(actor), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        reached = False
        for _ in range(60):
            if run(['docker', 'exec', CONTAINER, 'test', '-f', marker + '.reached']).returncode == 0:
                reached = True
                break
            if proc.poll() is not None:
                break
            time.sleep(0.2)
        assert reached, '未到达原生前置检查后的暂停点'
        direct('race', actor)
        revoked_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
        status, _ = api('GET', '/repos/' + S['仓库']['race']['full_name'], user=actor)
        fresh = run(['git', 'ls-remote', url('race', 'HTTP')], env_for(actor))
        run(['docker', 'exec', CONTAINER, 'touch', marker + '.release'])
        stdout, stderr = proc.communicate(timeout=50)
        unchanged_hook = run(['docker', 'exec', CONTAINER, 'cat', root + 'reference-transaction']).stdout == original
        record('F01', '原生引用Hook字节不变的独立反证', proc.returncode != 0 and before == refs('race'), 协议='HTTP', 用户=actor['name'], 原生Hook未改变=unchanged_hook, 原生HookSHA256=hashlib.sha256(original.encode()).hexdigest(), 撤权后详情状态=status, 撤权后新Git请求退出码=fresh.returncode, 撤权完成时间=revoked_at, 本地SHA=sha, 操作前=before, 操作后=refs('race'), 退出码=proc.returncode, 输出=stderr[-2200:], 暂停机制='Gitea支持的pre-receive.d扩展，排序在原生gitea检查之后；原生引用Hook未修改')
    finally:
        run(['docker', 'exec', CONTAINER, 'touch', marker + '.release'])
        if proc.poll() is None:
            proc.communicate(timeout=50)
        run(['docker', 'exec', CONTAINER, 'rm', custom])


def movement():
    for name, parent in [('move-source', ''), ('move-target', ''), ('move-source/child', 'move-source')]:
        S['组织'][name] = ok('POST', '/governance/groups', {'path': name.split('/')[-1], 'visibility': 2, 'parent_id': S['组织'][parent]['id'] if parent else 0})
        save()
    source, target = user('move-source-user'), user('move-target-user')
    member('move-source', source, 30)
    member('move-target', target, 30)
    for key, owner in [('transfer', 'move-source'), ('move-child', 'move-source/child')]:
        S['仓库'][key] = ok('POST', '/orgs/' + owner + '/repos', {'name': 'dev-' + key, 'private': True, 'auto_init': True, 'default_branch': 'main'})
        save()
    probe('C08', 'transfer', source, True, True, 'before')
    old_urls = {p: url('transfer', p) for p in ['HTTP', 'SSH']}
    old = S['仓库']['transfer']
    moved = ok('POST', '/repos/' + old['full_name'] + '/transfer', {'new_owner': 'move-target'})
    S['仓库']['transfer'] = ok('GET', '/repositories/' + str(old['id']))
    save()
    record('C08', '转移后所有者核验', S['仓库']['transfer']['owner']['login'] == 'move-target', 原所有者=old['owner']['login'], 新所有者=S['仓库']['transfer']['owner']['login'])
    probe('C08', 'transfer', source, False, False, 'old-member')
    probe('C08', 'transfer', target, True, True, 'new-member')
    for protocol, oldurl in old_urls.items():
        r = run(['git', 'ls-remote', oldurl], env_for(source))
        record('C08', '旧地址不能绕过转移后权限', r.returncode != 0, 协议=protocol, 退出码=r.returncode, 输出=r.stderr[-1600:])
    probe('C09', 'move-child', source, True, True, 'before')
    gid = S['组织']['move-source/child']['id']
    current = ok('GET', f'/governance/groups/{gid}')
    moved = ok('POST', f'/governance/groups/{gid}/move', {'path': 'child', 'parent_id': S['组织']['move-target']['id'], 'visibility': 2, 'revision': current['revision']})
    S['仓库']['move-child'] = ok('GET', '/repositories/' + str(S['仓库']['move-child']['id']))
    save()
    probe('C09', 'move-child', source, False, False, 'old-ancestor')
    probe('C09', 'move-child', target, True, True, 'new-ancestor')
    # 分页与无权限仓库排除；用不同页大小对照同一稳定数据集。
    actor = user('role-30')
    allrows = ok('GET', '/user/repos?limit=50', user=actor)
    small = []
    for page in range(1, 40):
        rows = ok('GET', '/user/repos?limit=2&page=' + str(page), user=actor)
        small.extend(rows)
        if len(rows) < 2:
            break
    expected = {x['id'] for x in allrows}
    actual = [x['id'] for x in small]
    forbidden = {S['仓库'][x]['id'] for x in ['beta', 'transfer', 'move-child']}
    record('B12', 'API分页不漏项不重复且排除无权仓库', set(actual) == expected and len(actual) == len(set(actual)) and not forbidden.intersection(actual), 大页ID=sorted(expected), 小页ID=actual, 无权ID=sorted(forbidden))


def local_work():
    for protocol in ['HTTP', 'SSH']:
        first, second = user('role-30'), user('role-40')
        a, ra = local('race', first, protocol, 'local-first')
        b, rb = local('race', second, protocol, 'local-second')
        assert ra.returncode == rb.returncode == 0
        sha_a = commit(a, '同事甲的修改-' + protocol)
        sha_b = commit(b, '同事乙的修改-' + protocol)
        r = run(['git', 'push', url('race', protocol), 'HEAD:main'], env_for(first), a)
        assert r.returncode == 0, r.stderr
        before = refs('race')
        r = run(['git', 'push', url('race', protocol), 'HEAD:main'], env_for(second), b)
        record('A08', '非快进拒绝且保留先提交者', r.returncode != 0 and refs('race') == before and before['refs/heads/main'] == sha_a, 协议=protocol, 本地SHA=sha_b, 操作前=before, 操作后=refs('race'), 输出=r.stderr[-1300:])
        r = run(['git', 'fetch', url('race', protocol), 'main'], env_for(second), b)
        assert r.returncode == 0, r.stderr
        r = run(['git', 'rebase', 'FETCH_HEAD'], env_for(second), b)
        record('A08', '重叠修改产生可见冲突', r.returncode != 0 and bool(run(['git', 'ls-files', '-u'], cwd=b).stdout), 协议=protocol, 输出=r.stdout[-1200:])
        (b / 'acceptance.txt').write_text('同事甲的修改-' + protocol + '\n同事乙的修改-' + protocol + '\n')
        assert run(['git', 'add', 'acceptance.txt'], cwd=b).returncode == 0
        env = env_for(second)
        env['GIT_EDITOR'] = 'true'
        r = run(['git', 'rebase', '--continue'], env, b)
        assert r.returncode == 0, r.stderr
        head = run(['git', 'rev-parse', 'HEAD'], cwd=b).stdout.strip()
        r = run(['git', 'push', url('race', protocol), 'HEAD:main'], env_for(second), b)
        record('A08', '解决冲突后提交双方修改', r.returncode == 0 and refs('race')['refs/heads/main'] == head and run(['git', 'merge-base', '--is-ancestor', sha_a, head], cwd=b).returncode == 0, 协议=protocol, 远端SHA=refs('race')['refs/heads/main'], 合并内容=(b/'acceptance.txt').read_text())
        r = run(['git', 'pull', '--ff-only', url('race', protocol), 'main'], env_for(first), a)
        record('A09', '旧克隆拉取同事新提交', r.returncode == 0 and run(['git', 'rev-parse', 'HEAD'], cwd=a).stdout.strip() == head, 协议=protocol, 远端SHA=head)
        nextsha = commit(a, '同步后继续开发-' + protocol)
        r = run(['git', 'push', url('race', protocol), 'HEAD:main'], env_for(first), a)
        record('A09', '旧克隆同步后继续交付', r.returncode == 0 and refs('race')['refs/heads/main'] == nextsha, 协议=protocol, 本地SHA=nextsha)


def target_change():
    repo = ok('POST', '/orgs/alpha/repos', {'name': 'dev-target-change', 'private': True, 'auto_init': True, 'default_branch': 'main'})
    S['仓库']['target-change'] = repo
    save()
    path = '/repos/' + repo['full_name']
    ok('POST', path + '/branch_protections', {'rule_name': 'main', 'enable_push': False, 'required_approvals': 2, 'enable_status_check': True, 'status_check_contexts': ['acceptance/check'], 'dismiss_stale_approvals': True, 'block_admin_merge_override': True})
    ok('PUT', f'/governance/repositories/{repo["id"]}/approval-settings', {'prevent_author': True, 'prevent_overrides': True, 'reset_on_change': True, 'revision': 0})
    actor = user('role-30')
    pulls = []
    for n in [1, 2]:
        folder, r = local('target-change', actor, 'HTTP', 'target-' + str(n))
        assert r.returncode == 0, r.stderr
        (folder / ('change-' + str(n) + '.txt')).write_text('独立文件变更-' + str(n))
        for cmd in [['git', 'config', 'user.name', '验收用户'], ['git', 'config', 'user.email', 'acceptance@example.invalid'], ['git', 'add', '.'], ['git', 'commit', '-m', 'test: 目标分支变化验收', '-m', 'Assisted-by: Codex:GPT-6']]:
            r = run(cmd, cwd=folder)
            assert r.returncode == 0, r.stderr
        sha = run(['git', 'rev-parse', 'HEAD'], cwd=folder).stdout.strip()
        branch = 'feature/target-' + str(n)
        r = run(['git', 'push', url('target-change', 'HTTP'), 'HEAD:refs/heads/' + branch], env_for(actor), folder)
        assert r.returncode == 0, r.stderr
        pr = ok('POST', path + '/pulls', {'title': '目标变化验收-' + str(n), 'head': branch, 'base': 'main'}, actor)
        pp = path + '/pulls/' + str(pr['number'])
        for name in ['approver-one', 'approver-two']:
            ok('POST', pp + '/reviews', {'event': 'APPROVED', 'body': '目标变化前审批'}, user(name))
        ok('POST', path + '/statuses/' + sha, {'state': 'success', 'context': 'acceptance/check'}, user('check-bot'))
        pulls.append((pp, sha))
    pp, sha = pulls[1]
    status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': sha}, actor)
    assert status in [200, 201], body
    before = refs('target-change')
    pp, sha = pulls[0]
    status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': sha}, actor)
    record('E06', '目标分支变化后原审批不能直接放行', status >= 400 and refs('target-change') == before, HTTP状态=status, 响应=body, 操作前=before, 操作后=refs('target-change'))
    if status >= 400:
        for name in ['approver-one', 'approver-two']:
            ok('POST', pp + '/reviews', {'event': 'APPROVED', 'body': '针对新目标重新核对'}, user(name))
        status, body = api('POST', pp + '/merge', {'Do': 'merge', 'head_commit_id': sha}, actor)
        record('E06', '重新审批后合并完成', status in [200, 201] and ok('GET', pp, user=actor)['merged'], HTTP状态=status, 响应=body)


if __name__ == '__main__':
    if len(sys.argv) != 2 or sys.argv[1] not in ['setup', 'roles', 'inheritance', 'protected', 'pulls', 'corrected', 'sharing', 'revocation', 'credentials', 'boundaries', 'lifecycle', 'races', 'race_confirmation', 'approval_edges', 'merge_race', 'native_hook_confirmation', 'movement', 'local_work', 'target_change']:
        raise SystemExit('请指定合法验收阶段；仅用于本次隔离环境')
    globals()[sys.argv[1]]()
    print('本阶段检查数：', len(ROWS), '失败：', sum(r['结果'] == '失败' for r in ROWS))
