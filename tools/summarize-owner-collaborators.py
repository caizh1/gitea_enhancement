"""合并本轮证据；只记录实际执行的子场景，不把部分覆盖提升为整项通过。"""
import argparse
import json
import re
from pathlib import Path

parser = argparse.ArgumentParser(description='生成成员权限需求与证据映射')
parser.add_argument('--go-log', type=Path, required=True)
parser.add_argument('--deployment-report', type=Path, action='append', default=[])
parser.add_argument('--frontend-report', type=Path)
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
plan = Path('docs/owner-collaborators-test-plan.md').read_text()
cases = []
for line in plan.splitlines():
    fields = [x.strip() for x in line.split('|')]
    if len(fields) == 6 and re.fullmatch(r'(NAV|VIEW|AUTH|SRC|OWN|PRE|SEC|UX|FLOW)-\d{2}', fields[1]):
        cases.append({'编号': fields[1], '前置条件及操作者': fields[2], '步骤': fields[3], '预期': fields[4], '执行记录': []})
assert len(cases) == 72, '需求目录必须保留全部 72 个编号'
by_id = {x['编号']: x for x in cases}
go_events = []
for line in args.go_log.read_text().splitlines():
    try:
        go_events.append(json.loads(line))
    except ValueError:
        continue
finished = [x for x in go_events if x['Action'] in ('pass', 'fail', 'skip') and 'Test' in x]
leaves = [x for x in finished if not any(y['Package'] == x['Package'] and y['Test'].startswith(x['Test'] + '/') for y in finished)]
# 旧用例只提供明确的覆盖参考，是否执行由本轮 Go JSON 判定。
mapping = {
    'TestAdminCannotProhibitLastRepositoryOwner': ['OWN-03', 'OWN-04'],
    'TestUnifiedRepositoryOwnerSourcesAndPreview': ['VIEW-01', 'VIEW-04', 'PRE-01'],
    'TestUnifiedRepositorySharedOwnerAndNativeAdmin': ['VIEW-05', 'VIEW-06', 'SRC-02'],
    'TestUnifiedMemberPrivacyExpiryAndNativeRemoval': ['VIEW-07', 'SEC-03', 'SRC-09', 'PRE-03'],
    'TestUnifiedOwnerConcurrentDemotion': ['OWN-01', 'OWN-06'],
    'TestUnifiedMembersFiltersAcrossPages': ['VIEW-09', 'VIEW-10'],
    'TestRepositoryMembersPreserveSources': ['SRC-05', 'SRC-09'],
    'TestRepositoryAllMembersSources': ['VIEW-02', 'VIEW-03'],
    'TestRepositoryAllMembersSharingExpansion': ['SRC-04'],
    'TestRepositorySharingIncludesInheritedMembersAndRevokesOnlyItsSource': ['SRC-04', 'SRC-06', 'SRC-08'],
    'TestPermanentOwnerRequiresActiveDirectOrInheritedSource': ['OWN-02', 'OWN-04'],
    'TestNativeGroupInheritanceAndPaths': ['SRC-07'],
    'TestGroupSharingDirectMembersRevocationAndCycles': ['SRC-03', 'SRC-08'],
    'TestNavigationPrivateHierarchyAndRevocation': ['NAV-01', 'NAV-04'],
    'TestMemberAcceptanceInvitationOwner': ['AUTH-07', 'VIEW-07'],
    'TestMemberAcceptanceAuditRollback': ['OWN-08'],
    'TestMemberAcceptanceLastPermanentOwner': ['OWN-01', 'OWN-02', 'OWN-03'],
    'TestMemberAcceptancePreviewIsolation': ['PRE-01', 'PRE-03', 'PRE-04'],
    'TestMemberAcceptanceRoleMatrix': ['AUTH-01', 'AUTH-02', 'AUTH-05', 'AUTH-10'],
    'TestMemberAcceptanceOwnerTargetProtection': ['AUTH-03', 'AUTH-04'],
}
for row in leaves:
    ids = set(re.findall(r'(?:NAV|VIEW|AUTH|SRC|OWN|PRE|SEC|UX|FLOW)-\d{2}', row['Test']))
    ids.update(mapping.get(row['Test'].split('/')[0], []))
    for case_id in ids:
        by_id[case_id]['执行记录'].append({'层级': 'Go 模型/服务', '场景': row['Test'], '状态': {'pass': '通过', 'fail': '失败', 'skip': '未执行'}[row['Action']], '耗时秒': row.get('Elapsed', 0), '证据': str(args.go_log)})
for report in args.deployment_report:
    data = json.loads(report.read_text())
    for row in data['结果']:
        for case_id in row['编号']:
            by_id[case_id]['执行记录'].append({'层级': '实际部署', **row, '证据': str(report), '运行编号': data['运行编号']})
if args.frontend_report:
    frontend = json.loads(args.frontend_report.read_text())
    for suite in frontend['testResults']:
        for row in suite['assertionResults']:
            name = row['title']
            ids = []
            if name.startswith('修改') or '角色变更' in name:
                ids.append('PRE-02')
            if '重复提交' in name or '保留输入' in name:
                ids.append('PRE-06')
            if '取消确认' in name:
                ids.append('PRE-05')
            if '文本显示' in name:
                ids.append('SEC-07')
            for case_id in ids:
                by_id[case_id]['执行记录'].append({'层级': '前端交互单测', '场景': name, '状态': '通过' if row['status'] == 'passed' else '失败', '耗时毫秒': row.get('duration', 0), '证据': str(args.frontend_report)})
for row in cases:
    records = row['执行记录']
    row['覆盖状态'] = '含失败子场景' if any(x['状态'] == '失败' for x in records) else '部分自动化覆盖，完整组合待验收' if records else '本轮未执行，按步骤人工执行或继续补自动化'
args.output.mkdir(parents=True, exist_ok=True)
(args.output / 'case-results.json').write_text(json.dumps(cases, ensure_ascii=False, indent=2) + '\n')
lines = ['# 需求—测试—证据映射', '', '本表只证明列出的子场景。历史失败保留在原始目录；选择的报告必须在分层报告中交代源码及修复先后。未执行及环境阻塞不计通过。', '', '| 编号 | 覆盖状态 | 本轮执行记录数 | 证据入口 |', '|---|---|---:|---|']
for row in cases:
    paths = sorted({x['证据'] for x in row['执行记录']})
    lines.append('| ' + row['编号'] + ' | ' + row['覆盖状态'] + ' | ' + str(len(row['执行记录'])) + ' | ' + '、'.join(f'`{p}`' for p in paths) + ' |')
lines += ['', f'Go 叶子测试共 {len(leaves)} 条；父测试不重复计数。', '', '每条前置条件、步骤、预期及实际子场景见同目录 case-results.json。浏览器交互单测另见前端报告，不把模拟 DOM 计为真实浏览器完成。']
(args.output / 'traceability.md').write_text('\n'.join(lines) + '\n')
print('已生成 72 项需求及子场景证据映射')
