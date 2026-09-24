#!/usr/bin/env python3
"""用真实已发生的操作核对审计投影、敏感字段和当前读取权限。"""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=Path, required=True)
    parser.add_argument("--secret-file", type=Path, action="append", default=[])
    parser.add_argument("--parent-id", type=int, required=True)
    parser.add_argument("--child-id", type=int, required=True)
    parser.add_argument("--owner", required=True)
    parser.add_argument("--child-owner", required=True)
    parser.add_argument("--outsider", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists():
        parser.error("结果文件已存在，请保留旧结果并使用新文件名")
    credentials = json.loads(args.credentials.read_text())
    sensitive = [value for value in credentials.values() if isinstance(value, str)]
    sensitive.extend(path.read_text().strip() for path in args.secret_file)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    checks, samples = [], []

    def query(user, scope, event_type):
        password = credentials.get(user, credentials.get("password"))
        if not isinstance(password, str):
            raise ValueError("私有文件缺少测试账号密码")
        query = urllib.parse.urlencode({"scope_type": "group", "scope_id": scope,
                                       "event_type": event_type, "limit": 100})
        request = urllib.request.Request(args.base.rstrip("/") + "/api/v1/governance/audit-events?" + query,
                                         headers={"Authorization": "Basic " + base64.b64encode((user + ":" + password).encode()).decode()})
        try:
            response = opener.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw, code = response.read(), response.status
        if any(value and value.encode() in raw for value in sensitive):
            raise AssertionError("审计响应出现测试敏感明文；不保存响应体")
        checks.append({"身份": user, "群组ID": scope, "事件": event_type, "HTTP": code, "明文命中": 0})
        return code, json.loads(raw) if code == 200 else None

    for event_type in ("member.removed", "repository.archived", "actions.secret_created",
                       "actions.config_updated", "merge.denied"):
        code, child = query(args.owner, args.child_id, event_type)
        assert code == 200 and child["events"], "缺少真实事件：" + event_type
        event = child["events"][0]
        assert args.child_id in event["ancestor_ids"] and args.parent_id in event["ancestor_ids"]
        assert event["actor"]["id"] > 0 and event["actor"]["name"] and event["actor"]["transport"]
        assert event["request_id"] and event["entity_type"] and event["result"]
        if event_type == "merge.denied":
            assert event["result"] == "denied" and event["details"]["reason"]
        if event_type == "actions.secret_created":
            assert set(event["details"]) <= {"name", "protected", "value_configured", "description_configured"}
        for user, scope in ((args.owner, args.parent_id), (args.child_owner, args.child_id)):
            code, page = query(user, scope, event_type)
            assert code == 200 and event in page["events"], "父子投影内容或独立来源身份不一致"
        samples.append(event)
    for user, scope in ((args.child_owner, args.parent_id), (args.outsider, args.child_id),
                        (args.outsider, args.parent_id)):
        code, _ = query(user, scope, "merge.denied")
        assert code == 404, "无权身份不能查看审计"
    result = {"说明": "真实成员、Secret、配置、生命周期和拒绝事件的范围与脱敏抽样；不代替导出或历史移动验收",
              "源码": args.source, "脚本SHA256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              "查询": checks, "实际事件": samples}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(f"审计抽样通过：{len(checks)} 次查询、{len(samples)} 类真实事件；测试敏感明文命中0")


if __name__ == "__main__":
    main()
