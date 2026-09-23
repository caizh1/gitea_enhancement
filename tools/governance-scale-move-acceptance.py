#!/usr/bin/env python3
"""固定规模组织移动后核对权限总数及新旧地址，不重复全量准备。"""
import base64,importlib.util,json,urllib.request,time
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'));c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
S=json.loads(Path('/tmp/governance-scale-20260918/state.json').read_text());root=S['groups'][0];other=S['groups'][1];g=next(g for g in S['groups'] if g['full_path']==root['full_path']+'/node-50');u=S['users'][0];repo=next(r for r in S['repos'] if r['owner']['id']==g['id']);before=c.refs(repo)
def count():
 req=urllib.request.Request(a.BASE+'/api/v1/user/repos?limit=1&page=1',headers={'Authorization':'Basic '+base64.b64encode((u['name']+':'+u['password']).encode()).decode()})
 with urllib.request.urlopen(req,timeout=40) as r:return r.status,int(r.headers['X-Total-Count'])
rev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision'];start=time.monotonic();moved=a.ok('POST',f'/governance/groups/{g["id"]}/move',{'path':'moved-scale-50','parent_id':other['id'],'revision':rev});elapsed=time.monotonic()-start
try:
 st,total=count();old=a.api('GET','/repos/'+repo['full_name'],user=u)[0];newpath=moved['full_path']+'/'+repo['name'];new=a.api('GET','/repos/'+newpath,user=u)[0];current=a.ok('GET','/repos/'+newpath)
 c.rec('C08','规模下移动十仓库子组后重算集合与旧地址',st==200 and total==990 and old in (403,404) and new in (403,404) and current['id']==repo['id'] and c.refs(current)==before,预期='授权树移出十仓库后1000变990，新旧路径均不保留旧权限，代码身份不变',总数=total,旧地址状态=old,新地址状态=new,移动秒=elapsed,仓库ID=current['id'],引用前=before,引用后=c.refs(current))
finally:
 rev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision'];a.ok('POST',f'/governance/groups/{g["id"]}/move',{'path':'node-50','parent_id':root['id'],'revision':rev})
st,total=count();c.rec('C08','规模移组恢复后授权总数',st==200 and total==1000,预期='恢复合法父组后重新可见1000仓库',总数=total)
