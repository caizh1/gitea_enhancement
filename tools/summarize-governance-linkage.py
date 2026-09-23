#!/usr/bin/env python3
# coding: utf-8
"""按明确筛选规则汇总本轮证据，保留部分覆盖与原始执行器失败的边界。"""
import collections,json,re
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];D=ROOT/'docs/evidence/governance-linkage-20260918'
def read(name):return json.loads((D/name).read_text())
def write(name,data): (D/name).write_text(json.dumps(data,ensure_ascii=False,indent=2))
expected=[f'{p}{i:02}' for p,n in [('R',12),('O',10),('M',12),('S',10),('P',12),('X',8),('C',8),('E',8)] for i in range(1,n+1)]
complete={'O01','P01','M04','E03'}
bugs={'R04':['CAP-PR-001'],'S04':['CAP-PR-001'],'X07':['CAP-PR-001'],'E04':['CAP-PR-001'],'C04':['PERM-RACE-001'],'C07':['LIFE-RECREATE-001'],'C08':['PERF-LIST-001'],'P12':['LIFE-RECREATE-001'],'E05':['MERGE-EVENT-001']}
coverage={}
for group in ['rm','or','sx','concurrency']:
 data=read(group+'-coverage.json')
 for r in data.get('覆盖',data.get('场景',[])):
  cid=r['场景'];done=r.get('已测',r.get('已测入口与变体',[]));gap=r.get('缺口',r.get('未测缺口',[]))
  if isinstance(done,list):done='；'.join(done)
  if isinstance(gap,list):gap='；'.join(gap)
  if group=='sx' and cid not in complete:
   ui=r.get('管理网页','')
   if ui and (ui.startswith('未') or '未测' in ui):gap=(gap+'；' if gap else '')+'网页边界：'+ui
  coverage[cid]={'场景':cid,'结论':'发现问题' if cid in bugs else '完整通过' if cid in complete else '部分通过','已测':done,'未验证':gap or '无新增声明缺口；尚未逐入口确证完整，保留分组说明','缺陷':bugs.get(cid,[]),'来源':group+'-coverage.json'}
assert set(coverage)==set(expected)
# 联动关系的补充证据明确引用，不凭其他场景通过推断本行所有变体。
coverage['X01']['已测']+='；根C08独立用户多页1000仓库集合/总数已核对'
coverage['X01']['未验证']='站内搜索及首页/组织页在规模下全部入口一致性未完整执行'
coverage['S02']['未验证']='Reporter共享交集的实际审批请求未单独执行；X05验证的是其他专用审批主体，不能替代本变体'
coverage['M02']['已测']+='；根R07另有同能力Maintainer仓库成员删除对照'
coverage['E03']['未验证']='无；按该只读供应商链路主要声明范围完整，其他共享GUI变体见S组'
for cid in ['O01','P01','M04']:coverage[cid]['未验证']='无'
write('coverage.json',{'口径':'完整通过要求该行预期全部必要断言；部分通过不是整行通过；发现问题行仍可能有未验证入口。网页HTTP GET/直接POST不自动等于浏览器点击。','场景':[coverage[k] for k in expected]})
selected=[];excluded=[];raw_count=0
base=set(f'R{i:02}' for i in range(1,13))|set(f'M{i:02}' for i in range(1,7))
follow={'M07','M12','E02','E04'}
for group in ['rm','or','sx','concurrency']:
 for line,text in enumerate((D/(group+'-results.jsonl')).read_text().splitlines(),1):
  r=json.loads(text);raw_count+=1;cid=r['场景'];reason='';run=r.get('运行标识',r.get('运行',''))
  if r.get('结果') not in ('通过','失败','部分通过'):reason='边界/占位记录，不是完成断言'
  if group=='rm':
   keep=(cid in base and run=='rm-160944-e906') or (cid in follow and run=='rm-161312-2f26') or (cid=='M11' and run=='rm-161526-6caf') or (cid=='M08' and run=='rm-162559-79b8') or (cid in {'M09','M10','E01'} and run=='rm-163320-9868') or (cid=='M04' and run=='rm-164105-82e9')
   if not keep:reason='早期准备/执行器错误或已被独立补测替代；详见rm-coverage说明'
  if group=='sx' and line in (17,19,22,23):reason={17:'原X05未执行占位，后续独立PR已补测',19:'供应商共享被既有跨根限制拒绝的前置错误，21行改正',22:'同PR已有第二自然人且已合并，执行器错误，24行独立双PR反证',23:'E05初始复合结果由25行重分类、26行数据库补证替代'}[line]
  if group=='concurrency':
   if run=='221297' and (cid=='C03' or r['检查']=='目标归档先提交、在途移组随后核验'):reason='请求发送顺序不能保证事务提交顺序，已换确定暂停点'
   if run=='f0f2a2' and cid=='C03':reason='整视图revision比较错误；同次原始响应由f0f2a2-oracle纠正，不是重试通过'
   if r['检查']=='规模准备网络中断后的实际资源对账':reason='夹具对账记录，不计功能断言'
  pointer={'文件':group+'-results.jsonl','行':line,'场景':cid,'检查':r['检查'],'运行':run}
  if reason:excluded.append({**pointer,'排除原因':reason})
  else:selected.append({**pointer,'结果':r['结果'],'原始记录':r})
(D/'effective-records.jsonl').write_text(''.join(json.dumps(r,ensure_ascii=False)+'\n' for r in selected))
write('excluded-records.json',excluded)
counts=collections.Counter(r['结果'] for r in selected);states=collections.Counter(r['结论'] for r in coverage.values())
summary={'基线':'1d07c4e','设计场景数':80,'实际触达场景数':len({r['场景'] for r in selected}),'完整通过场景数':states['完整通过'],'发现问题场景数':states['发现问题'],'部分通过场景数':states['部分通过'],'主结果文件原始记录数':raw_count,'有效证据记录数':len(selected),'有效记录结果':dict(counts),'排除记录数':len(excluded),'去重问题数':5,'P0':0,'P1':1,'P2':4,'VERDICT':'FAIL','统计边界':'有效记录可能聚合多个操作，不能视为独立原子用例或用其比例表示全矩阵通过率。OR首轮备份/重建摘要单独保存，不重复加入主统计。','正向成功率':'未提供全量百分比：部分记录聚合正反断言，不能可靠拆分统一分母；只报告已完成业务链与逐行状态。','越权写入':'当前C04两个协议各一次确定在途放行，归并同一PERM-RACE-001；新请求已拒绝，不声称所有未测路径均安全。','合法误拒绝':'CAP-PR-001四种角色/共享组合；LIFE-RECREATE-001两个独立样本未满足重建/恢复目标。不是按原始失败记录重复计数。'}
write('summary.json',summary)
lines=['# 角色、组织、权限与仓库联动实测报告','',
'本轮由 3 个 Sol 子代理分组执行，主执行者补充并发、故障回滚、规模测试及结果反证。源码基线 `1d07c4e`；仅本机隔离实例，未改业务源码，未部署阿里云或生产。',
'', '**VERDICT: FAIL**。原因是已知的严格在途撤权目标缺口 `PERM-RACE-001`（P1）再次复现；它继承上游在途语义，不是已证增强引入回归。另有 4 项 P2，详见[问题登记](role-organization-repository-test-bugs-20260918.md)。',
'',f'80 条设计场景均已触达，但只有 **{states["完整通过"]} 条完整通过、{states["发现问题"]} 条发现问题、{states["部分通过"]} 条部分通过并保留缺口**。场景触达 100% 不等于全入口覆盖或通过 100%；本轮不能宣布完整验收通过。',
'',f'主结果文件共有 {raw_count} 条记录，筛选后 {len(selected)} 条有效证据（{counts["通过"]} 条通过、{counts["失败"]} 条失败、{counts["部分通过"]} 条部分通过），排除 {len(excluded)} 条准备错误、被替代或边界记录。一条记录可能包含多个操作，该比例不作为独立用例通过率。OR 首轮另有明确的留存缺口，见后文。',
'', '## 已完成的重要业务与边界','',
'- 真实邮件邀请入职 → 后代仓库开发 → PR → 一票拒绝 → 两票但缺最新检查拒绝 → 检查通过后合并。测试邮件仅由本机接收器捕获，没有向外部收件人发送。',
'- 直接、继承、共享、原生 Team/协作者来源的核心边界；升级、降级、到期、禁用和多来源撤权。真实浏览器完成成员新增、升级及降级。',
'- 最后永久 Owner 删除、降级、改到期均拒绝；并发自退保留一名 Owner。撤管理权后的已排队成员修改拒绝；目标先归档后移组拒绝。',
'- 组织跨根移动、新旧权限核验、移动前邀请的真实邮件与浏览器接受；归档撤权恢复、延迟删除恢复及专用子树永久删除。E06 的审批候选重算至最终合并仍未验证。',
'- 审批组直接成员资格、同一账号多组去重、撤组重算；负责人完成受保护分支合并及标签发布。引用/PR/标签正确，但通用 main push 动态缺失再次复现。',
'- 独立双应用进程共享数据库的旧 Token 撤权、次进程重启；审计写入故障导致权限修改整事务回滚。不能替代不同物理主机和全协议组合。',
'', '## 固定规模结果','',
'200 个组织、2,000 个仓库、1,000 名成员，常规深度 10：分页精确匹配应见的 1,000 个仓库，无漏项、重复和越界。深度 20 创建成功，21 拒绝且无残留；十人成员批次变更、自然到期、移出十仓库后 1000→990→恢复1000 均符合预期。',
'', '分页 P95 **9.623 秒**，超过执行前冻结的 **2 秒**目标，登记 `PERF-LIST-001`（P2）。环境是共享 Docker 18 CPU、约 8 GiB；资源只做抽样，不能给出峰值或生产容量结论。200 个仓库有初始化内容，其余为空仓。准备中断后先对账已提交对象；一个被历史兼容别名占用的失败夹具使用不同名称补足规模，原始恢复失败仍登记。',
'', '## 80 条逐行结果与未验证项','',
'“完整通过”仅指该行声明范围。所有“部分通过”均不能用于签署整行通过；HTTP 网页请求与 Playwright 实际点击分别记录。原始[设计矩阵](role-organization-repository-test-matrix.md)保留每条预期，未为迎合实现而修改预期。','',
'| ID | 本轮结论 | 已测内容 | 未验证/限制 | 问题 |','|---|---|---|---|---|']
def cell(s):return str(s).replace('|','／').replace('\n',' ')
for cid in expected:
 r=coverage[cid];lines.append('| '+ ' | '.join(map(cell,[cid,r['结论'],r['已测'],r['未验证'],'、'.join(r['缺陷']) or '无已确认问题']))+' |')
lines += ['', '## 证据、统计与留存说明','',
'- [基线与二进制校验](evidence/governance-linkage-20260918/baseline.json)、[机器可读汇总](evidence/governance-linkage-20260918/summary.json)、[逐行覆盖](evidence/governance-linkage-20260918/coverage.json)。',
'- [有效证据及原行指针](evidence/governance-linkage-20260918/effective-records.jsonl)、[排除记录与理由](evidence/governance-linkage-20260918/excluded-records.json)。原始 [R/M](evidence/governance-linkage-20260918/rm-results.jsonl)、[O/P](evidence/governance-linkage-20260918/or-results.jsonl)、[S/X](evidence/governance-linkage-20260918/sx-results.jsonl)、[并发/规模及角色补证](evidence/governance-linkage-20260918/concurrency-results.jsonl)。',
'- OR 首轮 22 条中，16 条保留原行，6 条完整快照未保留，只有当轮状态日志与明确标注的重建摘要。重建摘要不是原始证据，最终判定依据独立补测。见[首轮日志](evidence/governance-linkage-20260918/or-initial-run.log)与[留存清单](evidence/governance-linkage-20260918/or-initial-results.jsonl)。',
'- 不提供不可靠的全量正向成功率：部分结果聚合正反操作，统一原子分母尚不具备。确定的在途越权放行为 HTTP/SSH 各一次，同一根因；PR 合法误拒绝覆盖四类能力组合，重建目标差异有两个独立样本。',
'- Windows/TortoiseGit 按用户要求跳过。本轮不重复计算此前 macOS GUI、Docker Remote-SSH 的历史结果；本次网页覆盖以表中实际操作为准。',
'- 测试暂停 Hook、数据库触发器及次应用/本机 SMTP 已清理；主服务仍健康，无受跟踪业务代码改动。保留隔离测试数据用于复现；没有声称减少人工 80% 或量化 Token 节省。',
'', '## 风险判定','',
'P0：0；P1：1；P2：4。只有已证实的 P1 严格撤权目标缺口导致 FAIL。Planner API、分页性能、同名恢复契约差异及合并动态为 P2，不单独阻塞；未验证入口、间歇连接中断首因和实际 Webhook/Actions 交付边界不升级为缺陷。',
'', '当前结果支持“多项核心权限和正常业务链可运行”，不足以支持“全部联动无大风险”。后续应先处理严格撤权目标，再补表中的全入口和高风险迁移/并发缺口。']
(ROOT/'docs/role-organization-repository-test-report-20260918.md').write_text('\n'.join(lines)+'\n')
print(json.dumps({k:v for k,v in summary.items() if k in ['实际触达场景数','完整通过场景数','发现问题场景数','部分通过场景数','主结果文件原始记录数','有效证据记录数','有效记录结果','排除记录数','VERDICT']},ensure_ascii=False))
