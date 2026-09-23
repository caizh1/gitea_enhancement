#!/usr/bin/env python3
"""固定规模补充：真实深度边界、分页小样本对照与成员批次变更。"""
import importlib.util,json,time,subprocess
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'));c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
root=c.group('depth');parent=root;paths=[root['full_path']]
for depth in range(2,21):
 parent=a.ok('POST','/governance/groups',{'path':'d'+str(depth),'name':'深度边界','parent_id':parent['id'],'visibility':2});paths.append(parent['full_path'])
rev=a.ok('GET',f'/governance/groups/{parent["id"]}')['revision']
st,body=a.api('POST','/governance/groups',{'path':'overflow','name':'超出深度上限','parent_id':parent['id'],'visibility':2})
lookup=a.api('GET','/governance/groups?query='+root['full_path'])
# 独立数据库计数确认超限创建未留下命名空间。
query="SELECT count(*) FROM governance_namespace WHERE lower_path='"+parent['full_path']+"/overflow';"
r=subprocess.run(['docker','exec','-i','user-permission-20260918-database-1','psql','-U','gitea','-d','gitea','-tA'],input=query,text=True,capture_output=True,timeout=15)
c.rec('C08','深度20与21真实创建边界',len(paths)==20 and len(paths[-1].split('/'))==20 and st>=400 and r.returncode==0 and r.stdout.strip()=='0',预期='深度20可创建，21拒绝且无孤立记录',合法最深路径=paths[-1],超限响应=[st,body],超限命名空间数量=r.stdout.strip())
state=json.loads(sorted(c.W.glob('state-*.json'))[-1].read_text());u=state['user'];times=[];ids=[]
for _ in range(10):
 start=time.monotonic();status,body=a.api('GET','/user/repos?limit=50&page=1',user=u);times.append(time.monotonic()-start);assert status==200;ids=[x['id'] for x in body]
c.rec('C08','单仓库授权小样本读取对照',state['repo']['id'] in ids,预期='小样本合法仓库可列出；耗时仅作当前共享实例对照',样本可见仓库数=len(ids),请求耗时秒=times,边界='数据库同时存在固定规模夹具；不是空数据库基准')
S=json.loads(Path('/tmp/governance-scale-20260918/state.json').read_text());g=S['groups'][0];changes=[]
for u in S['users'][1:11]:
 rev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision'];start=time.monotonic();status,_=a.api('PUT',f'/governance/groups/{g["id"]}/members/{u["id"]}',{'role':30,'revision':rev});changes.append({'用户ID':u['id'],'状态':status,'耗时秒':time.monotonic()-start})
# 成员列表可能分页，独立数据库核验整个批次，不把第一页缺席误判失败。
query="SELECT user_id,role FROM governance_membership WHERE scope_type='group' AND scope_id="+str(g['id'])+' AND user_id IN ('+','.join(str(x['id']) for x in S['users'][1:11])+') ORDER BY user_id;'
r=subprocess.run(['docker','exec','-i','user-permission-20260918-database-1','psql','-U','gitea','-d','gitea','-tA'],input=query,text=True,capture_output=True,timeout=15)
rows=[x.split('|') for x in r.stdout.splitlines()]
c.rec('C08','千名成员中的十人顺序批次角色变更',all(x['状态'] in (200,204) for x in changes) and len(rows)==10 and all(x[1]=='30' for x in rows),预期='每次使用最新revision，十名指定成员均更新为Developer；不代表单请求批量API',变更=changes,独立数据库角色=rows)
mem=a.run(['docker','stats','--no-stream','--format','{{.Name}} {{.MemUsage}} {{.CPUPerc}}','user-permission-20260918-gitea-1','user-permission-20260918-database-1'])
(c.OUT/'scale-resource-observation.txt').write_text('固定规模准备期间资源抽样，不是峰值：\n'+mem.stdout)
