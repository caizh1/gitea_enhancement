#!/usr/bin/env python3
"""验证待接受及过期邀请不授予仓库读写；仅用于已启用 dummy mailer 的隔离实例。"""
import base64, datetime, json, os, secrets, subprocess, time, urllib.error, urllib.request
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]; WORK=Path('/tmp/gitea-user-acceptance-20260918'); BASE='http://127.0.0.1:3538'; CONTAINER='user-permission-20260918-database-1'
OUT=ROOT/'docs/evidence/user-permission-20260918/invitation-results.jsonl'; ADMIN={'name':'acceptance-admin','password':json.loads((WORK/'secrets.json').read_text())['管理员密码']}
def api(method,path,data=None,user=None):
 user=user or ADMIN; auth='token '+user['token'] if 'token' in user else 'Basic '+base64.b64encode(f"{user['name']}:{user['password']}".encode()).decode(); req=urllib.request.Request(BASE+'/api/v1'+path,data=json.dumps(data).encode() if data is not None else None,headers={'Authorization':auth,'Content-Type':'application/json'},method=method)
 try:r=urllib.request.urlopen(req,timeout=60)
 except urllib.error.HTTPError as e:r=e
 raw=r.read()
 try:b=json.loads(raw)
 except ValueError:b=raw.decode(errors='replace')
 return r.status,b
def ok(m,p,d=None,u=None):
 s,b=api(m,p,d,u)
 if not 200<=s<300: raise RuntimeError(f'{m} {p}: {s} {b}')
 return b
def run(a,cwd=None,env=None): return subprocess.run(a,cwd=cwd,env=env,capture_output=True,text=True,timeout=90)
def new_user(name):
 pwd=secrets.token_urlsafe(24); x=ok('POST','/admin/users',{'username':name,'password':pwd,'email':name+'@example.invalid','must_change_password':False}); u={'name':name,'password':pwd,'id':x['id'],'email':name+'@example.invalid'}; u['token']=ok('POST',f'/users/{name}/tokens',{'name':'C07隔离验收','scopes':['all']},u)['sha1']; key=WORK/(name+'-key'); r=run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(key)]); assert r.returncode==0; ok('POST','/user/keys',{'title':'C07隔离验收','key':Path(str(key)+'.pub').read_text()},u); u['key']=str(key); return u
def env_for(u):
 e=os.environ.copy(); basic=base64.b64encode(f"{u['name']}:{u.get('token',u.get('password'))}".encode()).decode()
 for k in list(e):
  if k.startswith('GIT_'): del e[k]
 e.update({'GIT_CONFIG_NOSYSTEM':'1','GIT_CONFIG_GLOBAL':'/dev/null','GIT_CONFIG_COUNT':'2','GIT_CONFIG_KEY_0':'http.extraHeader','GIT_CONFIG_VALUE_0':'Authorization: Basic '+basic,'GIT_CONFIG_KEY_1':'credential.helper','GIT_CONFIG_VALUE_1':'','GIT_TERMINAL_PROMPT':'0','GIT_SSH_COMMAND':f"ssh -i {u.get('key','/dev/null')} -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={WORK}/known-hosts"}); return e
def refs(repo):
 r=run(['git','ls-remote',repo['clone_url']],env=env_for(ADMIN)); assert r.returncode==0; return dict(x.split()[::-1] for x in r.stdout.splitlines())
def record(check,passed,**ev):
 row={'场景':'C07','检查':check,'结果':'通过' if passed else '失败','时间':datetime.datetime.now(datetime.timezone.utc).isoformat(),**ev}
 with OUT.open('a') as f:f.write(json.dumps(row,ensure_ascii=False)+'\n')
 print(check,row['结果'])
def denied(repo,u,label):
 for protocol,url in [('HTTP',repo['clone_url']),('SSH',repo['ssh_url'])]:
  d=WORK/f"c07-{u['name']}-{label}-{protocol.lower()}"; r=run(['git','clone',url,str(d)],env=env_for(u)); before=refs(repo)
  seed=WORK/f"c07-seed-{u['name']}-{label}-{protocol.lower()}"; c=run(['git','clone',repo['clone_url'],str(seed)],env=env_for(ADMIN)); assert c.returncode==0
  (seed/'c07.txt').write_text('邀请未接受不得写入\n')
  for cmd in [['git','config','user.name','邀请验收'],['git','config','user.email','c07@example.invalid'],['git','add','c07.txt'],['git','commit','-m','test: 验证邀请边界']]: assert run(cmd,seed).returncode==0
  p=run(['git','push',url,f'HEAD:refs/heads/c07-{label}-{protocol.lower()}'],seed,env_for(u)); after=refs(repo); record(label+'邀请不授予读写',r.returncode!=0 and p.returncode!=0 and before==after,协议=protocol,克隆退出码=r.returncode,推送退出码=p.returncode,操作前=before,操作后=after,输出=(r.stderr+p.stderr)[-1600:])
def main():
 if OUT.exists():raise RuntimeError('证据文件已存在')
 sx=secrets.token_hex(4); g=ok('POST','/governance/groups',{'name':'邀请验收组','path':'invite-'+sx,'parent_id':0,'visibility':2,'revision':0}); repo=ok('POST',f"/orgs/{g['full_path']}/repos",{'name':'invite-repo-'+sx,'private':True,'auto_init':True,'default_branch':'main'}); pending=new_user('invite-pending-'+sx); expired=new_user('invite-expired-'+sx)
 inv=[]
 for u in [pending,expired]:
  state=ok('GET',f"/governance/groups/{g['id']}"); inv.append(ok('POST',f"/governance/groups/{g['id']}/invitations",{'email':u['email'],'role':30,'revision':state['revision']}))
 denied(repo,pending,'待接受'); denied(repo,expired,'待接受对照')
 past=int(time.time())-60; q=f"update governance_invitation set expires_unix={past} where id={int(inv[1]['id'])} returning id,expires_unix"; r=run(['docker','exec',CONTAINER,'psql','-U','gitea','-d','gitea','-tAc',q]); assert r.returncode==0 and str(inv[1]['id']) in r.stdout; denied(repo,expired,'过期fixture')
 state=ok('GET',f"/governance/groups/{g['id']}"); ok('PUT',f"/governance/groups/{g['id']}/members/{pending['id']}",{'role':30,'revision':state['revision']})
 for protocol,url in [('HTTP',repo['clone_url']),('SSH',repo['ssh_url'])]:
  d=WORK/f'c07-control-{sx}-{protocol.lower()}'; c=run(['git','clone',url,str(d)],env=env_for(pending)); assert c.returncode==0; filename='control-'+protocol.lower()+'.txt'; (d/filename).write_text('正式成员读写对照：'+protocol+'\n')
  for cmd in [['git','config','user.name','正式成员'],['git','config','user.email','control@example.invalid'],['git','add',filename],['git','commit','-m','test: 正式成员对照']]:assert run(cmd,d).returncode==0
  sha=run(['git','rev-parse','HEAD'],d).stdout.strip(); branch='refs/heads/c07-control-'+protocol.lower(); p=run(['git','push',url,'HEAD:'+branch],d,env_for(pending)); record('正式成员读写合法对照',p.returncode==0 and refs(repo).get(branch)==sha,协议=protocol,克隆退出码=c.returncode,推送退出码=p.returncode,本地SHA=sha)
 record('验收边界说明',True,dummy邮件='只记录本地日志，不代表真实邮件投递验收',过期方式='仅将本次专属邀请行 expires_unix 调整到过去，不等待90天，不修改授权业务逻辑，不读取或解密邀请令牌',未覆盖='邀请接受全流程')
if __name__=='__main__':main()
