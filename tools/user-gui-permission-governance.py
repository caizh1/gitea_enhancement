import importlib.util,json,sys,subprocess
from pathlib import Path
s=importlib.util.spec_from_file_location('c','/tmp/gitea-gui-permissions/control.py');c=importlib.util.module_from_spec(s);s.loader.exec_module(c)
a=c.a;st=c.st;w=c.w;u=st['reporter'];mode=sys.argv[1]
def mem(g,role):
 rev=a.ok('GET','/governance/groups/'+str(g['id']))['revision'];p=f'/governance/groups/{g["id"]}/members/{u["id"]}'
 return a.ok('PUT',p,{'role':role,'revision':rev}) if role else a.ok('DELETE',p+'?revision='+str(rev))
if mode=='groups':
 gs=a.ok('GET','/governance/groups'); gs=gs if isinstance(gs,list) else gs.get('data',[]); g=next(x for x in gs if x.get('path')=='gui-negative-parent'); child=a.ok('GET','/governance/groups/'+str(a.ok('GET','/orgs/gui-negative-parent/child')['id'])); invite=next(x for x in gs if x.get('path')=='gui-negative-invite');st.update(parent=g,child=child,invite=invite)
 org=a.ok('GET','/orgs/gui-negative-parent/child');r=a.ok('POST','/repos/'+st['repo']['full_name']+'/transfer',{'new_owner':org['username']});r=a.ok('GET','/repositories/'+str(st['repo']['id']));st['repo']=r
 (w/'state.json').write_text(json.dumps(st));(w/'state.json').chmod(0o600)
 subprocess.run(['git','remote','set-url','origin',a.BASE+'/'+r['full_name']+'.git'],cwd=c.repo,check=True)
elif mode=='parent':mem(st['parent'],int(sys.argv[2]))
elif mode=='invite':mem(st['invite'],int(sys.argv[2]))
elif mode=='share':
 rid=st['repo']['id'];gid=st['invite']['id'];state=a.ok('GET',f'/governance/repositories/{rid}/shares');p=f'/governance/repositories/{rid}/shares/{gid}';role=int(sys.argv[2]);a.ok('PUT',p,{'max_role':role,'revision':state['revision']}) if role else a.ok('DELETE',p+'?revision='+str(state['revision']))
elif mode=='direct':
 rid=st['repo']['id'];state=a.ok('GET',f'/governance/repositories/{rid}/members');p=f'/governance/repositories/{rid}/members/{u["id"]}';role=int(sys.argv[2]);a.ok('PUT',p,{'role':role,'revision':state['revision']}) if role else a.ok('DELETE',p+'?revision='+str(state['revision']))
print('隔离治理夹具更新完成',mode)
