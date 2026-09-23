#!/usr/bin/env python3
"""启动同版本第二个独立应用进程，核对跨进程撤权缓存；不重启主服务。"""
import configparser,datetime,importlib.util,json,subprocess,time
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'))
c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
state=json.loads(sorted(c.W.glob('state-*.json'))[-1].read_text());u=state['user'];g=state['group'];repo=state['repo']
raw=a.run(['docker','exec',a.CONTAINER,'cat','/data/gitea/conf/app.ini']);assert raw.returncode==0
cfg=configparser.ConfigParser(interpolation=None,strict=False);cfg.optionxform=str
cfg.read_string('[DEFAULT]\n'+raw.stdout)
for sect,vals in {'server':{'HTTP_ADDR':'127.0.0.1','HTTP_PORT':'3001','START_SSH_SERVER':'false'},'cron':{'ENABLED':'false'},'queue':{'TYPE':'channel'},'indexer':{'ISSUE_INDEXER_TYPE':'db','REPO_INDEXER_ENABLED':'false'},'log':{'ROOT_PATH':'/tmp/cc-secondary-log'}}.items():
 if not cfg.has_section(sect):cfg.add_section(sect)
 for k,v in vals.items():cfg.set(sect,k,v)
f=c.W/'secondary.ini'
with f.open('w') as stream:cfg.write(stream)
f.chmod(0o600)
a.run(['docker','cp',str(f),a.CONTAINER+':/tmp/cc-secondary.ini']);a.run(['docker','exec',a.CONTAINER,'chown','git:git','/tmp/cc-secondary.ini'])
def secondary(path):
 # 凭据通过标准输入传给仅容器内部可达的HTTP客户端，不输出。
 code='import sys,json,urllib.request,urllib.error\nx=json.load(sys.stdin)\nr=urllib.request.Request("http://127.0.0.1:3001/api/v1"+x["path"],headers={"Authorization":"token "+x["token"]})\ntry:\n a=urllib.request.urlopen(r,timeout=15)\nexcept urllib.error.HTTPError as e:a=e\nprint(a.status)\n'
 # Gitea容器的curl配置同样从标准输入读取，避免依赖Python运行时。
 data='url = "http://127.0.0.1:3001/api/v1'+path+'"\nheader = "Authorization: token '+u['token']+'"\n'
 r=subprocess.run(['docker','exec','-i',a.CONTAINER,'curl','-s','-o','/dev/null','-w','%{http_code}','-K','-'],input=data,text=True,capture_output=True,timeout=20)
 return r.stdout.strip()
def start():
 r=a.run(['docker','exec','-d','-u','git',a.CONTAINER,'sh','-c','echo $$ > /tmp/cc-secondary.pid; exec /usr/local/bin/gitea web --config /tmp/cc-secondary.ini > /tmp/cc-secondary.log 2>&1']);assert r.returncode==0
 for _ in range(60):
  r=a.run(['docker','exec',a.CONTAINER,'curl','-sf','http://127.0.0.1:3001/api/v1/version'])
  if r.returncode==0:return json.loads(r.stdout)
  time.sleep(.25)
 raise RuntimeError('第二应用进程未健康，日志留在容器/tmp/cc-secondary.log')
def stop():
 a.run(['docker','exec',a.CONTAINER,'sh','-c','kill -TERM "$(cat /tmp/cc-secondary.pid)"'])
 time.sleep(.5)
def main():
 try:
  version=start();assert c.member(g,u,30)[0] in (200,204)
  before=secondary('/repos/'+repo['full_name'])
  revoked=c.member(g,u,None);after=secondary('/repos/'+repo['full_name'])
  stop();version2=start();restarted=secondary('/repos/'+repo['full_name'])
  c.rec('C05','共享数据库双应用进程撤权与次节点重启',before=='200' and revoked[0] in (200,204) and after in ('403','404') and restarted in ('403','404'),预期='撤权后旧Token跨节点新请求立即拒绝，重启不复活',对照状态=before,撤权状态=revoked[0],次节点撤权后=after,次节点重启后=restarted,程序版本=version,拓扑='两个独立Gitea进程共享PostgreSQL及仓库存储；主进程3000、次进程仅容器回环3001；非独立物理主机',边界='本条实际验证仓库API读取和次节点重启；浏览器会话与跨节点Git组合另计')
 finally:
  stop();c.member(g,u,30)
  a.run(['docker','exec',a.CONTAINER,'rm','-f','/tmp/cc-secondary.ini','/tmp/cc-secondary.pid'])
  f.unlink(missing_ok=True)

if __name__=="__main__":main()
