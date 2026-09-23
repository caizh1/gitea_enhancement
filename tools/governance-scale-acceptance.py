#!/usr/bin/env python3
"""固定规模真实API夹具与权限分页验收，准备断点落盘避免重复创建。"""
import concurrent.futures,datetime,importlib.util,json,math,secrets,statistics,time
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'));c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
W=Path('/tmp/governance-scale-20260918');W.mkdir(mode=0o700,exist_ok=True);F=W/'state.json'
S=json.loads(F.read_text()) if F.exists() else {'run':secrets.token_hex(3),'groups':[],'repos':[],'users':[]}
def save():F.write_text(json.dumps(S));F.chmod(0o600)
def ok(m,p,d=None,u=None):return a.ok(m,p,d,u)
policy=c.OUT/'scale-baseline.json'
if not policy.exists():policy.write_text(json.dumps({'冻结时间':datetime.datetime.now(datetime.timezone.utc).isoformat(),'计划组织数':200,'计划仓库数':2000,'计划成员数':1000,'常规深度':10,'边界深度':[20,21],'准备并发':2,'测量并发':1,'API读取P95阈值秒':2,'测量请求失败允许数':0,'权限集合错误允许数':0,'资源':'Docker共享18CPU/约8GiB；应用无独立限制，与其他隔离测试共用，记录环境干扰','样本':'200个仓库初始化内容，其余空仓；不代表大仓库代码搜索性能'},ensure_ascii=False,indent=2))
save()
if not S['groups']:
 for label in ['allowed','denied']:
  S['groups'].append(ok('POST','/governance/groups',{'path':'sc-'+label+'-'+S['run'],'name':'规模验收','visibility':2}));save()
# 两棵树各100个组织，其中链深10，其他为根下兄弟。
for side in range(2):
 prefix=S['groups'][side]['full_path'];owned=[g for g in S['groups'] if g['full_path']==prefix or g['full_path'].startswith(prefix+'/')]
 while len(owned)<100:
  i=len(owned);parent=owned[-1] if i<10 else owned[0]
  g=ok('POST','/governance/groups',{'path':'node-'+str(i),'parent_id':parent['id'],'name':'规模子组','visibility':2});S['groups'].append(g);owned.append(g);save()
print('规模准备：200组织完成',flush=True)
for i in range(len(S['users']),1000):
 name='sc-user-'+S['run']+'-'+str(i);pwd=secrets.token_urlsafe(20)
 u=ok('POST','/admin/users',{'username':name,'password':pwd,'email':name+'@example.invalid','must_change_password':False})
 entry={'id':u['id'],'name':name,'password':pwd};S['users'].append(entry);save()
 if (i+1)%100==0:print('规模准备：账号',i+1,flush=True)
g=S['groups'][0]
for i,u in enumerate(S['users']):
 if u.get('member'):continue
 rev=ok('GET',f'/governance/groups/{g["id"]}')['revision']
 ok('PUT',f'/governance/groups/{g["id"]}/members/{u["id"]}',{'role':30 if i==0 else 20,'revision':rev});u['member']=True;save()
 if (i+1)%100==0:print('规模准备：成员',i+1,flush=True)
existing={r['name'] for r in S['repos']}
def create(task):
 group,i=task;base='scale-repo-'+str(group['id'])+'-'+str(i);name=S.get('name_overrides',{}).get(base,base)
 return ok('POST','/orgs/'+group['full_path']+'/repos',{'name':name,'private':True,'auto_init':i==0,'default_branch':'main'})
tasks=[(group,i) for group in S['groups'] for i in range(10) if S.get('name_overrides',{}).get('scale-repo-'+str(group['id'])+'-'+str(i),'scale-repo-'+str(group['id'])+'-'+str(i)) not in existing]
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
 for r in pool.map(create,tasks):
  S['repos'].append(r);save()
  if len(S['repos'])%100==0:print('规模准备：仓库',len(S['repos']),flush=True)
u=S['users'][0];expected={r['id'] for r in S['repos'] if r['clone_url'].split(':3538/')[1].startswith(g['full_path']+'/')};seen=[];times=[];statuses=[]
for page in range(1,42):
 start=time.monotonic();status,data=a.api('GET',f'/user/repos?limit=50&page={page}',user=u);times.append(time.monotonic()-start);statuses.append(status)
 if status!=200:break
 seen += [r['id'] for r in data]
 if not data:break
p95=sorted(times)[math.ceil(len(times)*.95)-1]
c.rec('C08','固定200组织2000仓库1000成员权限分页',len(S['groups'])==200 and len(S['repos'])==2000 and len(S['users'])==1000 and set(seen)==expected and len(seen)==len(set(seen)) and all(v==200 for v in statuses),预期='仅出现本树1000仓库、无漏项重复或越界',组织数=len(S['groups']),仓库数=len(S['repos']),成员数=len(S['users']),期望仓库数=len(expected),实际仓库数=len(seen),遗漏=sorted(expected-set(seen)),越界=sorted(set(seen)-expected),状态码=statuses,请求耗时秒=times,P95秒=p95)
c.rec('C08','固定资源分页读取性能',p95<=2 and all(v==200 for v in statuses),预期='冻结P95不超过2秒且请求零失败',P95秒=p95,冻结阈值秒=2,边界='固定样本和当前共享资源；不代表生产容量或大型代码搜索')
print('规模分页完成',flush=True)
