#!/usr/bin/env python3
"""专用对象上的版本竞争、永久Owner与在途撤权实测。"""
import concurrent.futures,datetime,hashlib,importlib.util,json,secrets,subprocess,time
from pathlib import Path
sp=importlib.util.spec_from_file_location('base',Path(__file__).with_name('user-permission-acceptance.py'))
a=importlib.util.module_from_spec(sp);sp.loader.exec_module(a)
OUT=a.ROOT/'docs/evidence/governance-linkage-20260918';OUT.mkdir(exist_ok=True)
W=Path('/tmp/governance-concurrency-20260918');W.mkdir(mode=0o700,exist_ok=True)
runid=secrets.token_hex(3)
def rec(case,check,passed,**ev):
 r={'场景':case,'检查':check,'结果':'通过' if passed else '失败','时间':datetime.datetime.now(datetime.timezone.utc).isoformat(),'运行':runid,**ev}
 with (OUT/'concurrency-results.jsonl').open('a') as f:f.write(json.dumps(r,ensure_ascii=False)+'\n')
 print(case,check,r['结果'],flush=True)
def user(label):
 name='cc-'+label+'-'+runid;pwd=secrets.token_urlsafe(24)
 u=a.ok('POST','/admin/users',{'username':name,'password':pwd,'email':name+'@example.invalid','must_change_password':False})
 u={'id':u['id'],'name':name,'password':pwd};u['token']=a.ok('POST','/users/'+name+'/tokens',{'name':'联动并发验收','scopes':['all']},u)['sha1']
 key=a.WORK/(name+'-key');r=a.run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(key)]);assert r.returncode==0
 a.ok('POST','/user/keys',{'title':'联动验收','key':Path(str(key)+'.pub').read_text()},u)
 return u
def group(label):return a.ok('POST','/governance/groups',{'path':'cc-'+label+'-'+runid,'name':'并发联动验收','visibility':2})
def member(g,u,role,actor=None,revision=None):
 st=a.ok('GET',f'/governance/groups/{g["id"]}')
 p=f'/governance/groups/{g["id"]}/members/{u["id"]}'
 rev=st['revision'] if revision is None else revision
 return a.api('DELETE',p+'?revision='+str(rev),user=actor) if role is None else a.api('PUT',p,{'role':role,'revision':rev},actor)
def refs(r):
 v=a.run(['git','ls-remote',r['clone_url']],a.env_for(a.ADMIN));assert v.returncode==0
 return dict(x.split()[::-1] for x in v.stdout.splitlines())
def parallel(fs):
 with concurrent.futures.ThreadPoolExecutor(max_workers=len(fs)) as ex:return list(ex.map(lambda f:f(),fs))
def main():
 u=user('developer');g=group('revision');assert member(g,u,20)[0] in (200,201,204)
 rev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision']
 ans=parallel([lambda:member(g,u,30,revision=rev),lambda:member(g,u,10,revision=rev)])
 after=a.ok('GET',f'/governance/groups/{g["id"]}/members')
 rec('C01','同一成员相同revision并发修改',sum(200<=s<300 for s,b in ans)==1 and sum(s==409 for s,b in ans)==1,预期='一成功一版本冲突；最终只有成功版本',响应=ans,最终成员=after)
 r=a.ok('POST','/governance/groups/'+str(g['id'])+'/roles',{'name':'并发角色','base_role':20,'abilities':[],'revision':a.ok('GET',f'/governance/groups/{g["id"]}')['revision']})
 rev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision'];path=f'/governance/groups/{g["id"]}/roles/{r["id"]}'
 ans=parallel([lambda:a.api('PUT',path,{'name':'版本一','base_role':20,'abilities':['manage_approvals'],'revision':rev}),lambda:a.api('PUT',path,{'name':'版本二','base_role':20,'abilities':[],'revision':rev})])
 rec('C01','自定义角色相同revision并发修改',sum(200<=s<300 for s,b in ans)==1 and sum(s==409 for s,b in ans)==1,预期='一成功一冲突，不静默覆盖',响应=ans,最终角色=a.ok('GET',f'/governance/groups/{g["id"]}/roles'))
 owners=[user('owner-a'),user('owner-b')];og=group('owners')
 for x in owners:assert member(og,x,50)[0] in (200,201,204)
 admin=a.ok('GET','/user');team=og['native_owner_team_id'];a.ok('DELETE',f'/teams/{team}/members/{admin["login"]}')
 rev=a.ok('GET',f'/governance/groups/{og["id"]}')['revision']
 ans=parallel([lambda:member(og,owners[0],None,owners[0],rev),lambda:member(og,owners[1],None,owners[1],rev)])
 remaining=a.ok('GET',f'/governance/groups/{og["id"]}/members')
 rec('C02','两名永久Owner同时移除自己',sum(200<=s<300 for s,b in ans)==1 and any(s>=400 for s,b in ans),预期='只允许一个成功，保留永久Owner',响应=ans,最终成员=remaining)
 member(g,u,30)
 repo=a.ok('POST','/orgs/'+g['full_path']+'/repos',{'name':'race','private':True,'auto_init':True,'default_branch':'main'})
 state={'user':u,'group':g,'repo':repo,'owners':owners,'owner_group':og};f=W/('state-'+runid+'.json');f.write_text(json.dumps(state));f.chmod(0o600)
 root='/data/git/repositories/'+repo['full_name'].lower()+'.git/hooks/'
 original=a.run(['docker','exec',a.CONTAINER,'cat',root+'reference-transaction']).stdout
 for proto in ['HTTP','SSH']:
  member(g,u,30);d=W/('clone-'+proto+'-'+runid)
  url=repo['clone_url'] if proto=='HTTP' else repo['ssh_url']
  rr=a.run(['git','clone',url,str(d)],a.env_for(u));assert rr.returncode==0,rr.stderr
  sha=a.commit(d,'严格撤权-'+proto);before=refs(repo)
  marker='/tmp/cc-pause-'+runid+'-'+proto;hook=root+'pre-receive.d/zz-cc-pause'
  body='#!/bin/sh\ncat >/dev/null\ntouch '+marker+'.reached\nfor n in $(seq 1 30); do [ -f '+marker+'.release ] && exit 0; sleep 1; done\nexit 1\n'
  rr=subprocess.run(['docker','exec','-i',a.CONTAINER,'sh','-c','cat > "$1" && chmod 755 "$1"','sh',hook],input=body,text=True,capture_output=True);assert rr.returncode==0
  pending=subprocess.Popen(['git','push',url,'HEAD:refs/heads/feature/'+proto.lower()],cwd=d,env=a.env_for(u),stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
  try:
   reached=False
   for _ in range(100):
    if a.run(['docker','exec',a.CONTAINER,'test','-f',marker+'.reached']).returncode==0:reached=True;break
    if pending.poll() is not None:break
    time.sleep(.1)
   assert reached,'暂停点未到达，不能判断产品'
   status,response=member(g,u,None);assert status in (200,204)
   revoked=datetime.datetime.now(datetime.timezone.utc).isoformat()
   fresh=a.run(['git','ls-remote',url],a.env_for(u))
   a.run(['docker','exec',a.CONTAINER,'touch',marker+'.release']);stdout,stderr=pending.communicate(timeout=45)
   after=refs(repo);unchanged=a.run(['docker','exec',a.CONTAINER,'cat',root+'reference-transaction']).stdout==original
   rec('C04','严格撤权后在途推送-'+proto,pending.returncode!=0 and before==after,预期='撤权先完成后不得写引用',缺陷='PERM-RACE-001' if before!=after else '',归属='上游继承行为，严格增强验收目标未满足；不是已证新增回归',协议=proto,撤权状态=status,撤权完成时间=revoked,新读取退出=fresh.returncode,原生Hook不变=unchanged,本地SHA=sha,引用前=before,引用后=after,推送退出=pending.returncode,推送输出=stderr[-1800:])
  finally:
   a.run(['docker','exec',a.CONTAINER,'touch',marker+'.release']);a.run(['docker','exec',a.CONTAINER,'rm','-f',hook,marker+'.reached',marker+'.release'])
 member(g,u,30)
 print('状态文件',f,flush=True)
if __name__=='__main__':main()
