#!/usr/bin/env python3
"""在独立 Demo 中复现顶层角色继承，凭据仅存本地工作目录。"""
import base64,json,secrets,urllib.request,urllib.error
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
WORK=ROOT/'work/group-access-regression'
BASE='http://127.0.0.1:3520'
ADMIN=('install-check',(ROOT/'work/navigation-review/combined-upgrade/test-password').read_text().strip())
def request(method,path,data=None,auth=ADMIN):
 headers={'Authorization':'Basic '+base64.b64encode(':'.join(auth).encode()).decode(),'Content-Type':'application/json'}
 req=urllib.request.Request(BASE+'/api/v1'+path,json.dumps(data).encode() if data is not None else None,headers,method=method)
 try:r=urllib.request.urlopen(req,timeout=30)
 except urllib.error.HTTPError as e:r=e
 b=r.read()
 try:b=json.loads(b)
 except ValueError:b=b.decode()
 return r.status,b
def api(method,path,data=None):
 status,b=request(method,path,data)
 assert status<300,(path,status,b)
 return b
f=WORK/'fixtures.json'
if not f.exists():
 top=api('POST','/governance/groups',{'path':'inherit-repro','visibility':2})
 child=api('POST','/governance/groups',{'path':'child','parent_id':top['id'],'visibility':2})
 repo=api('POST','/orgs/inherit-repro/child/repos',{'name':'private-code','private':True,'auto_init':True})
 users=[]
 for role in [5,10,20,30,40,50]:
  name='inherit-role-'+str(role);pwd=secrets.token_urlsafe(22)
  u=api('POST','/admin/users',{'username':name,'password':pwd,'email':name+'@example.invalid','must_change_password':False})
  state=api('GET',f'/governance/groups/{top["id"]}')
  api('PUT',f'/governance/groups/{top["id"]}/members/{u["id"]}',{'role':role,'revision':state['revision']})
  users.append({'name':name,'password':pwd,'id':u['id'],'role':role})
 f.write_text(json.dumps({'top':top['id'],'child':child['id'],'repo':repo['id'],'users':users}));f.chmod(0o600)
fixtures=json.loads(f.read_text());result=[]
for u in fixtures['users']:
 auth=(u['name'],u['password']);row={'角色':u['role']}
 for key,path in [('子组导航',f'/governance/navigation/groups/{fixtures["child"]}'),('仓库详情','/repos/inherit-repro/child/private-code'),('仓库列表','/orgs/inherit-repro/child/repos'),('个人仓库','/user/repos')]:
  status,data=request('GET',path,auth=auth)
  row[key]={'状态':status,'包含仓库':any(i.get('id')==fixtures['repo'] and i.get('type','repository')=='repository' for i in (data.get('items',[]) if isinstance(data,dict) else data)) if status==200 and (isinstance(data,list) or key=='子组导航') else None}
 result.append(row)
print(json.dumps(result,ensure_ascii=False,indent=2))
