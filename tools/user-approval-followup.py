#!/usr/bin/env python3
"""在确认 PR 后台合并计算完成后，补充 E03 合并闭环证据。"""

import base64, datetime, json, os, subprocess, urllib.error, urllib.request
from pathlib import Path

WORK=Path("/tmp/gitea-user-acceptance-20260918")
OUT=Path(__file__).resolve().parents[1]/"docs/evidence/user-permission-20260918/approval-followup-results.jsonl"
BASE="http://127.0.0.1:3538"
ADMIN={"name":"acceptance-admin","password":json.loads((WORK/"secrets.json").read_text())["管理员密码"]}
REPO="approval-root-38f377a2/approval-e03-38f377a2"

def api(method,path,data=None):
    auth=base64.b64encode(f"{ADMIN['name']}:{ADMIN['password']}".encode()).decode()
    req=urllib.request.Request(BASE+"/api/v1"+path,data=json.dumps(data).encode() if data is not None else None,headers={"Authorization":"Basic "+auth,"Content-Type":"application/json"},method=method)
    try:r=urllib.request.urlopen(req,timeout=60)
    except urllib.error.HTTPError as e:r=e
    raw=r.read()
    try:body=json.loads(raw)
    except ValueError:body=raw.decode(errors="replace")
    return r.status,body

def env():
    value=os.environ.copy(); auth=base64.b64encode(f"{ADMIN['name']}:{ADMIN['password']}".encode()).decode()
    value.update({"GIT_CONFIG_NOSYSTEM":"1","GIT_CONFIG_GLOBAL":"/dev/null","GIT_CONFIG_COUNT":"2","GIT_CONFIG_KEY_0":"http.extraHeader","GIT_CONFIG_VALUE_0":"Authorization: Basic "+auth,"GIT_CONFIG_KEY_1":"credential.helper","GIT_CONFIG_VALUE_1":"","GIT_TERMINAL_PROMPT":"0"})
    return value

def refs():
    r=subprocess.run(["git","ls-remote",BASE+"/"+REPO+".git"],env=env(),capture_output=True,text=True,timeout=60)
    if r.returncode: raise RuntimeError("管理员引用见证失败")
    return dict(line.split()[::-1] for line in r.stdout.splitlines())

def main():
    if OUT.exists(): raise RuntimeError("证据文件已存在")
    ps,pr=api("GET",f"/repos/{REPO}/pulls/1")
    ss,state=api("GET","/governance/pulls/9/approval-state")
    before=refs()
    ready=ps==200 and pr.get("mergeable") is True and pr.get("merged") is False and ss==200 and state["state"]["satisfied"] is True
    if ready:
        ms,merge=api("POST",f"/repos/{REPO}/pulls/1/merge",{"Do":"merge","head_commit_id":pr["head"]["sha"]})
    else:
        ms,merge=0,{"message":"前置状态未就绪，未发起合并"}
    fs,final=api("GET",f"/repos/{REPO}/pulls/1")
    after=refs()
    row={"场景":"E03","检查":"后台可合并状态明确后补充合并闭环","结果":"通过" if ready and ms in [200,201] and final.get("merged") is True and after.get("refs/heads/main")!=before.get("refs/heads/main") else "失败","时间":datetime.datetime.now(datetime.timezone.utc).isoformat(),"操作前PR":{"HTTP状态":ps,"mergeable":pr.get("mergeable"),"merged":pr.get("merged"),"head":pr.get("head",{}).get("sha")},"操作前审批状态":{"HTTP状态":ss,"satisfied":state.get("state",{}).get("satisfied"),"rules":state.get("state",{}).get("rules")},"合并HTTP状态":ms,"合并响应":merge,"操作后PR":{"HTTP状态":fs,"merged":final.get("merged"),"merge_commit_sha":final.get("merge_commit_sha")},"操作前引用":before,"操作后引用":after}
    OUT.write_text(json.dumps(row,ensure_ascii=False)+"\n")
    print(row["结果"],ms,final.get("merged"))

if __name__=="__main__":main()
