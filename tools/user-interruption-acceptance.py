#!/usr/bin/env python3
"""在专用隔离容器验证服务器退出及客户端中断后的治理对账。"""
import datetime
import hashlib
import importlib.util
import json
from pathlib import Path
import secrets
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor

spec = importlib.util.spec_from_file_location('a', Path(__file__).with_name('user-permission-acceptance.py'))
a = importlib.util.module_from_spec(spec)
spec.loader.exec_module(a)
mode = sys.argv[1] if len(sys.argv) > 1 else 'all'
assert mode in ('all', 'merge')
sys.argv = [sys.argv[0], 'interruption']

CONTAINER = 'user-permission-20260918-gitea-1'
DATABASE = 'user-permission-20260918-database-1'
assert a.CONTAINER == CONTAINER
run_id = datetime.datetime.now(datetime.timezone.utc).strftime('%H%M%S')
name = 'dev-interruption-' + run_id
actor = a.S['用户']['role-30']
repo = a.ok('POST', '/orgs/alpha/repos', {'name': name, 'private': True, 'auto_init': True, 'default_branch': 'main'})
a.S['仓库']['interruption'] = repo
api_path = '/repos/' + repo['full_name']
hook_root = f'/data/git/repositories/alpha/{name}.git/hooks/'
native_path = hook_root + 'reference-transaction'
native = a.run(['docker', 'exec', CONTAINER, 'cat', native_path])
assert native.returncode == 0 and 'Gitea 原生治理引用事务入口' in native.stdout
native_hash = hashlib.sha256(native.stdout.encode()).hexdigest()
custom_paths = []
markers = []


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def docker(*args):
    return a.run(['docker', 'exec', CONTAINER, *args])


def db(sql):
    for attempt in range(6):
        result = a.run(['docker', 'exec', DATABASE, 'psql', '-U', 'gitea', '-d', 'gitea', '-At', '-F', '|', '-c', sql])
        if result.returncode == 0 or attempt == 5:
            break
        time.sleep(.5)
    assert result.returncode == 0, result.stderr
    return [line.split('|') for line in result.stdout.splitlines() if line]


def state():
    result = a.run(['docker', 'inspect', '-f', '{{json .State}}', CONTAINER])
    assert result.returncode == 0, result.stderr
    return json.loads(result.stdout)


def health(timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if a.api('GET', '/version')[0] == 200:
                return True
        except Exception:
            pass
        time.sleep(.25)
    return False


def install(path, text):
    custom_paths.append(path)
    result = subprocess.run(
        ['docker', 'exec', '-i', CONTAINER, 'sh', '-c', 'cat > "$1" && chmod 755 "$1"', 'sh', path],
        input=text, text=True, capture_output=True,
    )
    assert result.returncode == 0, result.stderr


def marker(prefix):
    value = '/tmp/' + prefix + '-' + secrets.token_hex(5)
    markers.append(value)
    return value


def wait_file(path, pending, timeout=18):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if docker('test', '-f', path).returncode == 0:
            return True
        if pending.done() if hasattr(pending, 'done') else pending.poll() is not None:
            return False
        time.sleep(.15)
    return False


def transactions():
    rows = db(f"select id,state,coalesce(merge_authorization_id,''),business_applied from governance_ref_transaction where repo_id={repo['id']} order by created_at")
    return [{'id': x[0], 'state': x[1], 'merge': x[2], 'business_applied': x[3]} for x in rows]


def merge_authorizations():
    rows = db(f"select id,pull_id,state from governance_merge_authorization where repo_id={repo['id']} order by created_at")
    return [{'id': x[0], 'pull_id': int(x[1]), 'state': x[2]} for x in rows]


def reservations():
    return {
        '引用': db(f"select resource,transaction_id from governance_ref_reservation where transaction_id in (select id from governance_ref_transaction where repo_id={repo['id']})"),
        '合并': db(f"select resource,authorization_id from governance_reservation where authorization_id in (select id from governance_merge_authorization where repo_id={repo['id']})"),
    }


def recover(kind, item_id):
    result = a.run(['docker', 'exec', '-u', 'git', CONTAINER, 'gitea', 'admin', 'governance', 'recover', '--config', '/data/gitea/conf/app.ini', '--kind', kind, '--id', item_id])
    assert result.returncode == 0, result.stderr


def settle(before_ids, merge=False, timeout=12):
    terminal = ('committed', 'aborted', 'failed') if merge else ('committed', 'aborted')
    deadline = time.time() + timeout
    while time.time() < deadline:
        current = merge_authorizations() if merge else transactions()
        new = [x for x in current if x['id'] not in before_ids]
        if new and all(x['state'] in terminal for x in new):
            return new, False
        time.sleep(.4)
    current = merge_authorizations() if merge else transactions()
    new = [x for x in current if x['id'] not in before_ids]
    for item in new:
        if item['state'] not in terminal:
            recover('merge' if merge else 'reference', item['id'])
    return ([x for x in (merge_authorizations() if merge else transactions()) if x['id'] not in before_ids], True)


def audits():
    status, body = a.api('GET', f'/governance/audit-events?scope_type=repository&scope_id={repo["id"]}&limit=100')
    assert status == 200, body
    return body if isinstance(body, list) else body.get('data', body.get('events', []))


def cleanup():
    if not health(25):
        restart = a.run(['docker', 'restart', '-t', '1', CONTAINER])
        if restart.returncode != 0 or not health(30):
            raise RuntimeError('清理前无法恢复隔离 Gitea 健康')
    for value in markers:
        docker('touch', value + '.release')
    for path in custom_paths:
        docker('rm', '-f', path)
    for value in markers:
        docker('rm', '-f', value + '.reached', value + '.release', value + '.done')
    remaining = [path for path in custom_paths if docker('test', '-e', path).returncode == 0]
    current = a.run(['docker', 'exec', CONTAINER, 'cat', native_path])
    if remaining or current.returncode != 0 or hashlib.sha256(current.stdout.encode()).hexdigest() != native_hash:
        raise RuntimeError('临时 Hook 未清理或原生 Hook 已变化：' + repr(remaining))


def restart_at_pre_receive(protocol):
    folder, cloned = a.local('interruption', actor, protocol, 'interruption-' + run_id + '-' + protocol.lower())
    assert cloned.returncode == 0, cloned.stderr
    sha = a.commit(folder, '服务器退出恢复-' + protocol)
    branch = 'refs/heads/feature/restart-' + protocol.lower()
    point = marker('acceptance-server-' + protocol.lower())
    custom = hook_root + 'pre-receive.d/zz-acceptance-interruption-' + protocol.lower()
    install(custom, f'#!/bin/sh\ncat >/dev/null\ntouch {point}.reached\nfor n in $(seq 1 40); do [ -f {point}.release ] && exit 0; sleep 1; done\nexit 1\n')
    before_refs = a.refs('interruption')
    before_ids = {x['id'] for x in transactions()}
    process = subprocess.Popen(['git', 'push', a.url('interruption', protocol), 'HEAD:' + branch], cwd=folder, env=a.env_for(actor), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    assert wait_file(point + '.reached', process), '未到达 pre-receive 暂停点'
    stopped_at = now()
    before_state = state()
    restarted = a.run(['docker', 'restart', '-t', '1', CONTAINER])
    recovered_at = now()
    assert restarted.returncode == 0 and health(), restarted.stderr
    stdout, stderr = process.communicate(timeout=20)
    txs, used_recovery = settle(before_ids)
    after_refs = a.refs('interruption')
    clean = reservations() == {'引用': [], '合并': []}
    a.record('F03', 'pre-receive暂停时服务器退出并恢复-' + protocol,
             process.returncode != 0 and before_refs == after_refs and clean and all(x['state'] in ('committed', 'aborted') for x in txs),
             协议=protocol, 停止时间=stopped_at, 恢复时间=recovered_at, 重启前容器状态=before_state, 重启后容器状态=state(),
             退出码=process.returncode, 操作前引用=before_refs, 操作后引用=after_refs, 新事务=txs, 使用恢复命令=used_recovery,
             保留项=reservations(), 输出=(stdout + stderr)[-1200:])
    docker('rm', '-f', custom)
    result = a.run(['git', 'push', a.url('interruption', protocol), 'HEAD:' + branch], a.env_for(actor), folder)
    a.record('F03', '服务恢复后同一提交正常交付-' + protocol,
             result.returncode == 0 and a.refs('interruption').get(branch) == sha,
             协议=protocol, 本地SHA=sha, 远端SHA=a.refs('interruption').get(branch), 输出=result.stderr[-700:])
    return folder, branch


def disconnect_at_post_receive(protocol, folder, branch):
    sha = a.commit(folder, '客户端中断对账-' + protocol)
    point = marker('acceptance-client-' + protocol.lower())
    custom = hook_root + 'post-receive.d/zz-acceptance-interruption-' + protocol.lower()
    install(custom, f'#!/bin/sh\ncat >/dev/null\ntouch {point}.reached\nfor n in $(seq 1 30); do [ -f {point}.release ] && touch {point}.done && exit 0; sleep 1; done\ntouch {point}.done\nexit 0\n')
    before_ids = {x['id'] for x in transactions()}
    process = subprocess.Popen(['git', 'push', a.url('interruption', protocol), 'HEAD:' + branch], cwd=folder, env=a.env_for(actor), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    assert wait_file(point + '.reached', process), '未到达 post-receive 暂停点'
    written = a.refs('interruption').get(branch)
    process.terminate()
    stdout, stderr = process.communicate(timeout=15)
    docker('touch', point + '.release')
    deadline = time.time() + 12
    while time.time() < deadline and docker('test', '-f', point + '.done').returncode != 0:
        time.sleep(.2)
    txs, used_recovery = settle(before_ids)
    clean = reservations() == {'引用': [], '合并': []}
    retry = a.run(['git', 'push', a.url('interruption', protocol), 'HEAD:' + branch], a.env_for(actor), folder)
    a.record('F03', 'post-receive响应前客户端退出并对账-' + protocol,
             written == sha and a.refs('interruption').get(branch) == sha and retry.returncode == 0 and clean,
             协议=protocol, 客户端退出码=process.returncode, 远端SHA=written, 新事务=txs, 使用恢复命令=used_recovery,
             保留项=reservations(), 重试输出=retry.stderr[-700:], 中断输出=(stdout + stderr)[-700:])
    docker('rm', '-f', custom)


def interrupted_merge():
    a.ok('POST', api_path + '/branch_protections', {'rule_name': 'main', 'enable_push': False, 'required_approvals': 2, 'block_admin_merge_override': True})
    folder, cloned = a.local('interruption', actor, 'HTTP', 'interruption-' + run_id + '-merge')
    assert cloned.returncode == 0, cloned.stderr
    sha = a.commit(folder, '合并中断恢复')
    pushed = a.run(['git', 'push', repo['clone_url'], 'HEAD:refs/heads/feature/merge-restart'], a.env_for(actor), folder)
    assert pushed.returncode == 0, pushed.stderr
    pr = a.ok('POST', api_path + '/pulls', {'title': '合并中断恢复', 'head': 'feature/merge-restart', 'base': 'main'}, actor)
    pull_path = api_path + '/pulls/' + str(pr['number'])
    for approver in ('approver-one', 'approver-two'):
        a.ok('POST', pull_path + '/reviews', {'event': 'APPROVED', 'body': 'F03独立审批'}, a.S['用户'][approver])
    for _ in range(60):
        current = a.ok('GET', pull_path, user=actor)
        if current.get('mergeable') is True:
            break
        time.sleep(.25)
    else:
        raise RuntimeError('PR后台可合并状态未就绪')
    point = marker('acceptance-merge')
    custom = hook_root + 'pre-receive.d/zz-acceptance-interruption-merge'
    install(custom, f'#!/bin/sh\ncat >/dev/null\ntouch {point}.reached\nfor n in $(seq 1 40); do [ -f {point}.release ] && exit 0; sleep 1; done\nexit 1\n')
    before_refs = a.refs('interruption')
    before_tx = {x['id'] for x in transactions()}
    before_merge = {x['id'] for x in merge_authorizations()}
    with ThreadPoolExecutor(max_workers=1) as pool:
        pending = pool.submit(a.api, 'POST', pull_path + '/merge', {'Do': 'merge', 'head_commit_id': sha}, actor)
        assert wait_file(point + '.reached', pending), '合并未到达 pre-receive 暂停点'
        stopped_at = now()
        before_state = state()
        restarted = a.run(['docker', 'restart', '-t', '1', CONTAINER])
        recovered_at = now()
        assert restarted.returncode == 0 and health(), restarted.stderr
        try:
            merge_status, merge_body = pending.result(timeout=20)
        except Exception as exc:
            merge_status, merge_body = 0, {'客户端异常': str(exc)}
    auths, merge_recovery = settle(before_merge, merge=True)
    txs, ref_recovery = settle(before_tx)
    final_pr = a.ok('GET', pull_path, user=actor)
    final_refs = a.refs('interruption')
    remaining = reservations()
    repo_audits = audits()
    terminal = all(x['state'] in ('committed', 'aborted', 'failed') for x in auths) and all(x['state'] in ('committed', 'aborted') for x in txs)
    consistent = (final_pr.get('merged') and final_refs.get('refs/heads/main') != before_refs.get('refs/heads/main')) or (not final_pr.get('merged') and final_refs == before_refs)
    a.record('F03', '合并pre-receive暂停时服务器退出并恢复', terminal and consistent and remaining == {'引用': [], '合并': []},
             PR编号=pr['number'], 两名独立审批者=['approver-one', 'approver-two'], mergeable前置=True,
             停止时间=stopped_at, 恢复时间=recovered_at, 重启前容器状态=before_state, 重启后容器状态=state(),
             合并HTTP状态=merge_status, 合并响应=merge_body, 最终PR={'merged': final_pr.get('merged'), 'merge_commit_sha': final_pr.get('merge_commit_sha')},
             操作前引用=before_refs, 操作后引用=final_refs, 新合并授权=auths, 新引用事务=txs,
             使用合并恢复命令=merge_recovery, 使用引用恢复命令=ref_recovery, 保留项=remaining,
             审计事件数=len(repo_audits), 审计类型=sorted({x.get('event_type', x.get('type', '')) for x in repo_audits}))
    docker('rm', '-f', custom)


failure = None
try:
    if mode == 'all':
        for protocol in ('HTTP', 'SSH'):
            local_folder, remote_branch = restart_at_pre_receive(protocol)
            disconnect_at_post_receive(protocol, local_folder, remote_branch)
    interrupted_merge()
except Exception as exc:
    failure = exc
finally:
    try:
        cleanup()
        final_health = health()
        a.record('F03', '临时Hook清理、原生Hook及健康最终见证', final_health,
                 临时Hook全部删除=True, 原生HookSHA256=native_hash, 原生Hook字节不变=True, 服务健康=final_health, 容器状态=state())
    except Exception as cleanup_exc:
        a.record('F03', '临时Hook清理、原生Hook及健康最终见证', False,
                 临时Hook全部删除=False, 服务健康=False, 显式告警=str(cleanup_exc))
        if failure is None:
            failure = cleanup_exc
if failure is not None:
    raise failure
