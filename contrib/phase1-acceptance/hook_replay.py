#!/usr/bin/env python3
"""隔离群组 Hook 的真实失败历史、停用和删除重投验收。"""

import argparse
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
        raise SystemExit("隔离实例与夹具不一致")
    password = json.loads(args.credentials.read_text())["password"]
    api = API(args.base, state["根Owner"], password)
    rows = [json.loads(line) for line in (args.work / "receipts.jsonl").read_text().splitlines()]
    failed = {}
    for label in ("root", "child"):
        match = [row for row in rows if row["响应状态"] == 503 and row["签名密钥标签"] == ["signing-key-" + label]]
        if len(match) != 1:
            raise RuntimeError(label + " 来源须恰有一条固定失败样本")
        failed[label] = match[0]
    state["失败投递"] = {label: {"投递标识": row["投递标识"], "事件标识": row["事件标识"],
                                "载荷摘要": row["载荷摘要"]} for label, row in failed.items()}
    write_json(state_path, state)

    def request(method, path, expected, body=None, actor=None):
        status, value, _, _ = api.call(method, path, body, (actor or state["根Owner"], password))
        state["步骤"].append({"检查": method + " " + path, "身份": actor or state["根Owner"],
                            "状态": status, "期望": expected})
        write_json(state_path, state)
        if status != expected:
            raise RuntimeError(f"{method} {path} 返回 {status}，预期 {expected}；正文不保存")
        return value

    def web_replay(label, expected):
        actor = state["根Owner"] if label == "root" else state["子Owner"]
        group = state["根群组"] if label == "root" else state["子群组"]
        base = f"/org/{group['compatibility_name']}/settings/hooks/{state['Hook编号'][label]}"
        jar = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),
                                              urllib.request.ProxyHandler({}), NoRedirect())
        login = urllib.parse.urlencode({"user_name": actor, "password": password}).encode()
        try:
            opener.open(urllib.request.Request(args.base + "/user/login", data=login, method="POST"), timeout=10)
        except urllib.error.HTTPError as error:
            if error.code != 303:
                raise RuntimeError("网页会话登录失败") from error
        url = args.base + base + "/replay/" + failed[label]["投递标识"]
        try:
            response = opener.open(urllib.request.Request(url, data=b"", method="POST"), timeout=10)
            status = response.status
            response.close()
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
        state["步骤"].append({"检查": label + " 人工重投", "身份": actor, "状态": status,
                            "期望": expected, "来源Hook": state["Hook编号"][label]})
        write_json(state_path, state)
        if status != expected:
            raise RuntimeError(f"{label} 重投返回 {status}，预期 {expected}")
        return base

    def count_event(label):
        current = [json.loads(line) for line in (args.work / "receipts.jsonl").read_text().splitlines()]
        return [row for row in current if row["事件标识"] == failed[label]["事件标识"]]

    root = state["根群组"]["compatibility_name"]
    child = state["子群组"]["compatibility_name"]
    root_path = f"/orgs/{root}/hooks/{state['Hook编号']['root']}"
    child_path = f"/orgs/{child}/hooks/{state['Hook编号']['child']}"
    before_root, before_child = len(count_event("root")), len(count_event("child"))

    request("PATCH", root_path, 200, {"active": False})
    root_hook = request("GET", root_path, 200)
    if root_hook["active"] or "issues" not in root_hook["events"]:
        raise RuntimeError("停用应保留原事件目录")
    root_ui = web_replay("root", 409)
    time.sleep(0.5)
    if len(count_event("root")) != before_root:
        raise RuntimeError("停用后拒绝重投仍出现网络投递")
    request("PATCH", root_path, 200, {"active": True})
    web_replay("root", 303)
    for _ in range(30):
        current = count_event("root")
        if len(current) > before_root:
            break
        time.sleep(0.5)
    current = count_event("root")
    if len(current) != before_root + 1:
        raise RuntimeError("恢复后应恰有一条新投递")
    replayed = current[-1]
    if replayed["投递标识"] == failed["root"]["投递标识"] or replayed["载荷摘要"] != failed["root"]["载荷摘要"] or replayed["签名密钥标签"] != ["signing-key-root"] or replayed["响应状态"] != 200:
        raise RuntimeError("合法重投的事件标识、尝试标识、签名或载荷不符合预期")

    request("PATCH", child_path, 200, {"active": False}, actor=state["子Owner"])
    web_replay("child", 409)
    if len(count_event("child")) != before_child:
        raise RuntimeError("子 Hook 停用后仍出现网络投递")
    request("DELETE", child_path, 204, actor=state["子Owner"])
    web_replay("child", 404)
    if len(count_event("child")) != before_child:
        raise RuntimeError("子 Hook 删除后仍出现网络投递")
    state["重投结果"] = {"根历史页面": args.base + root_ui, "根失败投递": failed["root"]["投递标识"],
                       "根重投投递": replayed["投递标识"], "根事件标识": replayed["事件标识"],
                       "子失败投递": failed["child"]["投递标识"], "子Hook已删除": True}
    write_json(state_path, state)
    print("根 Hook 停用拒投、恢复成功重投；子 Hook 停用与删除均拒投，签名及历史标识通过")


if __name__ == "__main__":
    main()
