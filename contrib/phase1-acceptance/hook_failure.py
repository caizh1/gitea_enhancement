#!/usr/bin/env python3
"""在隔离仓库制造一次双来源 503 真实投递，保留首错历史。"""

import argparse
import json
from pathlib import Path
import time

from capacity_probe import API, write_json


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
    if "失败投递" in state:
        raise RuntimeError("失败样本已经存在，不得覆盖")
    password = json.loads(args.credentials.read_text())["password"]
    api = API(args.base, state["根Owner"], password)
    response_status = args.work / "response-status"
    if response_status.read_text().strip() != "200":
        raise RuntimeError("隔离接收端须先处于 HTTP 200")
    rows_before = (args.work / "receipts.jsonl").read_text().splitlines()
    try:
        response_status.write_text("503\n")
        issue = state["事件编号"]["Issue"]
        repo = state["仓库"]["full_name"]
        code, _, _, _ = api.call("POST", f"/repos/{repo}/issues/{issue}/comments",
                                 {"body": "一期隔离 Hook 失败投递 " + str(time.time_ns())})
        if code != 201:
            raise RuntimeError(f"失败投递评论创建返回 {code}")
        for _ in range(60):
            new_lines = (args.work / "receipts.jsonl").read_text().splitlines()[len(rows_before):]
            rows = [json.loads(line) for line in new_lines]
            found = {label: [row for row in rows if row["响应状态"] == 503
                             and row["签名密钥标签"] == ["signing-key-" + label]]
                     for label in ("root", "child")}
            if all(len(value) == 1 for value in found.values()):
                break
            time.sleep(0.25)
        else:
            raise TimeoutError("没有收到两个来源的真实 503 投递")
        if found["root"][0]["载荷摘要"] != found["child"][0]["载荷摘要"]:
            raise RuntimeError("两个来源的失败载荷摘要不一致")
        state["失败投递"] = {label: {"投递标识": value[0]["投递标识"],
                                      "事件标识": value[0]["事件标识"],
                                      "载荷摘要": value[0]["载荷摘要"]}
                         for label, value in found.items()}
        write_json(state_path, state)
        print("根/子来源各保留一条真实 503，签名有效且载荷相同")
    finally:
        response_status.write_text("200\n")


if __name__ == "__main__":
    main()
