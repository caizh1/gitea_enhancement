#!/usr/bin/env python3
"""补齐事项、共享能力交集、管理范围与标签入口的真实验收。"""
import importlib.util
import sys
from pathlib import Path
spec = importlib.util.spec_from_file_location('a', Path(__file__).with_name('user-permission-acceptance.py'))
a = importlib.util.module_from_spec(spec)
spec.loader.exec_module(a)
sys.argv = [sys.argv[0], 'api-completion']
path = '/repos/' + a.S['仓库']['sdk']['full_name']
gid = a.S['组织']['alpha']['id']
for role,case in [(5,'A02'),(10,'A03'),(15,'A04')]:
    actor=a.S['用户']['role-'+str(role)]
    status,body=a.api('GET',f'/governance/groups/{gid}',user=actor)
    a.record(case,'授权组织详情可见',status==200,角色=role,HTTP状态=status)
    status,body=a.api('GET',path+'/contents/README.md',user=actor)
    a.record(case,'事项角色不能取得私有代码内容API',status in [403,404],角色=role,HTTP状态=status,响应=body)
    status,body=a.api('GET',path+'/issues',user=actor)
    a.record(case,'事项读取能力',status==200 if role>=10 else status in [403,404],角色=role,HTTP状态=status)
    if role>=10:
        status,body=a.api('POST',path+'/issues',{'title':'事项权限验收-'+str(role),'body':'仅用于隔离验收。Assisted-by: Codex:GPT-6'},actor)
        a.record(case,'事项创建能力',status==201 if role==15 else status in [403,404],角色=role,HTTP状态=status)
        if status==201:
            es,eb=a.api('PATCH',path+'/issues/'+str(body['number']),{'title':'事项权限验收-已编辑'},actor)
            a.record(case,'Planner编辑事项',es==201 and eb.get('title')=='事项权限验收-已编辑',HTTP状态=es)
    if role==15:
        status,body=a.api('GET',path+'/pulls',user=actor)
        a.record(case,'Planner读取PR列表',status==200,HTTP状态=status)
outsider=a.S['用户']['outsider']
for endpoint,label in [(path,'详情API'),(path+'/contents/README.md','内容API')]:
    status,body=a.api('GET',endpoint,user=outsider)
    a.record('A01','非成员拒绝'+label,status in [403,404],HTTP状态=status,响应=body)
status,body=a.api('GET','/repos/search?q=dev-sdk&limit=50',user=outsider)
a.record('A01','搜索不返回无权私有仓库',status==200 and a.S['仓库']['sdk']['id'] not in {r['id'] for r in body.get('data',[])},HTTP状态=status,返回仓库ID=[r['id'] for r in body.get('data',[])])
u=a.S['用户']['shared-user']
for role,ceiling in [(15,20),(20,15),(15,30)]:
    a.member('partner',u,role)
    a.share('sdk','partner',ceiling)
    status,body=a.api('GET',path+'/pulls',user=u)
    a.record('C04','共享交集保留PR读取',status==200,成员角色=role,共享上限=ceiling,HTTP状态=status)
    status,body=a.api('POST',path+'/issues',{'title':f'共享事项交集-{role}-{ceiling}','body':'隔离测试。Assisted-by: Codex:GPT-6'},u)
    a.record('C04','共享事项写入按能力交集',status==201 if ceiling==30 else status in [403,404],成员角色=role,共享上限=ceiling,HTTP状态=status)
a.share('sdk','partner')
# 仅增加审批管理能力，不得得到代码或无关项目管理能力。
u=a.user('api-custom-user')
state=a.ok('GET',f'/governance/groups/{gid}')
role=a.ok('POST',f'/governance/groups/{gid}/roles',{'name':'仅审批管理验收','base_role':5,'abilities':['manage_approvals'],'revision':state['revision']})
a.member('alpha',u,5,custom_role_id=role['id'])
a.probe('B10','sdk',u,False,False,'approval-only')
before=a.ok('GET',path)
status,body=a.api('PATCH',path,{'description':'越界设置不可保存'},u)
after=a.ok('GET',path)
a.record('B10','审批管理角色不能修改无关仓库设置',status>=400 and before['description']==after['description'],HTTP状态=status,响应=body)
# 同一会话能力增减及恢复。
for label,abilities,read,write in [('read-push',['read_code','push_code'],True,True),('read-only',['read_code'],True,False),('restored',['read_code','push_code'],True,True)]:
    state=a.ok('GET',f'/governance/groups/{gid}')
    a.ok('PUT',f'/governance/groups/{gid}/roles/{role["id"]}',{'name':'窄能力生命周期验收','base_role':5,'abilities':abilities,'revision':state['revision']})
    a.probe('C11','sdk',u,read,write,label)
a.member('alpha',u)
a.member('alpha/platform',u,50)
for group,repo in [('alpha','root'),('alpha/business','business')]:
    target=a.S['组织'][group]['id']; rid=a.S['仓库'][repo]['id']
    state=a.ok('GET',f'/governance/groups/{target}')
    for label,endpoint,data,witness in [
        ('角色',f'/governance/groups/{target}/roles',{'name':'越界角色','base_role':50,'abilities':[],'revision':state['revision']},f'/governance/groups/{target}/roles'),
        ('共享',f'/governance/groups/{target}/shares/{a.S["组织"]["partner"]["id"]}',{'max_role':50,'revision':state['revision']},f'/governance/groups/{target}/shares'),
        ('项目授权',f'/governance/repositories/{rid}/members/{outsider["id"]}',{'role':50,'revision':a.ok('GET',f'/governance/repositories/{rid}/members')['revision']},f'/governance/repositories/{rid}/members')]:
        old=a.ok('GET',witness)
        status,body=a.api('POST' if label=='角色' else 'PUT',endpoint,data,u)
        new=a.ok('GET',witness)
        a.record('B11','子组Owner不得越界修改'+label,status in [403,404] and old==new,目标=group,HTTP状态=status,响应=body,数据未变=old==new)
a.member('alpha/platform',u)
# 正常标签与实际已有受保护标签更新分别验证。
for protocol in ['HTTP','SSH']:
    developer=a.S['用户']['role-30']; owner=a.S['用户']['role-50']; reporter=a.S['用户']['role-20']
    folder,r=a.local('sdk',developer,protocol,'tag-completion')
    assert r.returncode==0,r.stderr
    sha=a.commit(folder,'标签更新验收-'+protocol)
    tag='refs/tags/test-completion-'+protocol.lower()
    before=a.refs('sdk')
    r=a.run(['git','push',a.url('sdk',protocol),'HEAD:'+tag],a.env_for(reporter),folder)
    a.record('D07','Reporter不能创建普通标签',r.returncode!=0 and a.refs('sdk')==before,协议=protocol,退出码=r.returncode,操作前=before,操作后=a.refs('sdk'))
    r=a.run(['git','push',a.url('sdk',protocol),'HEAD:'+tag],a.env_for(developer),folder)
    a.record('D07','Developer可以创建普通标签',r.returncode==0 and a.refs('sdk').get(tag)==sha,协议=protocol,退出码=r.returncode,远端SHA=a.refs('sdk').get(tag))
    protected='refs/tags/release-existing-'+protocol.lower()
    before=a.refs('sdk');assert protected in before
    r=a.run(['git','push','--force',a.url('sdk',protocol),'HEAD:'+protected],a.env_for(developer),folder)
    a.record('D07','Developer不能更新已有保护标签',r.returncode!=0 and a.refs('sdk')==before,协议=protocol,退出码=r.returncode,操作前=before,操作后=a.refs('sdk'))
    r=a.run(['git','push','--force',a.url('sdk',protocol),'HEAD:'+protected],a.env_for(owner),folder)
    a.record('D07','指定发布者可以更新已有保护标签',r.returncode==0 and a.refs('sdk').get(protected)==sha,协议=protocol,退出码=r.returncode,远端SHA=a.refs('sdk').get(protected))
