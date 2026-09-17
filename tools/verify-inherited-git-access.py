#!/usr/bin/env python3
"""对隔离样本验证真实 Git 读取、撤权与列表消失，不记录密钥或密码。"""
import contextlib,io,runpy,subprocess,os,json,base64
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
with contextlib.redirect_stdout(io.StringIO()):m=runpy.run_path(str(ROOT/'tools/reproduce-group-access.py'))
f=m['fixtures'];u=next(x for x in f['users'] if x['role']==30);api=m['api'];req=m['request'];w=ROOT/'work/group-access-regression'
key=w/'git-test-key'
if not key.exists():
 subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(key)],check=True)
 status,_=req('POST','/user/keys',{'title':'隔离继承验收','key':Path(str(key)+'.pub').read_text()},auth=(u['name'],u['password']))
 assert status==201
headers='Authorization: Basic '+base64.b64encode((u['name']+':'+u['password']).encode()).decode()
env=os.environ.copy();env.update({'GIT_CONFIG_COUNT':'1','GIT_CONFIG_KEY_0':'http.extraHeader','GIT_CONFIG_VALUE_0':headers,'GIT_TERMINAL_PROMPT':'0','GIT_SSH_COMMAND':f'ssh -i {key} -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={w}/known-hosts'})
def git(url):
 r=subprocess.run(['git','ls-remote',url,'refs/heads/main'],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=30)
 return r.returncode==0 and b'refs/heads/main' in r.stdout
urls=['http://127.0.0.1:3520/inherit-repro/child/private-code.git','ssh://git@127.0.0.1:4520/inherit-repro/child/private-code.git']
result={'撤权前HTTP':git(urls[0]),'撤权前SSH':git(urls[1])}
assert all(result.values()),result
state=api('GET',f'/governance/groups/{f["top"]}')
api('DELETE',f'/governance/groups/{f["top"]}/members/{u["id"]}?revision={state["revision"]}')
result.update({'撤权后HTTP拒绝':not git(urls[0]),'撤权后SSH拒绝':not git(urls[1])})
status,rows=req('GET','/user/repos',auth=(u['name'],u['password']));result['撤权后列表移除']=status==200 and all(x['id']!=f['repo'] for x in rows)
status,_=req('GET','/repos/inherit-repro/child/private-code',auth=(u['name'],u['password']));result['撤权后详情拒绝']=status==404
(w/'git-revocation.json').write_text(json.dumps(result,ensure_ascii=False,indent=2));print(json.dumps(result,ensure_ascii=False))
assert all(result.values())
