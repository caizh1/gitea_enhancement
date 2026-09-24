#!/usr/bin/env python3
"""验证隔离仓库移出祖先后旧 Hook 事件不可重投。"""

import argparse
import base64
import http.cookiejar
import json
from pathlib import Path
import time
import urllib.error
import urllib.parse
import urllib.request

from capacity_probe import API, write_json


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, _request, _response, _code, _message, _headers, _url):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", required=True, type=Path)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    args = parser.parse_args()
    state_path = args.work / "state.json"
    state = json.loads(state_path.read_text())
    if args.base.rstrip("/") != state["实例"].rstrip("/"):
        raise RuntimeError("隔离实例与固定夹具不一致")
    if "移出结果" in state:
        raise RuntimeError("已有移出结果，不重复转移")
    password = json.loads(args.credentials.read_text())["password"]
    api = API(args.base, state["根Owner"], password)
    old_repo = state["仓库"]["full_name"]
    root = state["根群组"]["compatibility_name"]
    failed = state["失败投递"]["root"]

    def call(label, method, path, expected, body=None):
        code, value, _, _ = api.call(method, path, body)
        state["步骤"].append({"检查": label, "方法": method, "路径": path, "状态": code, "期望": expected})
        write_json(state_path, state)
        if code != expected:
            raise RuntimeError(f"{label} 返回 {code}，预期 {expected}；响应体不记录")
        return value

    def matching_receipts():
        return [row for row in all_receipts() if row["事件标识"] == failed["事件标识"]]

    def all_receipts():
        return [json.loads(line) for line in (args.work / "receipts.jsonl").read_text().splitlines()]

    target = call("建立另一个隔离根群组", "POST", "/governance/groups", 201,
                  {"path": "phase1-hook-moved-" + str(time.time_ns()),
                   "name": "一期 Hook 移出目标", "visibility": 2})
    state["移出目标群组"] = {"id": target["id"], "compatibility_name": target["compatibility_name"]}
    write_json(state_path, state)
    transferred = call("转移自建仓库", "POST", f"/repos/{old_repo}/transfer", 202,
                       {"new_owner": target["compatibility_name"]})
    new_repo = target["compatibility_name"] + "/" + old_repo.rsplit("/", 1)[1]
    checked = call("回读新归属仓库", "GET", "/repos/" + new_repo, 200)
    if checked["id"] != state["仓库"]["id"] or checked["full_name"] != new_repo:
        raise RuntimeError("转移后仓库身份或归属不符")
    state["移出结果"] = {"旧仓库": old_repo, "新仓库": new_repo, "仓库编号": checked["id"],
                       "目标群组": target["id"], "转移返回编号": transferred.get("id")}
    write_json(state_path, state)

    jar = http.cookiejar.CookieJar()
    web = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                      urllib.request.HTTPCookieProcessor(jar), NoRedirect())
    form = urllib.parse.urlencode({"user_name": state["根Owner"], "password": password}).encode()
    try:
        web.open(urllib.request.Request(args.base + "/user/login", data=form, method="POST"), timeout=10)
    except urllib.error.HTTPError as error:
        if error.code != 303:
            raise RuntimeError("网页会话登录失败") from error
    before = len(matching_receipts())
    root_before = sum(row["签名密钥标签"] == ["signing-key-root"] for row in all_receipts())
    history = f"/org/{root}/settings/hooks/{state['Hook编号']['root']}"
    replay_url = args.base + history + "/replay/" + failed["投递标识"]
    try:
        response = web.open(urllib.request.Request(replay_url, data=b"", method="POST"), timeout=10)
        replay_code = response.status
        response.close()
    except urllib.error.HTTPError as error:
        replay_code = error.code
        error.close()
    state["步骤"].append({"检查": "移出后旧祖先来源重投", "状态": replay_code, "期望": 409,
                        "历史页面": args.base + history})
    write_json(state_path, state)
    if replay_code != 409:
        raise RuntimeError(f"移出后旧祖先重投返回 {replay_code}，预期 409")
    marker = str(time.time_ns())
    call("移出后推送新归属仓库", "PUT", f"/repos/{new_repo}/contents/hook-moved-{marker}.txt", 201,
         {"branch": state["仓库"]["default_branch"], "message": "一期 Hook 移出后推送",
          "content": base64.b64encode(marker.encode()).decode()})
    time.sleep(2)
    if len(matching_receipts()) != before:
        raise RuntimeError("移出后旧事件重投产生新接收")
    if sum(row["签名密钥标签"] == ["signing-key-root"] for row in all_receipts()) != root_before:
        raise RuntimeError("移出后新 push 仍触发旧祖先 Hook")
    state["移出结果"].update({"旧来源重投HTTP": replay_code, "历史页面": args.base + history,
                            "旧事件新增接收": 0, "新Push旧来源新增接收": 0})
    write_json(state_path, state)
    print("自建仓库移出后旧祖先来源重投拒绝，旧事件无新增接收")


if __name__ == "__main__":
    main()
