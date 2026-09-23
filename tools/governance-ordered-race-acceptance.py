#!/usr/bin/env python3
"""利用短数据库锁固定请求排队顺序，核对撤权和移动竞争的实际状态。"""
import concurrent.futures,importlib.util,json,subprocess,time
from pathlib import Path
sp=importlib.util.spec_from_file_location('cc',Path(__file__).with_name('governance-concurrency-acceptance.py'));c=importlib.util.module_from_spec(sp);sp.loader.exec_module(c);a=c.a
s=json.loads(sorted(c.W.glob('state-*.json'))[-1].read_text());g=s['group'];u=s['user'];repo=s['repo'];target=s['owners'][0]
PSQL=['docker','exec','-i','user-permission-20260918-database-1','psql','-U','gitea','-d','gitea','-v','ON_ERROR_STOP=1','-tA']
def ordered(first,second):
 p=subprocess.Popen(PSQL,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,bufsize=1)
 p.stdin.write("BEGIN; UPDATE governance_write_lock SET revision=revision+1 WHERE id=1; SELECT '已锁定';\n");p.stdin.flush()
 while p.stdout.readline().strip()!='已锁定':
  if p.poll() is not None:raise RuntimeError('锁定失败')
 try:
  with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
   f=pool.submit(first);time.sleep(.35);h=pool.submit(second);time.sleep(.35)
   blocked=not f.done() and not h.done()
   p.stdin.write('COMMIT;\n\\q\n');p.stdin.flush()
   return f.result(timeout=30),h.result(timeout=30),blocked
 finally:
  if p.poll() is None:
   try:p.communicate('ROLLBACK;\n\\q\n',timeout=5)
   except Exception:p.kill()
def main():
 assert c.member(g,u,50)[0] in (200,204)
 path=f'/governance/repositories/{repo["id"]}/members'
 rev=a.ok('GET',path)['revision'];before=a.ok('GET',path)
 ans=ordered(lambda:c.member(g,u,None),lambda:a.api('PUT',path+'/'+str(target['id']),{'role':20,'revision':rev},u))
 after=a.ok('GET',path)
 c.rec('C03','撤组织管理权先提交、已排队仓库成员请求随后执行',ans[2] and ans[0][0] in (200,204) and ans[1][0] in (403,404) and before==after,预期='原仓库revision未改，旧操作者因当前权限拒绝且成员不变',撤权响应=ans[0],管理响应=ans[1],两请求释放前均在途=ans[2],修改前=before,修改后=after,暂停位置='治理全局写锁之前；覆盖排队后的事务内重新鉴权，不代表任意指令处暂停')
 c.member(g,u,30)
 src=c.group('move-src');dst=c.group('move-dst');child=a.ok('POST','/governance/groups',{'path':'child','name':'竞争子组','parent_id':src['id'],'visibility':2})
 cr=a.ok('POST','/orgs/'+child['full_path']+'/repos',{'name':'payload','private':True,'auto_init':True})
 before_refs=c.refs(cr);rev=a.ok('GET',f'/governance/groups/{child["id"]}')['revision']
 ans=c.parallel([lambda:a.api('POST',f'/governance/groups/{child["id"]}/move',{'path':'child','parent_id':dst['id'],'revision':rev}),lambda:c.member(child,target,20,revision=rev)])
 final=a.ok('GET',f'/governance/groups/{child["id"]}');rr=a.ok('GET','/repos/'+final['full_path']+'/payload')
 c.rec('C06','相同组织版本移组与成员修改竞争',sum(200<=v[0]<300 for v in ans)==1 and sum(v[0]==409 for v in ans)==1 and rr['id']==cr['id'] and c.refs(rr)==before_refs,预期='一成功一版本冲突，仓库唯一身份与代码不变',响应=ans,最终父组=final['parent_id'],最终路径=final['full_path'],仓库ID=rr['id'],引用前=before_refs,引用后=c.refs(rr))
 fresh=a.ok('POST','/governance/groups',{'path':'archived-target-child','name':'目标归档竞争','parent_id':src['id'],'visibility':2})
 drev=a.ok('GET',f'/governance/groups/{dst["id"]}')['revision']
 ans=ordered(lambda:a.api('PUT',f'/governance/groups/{dst["id"]}/archive',{'archived':True,'revision':drev}),lambda:a.api('POST',f'/governance/groups/{fresh["id"]}/move',{'path':'archived-target-child','parent_id':dst['id'],'revision':fresh['revision']}))
 after=a.ok('GET',f'/governance/groups/{fresh["id"]}')
 c.rec('C06','目标归档先提交、在途移组随后核验',ans[2] and ans[0][0] in (200,204) and ans[1][0]==409 and after['parent_id']==src['id'],预期='目标已归档则移动拒绝，源路径不变',响应=ans,最终路径=after['full_path'],最终父组=after['parent_id'])
if __name__=='__main__':main()
