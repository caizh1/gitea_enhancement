#!/usr/bin/env python3
"""保留原始证据，按显式更正生成验收台账；不把检查数量当完整实例覆盖率。"""
from pathlib import Path
import json,re,collections
ROOT=Path(__file__).resolve().parents[1]
OUT=ROOT/'docs/evidence/user-permission-20260918'

def exclude(file,n,r):
    check=r.get('检查',''); case=r.get('场景','')
    if r.get('结果') == '排除':return r.get('处理', '原始记录明确标记排除')
    if case in ['汇总','汇总更正','证据更正','评级复核'] or '评级' in check or check in ['反证结论','验收边界说明','历史 LFS 原始证据限制','Git LFS 客户端与服务配置']:
        return '说明、准备或汇总，不是独立业务检查'
    if file=='gui-permissions' and n in [1,6,7,8,9,10,13,14]:return 'GUI初轮前置/菜单选择/空提交或凭据提示影响；见gui-permissions追加更正及严格复验'
    if file=='roles' and n in [17,22]:return '列表预期错误，见corrected复核'
    if file=='roles' and n>=46:return '所谓公开仓库实际私有，全部A10原样本排除，见corrected'
    if file=='corrected' and n==3:return '修正公开样本的准备记录'
    if file=='boundaries' and check=='保护标签删除' and r.get('用户')=='role-30':return '目标标签当时不存在，见lifecycle真实已有标签拒绝'
    if file=='approval-subject' and n in [2,3,7]:return '复合断言或后台计算前置问题，见本文件复核及approval-followup'
    if file=='capability-review' and r.get('运行标识')=='910d64a4':return '首次反证含角色断言错误，整轮不计，采用b21a187e'
    if file=='lifecycle-completion' and 31<=n<=36:return '邀请未成功创建，不代表C07；有效邀请见invitation'
    if file=='api-completion' and n in [7,19,21]:return '原生reader/poster允许创建自己的事项，错误预期见capability-review'
    if file=='archive-merge' and n in [1,2]:return '断言遗漏归档423，见本文件追加复核'
    return ''

POSITIVE={
'全新克隆内容核对','个人仓库列表预期纠正','成员到期前读取','撤权前对照推送','恢复授权后原提交交付',
'足够范围Token对照','旧克隆拉取同事新提交','旧克隆同步后继续交付','解决冲突后提交双方修改',
'推送后创建PR','两人审批及检查通过后合并','拉取合并内容及祖先关系',
'恢复有效审批与合并资格后交付','重新审批后合并完成','后台可合并状态明确后补充合并闭环',
'恢复原Hook后原提交可交付','通过API创建用户私有Fork','克隆本人私有Fork','向本人私有Fork推送真实新提交','由Fork向上游创建PR',
'只读部署密钥读取目标仓库','可写部署密钥普通分支推送','正式成员读写合法对照',
'授权组织详情可见','Planner编辑事项','Planner读取PR列表','共享交集保留PR读取','Developer可以创建普通标签','指定发布者可以更新已有保护标签',
'read_pulls 用户读取 PR API','read_pulls 用户读取 PR 网页','write_issues 管理事项标签','write_issues 管理他人事项',
'只读事项用户可创建普通事项','只读事项用户可编辑自己事项','私有子模块递归克隆','主仓库与子仓库独立推送',
'LFS batch/object 上传下载','LFS 指针真实 Git 推送','主执行者LFS上传下载内容核对','主执行者复核Planner的PR API与网页差异',
'主仓库与子仓库独立授权读写及管理员 refs 见证','真实网页编辑转功能分支并创建PR','VS Code 图形克隆分支提交推送','VS Code 图形拉取同事提交',
'恢复后重新审批可完成合并','指定子组直接成员审批计票并可合并','复核：补第二名独立审批人后总人数门禁满足',
'父组织归档同步后代','父组织恢复同步项目','服务恢复后同一提交正常交付',
}

def kind(file,r):
    exp=r.get('预期');c=r.get('检查','')
    if exp in ['允许','拒绝']:return '正向' if exp=='允许' else '负向'
    if file in ['gui-flow','gui-remote','gui-permissions'] and any(t in c for t in ['成功','完整闭环','正常交付','独立全新克隆','拉取同步','克隆、','远端GUI连接与克隆','新分支、编辑'] ) and not any(t in c for t in ['拒绝','但推送','不能']):return '正向'
    if c in POSITIVE or c.startswith(('独立用户审批-', '服务恢复后同一提交正常交付-')):return '正向'
    if c=='个人仓库列表':return '负向' if r.get('用户')=='role-5' else '正向'
    if c=='事项读取能力':return '负向' if r.get('角色')==5 else '正向'
    if c in ['事项创建能力','共享事项写入按能力交集']:return '正向'
    if c.startswith('保护标签'):return '正向' if r.get('用户')=='role-50' else '负向'
    if c=='文件API写入':return '正向' if r.get('用户')=='role-30' and r.get('分支')!='main' else '负向'
    if file=='protected':return '负向'
    if file in ['merge-timing','assets-followup','concurrent-pr','interruption']:return '复合或一致性'
    if file=='races' and c=='引用准备前撤权先完成':return '负向'
    if file=='native_hook_confirmation' or (file=='race_confirmation' and c=='反证后恢复在途请求'):return '负向'
    if file=='credentials':return '负向'
    if any(t in c for t in ['拒绝','不能','不得','不泄漏','禁止','不授予','无直接编辑','无私有内容','不放行','不能放行','不计入']):return '负向'
    return '复合或一致性'

records=[]; cases=collections.defaultdict(set)
for f in sorted(OUT.glob('*-results.jsonl')):
    file=f.name.removesuffix('-results.jsonl')
    for n,line in enumerate(f.read_text().splitlines(),1):
        r=json.loads(line); reason=exclude(file,n,r); case=r.get('场景','')
        if case=='Guest':case='A03'
        if case=='Planner':case='A04'
        m=re.match(r'^[A-G]\d\d',case); case=m.group(0) if m else case
        result=r.get('结果','未验证')
        if result=='确认部分事件路径缺失':result='失败';case='E01'
        state='排除' if reason else result
        defect=''
        if state=='失败':
            if case=='F01':defect='PERM-RACE-001'
            elif case in ['A04','C04']:defect='CAP-PR-001'
            elif file=='post-receive-event':defect='MERGE-EVENT-001'
        item={'执行记录':f.name+':'+str(n),'场景':case,'检查':r.get('检查',''),'判定':state,'预期类别':kind(file,r),'缺陷':defect,'排除原因':reason,'原始结果':r.get('结果','')}
        records.append(item)
        if not reason and re.match(r'^[A-G]\d\d$',case):cases[case].add(f.name)
valid=[x for x in records if x['判定'] in ['通过','失败']]
pos=[x for x in valid if x['预期类别']=='正向']
summary = {
    '原始记录数': len(records),
    '有效检查记录数': len(valid),
    '通过': sum(x['判定']=='通过' for x in valid),
    '失败': sum(x['判定']=='失败' for x in valid),
    '排除或被替代': sum(x['判定']=='排除' for x in records),
    '原始记录中其余状态数': len(records)-len(valid)-sum(x['判定']=='排除' for x in records),
    '纯正向检查数': len(pos),
    '纯正向通过数': sum(x['判定']=='通过' for x in pos),
    '纯正向成功率': round(100*sum(x['判定']=='通过' for x in pos)/len(pos),2) if pos else None,
    '复合及一致性检查数': sum(x['预期类别']=='复合或一致性' for x in valid),
    '越权放行复现记录数': sum(x['缺陷']=='PERM-RACE-001' for x in valid),
    '合法操作误拒绝复现记录数': sum(x['缺陷']=='CAP-PR-001' for x in valid),
    '去重缺陷数': len({x['缺陷'] for x in valid if x['缺陷']}),
    '涉及矩阵场景数': len(cases),
    '矩阵场景总数': 70,
    '无有效执行证据场景数': 70-len(cases),
    '统计边界': '检查记录不等于全部预先拆分实例；复合检查单列；未在执行前冻结跨客户端/协议/入口全量分母，因此不提供完整实例执行覆盖率。重复复现不等于不同缺陷。',
}
(OUT/'execution-ledger.json').write_text(json.dumps({'统计':summary,'记录':records},ensure_ascii=False,indent=2)+'\n')
(OUT/'summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
notes={
'A01':'API搜索、详情、内容与Git读写已测；未穷尽网页搜索变体。',
'A04':'PR API误拒绝：CAP-PR-001；事项管理及代码拒绝已测。',
'B01':'两级后代Git闭环；网页首页/列表/搜索未逐入口全验。',
'B09':'协作者与治理叠加、Team加入/移出均测；未穷尽Team与治理叠加的全部排列。',
'B12':'已做API分页不同页大小对照；网页搜索、总数各入口待补。',
'C04':'PR API误拒绝；事项管理交集已反证，创建自己事项不是write_issues管理操作。',
'C06':'撤销、成员及共享到期、旧Git/网页会话已测。',
'C07':'邀请由API成功创建；过期使用专属数据库fixture；dummy非真实投递，未覆盖接受全流程。',
'C10':'仓库/父组归档、恢复、撤权不复活及审批齐全合并拒绝已测。',
'D10':'子模块与LFS真实协议已测；未安装git-lfs，客户端clean/smudge闭环待测。',
'E01':'合并主链路完成；发现MERGE-EVENT-001的push事件缺失。',
'E02':'API与网页排队自动合并门禁已测；自动执行器的全部状态组合待补。',
'E03':'独立总人数去重及第二人后合并已测；附带MERGE-EVENT-001。',
'E04':'继承与直接审批资格已测；附带MERGE-EVENT-001。',
'F01':'PERM-RACE-001，四条在途写入失败复现；更细权限撤销入口待修复后扩展。',
'F02':'原生Hook不变，审批人、合并人撤权及审批撤销均被409串行保护；不代表全部时序穷尽。',
'F03':'HTTP/SSH 中断对账及合并恢复已测；合并使用官方人工恢复命令，不代表纯自动恢复或长期故障演练。',
'F04':'同引用双push和双PR并发及恢复均测；一次真实调度不等于穷尽竞态。',
'F05':'macOS及真实Docker Linux Remote-SSH客户端已测；Windows由用户指定跳过，不声明独立Windows兼容通过。',
'G01':'macOS VS Code HTTP+Token、SSH克隆/分支/编辑/暂存/提交/发布已测，独立全新clone见证；Windows跳过。',
'G02':'仅顶层Developer向platform（HTTP）和sdk（SSH）图形开发成功；beta图形clone与SSH新提交push拒绝。',
'G03':'macOS Token GUI Fetch明确只更新跟踪引用，随后Pull同步同事提交；Linux远端Pull另有证据。',
'G04':'GUI非快进拒绝→选择变基→冲突可见→保留双方文本→继续并推送已测。',
'G05':'GUI脏工作区拉取拒绝→储藏→拉取→恢复冲突→保留双方内容并提交已测。',
'G06':'按gui-permissions逐条核对Reporter及低角色真实GUI拒绝；未把夹具clone当GUI clone。',
'G07':'GUI main普通/强推拒绝；客户端删除远程默认分支菜单无此选项，删除子项不适用，D06保留协议证据。',
'G08':'专用低权限Token拒绝，同一提交切换Developer Token后成功；凭据配置是准备，push为GUI。',
'G09':'新增真实待推送提交，按协作者/父组/共享/Token/SSH逐条复验；原始执行器错误记录显式排除。',
'G10':'GUI同步双阶段成功，以及Reporter拉取成功但推送拒绝且待交付提交保留均测。',
'G11':'本机VSCode通过真实Remote-SSH连接Docker Linux，远端Git完成克隆/编辑/提交/推拉/撤权与恢复。',
'G12':'GUI Push到达服务端暂停点后断开专用网络代理，refs对账后恢复网络，GUI Fetch/Push交付原提交。',
}
rows=[]
for line in (ROOT/'docs/user-development-permission-matrix.md').read_text().splitlines():
    m=re.match(r'\| ([A-G]\d\d) \| (.*?) \|',line)
    if not m:continue
    case,title=m.groups(); files=sorted(cases.get(case,[]))
    state='部分执行' if files else '未验证'
    if case in ['A04','C04','F01']:state='发现缺陷'
    if case in ['E01','E03','E04']:state='主要流程通过，附带P2'
    note=notes.get(case,'已完成证据中的检查；完整客户端、网页/API及生命周期变体不能由此自动推定。' if files else '无实际执行证据；不得由其他场景推定通过。Windows/TortoiseGit用户指定跳过。')
    rows.append({'场景':case,'条件':title,'状态':state,'证据文件':files,'边界':note})
assert len(rows)==70
(OUT/'coverage.json').write_text(json.dumps(rows,ensure_ascii=False,indent=2)+'\n')
text='# 70个场景执行覆盖台账\n\n“部分执行”只表示具有有效证据，绝不等于该行所有角色、协议、客户端及入口全通过。正文结论见[实际报告](../../user-development-permission-report-20260918.md)。\n\n| 场景 | 当前状态 | 已测范围与边界 | 证据 |\n|---|---|---|---|\n'
for r in rows:
    links='、'.join('['+x.removesuffix('-results.jsonl')+']('+x+')' for x in r['证据文件']) or '无'
    text+='| '+r['场景']+' | '+r['状态']+' | '+r['边界']+' | '+links+' |\n'
(OUT/'coverage.md').write_text(text)
print(json.dumps(summary,ensure_ascii=False,indent=2))
