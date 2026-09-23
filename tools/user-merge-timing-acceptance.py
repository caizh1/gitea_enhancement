#!/usr/bin/env python3
"""保持原生引用入口不变，验证隔离实例中的审批撤权时序。"""
import importlib.util
import sys
import subprocess
import time
import secrets
import hashlib
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
spec = importlib.util.spec_from_file_location('acceptance', Path(__file__).with_name('user-permission-acceptance.py'))
a = importlib.util.module_from_spec(spec)
spec.loader.exec_module(a)
kind = sys.argv[1] if len(sys.argv) > 1 else 'approver'
assert kind in ['approver', 'merger', 'review']
name = 'merge-timing' if kind == 'approver' else 'merge-timing-' + kind
if len(sys.argv) > 2:
    name += '-' + sys.argv[2]
sys.argv = [sys.argv[0], 'merge-timing']
actor = a.S['用户']['role-30']
repo = a.ok('POST', '/orgs/alpha/repos', {'name':'dev-' + name, 'private':True, 'auto_init':True, 'default_branch':'main'})
a.S['仓库'][name] = repo
path = '/repos/' + repo['full_name']
a.ok('POST', path + '/branch_protections', {'rule_name':'main', 'enable_push':False, 'required_approvals':2, 'block_admin_merge_override':True})
a.ok('PUT', f'/governance/repositories/{repo["id"]}/approval-settings', {'prevent_author':True, 'prevent_overrides':True, 'reset_on_change':True, 'revision':0})
folder, r = a.local(name, actor, 'HTTP', name)
assert r.returncode == 0, r.stderr
sha = a.commit(folder, '合并最终授权并发验收')
r = a.run(['git','push',repo['clone_url'],'HEAD:refs/heads/feature/timing'],a.env_for(actor),folder)
assert r.returncode == 0,r.stderr
pr = a.ok('POST',path+'/pulls',{'title':'审批撤权时序验收','head':'feature/timing','base':'main'},actor)
pp = path+'/pulls/'+str(pr['number'])
for approver_name in ['approver-one','approver-two']:
    a.ok('POST',pp+'/reviews',{'event':'APPROVED','body':'并发前审批'},a.S['用户'][approver_name])
root = '/data/git/repositories/alpha/dev-' + name + '.git/hooks/'
original = a.run(['docker','exec',a.CONTAINER,'cat',root+'reference-transaction']).stdout
marker = '/tmp/acceptance-merge-native-'+secrets.token_hex(5)
custom = root+'pre-receive.d/zz-acceptance-pause'
pause = f'#!/bin/sh\n# 隔离测试暂停，原生权限检查保持启用。\ncat >/dev/null\ntouch {marker}.reached\nfor n in $(seq 1 30); do [ -f {marker}.release ] && exit 0; sleep 1; done\nexit 1\n'
r = subprocess.run(['docker','exec','-i',a.CONTAINER,'sh','-c','cat > "$1" && chmod 755 "$1"','sh',custom],input=pause,text=True,capture_output=True)
assert r.returncode == 0,r.stderr
before = a.refs(name)
revoked = False
with ThreadPoolExecutor(max_workers=1) as pool:
    pending = pool.submit(a.api,'POST',pp+'/merge',{'Do':'merge','head_commit_id':sha},actor)
    try:
        reached = False
        for _ in range(80):
            if a.run(['docker','exec',a.CONTAINER,'test','-f',marker+'.reached']).returncode == 0:
                reached = True
                break
            if pending.done():
                break
            time.sleep(0.2)
        if reached:
            gid = a.S['组织']['alpha']['id']
            state = a.ok('GET',f'/governance/groups/{gid}')
            revoke_user = actor if kind == 'merger' else a.S['用户']['approver-one']
            if kind == 'review':
                reviews = a.ok('GET', pp + '/reviews')
                review = next(x for x in reviews if x['user']['id'] == revoke_user['id'] and x['state'] == 'APPROVED')
                status,body = a.api('POST', pp + '/reviews/' + str(review['id']) + '/dismissals', {'message': '并发撤销审批验收'})
            else:
                status,body = a.api('DELETE',f'/governance/groups/{gid}/members/{revoke_user["id"]}?revision={state["revision"]}')
            revoked = status == 204
            a.run(['docker','exec',a.CONTAINER,'touch',marker+'.release'])
            ms,mb = pending.result(timeout=45)
            merged = a.ok('GET',pp,user=actor)['merged']
            after = a.refs(name)
            samehook = a.run(['docker','exec',a.CONTAINER,'cat',root+'reference-transaction']).stdout == original
            safe = samehook and ((status == 409 and merged and ms in [200,201]) or (revoked and not merged and before == after))
            a.record('F02','原生Hook完整时合并授权与撤权排序-'+kind,safe,撤权HTTP状态=status,撤权响应=body,合并HTTP状态=ms,合并响应=mb,已合并=merged,操作前=before,操作后=after,原生Hook未改变=samehook,原生HookSHA256=hashlib.sha256(original.encode()).hexdigest(),暂停点='原生前置检查结束后、自定义pre-receive扩展中；引用尚未更新')
        else:
            ms,mb = pending.result(timeout=45)
            a.record('F02','本次暂停点未到达',False,HTTP状态=ms,响应=mb,判读='未验证：未构造目标时序，不作为产品失败',操作前=before,操作后=a.refs(name))
    finally:
        a.run(['docker','exec',a.CONTAINER,'touch',marker+'.release'])
        a.run(['docker','exec',a.CONTAINER,'rm',custom])
        if revoked:
            a.member('alpha',revoke_user,30) if kind != 'review' else None
