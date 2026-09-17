#!/usr/bin/env python3
"""验证专用 PostgreSQL 空库安装和当前 Demo 保留数据替换镜像。"""
import base64
import json
import os
import secrets
import subprocess
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORK = ROOT / 'work/navigation-review'
RESULTS = []

def command(args):
    return subprocess.run(args, cwd=ROOT, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.decode()

def wait(port):
    for _ in range(90):
        try:
            with urllib.request.urlopen(f'http://127.0.0.1:{port}/api/healthz', timeout=3) as response:
                if response.status == 200:
                    return
        except Exception:
            time.sleep(2)
    raise RuntimeError('隔离实例启动超时')

def run(name, port, subnet, image, replacement=False):
    folder = WORK / name
    folder.mkdir(exist_ok=True)
    compose = folder / 'compose.yml'
    original = (WORK / 'compose.yml').read_text().replace('gitea-governance-navigation:review-20260916', 'gitea-governance-combined:review-20260916')
    if not compose.exists():
        compose.write_text(original.replace('gitea-governance-combined:review-20260916', image).replace('10.251.240.0/28', subnet))
    env = folder / '.env'
    if not env.exists():
        env.write_text(f'POSTGRES_PASSWORD={secrets.token_urlsafe(28)}\nGITEA_ROOT_URL=http://127.0.0.1:{port}/\nGITEA_DOMAIN=127.0.0.1\nGITEA_HTTP_PORT={port}\nGITEA_SSH_PORT={port+1000}\n')
        env.chmod(0o600)
    prefix = ['docker','compose','--env-file',str(env),'-f',str(compose),'-p','codex-navigation-'+name]
    command(prefix + ['up','-d'])
    wait(port)
    container = 'codex-navigation-'+name+'-gitea-1'
    password_file = folder / 'test-password'
    if not password_file.exists():
        password_file.write_text(secrets.token_urlsafe(22))
        password_file.chmod(0o600)
        command(['docker','exec','-u','git',container,'gitea','admin','user','create','--username','install-check','--password',password_file.read_text(),'--email','install-check@example.invalid','--admin','--must-change-password=false'])
    auth = base64.b64encode(('install-check:'+password_file.read_text()).encode()).decode()
    def api(method, path, data=None):
        req = urllib.request.Request(f'http://127.0.0.1:{port}/api/v1'+path, data=json.dumps(data).encode() if data is not None else None, headers={'Authorization':'Basic '+auth,'Content-Type':'application/json'}, method=method)
        with urllib.request.urlopen(req) as response:
            return json.load(response)
    sample_file = folder / 'sample.json'
    if not sample_file.exists():
        group = api('POST','/governance/groups',{'path':'install-sample','name':'安装保留样本','visibility':2})
        repo = api('POST','/orgs/install-sample/repos',{'name':'preserved-repository','private':True,'auto_init':True})
        sample_file.write_text(json.dumps({'group_id':group['id'],'repository_id':repo['id'],'branch':repo['default_branch']}))
    sample = json.loads(sample_file.read_text())
    protection_path = '/repos/install-sample/preserved-repository/branch_protections'
    if replacement and not sample.get('protection_created'):
        api('POST', protection_path, {'rule_name': 'main', 'required_approvals': 2, 'enable_push': False})
        sample['protection_created'] = True
        sample_file.write_text(json.dumps(sample))
    before = command(['docker','run','--rm','--platform','linux/amd64','--entrypoint','/usr/local/bin/gitea',image,'--version']).strip()
    if replacement:
        compose.write_text(compose.read_text().replace(image,'gitea-governance-combined:review-20260916'))
        command(prefix+['up','-d'])
        wait(port)
    after = command(['docker','exec',container,'gitea','--version']).strip()
    auth = base64.b64encode(('install-check:'+password_file.read_text()).encode()).decode()
    req = urllib.request.Request(f'http://127.0.0.1:{port}/api/v1/user', headers={'Authorization':'Basic '+auth})
    with urllib.request.urlopen(req) as response:
        user=json.load(response)
    assert user['login']=='install-check'
    assert 'governance.demo.r3' in after
    repo = api('GET','/repos/install-sample/preserved-repository')
    assert repo['id'] == sample['repository_id'] and repo['default_branch'] == sample['branch']
    tree = api('GET',f'/governance/navigation/groups/{sample["group_id"]}')
    assert any(n['id'] == sample['repository_id'] and n['type'] == 'repository' for n in tree['items'])
    if replacement:
        protection = api('GET', protection_path + '/main')
        configuration = protection['approval_configuration']
        assert protection['required_approvals'] == 2 and configuration['version']
        assert any(rule['required'] == 2 for rule in configuration['rules'])
    database_version = command(['docker', 'exec', 'codex-navigation-'+name+'-database-1', 'psql', '-U', 'gitea', '-d', 'gitea', '-tAc', 'select version from version']).strip()
    assert database_version == '371'
    RESULTS.append({'用例': '当前 Demo 保留账号与数据卷替换镜像' if replacement else '最终镜像全新 PostgreSQL 安装', '结果':'通过','原程序':before,'新程序':after,'地址':f'http://127.0.0.1:{port}'})
    command(prefix+['stop'])

if __name__ == '__main__':
    try:
        run('combined-upgrade',3520,'10.251.250.0/28','gitea-native-governance-demo:1.27.3',True)
        run('combined-fresh',3521,'10.251.251.0/28','gitea-governance-combined:review-20260916')
    finally:
        (ROOT/'work/combined-release/installation-results.json').write_text(json.dumps(RESULTS,ensure_ascii=False,indent=2))
        print(json.dumps(RESULTS,ensure_ascii=False,indent=2))
