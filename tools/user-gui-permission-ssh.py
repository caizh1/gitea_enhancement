import importlib.util,sys,subprocess,json
s=importlib.util.spec_from_file_location('c','/tmp/gitea-gui-permissions/control.py');c=importlib.util.module_from_spec(s);s.loader.exec_module(c)
u=c.st['reporter'];key=c.w/'ssh-key';mode=sys.argv[1]
if mode=='ssh-setup':
 subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(key)],check=True);k=c.a.ok('POST','/user/keys',{'title':'图形SSH验收','key':key.with_suffix('.pub').read_text()},u);c.st['ssh_key_id']=k['id'];(c.w/'state.json').write_text(json.dumps(c.st));(c.w/'state.json').chmod(0o600)
 subprocess.run(['git','config','core.sshCommand',f'ssh -i {key} -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={c.w}/known-hosts'],cwd=c.repo,check=True)
 subprocess.run(['git','remote','set-url','origin','ssh://git@127.0.0.1:4538/'+c.st['repo']['full_name']+'.git'],cwd=c.repo,check=True)
elif mode=='ssh-revoke':c.a.ok('DELETE','/user/keys/'+str(c.st['ssh_key_id']),user=u)
elif mode=='ssh-restore':
 k=c.a.ok('POST','/user/keys',{'title':'图形SSH恢复','key':key.with_suffix('.pub').read_text()},u);c.st['ssh_key_id']=k['id'];(c.w/'state.json').write_text(json.dumps(c.st));(c.w/'state.json').chmod(0o600)
print('隔离SSH夹具完成',mode)
