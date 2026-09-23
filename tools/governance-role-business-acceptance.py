#!/usr/bin/env python3
"""补充角色对应的事项、发布、Wiki和仓库管理实际业务状态。"""
import base64,importlib.util,json
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'));c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
S=json.loads(Path('/tmp/gitea-user-acceptance-20260918/rm-160944-e906/state.json').read_text());users=S['用户'];g=c.group('role-business')
for label,role in [('minimal',5),('guest',10),('planner',15),('reporter',20),('developer',30),('maintainer',40),('owner',50)]:assert c.member(g,users[label],role)[0] in (200,204)
r=a.ok('POST','/orgs/'+g['full_path']+'/repos',{'name':'business','private':True,'auto_init':True});path='/repos/'+r['full_name'];other=a.ok('POST',path+'/issues',{'title':'开发者事项','body':'原文'},users['developer'])
status,_=a.api('GET',f'/governance/groups/{g["id"]}',user=users['minimal']);issues=a.api('GET',path+'/issues',user=users['minimal'])[0];pulls=a.api('GET',path+'/pulls',user=users['minimal'])[0]
c.rec('R02','Minimal可见组织但不可读私有事项与PR',status==200 and issues in (403,404) and pulls in (403,404),预期='组织读取200，私有事项和PR拒绝',状态=[status,issues,pulls])
for label,case,manage in [('guest','R03',False),('reporter','R05',False),('planner','R04',True)]:
 u=users[label];own=a.ok('POST',path+'/issues',{'title':label+'本人事项','body':'创建'},u);edit_status,_=a.api('PATCH',path+'/issues/'+str(own['number']),{'body':'本人已编辑'},u);own_after=a.ok('GET',path+'/issues/'+str(own['number']))
 before=a.ok('GET',path+'/issues/'+str(other['number']));st,_=a.api('PATCH',path+'/issues/'+str(other['number']),{'title':label+'修改他人事项'},u);after=a.ok('GET',path+'/issues/'+str(other['number']))
 c.rec(case,label+'本人事项与他人管理边界',edit_status==201 and own_after['body']=='本人已编辑' and ((st==201 and after['title']==label+'修改他人事项') if manage else (st in (403,404) and before==after)),预期='本人创建编辑成功；仅Planner可管理他人事项',本人编辑状态=edit_status,本人正文=own_after['body'],他人编辑状态=st,他人标题前=before['title'],他人标题后=after['title'])
rel=a.ok('POST',path+'/releases',{'tag_name':'v-role','target_commitish':'main','name':'角色读取发布','body':'验收发布','draft':False})
st,body=a.api('GET',path+'/releases/'+str(rel['id']),user=users['reporter'])
c.rec('R05','Reporter读取已发布版本',st==200 and body.get('body')=='验收发布',预期='发布内容可读',状态=st,发布ID=rel['id'])
wst,wbody=a.api('POST',path+'/wiki/new',{'title':'Home','content_base64':base64.b64encode('私有Wiki验收'.encode()).decode(),'message':'验收初始化'})
if wst in (200,201):
 st,body=a.api('GET',path+'/wiki/page/Home',user=users['reporter']);content=base64.b64decode(body.get('content_base64','')).decode() if isinstance(body,dict) else ''
 c.rec('R05','Reporter读取私有Wiki',st==200 and content=='私有Wiki验收',预期='具备read_wiki可读取Wiki正文',状态=st,正文匹配=content=='私有Wiki验收')
else:c.rec('R05','Wiki准备未完成',False,预期='Wiki准备成功后测读',状态=wst,响应=wbody,归类='执行器前置待核实，不能直接登记产品缺陷')
u=users['maintainer'];created=a.ok('POST','/orgs/'+g['full_path']+'/repos',{'name':'maint-created','private':True,'auto_init':True},u);p='/repos/'+created['full_name'];st,_=a.api('PATCH',p,{'description':'维护者合法设置'},u);actual=a.ok('GET',p)
mp=f'/governance/repositories/{created["id"]}/members';rev=a.ok('GET',mp)['revision'];add=a.api('PUT',mp+'/'+str(users['outsider']['id']),{'role':10,'revision':rev},u)[0];rev=a.ok('GET',mp)['revision'];delete=a.api('DELETE',mp+'/'+str(users['outsider']['id'])+'?revision='+str(rev),user=u)[0];final=a.ok('GET',mp)
c.rec('R07','Maintainer建库设置与仓库成员增删',st==200 and actual['description']=='维护者合法设置' and add==204 and delete==204 and not final['members'] and bool(c.refs(created)),预期='有明确能力的仓库管理与创建成功，成员最终无残留',仓库ID=created['id'],设置状态=st,添加状态=add,删除状态=delete,成员最终=final['members'])
