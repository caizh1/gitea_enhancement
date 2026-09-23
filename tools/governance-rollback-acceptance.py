#!/usr/bin/env python3
"""仅专用组触发审计落库失败，验证权限更新原子回滚及恢复。"""
import importlib.util,json,subprocess
from pathlib import Path
sp=importlib.util.spec_from_file_location('multi',Path(__file__).with_name('governance-multinode-acceptance.py'));m=importlib.util.module_from_spec(sp);sp.loader.exec_module(m)
a=m.a;c=m.c;g=m.g;u=m.u
DB='user-permission-20260918-database-1'
def sql(s):
 r=subprocess.run(['docker','exec','-i',DB,'psql','-U','gitea','-d','gitea','-v','ON_ERROR_STOP=1','-tA'],input=s,text=True,capture_output=True,timeout=30);assert r.returncode==0,r.stderr
 return r.stdout.strip()
def request(role,rev):
 import base64
 header='Authorization: Basic '+base64.b64encode((a.ADMIN['name']+':'+a.ADMIN['password']).encode()).decode()
 config='url = "http://127.0.0.1:3001/api/v1/governance/groups/'+str(g['id'])+'/members/'+str(u['id'])+'"\nheader = "'+header+'"\nheader = "Content-Type: application/json"\n'
 payload=json.dumps({'role':role,'revision':rev})
 config+='data = '+json.dumps(payload)+'\nrequest = "PUT"\n'
 r=subprocess.run(['docker','exec','-i',a.CONTAINER,'curl','-s','-o','/tmp/cc-rollback-response','-w','%{http_code}','-K','-'],input=config,text=True,capture_output=True,timeout=30)
 return int(r.stdout)
cleanup='DROP TRIGGER IF EXISTS cc_failure ON governance_audit_event; DROP FUNCTION IF EXISTS cc_audit_failure();'
try:
 m.start();c.member(g,u,30)
 rev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision']
 query=f"SELECT role FROM governance_membership WHERE scope_type='group' AND scope_id={g['id']} AND user_id={u['id']};"
 before=sql(query)
 sql(f"CREATE FUNCTION cc_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.scope_type='group' AND NEW.scope_id={g['id']} AND NEW.type='member.updated' THEN RAISE EXCEPTION '隔离验收注入审计失败'; END IF; RETURN NEW; END $$; CREATE TRIGGER cc_failure BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION cc_audit_failure();")
 failed=request(20,rev);after=sql(query);revafter=a.ok('GET',f'/governance/groups/{g["id"]}')['revision'];sql(cleanup)
 recovered=request(20,rev);final=sql(query)
 c.rec('C07','专用组审计写失败回滚及同版本恢复',failed>=400 and before==after=='30' and revafter==rev and recovered in (200,204) and final=='20',预期='失败不能半改权限/版本，恢复后合法修改成功',失败状态=failed,失败前角色=before,失败后角色=after,版本前=rev,版本后=revafter,恢复状态=recovered,最终角色=final,注入范围='仅本测试组member.updated审计INSERT；在第二应用进程执行，业务源码不修改',边界='仅权限更新数据库故障；创建/迁移/删除的进程退出及重放仍需独立样本')
finally:
 sql(cleanup);m.stop();c.member(g,u,30)
 a.run(['docker','exec',a.CONTAINER,'rm','-f','/tmp/cc-secondary.ini','/tmp/cc-secondary.pid','/tmp/cc-rollback-response']);m.f.unlink(missing_ok=True)
