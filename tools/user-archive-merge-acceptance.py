#!/usr/bin/env python3
"""合法审批已齐全时，仓库和父组归档均须禁止合并。"""
import importlib.util
from pathlib import Path
import sys,time
spec=importlib.util.spec_from_file_location('a',Path(__file__).with_name('user-permission-acceptance.py'))
a=importlib.util.module_from_spec(spec);spec.loader.exec_module(a)
sys.argv=[sys.argv[0],'archive-merge']
g=a.ok('POST','/governance/groups',{'path':'archive-merge-check','visibility':2});a.S['组织']['archive-merge']=g
for name in ['role-30','approver-one','approver-two']:a.member('archive-merge',a.S['用户'][name],30)
repo=a.ok('POST','/orgs/'+g['full_path']+'/repos',{'name':'private-project','private':True,'auto_init':True,'default_branch':'main'});a.S['仓库']['archive-merge']=repo
path='/repos/'+repo['full_name'];actor=a.S['用户']['role-30']
a.ok('POST',path+'/branch_protections',{'rule_name':'main','enable_push':False,'required_approvals':2,'block_admin_merge_override':True})
folder,r=a.local('archive-merge',actor,'HTTP','archive-merge');assert r.returncode==0,r.stderr
sha=a.commit(folder,'归档合并验收');r=a.run(['git','push',repo['clone_url'],'HEAD:refs/heads/feature/archive'],a.env_for(actor),folder);assert r.returncode==0,r.stderr
pr=a.ok('POST',path+'/pulls',{'title':'归档合并边界','head':'feature/archive','base':'main'},actor);pp=path+'/pulls/'+str(pr['number'])
for name in ['approver-one','approver-two']:a.ok('POST',pp+'/reviews',{'event':'APPROVED','body':'归档前有效审批'},a.S['用户'][name])
for _ in range(50):
 if a.ok('GET',pp)['mergeable']:break
 time.sleep(.2)
else:raise RuntimeError('未满足可合并前置条件')
before=a.refs('archive-merge')
for kind in ['repository','group']:
 if kind=='repository':a.ok('PATCH',path,{'archived':True})
 else:
  current=a.ok('GET',f'/governance/groups/{g["id"]}');a.ok('PUT',f'/governance/groups/{g["id"]}/archive',{'archived':True,'revision':current['revision']})
 status,body=a.api('POST',pp+'/merge',{'Do':'merge','head_commit_id':sha},actor)
 a.record('C10','审批齐全但归档仍拒绝合并-'+kind,status in [403,409,423] and a.refs('archive-merge')==before and not a.ok('GET',pp)['merged'],HTTP状态=status,响应=body,操作前=before,操作后=a.refs('archive-merge'))
 if kind=='repository':a.ok('PATCH',path,{'archived':False})
 else:
  current=a.ok('GET',f'/governance/groups/{g["id"]}');a.ok('PUT',f'/governance/groups/{g["id"]}/archive',{'archived':False,'revision':current['revision']})
for name in ['approver-one','approver-two']:a.ok('POST',pp+'/reviews',{'event':'APPROVED','body':'恢复后重新核对'},a.S['用户'][name])
for _ in range(50):
 if a.ok('GET',pp)['mergeable']:break
 time.sleep(.2)
status,body=a.api('POST',pp+'/merge',{'Do':'merge','head_commit_id':sha},actor)
a.record('C10','恢复后重新审批可完成合并',status==200 and a.ok('GET',pp)['merged'],HTTP状态=status,响应=body,操作后=a.refs('archive-merge'))
