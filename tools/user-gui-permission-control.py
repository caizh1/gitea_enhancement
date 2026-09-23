import importlib.util,json,subprocess,base64,sys,datetime
from pathlib import Path
s=importlib.util.spec_from_file_location('a','/Users/archer/Work/gitea-enhancement/tools/user-permission-acceptance.py');a=importlib.util.module_from_spec(s);s.loader.exec_module(a)
w=Path('/tmp/gitea-gui-permissions');st=json.loads((w/'state.json').read_text());repo=w/'repo';p='/repos/'+st['repo']['full_name'];mode=sys.argv[1]
def refs():
 r=subprocess.run(['git','ls-remote',a.BASE+'/'+st['repo']['full_name']+'.git'],env=a.env_for(a.ADMIN),text=True,capture_output=True,check=True);return dict(x.split()[::-1] for x in r.stdout.splitlines())
if mode=='identity':
 u=st[sys.argv[2]];h='Authorization: Basic '+base64.b64encode((u['name']+':'+u['token']).encode()).decode();subprocess.run(['git','config','--file',str(w/'gitconfig'),'http.extraHeader',h],cwd=repo,check=True)
elif mode=='before': (w/'before.json').write_text(json.dumps(refs()))
elif mode=='revoke': a.ok('DELETE',p+'/collaborators/'+st['reporter']['name'])
elif mode=='grant': a.ok('PUT',p+'/collaborators/'+st['reporter']['name'],{'permission':sys.argv[2]})
elif mode=='token-revoke':
 a.ok('DELETE','/users/'+st['reporter']['name']+'/tokens/'+str(st['reporter']['token_id']),user={'name':st['reporter']['name'],'password':st['reporter']['password']})
elif mode=='token-restore':
 u=st['reporter'];t=a.ok('POST','/users/'+u['name']+'/tokens',{'name':'恢复图形验收','scopes':['all']},user={'name':u['name'],'password':u['password']});u['token']=t['sha1'];u['token_id']=t['id'];(w/'state.json').write_text(json.dumps(st));(w/'state.json').chmod(0o600)
elif mode=='record':
 before=json.loads((w/'before.json').read_text());after=refs();head=subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip();logs=sorted((w/'profile/logs').glob('*/window*/exthost/vscode.git/Git.log'));tail=logs[-1].read_text()[-9000:];expect=sys.argv[4];passed=(before==after if expect=='deny' else after.get('refs/heads/feature/gui-negative')==head)
 row={'场景':sys.argv[2],'检查':sys.argv[3],'结果':'通过' if passed else '失败','时间':datetime.datetime.now(datetime.timezone.utc).isoformat(),'客户端':'macOS VS Code 1.138.0 CDP真实渲染UI','协议':('SSH+用户密钥' if subprocess.check_output(['git','remote','get-url','origin'],cwd=repo,text=True).startswith('ssh:') else 'HTTP+Token'),'远端之前':before,'远端之后':after,'本地提交':head,'日志':tail,'边界':'此记录仅覆盖明确列出的GUI动作，仓库初始clone由夹具准备，不算GUI clone'}
 out=Path('/Users/archer/Work/gitea-enhancement/docs/evidence/user-permission-20260918/gui-permissions-results.jsonl');out.open('a').write(json.dumps(row,ensure_ascii=False)+'\n');print(row['场景'],row['检查'],row['结果'])
