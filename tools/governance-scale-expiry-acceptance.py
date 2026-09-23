#!/usr/bin/env python3
"""规模夹具中固定成员自然到期，保留旧凭据测试新读取请求。"""
import importlib.util,json,time
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'));c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
S=json.loads(Path('/tmp/governance-scale-20260918/state.json').read_text());g=S['groups'][0];u=S['users'][12];repo=next(r for r in S['repos'] if r['owner']['id']==g['id'] and not r['empty'])
expiry=int(time.time())+15;rev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision'];a.ok('PUT',f'/governance/groups/{g["id"]}/members/{u["id"]}',{'role':20,'expires_unix':expiry,'revision':rev})
before=a.api('GET','/repos/'+repo['full_name'],user=u)[0]
while time.time()<=expiry+1:time.sleep(.5)
after=a.api('GET','/repos/'+repo['full_name'],user=u)[0]
c.rec('C08','千人成员规模下自然到期不等待清理',before==200 and after in (403,404),预期='固定Reporter到期前可读，到期后同一凭据新读取拒绝',账号ID=u['id'],仓库ID=repo['id'],到期Unix=expiry,到期前状态=before,到期后状态=after,观测Unix=int(time.time()),边界='本条API读取；HTTP/SSH到期入口另见M07')
