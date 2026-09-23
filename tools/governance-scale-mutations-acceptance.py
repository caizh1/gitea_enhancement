#!/usr/bin/env python3
"""固定规模补充：分页小样本对照与成员批次变更。"""
import importlib.util,json,time,subprocess
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'));c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
S=json.loads(Path('/tmp/governance-scale-20260918/state.json').read_text());g=S['groups'][0];changes=[]
for u in S['users'][1:11]:
 rev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision'];start=time.monotonic();status,_=a.api('PUT',f'/governance/groups/{g["id"]}/members/{u["id"]}',{'role':20,'revision':rev});changes.append({'用户ID':u['id'],'状态':status,'耗时秒':time.monotonic()-start})
# 成员列表可能分页，独立数据库核验整个批次，不把第一页缺席误判失败。
query="SELECT user_id,role FROM governance_membership WHERE scope_type='group' AND scope_id="+str(g['id'])+' AND user_id IN ('+','.join(str(x['id']) for x in S['users'][1:11])+') ORDER BY user_id;'
r=subprocess.run(['docker','exec','-i','user-permission-20260918-database-1','psql','-U','gitea','-d','gitea','-tA'],input=query,text=True,capture_output=True,timeout=15)
rows=[x.split('|') for x in r.stdout.splitlines()]
c.rec('C08','千名成员中的十人顺序批次角色变更',all(x['状态'] in (200,204) for x in changes) and len(rows)==10 and all(x[1]=='20' for x in rows),预期='每次使用最新revision，十名指定成员从Developer恢复Reporter；不代表单请求批量API',变更=changes,独立数据库角色=rows)
mem=a.run(['docker','stats','--no-stream','--format','{{.Name}} {{.MemUsage}} {{.CPUPerc}}','user-permission-20260918-gitea-1','user-permission-20260918-database-1'])
(c.OUT/'scale-resource-observation.txt').write_text('固定规模准备期间资源抽样，不是峰值：\n'+mem.stdout)
