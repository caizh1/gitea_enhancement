#!/usr/bin/env python3
"""专用数据库触发器暂停首个事务，确定提交顺序；结束后清除注入。"""
import concurrent.futures,importlib.util,json,subprocess,time
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'));c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
s=json.loads(sorted(c.W.glob('state-*.json'))[-1].read_text());g=s['group'];u=s['user'];repo=s['repo'];target=s['owners'][0]
PSQL=['docker','exec','-i','user-permission-20260918-database-1','psql','-U','gitea','-d','gitea','-v','ON_ERROR_STOP=1','-tA'];KEY=9181631
def sql(q):
 r=subprocess.run(PSQL,input=q,text=True,capture_output=True,timeout=15);assert r.returncode==0,r.stderr;return r.stdout.strip()
def ordered(table,clause,first,second):
 sql(f'CREATE OR REPLACE FUNCTION cc_order_pause() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock({KEY}); IF TG_OP=\'DELETE\' THEN RETURN OLD; ELSE RETURN NEW; END IF; END $$; CREATE TRIGGER cc_order_trigger BEFORE {clause} EXECUTE FUNCTION cc_order_pause();')
 p=subprocess.Popen(PSQL,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,bufsize=1)
 p.stdin.write(f"SELECT pg_advisory_lock({KEY}); SELECT '已锁定';\n");p.stdin.flush()
 while p.stdout.readline().strip()!='已锁定':
  if p.poll() is not None:raise RuntimeError('暂停控制器失败')
 try:
  with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
   f=pool.submit(first);paused=False
   for _ in range(30):
    paused=sql(f"SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid={KEY} AND NOT granted;")=='1'
    if paused or f.done():break
    time.sleep(.05)
   assert paused,'首事务未进入指定暂停点，不能判定产品'
   h=pool.submit(second);time.sleep(.35);blocked=not h.done()
   p.stdin.write(f'SELECT pg_advisory_unlock({KEY});\n\\q\n');p.stdin.flush()
   return f.result(timeout=30),h.result(timeout=30),blocked
 finally:
  if p.poll() is None:
   try:p.communicate('\\q\n',timeout=3)
   except Exception:p.kill()
  sql(f'DROP TRIGGER IF EXISTS cc_order_trigger ON {table}; DROP FUNCTION IF EXISTS cc_order_pause();')
def main():
 path=f'/governance/repositories/{repo["id"]}/members'
 current=a.ok('GET',path)
 if any(m['user_id']==target['id'] for m in current['members']):a.ok('DELETE',path+'/'+str(target['id'])+'?revision='+str(current['revision']))
 assert c.member(g,u,50)[0] in (200,204)
 rev=a.ok('GET',path)['revision'];before=a.ok('GET',path);grev=a.ok('GET',f'/governance/groups/{g["id"]}')['revision']
 ans=ordered('governance_membership',f"DELETE ON governance_membership FOR EACH ROW WHEN (OLD.scope_type='group' AND OLD.scope_id={g['id']} AND OLD.user_id={u['id']})",lambda:c.member(g,u,None,revision=grev),lambda:a.api('PUT',path+'/'+str(target['id']),{'role':20,'revision':rev},u))
 after=a.ok('GET',path)
 c.rec('C03','数据库暂停确定撤权先提交后重验成员管理权限',ans[2] and ans[0][0] in (200,204) and ans[1][0] in (403,404) and before["members"]==after["members"] and before["roles"]==after["roles"],预期='首事务撤权提交后排队旧操作者拒绝；仓库成员与角色不变，根组revision随撤权正常递增',响应=ans,修改前=before,修改后=after,注入='仅专用成员DELETE进入advisory锁暂停，首事务持有治理锁；新管理请求进入后释放；触发器已清除',替代无效运行='221297：原执行器未确定真实提交顺序，不是产品缺陷')
 c.member(g,u,30)
 src=c.group('ordered-src');dst=c.group('ordered-dst');child=a.ok('POST','/governance/groups',{'path':'child','name':'目标归档有序竞争','parent_id':src['id'],'visibility':2});drev=a.ok('GET',f'/governance/groups/{dst["id"]}')['revision']
 ans=ordered('governance_namespace',f"UPDATE ON governance_namespace FOR EACH ROW WHEN (OLD.id={dst['id']} AND NEW.archived=true AND OLD.archived=false)",lambda:a.api('PUT',f'/governance/groups/{dst["id"]}/archive',{'archived':True,'revision':drev}),lambda:a.api('POST',f'/governance/groups/{child["id"]}/move',{'path':'child','parent_id':dst['id'],'revision':child['revision']}))
 after=a.ok('GET',f'/governance/groups/{child["id"]}')
 c.rec('C06','数据库暂停确定目标归档先提交后拒绝移动',ans[2] and ans[0][0] in (200,204) and ans[1][0]==409 and after['parent_id']==src['id'],预期='目标归档先提交，源路径完整保留',响应=ans,最终路径=after['full_path'],替代无效运行='221297：原执行器未确定真实提交顺序，不是产品缺陷')
if __name__=='__main__':main()
