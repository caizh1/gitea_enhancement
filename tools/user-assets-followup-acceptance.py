#!/usr/bin/env python3
"""补验 D10 主、子仓库独立读写和撤权边界，不覆盖已有原始证据。"""
import importlib.util
import json
from pathlib import Path
import secrets

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('assets_base', HERE / 'user-assets-acceptance.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
base.OUT = base.ROOT / 'docs/evidence/user-permission-20260918/assets-followup-results.jsonl'
TAG = secrets.token_hex(4)


def refs(repo):
    result = base.run(['git', 'ls-remote', repo['clone_url']], env=base.env_for(base.ADMIN))
    assert result.returncode == 0, result.stderr
    return {parts[1]: parts[0] for line in result.stdout.splitlines() if len(parts := line.split()) == 2}


def clone(repo, actor, protocol, label):
    folder = base.ASSET_WORK / f'followup-{label}-{protocol.lower()}-{TAG}'
    result = base.run(['git', 'clone', base.repo_url(repo, protocol), str(folder)], env=base.env_for(actor))
    return folder, result


def new_commit(folder, filename, text):
    base.git_config(folder)
    (folder / filename).write_text(text + '\n')
    for args in (['git', 'add', filename], ['git', 'commit', '-m', 'test: D10 独立权限补验']):
        result = base.run(args, folder)
        assert result.returncode == 0, result.stderr
    return base.run(['git', 'rev-parse', 'HEAD'], folder).stdout.strip()


def push(folder, repo, actor, protocol, sha, branch):
    before = refs(repo)
    result = base.run(['git', 'push', base.repo_url(repo, protocol), f'{sha}:refs/heads/{branch}'],
                      folder, base.env_for(actor))
    after = refs(repo)
    return result, before, after


def evidence_limit():
    logs = base.run(['docker', 'logs', '--since', '2026-09-18T03:20:00Z',
                     '--until', '2026-09-18T03:24:00Z', 'user-permission-20260918-gitea-1'])
    lines = [line for line in (logs.stdout + logs.stderr).splitlines()
             if 'private-lfs.git/info/lfs/objects/batch' in line or 'LFS_START_SERVER' in line]
    base.record('历史 LFS 原始证据限制', True,
        证据性质='限制说明，不是重造 raw',
        已知事实='最早 LFS_START_SERVER=false 时的 404 JSONL，以及随后测试客户端错误复制 Transfer-Encoding 导致的 422 JSONL，均被旧版脚本 OUT.write_text 清空覆盖',
        可保留旁证=lines[-12:],
        当前修正='主脚本改为仅追加；本轮写入独立 assets-followup-results.jsonl',
        不可恢复项='被覆盖 JSONL 的原始行不能恢复，不能作为现存 raw 引用')


def authorized(actor, protocol):
    for repo in (base.S['仓库']['main'], base.S['仓库']['child']):
        base.collaborator(repo, actor, True)
    results = {}
    for kind in ('main', 'child'):
        repo = base.S['仓库'][kind]
        folder, cloned = clone(repo, actor, protocol, 'authorized-' + kind)
        assert cloned.returncode == 0, cloned.stderr
        sha = new_commit(folder, f'{kind}-{protocol.lower()}-{TAG}.txt', f'{kind} 已授权独立写入')
        branch = f'followup-authorized-{kind}-{protocol.lower()}-{TAG}'
        pushed, before, after = push(folder, repo, actor, protocol, sha, branch)
        results[kind] = {'SHA': sha, '退出码': pushed.returncode,
                         '管理员远端SHA': after.get('refs/heads/' + branch), '操作前': before, '操作后': after}
    base.record('主仓库与子仓库独立授权读写及管理员 refs 见证',
                all(x['退出码'] == 0 and x['管理员远端SHA'] == x['SHA'] for x in results.values()),
                协议=protocol, 主仓库=results['main'], 子仓库=results['child'])


def revoke_main(actor, protocol):
    main, child = base.S['仓库']['main'], base.S['仓库']['child']
    base.collaborator(main, actor, True); base.collaborator(child, actor, True)
    main_dir, r1 = clone(main, actor, protocol, 'main-revoke-main')
    child_dir, r2 = clone(child, actor, protocol, 'child-keep-main')
    assert r1.returncode == 0 and r2.returncode == 0
    main_sha = new_commit(main_dir, f'main-revoked-{protocol.lower()}-{TAG}.txt', '主仓库撤权后的离线提交')
    child_sha = new_commit(child_dir, f'child-kept-{protocol.lower()}-{TAG}.txt', '主仓库撤权后子仓库仍可写')
    base.collaborator(main, actor, False)
    main_branch = f'followup-main-denied-{protocol.lower()}-{TAG}'
    child_branch = f'followup-child-kept-{protocol.lower()}-{TAG}'
    denied, mb, ma = push(main_dir, main, actor, protocol, main_sha, main_branch)
    allowed, cb, ca = push(child_dir, child, actor, protocol, child_sha, child_branch)
    passed = denied.returncode != 0 and mb == ma and allowed.returncode == 0 and ca.get('refs/heads/' + child_branch) == child_sha
    base.record('仅撤销主仓库：主写拒绝且子仍可写', passed, 协议=protocol,
                主仓库={'本地SHA': main_sha, '退出码': denied.returncode, '操作前': mb, '操作后': ma, '输出': denied.stderr[-1200:]},
                子仓库={'本地SHA': child_sha, '退出码': allowed.returncode, '管理员远端SHA': ca.get('refs/heads/' + child_branch)})
    base.collaborator(main, actor, True)


def revoke_child(actor, protocol):
    main, child = base.S['仓库']['main'], base.S['仓库']['child']
    base.collaborator(main, actor, True); base.collaborator(child, actor, True)
    main_dir, r1 = clone(main, actor, protocol, 'main-keep-child')
    child_dir, r2 = clone(child, actor, protocol, 'child-revoke-child')
    assert r1.returncode == 0 and r2.returncode == 0
    main_sha = new_commit(main_dir, f'main-kept-{protocol.lower()}-{TAG}.txt', '子仓库撤权后主仓库仍可写')
    child_sha = new_commit(child_dir, f'child-revoked-{protocol.lower()}-{TAG}.txt', '子仓库撤权后的离线提交')
    base.collaborator(child, actor, False)
    main_branch = f'followup-main-kept-{protocol.lower()}-{TAG}'
    child_branch = f'followup-child-denied-{protocol.lower()}-{TAG}'
    allowed, mb, ma = push(main_dir, main, actor, protocol, main_sha, main_branch)
    denied, cb, ca = push(child_dir, child, actor, protocol, child_sha, child_branch)

    admin_dir, admin_clone = clone(child, base.ADMIN, 'HTTP', 'admin-new-child-object-' + protocol.lower())
    assert admin_clone.returncode == 0, admin_clone.stderr
    new_sha = new_commit(admin_dir, f'private-new-{protocol.lower()}-{TAG}.txt', '子仓库撤权后新增私有内容')
    new_branch = f'followup-private-new-{protocol.lower()}-{TAG}'
    admin_push, _, admin_after = push(admin_dir, child, base.ADMIN, 'HTTP', new_sha, new_branch)
    fetch = base.run(['git', 'fetch', base.repo_url(child, protocol), f'refs/heads/{new_branch}'], child_dir, base.env_for(actor))
    absent_in_child_git = base.run(['git', 'cat-file', '-e', new_sha], child_dir).returncode != 0

    recursive = base.ASSET_WORK / f'followup-recursive-child-denied-{protocol.lower()}-{TAG}'
    recursive_result = base.run(['git', 'clone', '--recurse-submodules', base.repo_url(main, protocol), str(recursive)], env=base.env_for(actor))
    subpath = recursive / 'modules/private-child'
    submodule_empty = not subpath.exists() or not any(subpath.iterdir())
    passed = (allowed.returncode == 0 and ma.get('refs/heads/' + main_branch) == main_sha and
              denied.returncode != 0 and cb == ca and admin_push.returncode == 0 and
              admin_after.get('refs/heads/' + new_branch) == new_sha and fetch.returncode != 0 and
              absent_in_child_git and recursive_result.returncode != 0 and submodule_empty)
    base.record('仅撤销子仓库：子写拒绝、主仍可写且新子对象未取得', passed, 协议=protocol,
                主仓库={'本地SHA': main_sha, '退出码': allowed.returncode, '管理员远端SHA': ma.get('refs/heads/' + main_branch)},
                子仓库={'离线SHA': child_sha, '推送退出码': denied.returncode, '操作前': cb, '操作后': ca, '输出': denied.stderr[-1000:]},
                撤权后新对象={'SHA': new_sha, '管理员远端SHA': admin_after.get('refs/heads/' + new_branch),
                              '直接抓取退出码': fetch.returncode, '子仓库Git对象不存在': absent_in_child_git,
                              '递归克隆退出码': recursive_result.returncode, '子模块目录不存在或为空': submodule_empty,
                              '输出': recursive_result.stderr[-1400:]})
    base.collaborator(child, actor, True)


def main():
    base.OUT.parent.mkdir(parents=True, exist_ok=True)
    base.OUT.touch(exist_ok=True)
    actor = base.setup()
    evidence_limit()
    for protocol in ('HTTP', 'SSH'):
        authorized(actor, protocol)
        revoke_main(actor, protocol)
        revoke_child(actor, protocol)
    rows = [json.loads(line) for line in base.OUT.read_text().splitlines()]
    print(json.dumps({'本次文件累计': len(rows), '失败': sum(row['结果'] == '失败' for row in rows)}, ensure_ascii=False))


if __name__ == '__main__':
    main()
