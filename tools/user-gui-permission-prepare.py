import importlib.util,secrets,json,subprocess,base64
from pathlib import Path
s=importlib.util.spec_from_file_location('a','/Users/archer/Work/gitea-enhancement/tools/user-permission-acceptance.py');a=importlib.util.module_from_spec(s);s.loader.exec_module(a)
w=Path('/tmp/gitea-gui-permissions');state={}
for label in ['developer','reporter']:
 name='gui-negative-'+label;pwd=secrets.token_urlsafe(24)
 u=a.ok('POST','/admin/users',{'username':name,'password':pwd,'email':name+'@example.invalid','must_change_password':False})
 user={'name':name,'password':pwd,'id':u['id']};t=a.ok('POST','/users/'+name+'/tokens',{'name':'图形权限测试','scopes':['all']},user);user['token']=t['sha1'];user['token_id']=t['id'];state[label]=user
r=a.ok('POST','/admin/users/'+state['developer']['name']+'/repos',{'name':'gui-permissions','private':True,'auto_init':True,'default_branch':'main'});state['repo']=r
p='/repos/'+r['full_name'];a.ok('PUT',p+'/collaborators/'+state['reporter']['name'],{'permission':'read'})
a.ok('POST',p+'/branch_protections',{'branch_name':'main','enable_push':False,'enable_force_push':False,'enable_merge_whitelist':False})
(w/'state.json').write_text(json.dumps(state));(w/'state.json').chmod(0o600)
repo=w/'repo';env=a.env_for(state['developer']);subprocess.run(['git','clone',a.BASE+'/'+r['full_name']+'.git',str(repo)],env=env,check=True,capture_output=True)
for k,v in [('user.name','图形权限验收'),('user.email','gui@example.invalid'),('credential.helper',''),('http.extraHeader','Authorization: Basic '+base64.b64encode((state['reporter']['name']+':'+state['reporter']['token']).encode()).decode())]:subprocess.run(['git','config',k,v],cwd=repo,check=True)
print('独立夹具已准备；当前仓库HTTP认证身份Reporter')
