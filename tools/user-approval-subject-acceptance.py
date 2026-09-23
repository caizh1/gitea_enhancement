#!/usr/bin/env python3
"""验证审批组直接成员语义及独立审批人数门禁。"""

import base64, datetime, json, os, secrets, subprocess, urllib.error, urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORK = Path("/tmp/gitea-user-acceptance-20260918")
BASE = "http://127.0.0.1:3538"
OUT = ROOT / "docs/evidence/user-permission-20260918/approval-subject-results.jsonl"
ADMIN = {"name": "acceptance-admin", "password": json.loads((WORK / "secrets.json").read_text())["管理员密码"]}


def api(method, path, data=None, user=None):
    user = user or ADMIN
    auth = "token " + user["token"] if "token" in user else "Basic " + base64.b64encode(f"{user['name']}:{user['password']}".encode()).decode()
    req = urllib.request.Request(BASE + "/api/v1" + path, data=json.dumps(data).encode() if data is not None else None, headers={"Authorization": auth, "Content-Type": "application/json"}, method=method)
    try: response = urllib.request.urlopen(req, timeout=40)
    except urllib.error.HTTPError as error: response = error
    raw = response.read()
    try: body = json.loads(raw)
    except ValueError: body = raw.decode(errors="replace")
    return response.status, body


def ok(method, path, data=None, user=None):
    status, body = api(method, path, data, user)
    if not 200 <= status < 300: raise RuntimeError(f"准备失败：{method} {path}，状态 {status}，响应 {body}")
    return body


def run(args, env=None, cwd=None): return subprocess.run(args, env=env, cwd=cwd, capture_output=True, text=True, timeout=90)


def env_for(user):
    env = os.environ.copy()
    for key in list(env):
        if key.startswith("GIT_"): del env[key]
    basic = base64.b64encode(f"{user['name']}:{user.get('token', user.get('password'))}".encode()).decode()
    env.update({"GIT_CONFIG_NOSYSTEM":"1", "GIT_CONFIG_GLOBAL":"/dev/null", "GIT_CONFIG_COUNT":"2", "GIT_CONFIG_KEY_0":"http.extraHeader", "GIT_CONFIG_VALUE_0":"Authorization: Basic "+basic, "GIT_CONFIG_KEY_1":"credential.helper", "GIT_CONFIG_VALUE_1":"", "GIT_TERMINAL_PROMPT":"0"})
    return env


def refs(repo):
    result = run(["git", "ls-remote", repo["clone_url"]], env_for(ADMIN))
    if result.returncode: raise RuntimeError("管理员见证引用失败")
    return dict(line.split()[::-1] for line in result.stdout.splitlines())


def record(case, check, passed, **evidence):
    row = {"场景":case, "检查":check, "结果":"通过" if passed else "失败", "时间":datetime.datetime.now(datetime.timezone.utc).isoformat(), **evidence}
    with OUT.open("a") as stream: stream.write(json.dumps(row, ensure_ascii=False)+"\n")
    print(case, check, row["结果"], flush=True)


def new_user(name):
    password = secrets.token_urlsafe(24)
    created = ok("POST", "/admin/users", {"username":name, "password":password, "email":name+"@example.invalid", "must_change_password":False})
    user = {"name":name, "password":password, "id":created["id"]}
    user["token"] = ok("POST", f"/users/{name}/tokens", {"name":"审批主体验收", "scopes":["all"]}, user)["sha1"]
    return user


def add_member(group, user, role=30):
    state = ok("GET", f"/governance/groups/{group['id']}")
    ok("PUT", f"/governance/groups/{group['id']}/members/{user['id']}", {"role":role, "revision":state["revision"]})


def rule(repo, name, required, **subjects):
    body = {"rule":{"name":name, "required":required, "branch_mode":"all", "enabled":True, **subjects}, "revision":0}
    return ok("POST", f"/governance/repositories/{repo['id']}/approval-rules", body)


def prepare_repo(parent, name):
    repo = ok("POST", f"/orgs/{parent['full_path']}/repos", {"name":name, "private":True, "auto_init":True, "default_branch":"main"})
    ok("PUT", f"/governance/repositories/{repo['id']}/approval-settings", {"prevent_author":True, "prevent_committer":False, "prevent_overrides":True, "reset_on_change":True, "require_reauthentication":False, "revision":0})
    ok("POST", f"/repos/{repo['full_name']}/branch_protections", {"rule_name":"main", "enable_push":False, "required_approvals":0, "require_governance_approval":True})
    return repo


def create_pr(repo, author, suffix):
    folder = WORK / ("approval-subject-"+suffix)
    result = run(["git", "clone", repo["clone_url"], str(folder)], env_for(author))
    if result.returncode: raise RuntimeError("作者克隆失败："+result.stderr[-500:])
    (folder/"approval.txt").write_text("审批主体真实验收："+suffix+"\n")
    for command in (["git","config","user.name","审批作者"],["git","config","user.email","author@example.invalid"],["git","add","approval.txt"],["git","commit","-m","test: 验证审批主体"]):
        result=run(command,cwd=folder)
        if result.returncode: raise RuntimeError("创建提交失败")
    sha=run(["git","rev-parse","HEAD"],cwd=folder).stdout.strip(); branch="feature/"+suffix
    result=run(["git","push",repo["clone_url"],"HEAD:refs/heads/"+branch],env_for(author),folder)
    if result.returncode: raise RuntimeError("推送功能分支失败："+result.stderr[-500:])
    pr=ok("POST",f"/repos/{repo['full_name']}/pulls",{"title":"审批主体 "+suffix,"head":branch,"base":"main"},author)
    return pr, sha


def approve(repo, pr, user, sha):
    return api("POST", f"/repos/{repo['full_name']}/pulls/{pr['number']}/reviews", {"event":"APPROVED", "body":"审批主体实测", "commit_id":sha}, user)


def state(pr, user): return ok("GET", f"/governance/pulls/{pr['id']}/approval-state", user=user)


def merge(repo, pr, actor, sha): return api("POST", f"/repos/{repo['full_name']}/pulls/{pr['number']}/merge", {"Do":"merge", "head_commit_id":sha}, actor)


def main():
    OUT.parent.mkdir(parents=True, exist_ok=True)
    if OUT.exists(): raise RuntimeError("证据文件已存在，拒绝覆盖")
    suffix=secrets.token_hex(4)
    parent=ok("POST","/governance/groups",{"name":"审批根组","path":"approval-root-"+suffix,"parent_id":0,"visibility":2,"revision":0})
    group_a=ok("POST","/governance/groups",{"name":"审批组甲","path":"pool-a","parent_id":parent["id"],"visibility":2,"revision":parent["revision"]})
    parent=ok("GET",f"/governance/groups/{parent['id']}")
    group_b=ok("POST","/governance/groups",{"name":"审批组乙","path":"pool-b","parent_id":parent["id"],"visibility":2,"revision":parent["revision"]})
    users={name:new_user(name+"-"+suffix) for name in ["author","dual","second","inherited","direct"]}
    for user in users.values(): add_member(parent,user)
    add_member(group_a,users["dual"]); add_member(group_b,users["dual"]); add_member(group_a,users["second"])
    add_member(group_b,users["direct"])

    repo3=prepare_repo(parent,"approval-e03-"+suffix)
    rule(repo3,"审批组甲",1,group_ids=[group_a["id"]]); rule(repo3,"审批组乙",1,group_ids=[group_b["id"]]); rule(repo3,"独立总人数",2,all_eligible=True)
    pr3,sha3=create_pr(repo3,users["author"],"e03-"+suffix)
    approve(repo3,pr3,users["dual"],sha3); s1=state(pr3,users["dual"]); before=refs(repo3); m1,b1=merge(repo3,pr3,users["dual"],sha3); after=refs(repo3)
    group_rules=[r for r in s1["state"]["rules"] if r["name"] in ["审批组甲","审批组乙"]]; total=next(r for r in s1["state"]["rules"] if r["name"]=="独立总人数")
    record("E03","同一人满足两个指定组但不能满足两人总门禁",not s1["state"]["satisfied"] and all(r["satisfied"] for r in group_rules) and total["missing"]==1 and m1>=400 and before==after,审批状态=s1["state"],合并HTTP状态=m1,合并响应=str(b1)[:800],操作前=before,操作后=after)
    approve(repo3,pr3,users["second"],sha3); s2=state(pr3,users["second"]); m2,b2=merge(repo3,pr3,users["second"],sha3); merged=ok("GET",f"/repos/{repo3['full_name']}/pulls/{pr3['number']}",user=users["second"])["merged"]
    record("E03","补第二名独立审批人后门禁满足并可合并",s2["state"]["satisfied"] and m2 in [200,201] and merged,审批状态=s2["state"],合并HTTP状态=m2,合并响应=str(b2)[:800],是否合并=merged)

    repo4=prepare_repo(parent,"approval-e04-"+suffix); rule(repo4,"子组直接成员",1,group_ids=[group_b["id"]])
    pr4,sha4=create_pr(repo4,users["author"],"e04-"+suffix)
    approve(repo4,pr4,users["inherited"],sha4); s3=state(pr4,users["inherited"]); before=refs(repo4); m3,b3=merge(repo4,pr4,users["inherited"],sha4); after=refs(repo4)
    rr=next(r for r in s3["state"]["rules"] if r["name"]=="子组直接成员")
    record("E04","仅继承父组角色的审批不计入指定子组",not s3["state"]["satisfied"] and rr["missing"]==1 and users["inherited"]["id"] not in rr["approved_user_ids"] and m3>=400 and before==after,审批状态=s3["state"],合并HTTP状态=m3,合并响应=str(b3)[:800],操作前=before,操作后=after)
    approve(repo4,pr4,users["direct"],sha4); s4=state(pr4,users["direct"]); m4,b4=merge(repo4,pr4,users["direct"],sha4); merged=ok("GET",f"/repos/{repo4['full_name']}/pulls/{pr4['number']}",user=users["direct"])["merged"]
    rr=next(r for r in s4["state"]["rules"] if r["name"]=="子组直接成员")
    record("E04","指定子组直接成员审批计票并可合并",s4["state"]["satisfied"] and users["direct"]["id"] in rr["approved_user_ids"] and m4 in [200,201] and merged,审批状态=s4["state"],合并HTTP状态=m4,是否合并=merged)


if __name__=="__main__": main()
