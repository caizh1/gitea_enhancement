#!/usr/bin/env python3
"""补录F03中断恢复后的审计与正常交付见证，不再注入故障或重启。"""
import datetime
import importlib.util
import json
from pathlib import Path
import sys
import time

spec = importlib.util.spec_from_file_location('a', Path(__file__).with_name('user-permission-acceptance.py'))
a = importlib.util.module_from_spec(spec)
spec.loader.exec_module(a)
mode = sys.argv[1] if len(sys.argv) > 1 else 'followup'
sys.argv = [sys.argv[0], 'interruption']
evidence = Path(__file__).resolve().parents[1] / 'docs/evidence/user-permission-20260918/interruption-results.jsonl'

repo = a.ok('GET', '/repos/alpha/dev-interruption-034917')
a.S['仓库']['interruption'] = repo
path = '/repos/' + repo['full_name']
pull = path + '/pulls/1'
actor = a.S['用户']['role-30']
before = a.refs('interruption')
head = before['refs/heads/feature/merge-restart']

status, body = a.api('GET', f'/governance/audit-events?scope_type=repository&scope_id={repo["id"]}&limit=100')
assert status == 200, body
events = body.get('events', body.get('data', [])) if isinstance(body, dict) else body
event_types = sorted({event.get('event_type', event.get('type', '')) for event in events})
a.record('F03', '中断恢复审计类型回读',
         bool(events) and '' not in event_types and 'governance.operation_recovered' in event_types and 'merge.reconciled' in event_types,
         审计事件数=len(events), 审计类型=event_types,
         说明='API响应字段为event_type；保留原始空类型记录并追加更正见证')

if mode == 'exclude':
    with evidence.open('a', encoding='utf-8') as output:
        for stage, error, handling in [
            ('首次合并中断恢复', 'Gitea is not supposed to be run as root', '改为容器内git用户执行支持的恢复命令；遗留授权恢复为failed且保留项清零'),
            ('第二次合并中断只读对账', 'Cannot connect to the Docker daemon at unix:///Users/archer/.docker/run/docker.sock', '保留HTTP/SSH通过证据，增加有界只读查询重试，仅用新仓库重跑merge子阶段'),
        ]:
            output.write(json.dumps({'场景': 'F03', '检查': '测试执行器首因排除记录', '结果': '排除',
                                     '时间': datetime.datetime.now(datetime.timezone.utc).isoformat(), '阶段': stage,
                                     '原始错误': error, '处理': handling, '产品缺陷': False}, ensure_ascii=False) + '\n')
    raise SystemExit(0)

for approver in ('approver-one', 'approver-two'):
    a.ok('POST', pull + '/reviews', {'event': 'APPROVED', 'body': 'F03恢复后重新审批'}, a.S['用户'][approver])
for _ in range(60):
    current = a.ok('GET', pull, user=actor)
    if current.get('mergeable') is True and not current.get('merged'):
        break
    time.sleep(.25)
else:
    raise RuntimeError('恢复后PR未重新达到可合并状态')
merge_status, merge_body = a.api('POST', pull + '/merge', {'Do': 'merge', 'head_commit_id': head}, actor)
final = a.ok('GET', pull, user=actor)
after = a.refs('interruption')
ancestor = a.run(['docker', 'exec', 'user-permission-20260918-gitea-1', 'git', '--git-dir', f'/data/git/repositories/alpha/{repo["name"]}.git', 'merge-base', '--is-ancestor', head, after['refs/heads/main']])
passed = merge_status in (200, 201) and final.get('merged') is True and after['refs/heads/main'] != before['refs/heads/main'] and ancestor.returncode == 0
a.record('F03', '中断恢复后重新审批并正常交付', passed,
         两名独立审批者=['approver-one', 'approver-two'], 恢复后mergeable=True,
         合并HTTP状态=merge_status, 合并响应=merge_body,
         最终PR={'merged': final.get('merged'), 'merge_commit_sha': final.get('merge_commit_sha')},
         操作前主分支=before['refs/heads/main'], 操作后主分支=after['refs/heads/main'],
         原始Head=head, 原始Head为最终主分支祖先=ancestor.returncode == 0)

with evidence.open('a', encoding='utf-8') as output:
    for stage, error, handling in [
        ('首次合并中断恢复', 'Gitea is not supposed to be run as root', '改为容器内git用户执行支持的恢复命令；遗留授权恢复为failed且保留项清零'),
        ('第二次合并中断只读对账', 'Cannot connect to the Docker daemon at unix:///Users/archer/.docker/run/docker.sock', '保留HTTP/SSH通过证据，增加有界只读查询重试，仅用新仓库重跑merge子阶段'),
    ]:
        output.write(json.dumps({'场景': 'F03', '检查': '测试执行器首因排除记录', '结果': '排除',
                                 '时间': datetime.datetime.now(datetime.timezone.utc).isoformat(), '阶段': stage,
                                 '原始错误': error, '处理': handling, '产品缺陷': False}, ensure_ascii=False) + '\n')
