#!/usr/bin/env python3
"""导航边界回归：仅修改本机专用验收实例中的独立样本。"""
import importlib.util
import json
import urllib.parse
from pathlib import Path

spec = importlib.util.spec_from_file_location('navigation_test', Path(__file__).with_name('test-governance-navigation.py'))
t = importlib.util.module_from_spec(spec)
spec.loader.exec_module(t)
r, check = t.request, t.check
fixture_path = t.WORK / 'boundary-fixtures.json'
fixtures = json.loads(fixture_path.read_text()) if fixture_path.exists() else {}

def saved_group(key, slug, parent=0):
    if key not in fixtures:
        fixtures[key] = t.group(slug, '边界验收 ' + slug, parent)['id']
        fixture_path.write_text(json.dumps(fixtures, ensure_ascii=False, indent=2))
    return fixtures[key]

def run():
    base = '/governance/navigation/groups'
    root = saved_group('root', 'navigation-boundaries')
    children = [saved_group('page-' + str(i), 'page-' + str(i).zfill(2), root) for i in range(23)]
    first = r('GET', f'{base}/{root}')
    check('默认每页二十项', len(first['items']) == 20 and first['has_more'])
    pages = list(first['items'])
    page = first
    while page['has_more']:
        page = r('GET', f'{base}/{root}?cursor=' + urllib.parse.quote(page['next_cursor']))
        pages.extend(page['items'])
    ids = [n['id'] for n in pages]
    check('分页无重复遗漏', len(ids) == len(set(ids)) and set(children).issubset(ids))
    a = saved_group('same-a', 'same-name', children[0])
    b = saved_group('same-b', 'same-name', children[1])
    matches = r('GET', f'{base}/{root}?q=same-name')['items']
    check('同名子组保持不同完整路径', {n['id'] for n in matches} == {a, b} and len({n['full_path'] for n in matches}) == 2)
    parent = root
    for depth in range(2, 21):
        parent = saved_group('depth-' + str(depth), 'level-' + str(depth), parent)
    detail = r('GET', f'{base}/{parent}')
    check('二十层面包屑完整', len(detail['breadcrumbs']) == 20)
    r('POST', '/governance/groups', {'path': 'level-21', 'parent_id': parent, 'visibility': 2}, expected=400)
    check('二十一层创建被拒绝', True)
    leaf = children[-1]
    current = r('GET', f'/governance/groups/{leaf}')
    r('PUT', f'/governance/groups/{leaf}/archive', {'archived': True, 'revision': current['revision']})
    inactive = r('GET', f'{base}/{root}?state=inactive')['items']
    check('归档资源进入非活动筛选', any(n['id'] == leaf and n['state'] == 'archived' for n in inactive))
    current = r('GET', f'/governance/groups/{leaf}')
    r('PUT', f'/governance/groups/{leaf}/archive', {'archived': False, 'revision': current['revision']})
    current = r('GET', f'/governance/groups/{leaf}')
    r('POST', f'/governance/groups/{leaf}/deletion', {'revision': current['revision'], 'confirmation_path': current['full_path']})
    inactive = r('GET', f'{base}/{root}?state=inactive')['items']
    check('待删除资源进入非活动筛选', any(n['id'] == leaf and n['state'] == 'pending_deletion' for n in inactive))
    current = r('GET', f'/governance/groups/{leaf}')
    r('POST', f'/governance/groups/{leaf}/restore', {'revision': current['revision'], 'confirmation_path': current['full_path']})
    check('恢复后重新成为活动资源', r('GET', f'{base}/{leaf}')['group']['state'] == 'active')
    current = r('GET', f'/governance/groups/{a}')
    moved = r('POST', f'/governance/groups/{a}/move', {'path': 'renamed', 'parent_id': children[2], 'revision': current['revision']})
    check('转移改名后导航使用新路径', r('GET', f'{base}/{a}')['group']['full_path'] == moved['full_path'])
    current = r('GET', f'/governance/groups/{a}')
    r('POST', f'/governance/groups/{a}/move', {'path': 'same-name', 'parent_id': children[0], 'revision': current['revision']})

    public_key = 'public-root'
    if public_key not in fixtures:
        public = r('POST', '/governance/groups', {'path': 'navigation-public', 'name': '公开导航验收', 'visibility': 0}, expected=201)
        fixtures[public_key] = public['id']
        fixture_path.write_text(json.dumps(fixtures, ensure_ascii=False, indent=2))
        r('POST', '/orgs/navigation-public/repos', {'name': 'public-demo', 'private': False, 'auto_init': True}, expected=201)
    original = t.setup()
    auth = ('nav-child', original['users']['nav-child']['password'])
    mine = r('GET', base, auth=auth)['items']
    check('公开可读不会加入我的群组', fixtures[public_key] not in [n['id'] for n in mine])
    public_detail = r('GET', f"{base}/{fixtures[public_key]}", auth=None)
    check('匿名公开群组仍可浏览仓库', any(n['type'] == 'repository' for n in public_detail['items']))
    invited = saved_group('invited', 'invited', root)
    t.grant(invited, original['users']['nav-child']['id'], 30)
    shared = children[5]
    current = r('GET', f'/governance/groups/{shared}')
    r('PUT', f'/governance/groups/{shared}/shares/{invited}', {'max_role': 20, 'revision': current['revision']}, expected=204)
    check('共享成员获得导航入口', r('GET', f'{base}/{shared}', auth=auth)['group']['id'] == shared)
    own = r('GET', f'/governance/groups/{shared}/members?own=true&source=shared', auth=auth)
    check('共享来源与实际角色显示一致', len(own) == 1 and own[0]['effective_role'] == 'Reporter' and any(s['kind'] == 'shared' for s in own[0]['sources']))
    current = r('GET', f'/governance/groups/{shared}')
    r('DELETE', f'/governance/groups/{shared}/shares/{invited}?revision={current["revision"]}', expected=204)
    r('GET', f'{base}/{shared}', auth=auth, expected=404)
    check('撤销共享后导航即时拒绝', True)

    if 'custom-role' not in fixtures:
        current = r('GET', f'/governance/groups/{root}')
        role = r('POST', f'/governance/groups/{root}/roles', {'name': '导航审计员', 'base_role': 20, 'abilities': ['read_audit'], 'revision': current['revision']})
        fixtures['custom-role'] = role['id']
        fixture_path.write_text(json.dumps(fixtures, ensure_ascii=False, indent=2))
    current = r('GET', f'/governance/groups/{children[6]}')
    r('PUT', f'/governance/groups/{children[6]}/members/{original["users"]["nav-child"]["id"]}', {'role': 20, 'custom_role_id': fixtures['custom-role'], 'revision': current['revision']}, expected=204)
    custom = r('GET', f'{base}/{children[6]}', auth=auth)
    check('自定义角色按实际能力提供审计入口', 'read_audit' in custom['allowed_actions'] and 'manage_group' not in custom['allowed_actions'])
    own = r('GET', f'/governance/groups/{children[6]}/members?own=true', auth=auth)
    check('自定义能力显示组合授权', own[0]['effective_role'] == '组合授权')
    if 'native-team' not in fixtures:
        team = r('POST', '/orgs/rd/storage/teams', {'name': 'navigation-native', 'permission': 'read', 'units': ['repo.code'], 'includes_all_repositories': True}, expected=201)
        fixtures['native-team'] = team['id']
        fixture_path.write_text(json.dumps(fixtures, ensure_ascii=False, indent=2))
    r('PUT', f'/teams/{fixtures["native-team"]}/members/nav-repository', expected=204)
    repo_auth = ('nav-repository', original['users']['nav-repository']['password'])
    native = r('GET', base, auth=repo_auth)
    check('原生团队成员拥有所在群组入口', any(n['id'] == original['storage'] for n in native['items']))
    members = r('GET', f'/governance/groups/{original["storage"]}/members?source=native_team')
    check('原生团队保留分单元来源', any(m['username'] == 'nav-repository' and any(source.get('units') for source in m['sources']) for m in members))
    r('DELETE', f'/teams/{fixtures["native-team"]}/members/nav-repository', expected=204)

if __name__ == '__main__':
    try:
        run()
    except Exception as error:
        t.RESULTS.append({'结果': '失败', '原因': str(error)})
        raise
    finally:
        (t.WORK / 'boundary-results.json').write_text(json.dumps(t.RESULTS, ensure_ascii=False, indent=2))
        print(json.dumps(t.RESULTS, ensure_ascii=False, indent=2))
