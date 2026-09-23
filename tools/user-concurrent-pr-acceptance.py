#!/usr/bin/env python3
"""验证两个 PR 并发合并同一目标分支时不丢提交。"""
import base64, concurrent.futures, datetime, json, os, secrets, subprocess, time, urllib.error, urllib.request
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]; WORK=Path('/tmp/gitea-user-acceptance-20260918'); BASE='http://127.0.0.1:3538'
OUT=ROOT/'docs/evidence/user-permission-20260918/concurrent-pr-results.jsonl'; ADMIN={'name':'acceptance-admin','password':json.loads((WORK/'secrets.json').read_text())['管理员密码']}
def api(method,path,data=None,user=None):
 user=user or ADMIN; auth='token '+user['token'] if 'token' in user else 'Basic '+base64.b64encode(f"{user['name']}:{user['password']}".encode()).decode(); req=urllib.request.Request(BASE+'/api/v1'+path,data=json.dumps(data).encode() if data is not None else None,headers={'Authorization':auth,'Content-Type':'application/json'},method=method)
 try:r=urllib.request.urlopen(req,timeout=90)
 except urllib.error.HTTPError as e:r=e
 raw=r.read()
 try:b=json.loads(raw)
 except ValueError:b=raw.decode(errors='replace')
 return r.status,b
def ok(m,p,d=None,u=None):
 s,b=api(m,p,d,u)
 if not 200<=s<300: raise RuntimeError(f'{m} {p}: {s} {b}')
 return b
def env(u):
 e=os.environ.copy(); basic=base64.b64encode(f"{u['name']}:{u.get('token',u.get('password'))}".encode()).decode()
 for k in list(e):
  if k.startswith('GIT_'): del e[k]
 e.update({'GIT_CONFIG_NOSYSTEM':'1','GIT_CONFIG_GLOBAL':'/dev/null','GIT_CONFIG_COUNT':'2','GIT_CONFIG_KEY_0':'http.extraHeader','GIT_CONFIG_VALUE_0':'Authorization: Basic '+basic,'GIT_CONFIG_KEY_1':'credential.helper','GIT_CONFIG_VALUE_1':'','GIT_TERMINAL_PROMPT':'0'}); return e
def run(a,cwd=None,u=ADMIN): return subprocess.run(a,cwd=cwd,env=env(u),capture_output=True,text=True,timeout=90)
def user(name):
 pwd=secrets.token_urlsafe(24); x=ok('POST','/admin/users',{'username':name,'password':pwd,'email':name+'@example.invalid','must_change_password':False}); u={'name':name,'password':pwd,'id':x['id']}; u['token']=ok('POST',f'/users/{name}/tokens',{'name':'并发PR验收','scopes':['all']},u)['sha1']; return u
def member(g,u):
 st=ok('GET',f"/governance/groups/{g['id']}"); ok('PUT',f"/governance/groups/{g['id']}/members/{u['id']}",{'role':30,'revision':st['revision']})
def refs(repo):
 r=run(['git','ls-remote',repo['clone_url']]);
 if r.returncode: raise RuntimeError('refs failed')
 return dict(x.split()[::-1] for x in r.stdout.splitlines())
def record(check,result,**ev):
 row={'场景':'F04','检查':check,'结果':result,'时间':datetime.datetime.now(datetime.timezone.utc).isoformat(),**ev}; OUT.write_text(json.dumps(row,ensure_ascii=False)+'\n'); print(result)
def wait_ready(repo,prs,actor):
 states={}
 for _ in range(80):
  states={p['number']:api('GET',f"/repos/{repo['full_name']}/pulls/{p['number']}",user=actor)[1] for p in prs}
  if all(x.get('mergeable') is True and not x.get('merged') for x in states.values()): return states
  time.sleep(.5)
 raise RuntimeError('后台合并计算未就绪')
def approve(repo,pr,reviewer,sha): ok('POST',f"/repos/{repo['full_name']}/pulls/{pr['number']}/reviews",{'event':'APPROVED','body':'F04并发验收','commit_id':sha},reviewer)
def main():
 if OUT.exists(): raise RuntimeError('证据已存在')
 sx=secrets.token_hex(4); g=ok('POST','/governance/groups',{'name':'并发合并组','path':'concurrent-'+sx,'parent_id':0,'visibility':2,'revision':0}); us={n:user(n+'-'+sx) for n in ['author','reviewer-a','reviewer-b','merger']}
 for u in us.values(): member(g,u)
 repo=ok('POST',f"/orgs/{g['full_path']}/repos",{'name':'concurrent-pr-'+sx,'private':True,'auto_init':True,'default_branch':'main'}); ok('PUT',f"/governance/repositories/{repo['id']}/approval-settings",{'prevent_author':True,'prevent_committer':False,'prevent_overrides':True,'reset_on_change':True,'require_reauthentication':False,'revision':0}); ok('POST',f"/governance/repositories/{repo['id']}/approval-rules",{'rule':{'name':'两人独立审批','required':2,'all_eligible':True,'branch_mode':'all','enabled':True},'revision':0}); ok('POST',f"/repos/{repo['full_name']}/branch_protections",{'rule_name':'main','enable_push':False,'required_approvals':0,'require_governance_approval':True})
 work=[]
 for i in [1,2]:
  d=WORK/f'concurrent-pr-{sx}-{i}'; r=run(['git','clone',repo['clone_url'],str(d)],u=us['author']);
  if r.returncode: raise RuntimeError(r.stderr)
  (d/f'change-{i}.txt').write_text(f'并发合并内容{i}\n')
  for c in [['git','config','user.name','并发作者'],['git','config','user.email','author@example.invalid'],['git','add',f'change-{i}.txt'],['git','commit','-m',f'test: 并发变更{i}']]:
   if run(c,d).returncode: raise RuntimeError('commit failed')
  sha=run(['git','rev-parse','HEAD'],d).stdout.strip(); branch=f'feature/concurrent-{i}'; r=run(['git','push',repo['clone_url'],f'HEAD:refs/heads/{branch}'],d,us['author']);
  if r.returncode: raise RuntimeError(r.stderr)
  pr=ok('POST',f"/repos/{repo['full_name']}/pulls",{'title':f'并发PR{i}','head':branch,'base':'main'},us['author']); work.append((pr,sha))
 for pr,sha in work:
  approve(repo,pr,us['reviewer-a'],sha); approve(repo,pr,us['reviewer-b'],sha)
 ready=wait_ready(repo,[x[0] for x in work],us['merger']); before=refs(repo)
 def merge(item):
  pr,sha=item; return pr['number'],api('POST',f"/repos/{repo['full_name']}/pulls/{pr['number']}/merge",{'Do':'merge','head_commit_id':sha},us['merger'])
 with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool: first=list(pool.map(merge,work))
 mid_refs=refs(repo); mid_pr={pr['number']:ok('GET',f"/repos/{repo['full_name']}/pulls/{pr['number']}",u=us['merger']) for pr,_ in work}; pending=[(pr,sha) for pr,sha in work if not mid_pr[pr['number']]['merged']]
 follow=[]
 for pr,sha in pending:
  approve(repo,pr,us['reviewer-a'],sha); approve(repo,pr,us['reviewer-b'],sha); wait_ready(repo,[pr],us['merger']); follow.append((pr['number'],api('POST',f"/repos/{repo['full_name']}/pulls/{pr['number']}/merge",{'Do':'merge','head_commit_id':sha},us['merger'])))
 final_pr={pr['number']:ok('GET',f"/repos/{repo['full_name']}/pulls/{pr['number']}",u=us['merger']) for pr,_ in work}; final_refs=refs(repo); verify=WORK/f'concurrent-pr-{sx}-verify'; run(['git','clone',repo['clone_url'],str(verify)]); contents=all((verify/f'change-{i}.txt').read_text()==f'并发合并内容{i}\n' for i in [1,2]); ancestors={sha:run(['git','merge-base','--is-ancestor',sha,'HEAD'],verify).returncode==0 for _,sha in work}; passed=all(x['merged'] for x in final_pr.values()) and contents and all(ancestors.values())
 record('两个PR并发合并及失败方重审恢复','通过' if passed else '失败',并发前PR={k:{'mergeable':v.get('mergeable'),'merged':v.get('merged')} for k,v in ready.items()},并发响应=first,操作前引用=before,并发后引用=mid_refs,并发后PR={k:{'merged':v['merged'],'merge_commit_sha':v.get('merge_commit_sha')} for k,v in mid_pr.items()},待恢复PR=[p['number'] for p,_ in pending],恢复响应=follow,最终引用=final_refs,最终PR={k:{'merged':v['merged'],'merge_commit_sha':v.get('merge_commit_sha')} for k,v in final_pr.items()},内容核对=contents,祖先核对=ancestors,说明='本次只证明该次常规同时调度，不代表穷尽所有竞态')
if __name__=='__main__': main()
