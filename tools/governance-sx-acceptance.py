#!/usr/bin/env python3
"""在 3538 隔离实例验收 S01-S10、X01-X08；所有数据使用 sx- 前缀。"""
import base64
import datetime as dt
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
TMP = Path('/tmp/gitea-user-acceptance-20260918')
STATE = TMP / 'sx-state.json'
OUT = ROOT / 'docs/evidence/governance-linkage-20260918/sx-results.jsonl'
LOG = TMP / 'sx-run.log'
TMP.mkdir(parents=True, exist_ok=True)
OUT.parent.mkdir(parents=True, exist_ok=True)

spec = importlib.util.spec_from_file_location('acceptance_helper', ROOT / 'tools/user-permission-acceptance.py')
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)
api, ok, env_for = h.api, h.ok, h.env_for
ADMIN = h.ADMIN
S = json.loads(STATE.read_text()) if STATE.exists() else {'用户': {}, '组织': {}, '仓库': {}, '其他': {}}


def save():
    STATE.write_text(json.dumps(S, ensure_ascii=False, indent=2))
    STATE.chmod(0o600)


def record(case, check, passed, expected, before=None, after=None, defect='', **evidence):
    row = {'场景': case, '检查': check, '结果': '通过' if passed else '失败', '预期': expected,
           '前后证据': {'前': before, '后': after}, '时间': dt.datetime.now(dt.timezone.utc).isoformat(),
           '缺陷': defect if not passed else '', **evidence}
    with OUT.open('a') as f:
        f.write(json.dumps(row, ensure_ascii=False) + '\n')
    print(case, check, row['结果'], flush=True)
    return passed


def user(name):
    name = 'sx-link-f1-' + name
    if name not in S['用户']:
        pwd = secrets.token_urlsafe(24)
        created = ok('POST', '/admin/users', {'username': name, 'password': pwd, 'email': name + '@example.invalid', 'must_change_password': False})
        S['用户'][name] = {'name': name, 'password': pwd, 'id': created['id']}
        save()
    u = S['用户'][name]
    if 'token' not in u:
        token = ok('POST', '/users/' + name + '/tokens', {'name': 'sx隔离验收', 'scopes': ['all']}, u)
        u['token'] = token['sha1']
        save()
    key = TMP / (name + '-key')
    if not key.exists():
        subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(key)], check=True)
        key_data = Path(str(key) + '.pub').read_text()
        u['key_id'] = ok('POST', '/user/keys', {'title': 'sx隔离验收', 'key': key_data}, u)['id']
        save()
    return u


def group(key, parent=None):
    if key not in S['组织']:
        data = {'path': 'sx-' + key + '-f1', 'name': 'sx隔离组织', 'visibility': 2}
        if parent:
            data['parent_id'] = S['组织'][parent]['id']
        S['组织'][key] = ok('POST', '/governance/groups', data)
        save()
    return S['组织'][key]


def repo(key, owner):
    if key not in S['仓库']:
        owner_path = S['组织'][owner]['full_path']
        S['仓库'][key] = ok('POST', '/orgs/' + owner_path + '/repos', {'name': 'sx-' + key + '-f1', 'private': True, 'auto_init': True, 'default_branch': 'main'})
        save()
    return S['仓库'][key]


def member(gkey, actor, role=None, custom_role_id=0, expires_unix=0):
    gid = S['组织'][gkey]['id']
    state = ok('GET', f'/governance/groups/{gid}')
    path = f'/governance/groups/{gid}/members/{actor["id"]}'
    if role is None:
        return api('DELETE', path + '?revision=' + str(state['revision']))
    return api('PUT', path, {'role': role, 'custom_role_id': custom_role_id, 'expires_unix': expires_unix, 'revision': state['revision']})


def direct(rkey, actor, role=None):
    rid = S['仓库'][rkey]['id']
    state = ok('GET', f'/governance/repositories/{rid}/members')
    path = f'/governance/repositories/{rid}/members/{actor["id"]}'
    return api('DELETE', path + '?revision=' + str(state['revision'])) if role is None else api('PUT', path, {'role': role, 'revision': state['revision']})


def share_repo(rkey, gkey, role=None, expires_unix=0):
    rid, gid = S['仓库'][rkey]['id'], S['组织'][gkey]['id']
    state = ok('GET', f'/governance/repositories/{rid}/shares')
    path = f'/governance/repositories/{rid}/shares/{gid}'
    return api('DELETE', path + '?revision=' + str(state['revision'])) if role is None else api('PUT', path, {'max_role': role, 'expires_unix': expires_unix, 'revision': state['revision']})


def share_group(source, invited, role=None, expires_unix=0):
    sid, iid = S['组织'][source]['id'], S['组织'][invited]['id']
    state = ok('GET', f'/governance/groups/{sid}')
    path = f'/governance/groups/{sid}/shares/{iid}'
    return api('DELETE', path + '?revision=' + str(state['revision'])) if role is None else api('PUT', path, {'max_role': role, 'expires_unix': expires_unix, 'revision': state['revision']})


def git(rkey, actor, write=False, suffix='x'):
    r = S['仓库'][rkey]
    result = {}
    for protocol, remote in [('HTTP', r['clone_url']), ('SSH', r['ssh_url'])]:
        folder = TMP / 'sx-clones' / f'{actor["name"]}-{rkey}-{suffix}-{protocol.lower()}'
        if folder.exists():
            subprocess.run(['rm', '-rf', str(folder)], check=True)
        folder.parent.mkdir(exist_ok=True)
        clone = subprocess.run(['git', 'clone', remote, str(folder)], env=env_for(actor), capture_output=True, text=True)
        result[protocol + '读取'] = clone.returncode == 0
        if clone.returncode != 0:
            continue
        (folder / 'sx.txt').write_text('sx真实协议验收\n')
        for cmd in [['git', 'config', 'user.name', 'sx验收'], ['git', 'config', 'user.email', 'sx@example.invalid'], ['git', 'add', 'sx.txt'], ['git', 'commit', '-m', 'test: sx协议验收']]:
            subprocess.run(cmd, cwd=folder, check=True, capture_output=True)
        branch = f'sx/{suffix}-{protocol.lower()}-{time.time_ns()}'
        push = subprocess.run(['git', 'push', remote, 'HEAD:refs/heads/' + branch], cwd=folder, env=env_for(actor), capture_output=True, text=True)
        result[protocol + '写入'] = push.returncode == 0
    return result


class Web:
    def __init__(self, actor):
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, req, fp, code, msg, headers, newurl):
                return None
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(), NoRedirect())
        page = self.get('/user/login')
        csrf = re.search(r'name="_csrf" value="([^"]+)"', page[1])
        data = urllib.parse.urlencode({'_csrf': csrf.group(1) if csrf else '', 'user_name': actor['name'], 'password': actor['password']}).encode()
        self._open('/user/login', data)

    def _open(self, path, data=None):
        req = urllib.request.Request(h.BASE + path, data=data)
        try:
            r = self.opener.open(req, timeout=30)
        except urllib.error.HTTPError as e:
            r = e
        return r.status, r.read().decode(errors='replace'), r.geturl()

    def get(self, path):
        return self._open(path)

    def post(self, path, fields):
        status, html, _ = self.get(path.split('?')[0])
        csrf = re.search(r'name="_csrf" value="([^"]+)"', html)
        fields = {**fields, '_csrf': csrf.group(1) if csrf else ''}
        return self._open(path, urllib.parse.urlencode(fields).encode())


def readable(rkey, actor):
    return api('GET', '/repos/' + S['仓库'][rkey]['full_name'], user=actor)[0] == 200


def refs(rkey):
    r = S['仓库'][rkey]
    out = subprocess.run(['git', 'ls-remote', r['clone_url']], env=env_for(ADMIN), capture_output=True, text=True, timeout=60)
    if out.returncode:
        raise RuntimeError('管理员读取引用失败：' + out.stderr[-500:])
    return dict(line.split()[::-1] for line in out.stdout.splitlines())


def create_pr(rkey, actor, label):
    r = S['仓库'][rkey]; folder = TMP / 'sx-clones' / ('pr-' + label)
    if folder.exists(): subprocess.run(['rm', '-rf', str(folder)], check=True)
    subprocess.run(['git','clone',r['clone_url'],str(folder)],env=env_for(actor),check=True,capture_output=True)
    (folder/'approval.txt').write_text('审批与发布真实闭环：'+label+'\n')
    for cmd in [['git','config','user.name','sx审批'],['git','config','user.email','sx@example.invalid'],['git','add','.'],['git','commit','-m','test: sx审批闭环']]: subprocess.run(cmd,cwd=folder,check=True,capture_output=True)
    sha=subprocess.run(['git','rev-parse','HEAD'],cwd=folder,capture_output=True,text=True,check=True).stdout.strip(); branch='sx/'+label
    subprocess.run(['git','push',r['clone_url'],'HEAD:refs/heads/'+branch],cwd=folder,env=env_for(actor),check=True,capture_output=True)
    pr=ok('POST','/repos/'+r['full_name']+'/pulls',{'title':'sx '+label,'head':branch,'base':'main'},actor)
    return folder, pr, sha


def approval_rule(rkey, name, required, actor=None, **subjects):
    rid=S['仓库'][rkey]['id']; body={'rule':{'name':name,'required':required,'branch_mode':'all','enabled':True,**subjects},'revision':0}
    return ok('POST',f'/governance/repositories/{rid}/approval-rules',body,actor)


def approve(rkey, pr, actor, sha):
    return api('POST',f'/repos/{S["仓库"][rkey]["full_name"]}/pulls/{pr["number"]}/reviews',{'event':'APPROVED','body':'sx独立审批','commit_id':sha},actor)


def approval_state(pr, actor):
    return ok('GET',f'/governance/pulls/{pr["id"]}/approval-state',user=actor)['state']


def setup():
    for key, parent in [('alpha', None), ('alpha-child', 'alpha'), ('alpha-peer', 'alpha'), ('beta', None), ('beta-child', 'beta'), ('gamma', None), ('delta', None)]:
        group(key, parent)
    for key, owner in [('main', 'alpha-child'), ('neighbor', 'alpha-peer'), ('beta-main', 'beta-child'), ('xray', 'alpha-child'), ('protect', 'alpha-child')]:
        repo(key, owner)


def s01_s03():
    cases = [('S01', 's01', 30, 20, True, False), ('S02', 's02', 20, 30, True, False), ('S03', 's03', 30, 30, True, True)]
    for case, uname, role, ceiling, can_read, can_write in cases:
        u = user(uname); member('beta', u, role); share_repo('main', 'beta', ceiling)
        got = git('main', u, can_write, case.lower())
        member_status = api('GET', f'/governance/repositories/{S["仓库"]["main"]["id"]}/members', user=u)[0]
        neighbor = readable('neighbor', u)
        passed = all(got[p + '读取'] == can_read and got.get(p + '写入', False) == can_write for p in ['HTTP', 'SSH']) and member_status >= 400 and not neighbor
        record(case, '共享上限与双Git协议', passed, f'读={can_read} 写={can_write}，不能管理成员或访问邻库', after={'Git': got, '成员接口': member_status, '邻库': neighbor}, defect='SX-SHARE-BASE' if not passed else '')


def prepare_pr():
    r = S['仓库']['main']; path = '/repos/' + r['full_name']
    if 'pr' in S['其他']:
        return S['其他']['pr']
    folder = TMP / 'sx-pr-seed'
    if folder.exists(): subprocess.run(['rm', '-rf', str(folder)], check=True)
    subprocess.run(['git', 'clone', r['clone_url'], str(folder)], env=env_for(ADMIN), check=True, capture_output=True)
    (folder / 'pr.txt').write_text('sx PR私有代码\n')
    for cmd in [['git','config','user.name','sx'],['git','config','user.email','sx@example.invalid'],['git','add','pr.txt'],['git','commit','-m','test: sx PR']]: subprocess.run(cmd,cwd=folder,check=True,capture_output=True)
    subprocess.run(['git','push',r['clone_url'],'HEAD:refs/heads/sx/pr-meta'],cwd=folder,env=env_for(ADMIN),check=True,capture_output=True)
    issue = ok('POST', path + '/issues', {'title':'sx事项','body':'元数据'}, ADMIN)
    pr = ok('POST', path + '/pulls', {'title':'sx PR元数据','head':'sx/pr-meta','base':'main','body':'元数据'}, ADMIN)
    S['其他']['pr']={'number':pr['number'],'issue':issue['number'],'sha':pr['head']['sha']}; save(); return S['其他']['pr']


def s04_x07():
    pr = prepare_pr(); repo_path = S['仓库']['main']['full_name']; combos=[('pr',15,20,False),('rp',20,15,False),('pd',15,30,True)]
    all_ok=True; evidence={}
    for label, role, ceiling, issue_write in combos:
        g='s04-'+label; group(g); u=user('s04-'+label); member(g,u,role); share_repo('main',g,ceiling)
        pulls=api('GET',f'/repos/{repo_path}/pulls/{pr["number"]}',user=u)[0]
        issues=api('GET',f'/repos/{repo_path}/issues/{pr["issue"]}',user=u)[0]
        code=api('GET',f'/repos/{repo_path}/git/blobs/{pr["sha"]}',user=u)[0]
        patch=api('GET',f'/repos/{repo_path}/pulls/{pr["number"]}.patch',user=u)[0]
        edit=api('PATCH',f'/repos/{repo_path}/issues/{pr["issue"]}',{'title':'sx他人事项-'+label},u)[0]
        web=Web(u); wp=web.get('/'+repo_path+f'/pulls/{pr["number"]}')[0]; wc=web.get('/'+repo_path+'/src/branch/sx/pr-meta')[0]
        one = pulls==200 and issues==200 and code>=400 and patch>=400 and (200<=edit<300)==issue_write and wp==200 and wc>=400
        all_ok &= one; evidence[label]={'PR_API':pulls,'事项_API':issues,'代码_API':code,'补丁_API':patch,'事项修改':edit,'PR网页':wp,'代码网页':wc}
    record('S04','三组非线性交集及网页/API',all_ok,'PR/事项元数据可读，代码/补丁拒绝；仅Planner∩Developer可写事项',after=evidence,defect='CAP-PR-001' if any(v['PR_API']!=200 for v in evidence.values()) else ('SX-S04' if not all_ok else ''))
    record('X07','PR元数据与代码资源隔离',all_ok,'元数据网页/API可读不泄露代码、补丁或原始文件',after=evidence,defect='CAP-PR-001' if not all_ok else '')


def s05_x06():
    g='custom'; group(g); u=user('custom'); gid=S['组织'][g]['id']; state=ok('GET',f'/governance/groups/{gid}')
    role=ok('POST',f'/governance/groups/{gid}/roles',{'name':'sx成员管理开发者','base_role':30,'abilities':['manage_members'],'revision':state['revision']})
    member(g,u,30,role['id']); share_repo('xray',g,30)
    got=git('xray',u,True,'s05'); target=user('custom-target'); before=ok('GET',f'/governance/repositories/{S["仓库"]["xray"]["id"]}/members')
    status,_=api('PUT',f'/governance/repositories/{S["仓库"]["xray"]["id"]}/members/{target["id"]}',{'role':20,'revision':before['revision']},u)
    after=ok('GET',f'/governance/repositories/{S["仓库"]["xray"]["id"]}/members')
    passed=all(got.get(p+'写入') for p in ['HTTP','SSH']) and status>=400 and before==after
    record('S05','自定义额外能力被共享上限截断',passed,'普通开发成功，成员管理拒绝且状态不变',before=before,after={'Git':got,'HTTP':status,'成员':after},defect='SX-S05' if not passed else '')
    record('X06','单项管理能力不变成完全管理员',passed,'共享只开放交集内能力',before=before,after={'HTTP':status,'成员':after},defect='SX-X06' if not passed else '')


def s06():
    for g,p in [('s06-source',None),('s06-child','s06-source'),('s06-target',None),('s06-target-child','s06-target'),('s06-out',None)]: group(g,p)
    repo('s06-main','s06-child'); repo('s06-neighbor','s06-out'); repo('s06-repo-share','s06-child')
    direct_u=user('s06-direct'); inherited=user('s06-inherited'); member('s06-target-child',direct_u,20); member('s06-target',inherited,20)
    share_group('s06-source','s06-target-child',20)
    direct_ok=readable('s06-main',direct_u); inherited_ok=readable('s06-main',inherited); descendant_web=Web(direct_u).get('/'+S['仓库']['s06-main']['full_name'])[0]
    share_repo('s06-repo-share','s06-target-child',20); repo_inherited=readable('s06-repo-share',inherited); neighbor=readable('s06-neighbor',direct_u)
    passed=direct_ok and descendant_web==200 and not inherited_ok and repo_inherited and not neighbor
    record('S06','组织共享直系成员与仓库共享有效授权差异',passed,'组织共享仅邀请组直接成员并覆盖后代；仓库共享采用有效GroupGrants；邻库不扩权',after={'直接成员目标库':direct_ok,'继承成员组织共享':inherited_ok,'继承成员仓库共享':repo_inherited,'目标网页':descendant_web,'邻库':neighbor},defect='SX-S06' if not passed else '')


def s07_s08():
    g='s07'; group(g); u=user('s07'); member(g,u,20); share_repo('xray',g,20); direct('xray',u,30)
    both=git('xray',u,True,'s07-both'); direct('xray',u); shared=git('xray',u,False,'s07-shared'); share_repo('xray',g); none=readable('xray',u)
    direct('xray',u,30); only_direct=git('xray',u,True,'s07-direct'); direct('xray',u)
    passed=both['HTTP写入'] and shared['HTTP读取'] and not shared['HTTP写入'] and not none and only_direct['HTTP写入']
    record('S07','独立来源撤销顺序',passed,'共享不降级直接授权；逐来源撤销后能力准确，全部撤销后拒绝',after={'双来源':both,'仅共享':shared,'无来源':none,'仅直接':only_direct},defect='SX-S07' if not passed else '')
    g='s08'; group(g); u=user('s08'); expiry=int(time.time())+3; member(g,u,30,expires_unix=expiry+10); share_repo('xray',g,30,expires_unix=expiry)
    before=readable('xray',u); time.sleep(max(0,expiry+1-time.time())); after=readable('xray',u)
    share_repo('xray',g,30,expires_unix=int(time.time())+20); restored=readable('xray',u); member('s08',u,None); member_expired=readable('xray',u)
    passed=before and not after and restored and not member_expired
    record('S08','成员/共享较早到期与手动撤销',passed,'任一必要条件失效移除共享来源',before={'到期前':before},after={'共享先到期':after,'重建':restored,'成员撤销':member_expired},defect='SX-S08' if not passed else '')


def s09_s10(skip_s09=False):
    if not skip_s09:
        admin_web=Web(ADMIN); alpha=ok('GET',f'/governance/groups/{S["组织"]["alpha"]["id"]}')
        status,_,_=admin_web.post(f'/governance/groups/{alpha["id"]}/share-restriction',{'revision':str(alpha['revision']),'enabled':'true'})
        cross=share_group('alpha-child','gamma',20)[0]; same=share_group('alpha-child','alpha-peer',20)[0]
        passed=status in [200,302] and cross>=400 and 200<=same<300
        record('S09','外部共享限制网页设置与API绕过',passed,'网页合法启用；跨根API拒绝；同根API成功',after={'网页设置':status,'跨根':cross,'同根':same},defect='SX-S09' if not passed else '')
    # 独立链避免 S09 已有关系干扰。
    for g in ['chain-a','chain-b','chain-c']: group(g)
    cu=user('chain-c'); member('chain-c',cu,20); share_group('chain-a','chain-b',20); share_group('chain-b','chain-c',20)
    repo('chain-repo','chain-a'); two_hop=readable('chain-repo',cu)
    share_repo('chain-repo','chain-b',20); repo_via=readable('chain-repo',cu)
    self_status=share_group('chain-a','chain-a',20)[0]; cycle=share_group('chain-c','chain-a',20)[0]
    state1=ok('GET',f'/governance/groups/{S["组织"]["chain-a"]["id"]}/shares'); duplicate=share_group('chain-a','chain-b',20)[0]; state2=ok('GET',f'/governance/groups/{S["组织"]["chain-a"]["id"]}/shares')
    count1=sum(x['group_id']==S['组织']['chain-b']['id'] for x in state1); count2=sum(x['group_id']==S['组织']['chain-b']['id'] for x in state2)
    passed=not two_hop and repo_via and self_status>=400 and cycle>=400 and duplicate<300 and count1==count2==1
    record('S10','不无限转授、仓库有效授权与环/重复',passed,'两跳组织拒绝；仓库共享可取有效授权；自共享/环拒绝；重复不增行',before={'重复前':count1},after={'两跳':two_hop,'仓库共享':repo_via,'自共享':self_status,'环':cycle,'重复':duplicate,'重复后':count2},defect='SX-S10' if not passed else '')


def x01_x03():
    for g,p in [('x01-source',None),('x01-child','x01-source'),('x01-hidden-group',None),('x01-target',None)]: group(g,p)
    repo('x01-shared','x01-child'); repo('x01-direct','x01-child'); repo('x01-hidden','x01-hidden-group')
    u=user('x01'); member('x01-target',u,20); share_repo('x01-shared','x01-target',20); direct('x01-direct',u,20)
    expected={S['仓库']['x01-shared']['id'],S['仓库']['x01-direct']['id']}
    status,repos=api('GET','/user/repos?limit=100',user=u); actual={r['id'] for r in repos if r['name'].startswith('sx-')}
    details={k:readable(k,u) for k in ['x01-shared','x01-direct','x01-hidden']}; web=Web(u); web_status=web.get('/')[0]
    passed=status==200 and expected.issubset(actual) and S['仓库']['x01-hidden']['id'] not in actual and details=={'x01-shared':True,'x01-direct':True,'x01-hidden':False} and web_status==200
    record('X01','列表/详情/API/首页资源集合',passed,'授权集合一致，无权邻库不出现在列表或详情',after={'列表ID':sorted(actual),'详情':details,'首页':web_status},defect='SX-X01' if not passed else '')
    before=ok('GET',f'/governance/repositories/{S["仓库"]["x01-direct"]["id"]}/members'); target=user('x02-target')
    cross_status,_=api('PUT',f'/governance/repositories/{S["仓库"]["x01-hidden"]["id"]}/members/{target["id"]}',{'role':20,'revision':before['revision']},u)
    after=ok('GET',f'/governance/repositories/{S["仓库"]["x01-hidden"]["id"]}/members'); passed=cross_status>=400 and all(x.get('user_id')!=target['id'] for x in after['members'])
    record('X02','跨租户对象ID混用',passed,'无权对象重新鉴权，成员状态不变',before=before,after={'HTTP':cross_status,'目标成员':after},defect='SX-X02' if not passed else '')
    basic={'name':u['name'],'password':u['password']}; limited=ok('POST','/users/'+u['name']+'/tokens',{'name':'sx只读Token-'+str(time.time_ns()),'scopes':['read:repository']},basic); lu={**u,'token':limited['sha1']}
    code_ok=api('GET','/repos/'+S['仓库']['x01-direct']['full_name'],user=lu)[0]; write=api('POST','/repos/'+S['仓库']['x01-direct']['full_name']+'/issues',{'title':'sx范围拒绝'},lu)[0]
    outsider=user('x03-out'); out=api('GET','/repos/'+S['仓库']['x01-direct']['full_name'],user=outsider)[0]; control=api('GET','/repos/'+S['仓库']['x01-direct']['full_name'],user=u)[0]
    passed=code_ok==200 and write>=400 and out>=400 and control==200
    record('X03','Token范围与资源权限交集',passed,'任一侧不足拒绝，双侧满足成功',after={'只读Token读取':code_ok,'只读Token写入':write,'全范围无资源权':out,'对照':control},defect='SX-X03' if not passed else '')


def x04_x05():
    r=S['仓库']['protect']; path='/repos/'+r['full_name']; u=user('x04-dev'); member('alpha-child',u,30)
    status,_=api('POST',path+'/branch_protections',{'rule_name':'main','enable_push':False,'required_approvals':1,'enable_status_check':True,'status_check_contexts':['sx/check']},ADMIN)
    folder=TMP/'sx-x04';
    if folder.exists(): subprocess.run(['rm','-rf',str(folder)],check=True)
    subprocess.run(['git','clone',r['clone_url'],str(folder)],env=env_for(u),check=True,capture_output=True); (folder/'x04.txt').write_text('x04\n')
    for cmd in [['git','config','user.name','sx'],['git','config','user.email','sx@example.invalid'],['git','add','.'],['git','commit','-m','test: x04']]: subprocess.run(cmd,cwd=folder,check=True,capture_output=True)
    main=subprocess.run(['git','push',r['clone_url'],'HEAD:main'],cwd=folder,env=env_for(u),capture_output=True).returncode; feature=subprocess.run(['git','push',r['clone_url'],'HEAD:refs/heads/sx/x04'],cwd=folder,env=env_for(u),capture_output=True).returncode
    file_status=api('POST',path+'/contents/x04-api.txt',{'content':base64.b64encode(b'x04').decode(),'message':'test: x04','branch':'main'},u)[0]
    passed=status in [201,422] and main!=0 and feature==0 and file_status>=400
    record('X04','保护分支跨Git与文件API',passed,'main各写入口拒绝，功能分支开发成功',after={'保护配置':status,'Git_main':main,'Git_feature':feature,'文件API':file_status},defect='SX-X04' if not passed else '')
    record('X05','审批指定组动态资格',False,'需要指定子组直接成员、成员变更及同人多组的完整合并闭环',after={'状态':'当前脚本未建立指定组审批规则闭环'},defect='UNVERIFIED-X05')


def x05_complete():
    group('x05-root'); group('x05-a','x05-root'); group('x05-b','x05-root'); repo('x05-repo','x05-root')
    actors={n:user('x05-'+n) for n in ['author','dual','second','inherited','direct']}
    for u in actors.values(): member('x05-root',u,30)
    member('x05-a',actors['dual'],30); member('x05-b',actors['dual'],30); member('x05-a',actors['second'],30); member('x05-b',actors['direct'],30)
    r=S['仓库']['x05-repo']; path='/repos/'+r['full_name']
    ok('PUT',f'/governance/repositories/{r["id"]}/approval-settings',{'prevent_author':True,'prevent_overrides':True,'reset_on_change':True,'revision':0})
    ok('POST',path+'/branch_protections',{'rule_name':'main','enable_push':False,'required_approvals':0,'require_governance_approval':True})
    approval_rule('x05-repo','组甲',1,group_ids=[S['组织']['x05-a']['id']]); approval_rule('x05-repo','组乙',1,group_ids=[S['组织']['x05-b']['id']]); approval_rule('x05-repo','独立人数',2,all_eligible=True)
    _,pr,sha=create_pr('x05-repo',actors['author'],'x05-subject-f1'); approve('x05-repo',pr,actors['inherited'],sha); inherited_state=approval_state(pr,actors['inherited'])
    approve('x05-repo',pr,actors['dual'],sha); dual_state=approval_state(pr,actors['dual']); before=refs('x05-repo'); denied=api('POST',path+f'/pulls/{pr["number"]}/merge',{'Do':'merge','head_commit_id':sha},actors['dual'])[0]
    member('x05-b',actors['dual'],None); removed_state=approval_state(pr,actors['dual'])
    approve('x05-repo',pr,actors['direct'],sha); approve('x05-repo',pr,actors['second'],sha); final_state=approval_state(pr,actors['direct'])
    merge=api('POST',path+f'/pulls/{pr["number"]}/merge',{'Do':'merge','head_commit_id':sha},actors['direct'])[0]; final=ok('GET',path+f'/pulls/{pr["number"]}',user=actors['direct']); after=refs('x05-repo')
    def rule(state,name): return next(x for x in state['rules'] if x['name']==name)
    # 合并前拒绝由最终引用变化后不能重读前态，使用已保存的拒绝状态和撤组即时重算共同反证。
    passed=(not inherited_state['satisfied'] and rule(dual_state,'独立人数')['missing']==1 and denied>=400 and
            not rule(removed_state,'组乙')['satisfied'] and actors['dual']['id'] not in rule(removed_state,'组乙')['approved_user_ids'] and
            final_state['satisfied'] and merge in [200,201] and final['merged'] and before.get('refs/heads/main')!=after.get('refs/heads/main'))
    record('X05','审批子组资格、自然人去重、撤组重算与合并',passed,'父组继承不计指定子组；同人可满足两个组但独立人数只计一次；撤组即时失去资格；补足两名独立审批后合并',before={'引用':before,'继承审批':inherited_state,'双组同人':dual_state},after={'撤组后':removed_state,'补足后':final_state,'合并HTTP':merge,'PR已合并':final['merged'],'引用':after},defect='SX-X05' if not passed else '')


def x05_followup():
    repo('x05-repo2','x05-root'); r=S['仓库']['x05-repo2']; path='/repos/'+r['full_name']
    actors={n:S['用户']['sx-link-f1-x05-'+n] for n in ['author','dual','second','inherited','direct']}
    member('x05-b',actors['dual'],30)
    ok('PUT',f'/governance/repositories/{r["id"]}/approval-settings',{'prevent_author':True,'prevent_overrides':True,'reset_on_change':True,'revision':0})
    ok('POST',path+'/branch_protections',{'rule_name':'main','enable_push':False,'required_approvals':0,'require_governance_approval':True})
    approval_rule('x05-repo2','组甲',1,group_ids=[S['组织']['x05-a']['id']]); approval_rule('x05-repo2','组乙',1,group_ids=[S['组织']['x05-b']['id']]); approval_rule('x05-repo2','独立人数',2,all_eligible=True)
    _,pr1,sha1=create_pr('x05-repo2',actors['author'],'x05-inherited-f2'); approve('x05-repo2',pr1,actors['inherited'],sha1); inherited_state=approval_state(pr1,actors['inherited'])
    _,pr,sha=create_pr('x05-repo2',actors['author'],'x05-dual-f2'); approve('x05-repo2',pr,actors['dual'],sha); dual_state=approval_state(pr,actors['dual']); before=refs('x05-repo2')
    denied=api('POST',path+f'/pulls/{pr["number"]}/merge',{'Do':'merge','head_commit_id':sha},actors['dual'])[0]; denied_pr=ok('GET',path+f'/pulls/{pr["number"]}',user=actors['dual'])
    member('x05-b',actors['dual'],None); removed_state=approval_state(pr,actors['dual'])
    approve('x05-repo2',pr,actors['dual'],sha); approve('x05-repo2',pr,actors['direct'],sha); final_state=approval_state(pr,actors['direct'])
    for _ in range(20):
        current=ok('GET',path+f'/pulls/{pr["number"]}',user=actors['direct'])
        if current.get('mergeable') is True: break
        time.sleep(.25)
    merge=api('POST',path+f'/pulls/{pr["number"]}/merge',{'Do':'merge','head_commit_id':sha},actors['direct'])[0]; final=ok('GET',path+f'/pulls/{pr["number"]}',user=actors['direct']); after=refs('x05-repo2')
    def rule(state,name): return next(x for x in state['rules'] if x['name']==name)
    passed=(not rule(inherited_state,'组甲')['satisfied'] and not rule(inherited_state,'组乙')['satisfied'] and
            rule(dual_state,'组甲')['satisfied'] and rule(dual_state,'组乙')['satisfied'] and rule(dual_state,'独立人数')['missing']==1 and
            denied>=400 and not denied_pr['merged'] and not rule(removed_state,'组乙')['satisfied'] and
            final_state['satisfied'] and merge in [200,201] and final['merged'] and before.get('refs/heads/main')!=after.get('refs/heads/main'))
    record('X05','独立PR反证审批主体与撤组重算',passed,'父组继承审批不计指定子组；同一人在两组可满足组规则但独立人数仅计一人；撤组后旧审批失效；两名当前直接成员重新审批后合并',before={'继承审批':inherited_state,'双组同人':dual_state,'拒绝HTTP':denied,'拒绝后已合并':denied_pr['merged'],'引用':before},after={'撤组后':removed_state,'重新审批':final_state,'合并HTTP':merge,'PR已合并':final['merged'],'引用':after},defect='SX-X05' if not passed else '')


def e05_complete():
    repo('e05-full','alpha-child'); lead=user('e05-lead'); dev=user('e05-dev'); a1=user('e05-a1'); a2=user('e05-a2')
    direct('e05-full',lead,40)
    for u in [dev,a1,a2]: direct('e05-full',u,30)
    r=S['仓库']['e05-full']; path='/repos/'+r['full_name']
    protection=ok('POST',path+'/branch_protections',{'rule_name':'main','enable_push':False,'required_approvals':2,'enable_status_check':True,'status_check_contexts':['sx/latest'],'dismiss_stale_approvals':True,'require_governance_approval':True},lead)
    ok('PUT',f'/governance/repositories/{r["id"]}/approval-settings',{'prevent_author':True,'prevent_overrides':True,'reset_on_change':True,'revision':0},lead)
    approval_rule('e05-full','两名独立审批',2,actor=lead,all_eligible=True)
    folder,pr,sha=create_pr('e05-full',dev,'e05-release-f1'); approve('e05-full',pr,a1,sha); approve('e05-full',pr,a2,sha)
    pre=approval_state(pr,lead); before=refs('e05-full'); missing_check=api('POST',path+f'/pulls/{pr["number"]}/merge',{'Do':'merge','head_commit_id':sha},lead)[0]
    ok('POST',path+'/statuses/'+sha,{'state':'success','context':'sx/latest','description':'sx最新提交检查'},lead)
    merge=api('POST',path+f'/pulls/{pr["number"]}/merge',{'Do':'merge','head_commit_id':sha},lead)[0]; final=ok('GET',path+f'/pulls/{pr["number"]}',user=lead); merged_refs=refs('e05-full')
    tag_rule=ok('POST',path+'/tag_protections',{'name_pattern':'release-*','whitelist_usernames':[lead['name']]},lead)
    subprocess.run(['git','fetch','origin','main'],cwd=folder,env=env_for(dev),check=True,capture_output=True); subprocess.run(['git','tag','sx-build-f1','FETCH_HEAD'],cwd=folder,check=True,capture_output=True)
    normal=subprocess.run(['git','push',r['clone_url'],'refs/tags/sx-build-f1'],cwd=folder,env=env_for(dev),capture_output=True).returncode
    subprocess.run(['git','tag','release-f1','FETCH_HEAD'],cwd=folder,check=True,capture_output=True); blocked=subprocess.run(['git','push',r['clone_url'],'refs/tags/release-f1'],cwd=folder,env=env_for(dev),capture_output=True).returncode
    lead_folder=TMP/'sx-clones/e05-lead-release';
    if lead_folder.exists(): subprocess.run(['rm','-rf',str(lead_folder)],check=True)
    subprocess.run(['git','clone',r['clone_url'],str(lead_folder)],env=env_for(lead),check=True,capture_output=True); subprocess.run(['git','tag','release-f1'],cwd=lead_folder,check=True,capture_output=True)
    release=subprocess.run(['git','push',r['clone_url'],'refs/tags/release-f1'],cwd=lead_folder,env=env_for(lead),capture_output=True).returncode
    final_refs=refs('e05-full'); feeds_status,feeds=api('GET',path+'/activities/feeds?limit=50',user=lead); org_denied=api('GET',f'/governance/groups/{S["组织"]["alpha-child"]["id"]}/members',user=lead)[0]
    feed_text=json.dumps(feeds,ensure_ascii=False); dynamic_ok=feeds_status==200 and str(pr['number']) in feed_text and ('release-f1' in feed_text or 'sx-build-f1' in feed_text)
    passed=(pre['satisfied'] and missing_check>=400 and merge in [200,201] and final['merged'] and before.get('refs/heads/main')!=merged_refs.get('refs/heads/main') and
            normal==0 and blocked!=0 and release==0 and 'refs/tags/sx-build-f1' in final_refs and 'refs/tags/release-f1' in final_refs and dynamic_ok and org_denied>=400)
    record('E05','负责人配置至审批检查、合并和标签发布完整闭环',passed,'两名独立审批仍须最新检查；检查通过后合并；普通标签可发布，受保护release标签仅负责人发布；组织越权拒绝；动态可追溯',before={'保护':protection,'审批':pre,'缺检查合并HTTP':missing_check,'引用':before},after={'合并HTTP':merge,'PR':{'merged':final['merged'],'merge_commit_sha':final.get('merge_commit_sha')},'合并引用':merged_refs,'标签保护':tag_rule,'普通标签退出码':normal,'低成员release退出码':blocked,'负责人release退出码':release,'最终引用':final_refs,'动态HTTP':feeds_status,'动态命中':dynamic_ok,'组织管理':org_denied},defect='SX-E05' if not passed else '')


def e05_event_reconcile():
    repo_id=S['仓库']['e05-full']['id']; main_sha=refs('e05-full')['refs/heads/main']
    sql=("select json_build_object('repo_id',%d,'feature_push',count(*) filter (where op_type=5 and ref_name='refs/heads/sx/e05-release-f1'),"
         "'main_push',count(*) filter (where op_type=5 and ref_name='refs/heads/main'),'merge_action',count(*) filter (where op_type=11),"
         "'normal_tag',count(*) filter (where op_type=9 and ref_name='refs/tags/sx-build-f1'),'release_tag',count(*) filter (where op_type=9 and ref_name='refs/tags/release-f1')) from action where repo_id=%d"%(repo_id,repo_id))
    db=subprocess.run(['docker','exec','user-permission-20260918-database-1','psql','-U','gitea','-d','gitea','-tAc',sql],capture_output=True,text=True,check=True)
    counts=json.loads(db.stdout.strip())
    log_result=subprocess.run(['docker','logs','--since','2026-09-18T08:32:15Z','--until','2026-09-18T08:32:38Z','user-permission-20260918-gitea-1'],capture_output=True,text=True,check=True)
    logs=log_result.stdout+log_result.stderr
    matched=[line for line in logs.splitlines() if 'post-receive' in line and 'sx-e05-full-f1' in line]
    has_500=any('500 Internal Server Error' in line for line in matched)
    passed=counts['feature_push']>0 and counts['merge_action']>0 and counts['normal_tag']>0 and counts['release_tag']>0 and counts['main_push']==0 and has_500
    record('E05','合并事件数据库与post-receive对账',False,'普通feature push、合并动作和标签动作存在；治理合并的通用main push动作缺失并有同次post-receive 500',before={'仓库ID':repo_id,'目标main SHA':main_sha},after={'action计数':counts,'post-receive日志':matched},defect='MERGE-EVENT-001' if passed else 'UNVERIFIED-E05-EVENT',影响边界='仅确认通用main push动态缺失；合并动作和标签动作存在；未配置Webhook或Actions，不推断其影响')


def x08():
    owner=user('x08-owner'); member('alpha',owner,50); reader=user('x08-reader')
    gid=S['组织']['alpha']['id']; state=ok('GET',f'/governance/groups/{gid}'); role=ok('POST',f'/governance/groups/{gid}/roles',{'name':'sx审计读取','base_role':40,'abilities':['read_audit'],'revision':state['revision']})
    member('alpha',reader,40,role['id']); none=user('x08-none'); cross=user('x08-cross'); member('gamma',cross,50)
    now=dt.datetime.now(dt.timezone.utc); q='?'+urllib.parse.urlencode({'scope_type':'group','scope_id':S['组织']['alpha']['id'],'from':(now-dt.timedelta(days=1)).isoformat(),'to':(now+dt.timedelta(minutes=1)).isoformat(),'limit':100})
    statuses={a['name']:api('GET','/governance/audit-events'+q,user=a)[0] for a in [owner,reader,none,cross]}
    st,body=api('GET','/governance/audit-events'+q,user=reader); raw=json.dumps(body,ensure_ascii=False).lower(); secrets_absent=all(x not in raw for x in ['password','token','sha1'])
    web_ok=Web(reader).get('/governance/audit'+q)[0]; web_none=Web(none).get('/governance/audit'+q)[0]
    passed=statuses[owner['name']]==200 and statuses[reader['name']]==200 and statuses[none['name']]>=400 and statuses[cross['name']]>=400 and secrets_absent and web_ok==200 and web_none>=400
    record('X08','审计API/网页范围鉴权与秘密检查',passed,'有权可查；无权和跨组拒绝；结果不含Token/密码',after={'API':statuses,'网页有权':web_ok,'网页无权':web_none,'无秘密字段':secrets_absent},defect='SX-X08' if not passed else '')


def business_chains(skip_e05=False):
    group('e03-root'); group('e03-target','e03-root'); group('vendor','e03-root'); repo('e03-repo','e03-target'); repo('e03-neighbor','alpha-peer')
    vendor=user('vendor'); member('vendor',vendor,20); expiry=int(time.time())+10; share_repo('e03-repo','vendor',20,expires_unix=expiry)
    before=git('e03-repo',vendor,False,'e03-active'); adjacent=readable('e03-neighbor',vendor)
    rid=S['仓库']['e03-repo']['id']; manage=api('GET',f'/governance/repositories/{rid}/members',user=vendor)[0]
    time.sleep(max(0,expiry+1-time.time())); expired=readable('e03-repo',vendor)
    passed=before['HTTP读取'] and before['SSH读取'] and not before['HTTP写入'] and not before['SSH写入'] and not adjacent and manage>=400 and not expired
    record('E03','供应商短期单仓只读闭环',passed,'有效期内仅目标仓库双协议可读不可写、不能管理或访问邻库；到期后拒绝',before={'有效期':expiry,'访问':before},after={'邻库':adjacent,'成员管理':manage,'到期读取':expired},defect='SX-E03' if not passed else '')
    if skip_e05:
        return
    repo('delivery','alpha-child'); lead=user('delivery-lead'); dev=user('delivery-dev'); direct('delivery',lead,40)
    r=S['仓库']['delivery']; path='/repos/'+r['full_name']; protection=api('POST',path+'/branch_protections',{'rule_name':'main','enable_push':False,'required_approvals':1},lead)[0]
    approval=api('PUT',f'/governance/repositories/{r["id"]}/approval-settings',{'prevent_author':True,'prevent_overrides':True,'reset_on_change':True,'revision':0},lead)[0]
    members=ok('GET',f'/governance/repositories/{r["id"]}/members',user=lead); grant=api('PUT',f'/governance/repositories/{r["id"]}/members/{dev["id"]}',{'role':30,'revision':members['revision']},lead)[0]
    delivery=git('delivery',dev,True,'e05-delivery'); group_admin=api('GET',f'/governance/groups/{S["组织"]["alpha-child"]["id"]}/members',user=lead)[0]
    passed=protection in [201,422] and 200<=approval<300 and 200<=grant<300 and delivery['HTTP写入'] and delivery['SSH写入'] and group_admin>=400
    record('E05','仓库负责人配置门禁并授权交付',passed,'负责人配置保护/审批并授予Developer；成员可交付功能分支；负责人不能越界组织管理',after={'保护':protection,'审批设置':approval,'成员授权':grant,'双协议交付':delivery,'组织管理':group_admin},defect='SX-E05' if not passed else '')


def main():
    arg = sys.argv[1] if len(sys.argv) > 1 else ''
    if arg == '--classify-e05':
        source=next(r for r in reversed([json.loads(x) for x in OUT.read_text().splitlines()]) if r['场景']=='E05')
        record('E05',source['检查'],False,source['预期'],before=source['前后证据']['前'],after=source['前后证据']['后'],defect='MERGE-EVENT-001',核心交付结果='通过',整行结果='失败')
        return
    if arg == '--reconcile-e05-events':
        e05_event_reconcile()
        return
    resume = arg in ('--resume-s09', '--resume-s10', '--resume-x08', '--resume-e03', '--complete-x05-e05', '--followup-x05')
    if OUT.exists() and not resume: OUT.unlink()
    setup()
    if arg == '--complete-x05-e05':
        x05_complete(); e05_complete()
        return
    if arg == '--followup-x05':
        x05_followup()
        return
    if not resume:
        s01_s03(); s04_x07(); s05_x06(); s06(); s07_s08()
    if arg not in ('--resume-x08', '--resume-e03'):
        s09_s10(skip_s09=arg == '--resume-s10'); x01_x03(); x04_x05()
    if arg != '--resume-e03':
        x08()
    business_chains(skip_e05=arg == '--resume-e03')
    rows=[json.loads(x) for x in OUT.read_text().splitlines()]
    missing=[x for x in [*(f'S{i:02d}' for i in range(1,11)),*(f'X{i:02d}' for i in range(1,9))] if x not in {r['场景'] for r in rows}]
    assert not missing, '缺少场景：'+','.join(missing)
    print(json.dumps({'记录':len(rows),'通过':sum(r['结果']=='通过' for r in rows),'失败':sum(r['结果']=='失败' for r in rows)},ensure_ascii=False))


if __name__ == '__main__':
    main()
