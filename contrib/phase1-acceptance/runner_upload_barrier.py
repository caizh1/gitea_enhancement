#!/usr/bin/env python3
"""在独立本机仓库固定真实 Runner v4 上传中的撤权或待删恢复。"""

import argparse
import base64
import hashlib
import io
import json
from pathlib import Path
import sqlite3
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("revoke", "pending-delete"))
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--credentials", type=Path, required=True)
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--database", type=Path, required=True, help="运行中本机隔离实例的 SQLite 数据库")
    parser.add_argument("--artifact-root", type=Path, required=True, help="运行中实例的 data/actions_artifacts")
    parser.add_argument("--owner", required=True)
    parser.add_argument("--actor", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    fixture = json.loads(args.fixture.read_text())
    base = fixture["实例"].rstrip("/")
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "localhost"):
        parser.error("仅允许本机隔离 HTTP 实例")
    if fixture["仓库ID"] != 72 or fixture["仓库"] != "a03b-lifecycle-20260924":
        parser.error("仅允许本轮专用仓库 72")
    if not args.database.is_file() or not (args.artifact_root / "tmp-upload").is_dir():
        parser.error("隔离数据库或产物存储目录不存在")
    password = json.loads(args.credentials.read_text()).get("password")
    if not isinstance(password, str):
        parser.error("私有凭据缺少 password")
    repo = "/api/v1/repos/" + fixture["组织"] + "/" + fixture["仓库"]
    governance = "/api/v1/governance/repositories/72"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    db = sqlite3.connect("file:" + urllib.parse.quote(str(args.database)) + "?mode=ro", uri=True, timeout=2)
    db.execute("PRAGMA query_only=ON")
    baseline = {p.name for p in (args.artifact_root / "tmp").glob("upload-*")}
    result = {"说明": "只对仓库72真实Runner上传正文已写入时变更范围，失败/错过屏障均保留现场",
              "模式": args.mode, "仓库ID": 72, "步骤": [], "通过": False}

    def save():
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")

    def auth(user):
        return "Basic " + base64.b64encode((user + ":" + password).encode()).decode()

    def request(path, method="GET", body=None, user=args.owner):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(base + path, method=method, data=data,
                                     headers={"Authorization": auth(user), "Content-Type": "application/json"})
        try:
            with opener.open(req, timeout=20) as response:
                return response.status, dict(response.headers), response.read()
        except urllib.error.HTTPError as error:
            return error.code, dict(error.headers), error.read()

    def checked(path, user=args.owner):
        code, _, raw = request(path, user=user)
        if code != 200:
            raise RuntimeError("预期可读的专用接口返回 " + str(code) + ": " + path)
        return json.loads(raw)

    def artifact_digest(artifact_id, member):
        code, _, raw = request(repo + "/actions/artifacts/" + str(artifact_id) + "/zip")
        if code != 200:
            return code, ""
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            return code, hashlib.sha256(archive.read(member)).hexdigest()

    def new_run(before, workflow):
        until = time.monotonic() + 15
        while time.monotonic() < until:
            runs = checked(repo + "/actions/runs?limit=20")["workflow_runs"]
            found = [r for r in runs if r["id"] > before and r.get("path", "").startswith(workflow + "@")]
            if found:
                return max(found, key=lambda r: r["id"])["id"]
            time.sleep(0.03)
        raise TimeoutError("派发已返回，但未找到本工作流的新 Run")

    def terminal(run_id, timeout=90):
        until = time.monotonic() + timeout
        while time.monotonic() < until:
            rows = db.execute("SELECT status FROM action_run WHERE id=? AND repo_id=72", (run_id,)).fetchall()
            if len(rows) == 1 and rows[0][0] in (1, 2, 3, 4):
                return rows[0][0]
            time.sleep(0.1)
        raise TimeoutError("Run 未进入真实终态，保留范围状态供人工检查，不能立即恢复")

    own = checked(repo)
    if own["id"] != 72 or own["archived"]:
        raise RuntimeError("专用仓库当前非正常可写状态")
    if checked(governance + "/deletion").get("pending") is not None:
        raise RuntimeError("专用仓库已有待删计划")
    keep = next(item for item in fixture["产物"] if item["name"] == "phase1-retain")
    code, digest = artifact_digest(keep["id"], "payload.txt")
    if code != 200 or digest != fixture["内容摘要"]:
        raise RuntimeError("原有产物基线不符")
    if args.mode == "revoke":
        code, _, _ = request(repo + "/collaborators/" + args.actor, "PUT", {"permission": "write"})
        result["步骤"].append({"检查": "临时本仓写权","HTTP": code})
        save()
        if code != 204:
            raise RuntimeError("未取得协作者本仓写权")
    before = max(r["id"] for r in checked(repo + "/actions/runs?limit=1")["workflow_runs"])
    trigger = args.actor if args.mode == "revoke" else args.owner
    code, _, _ = request(repo + "/actions/workflows/a03b-upload-archive2.yml/dispatches", "POST",
                         {"ref": "main"}, trigger)
    result["步骤"].append({"检查": "真实工作流派发", "身份": "临时协作者" if args.mode == "revoke" else "Owner", "HTTP": code})
    save()
    if code not in (200, 204):
        raise RuntimeError("真实工作流派发失败")
    run_id = new_run(before, "a03b-upload-archive2.yml")
    result["RunID"] = run_id
    save()

    artifact_id = None
    observed = None
    until = time.monotonic() + 45
    while time.monotonic() < until:
        row = db.execute("SELECT id,status,file_size FROM action_artifact WHERE repo_id=72 AND run_id=? AND artifact_name='a03b-upload-archive2' ORDER BY id DESC LIMIT 1", (run_id,)).fetchone()
        if row:
            artifact_id = row[0]
            run_dir = args.artifact_root / "tmp-upload" / ("run-" + str(run_id) + "-v4")
            temps = [p for p in (args.artifact_root / "tmp").glob("upload-*") if p.name not in baseline]
            active = [(p, p.stat().st_size) for p in temps if p.is_file()]
            large = [(p, size) for p, size in active if size >= 1048576]
            if row[1] == 1 and row[2] == 0 and run_dir.is_dir() and len(large) == 1:
                p, size = large[0]
                observed = {"RunID": run_id, "产物ID": artifact_id, "产物状态": row[1],
                            "产物已提交文件字节": row[2], "Run专用临时目录存在": True,
                            "唯一新上传临时正文已写字节": size, "临时正文文件名": p.name}
                break
        time.sleep(0.003)
    if observed is None:
        result["步骤"].append({"检查": "真实上传正文屏障", "结果": "未观察到；不执行范围变更，不计通过"})
        save()
        raise RuntimeError("未捕获本 Run 的真实上传正文")
    result["步骤"].append({"检查": "真实上传正文屏障", "观察": observed})
    save()

    if args.mode == "revoke":
        code, _, _ = request(repo + "/collaborators/" + args.actor, "DELETE")
    else:
        code, headers, _ = request(repo, "DELETE")
        result["删除计划响应头"] = headers.get("X-Gitea-Deletion-State", "")
    result["步骤"].append({"检查": "上传正文中变更范围", "HTTP": code})
    save()
    expected = 204
    if code != expected or (args.mode == "pending-delete" and result["删除计划响应头"] != "scheduled"):
        raise RuntimeError("范围变更未按预期生效，保留现场")
    status = terminal(run_id)
    result["步骤"].append({"检查": "范围变更后真实Run终态", "状态枚举": status})
    save()
    if args.mode == "pending-delete":
        pending = checked(governance + "/deletion")
        if not pending.get("pending"):
            raise RuntimeError("删除计划状态不存在")
        denied_pending, _, _ = request(repo + "/actions/artifacts/" + str(artifact_id) + "/zip")
        result["步骤"].append({"检查": "待删期间未完成新产物下一请求", "HTTP": denied_pending})
        denied_dispatch, _, _ = request(repo + "/actions/workflows/a03b-large.yml/dispatches", "POST", {"ref": "main"})
        result["步骤"].append({"检查": "待删期间新Runner任务派发", "HTTP": denied_dispatch})
        save()
        if denied_pending != 404 or denied_dispatch not in (403, 404, 409, 423):
            raise RuntimeError("待删期间新产物或新任务请求仍被接受")
        original = pending["full_path"]
        due = pending["pending"]["due_unix"]
        code, _, _ = request(governance + "/restore", "POST", {"confirmation_path": original, "due_unix": due})
        result["步骤"].append({"检查": "终态后正常恢复原路径", "HTTP": code, "原路径不同": original != fixture["组织"] + "/" + fixture["仓库"]})
        save()
        if code != 200:
            raise RuntimeError("仓库恢复失败，保留准确状态")
    run = checked(repo + "/actions/runs/" + str(run_id))
    artifacts = checked(repo + "/actions/runs/" + str(run_id) + "/artifacts")["artifacts"]
    denied, _, _ = request(repo + "/actions/artifacts/" + str(artifact_id) + "/zip")
    result["步骤"].append({"检查": "失败Run和未完成产物", "状态": run["status"],
                            "结论": run["conclusion"], "产物列表数": len(artifacts), "未完成产物新请求HTTP": denied})
    job = checked(repo + "/actions/runs/" + str(run_id) + "/jobs")["jobs"]
    if len(job) != 1:
        raise RuntimeError("失败Run的Job数量不是一")
    unauthorized = False
    log_code = 0
    for _ in range(25):
        log_code, _, log_raw = request(repo + "/actions/jobs/" + str(job[0]["id"]) + "/logs")
        unauthorized = log_code == 200 and b"task is not authorized" in log_raw
        if unauthorized:
            break
        time.sleep(0.2)
    result["步骤"].append({"检查": "真实Runner上传拒绝原因", "JobID": job[0]["id"],
                            "日志HTTP": log_code, "任务未授权": unauthorized})
    code, digest = artifact_digest(keep["id"], "payload.txt")
    result["步骤"].append({"检查": "原有其他产物正文未误删", "HTTP": code, "摘要一致": digest == fixture["内容摘要"]})
    if args.mode == "revoke":
        actor_code, _, _ = request(repo + "/actions/artifacts/" + str(keep["id"]) + "/zip", user=args.actor)
        result["步骤"].append({"检查": "被撤权身份下一请求", "HTTP": actor_code})
    assets_code, _, raw = request(repo + "/releases/4/assets")
    asset_ids = [a["id"] for a in json.loads(raw)] if assets_code == 200 else []
    result["步骤"].append({"检查": "其他Release附件未误删", "HTTP": assets_code, "保留ID": asset_ids})
    code, _, _ = request(repo + "/actions/workflows/a03b-large.yml/dispatches", "POST", {"ref": "main"})
    result["步骤"].append({"检查": "范围合法后新工作流派发", "HTTP": code})
    save()
    if code not in (200, 204):
        raise RuntimeError("恢复后的合法新工作流不能派发")
    fresh_id = new_run(run_id, "a03b-large.yml")
    fresh_status = terminal(fresh_id)
    fresh = checked(repo + "/actions/runs/" + str(fresh_id))
    fresh_arts = checked(repo + "/actions/runs/" + str(fresh_id) + "/artifacts")["artifacts"]
    fresh_artifact = next((a for a in fresh_arts if a["name"] == "a03b-large"), None)
    if not fresh_artifact:
        raise RuntimeError("恢复后的合法Run没有产物")
    code, digest = artifact_digest(fresh_artifact["id"], "payload-large.bin")
    expected_digest = hashlib.sha256(__import__("random").Random(20260924).randbytes(33554432)).hexdigest()
    result["步骤"].append({"检查": "范围合法后真实Runner新任务与正文", "RunID": fresh_id,
                            "状态枚举": fresh_status, "结论": fresh["conclusion"], "产物ID": fresh_artifact["id"],
                            "下载HTTP": code, "摘要一致": digest == expected_digest})
    result["通过"] = (status in (2, 3) and run["conclusion"] in ("failure", "cancelled")
                    and len(artifacts) == 0 and denied == 404 and unauthorized
                    and any(s.get("检查") == "原有其他产物正文未误删" and s.get("HTTP") == 200 and s.get("摘要一致") for s in result["步骤"])
                    and assets_code == 200 and 4 in asset_ids and 6 in asset_ids
                    and (args.mode != "revoke" or actor_code in (403, 404))
                    and (args.mode != "pending-delete" or (denied_pending == 404 and denied_dispatch in (403, 404, 409, 423)))
                    and digest == expected_digest and code == 200 and fresh_status == 1
                    and fresh["conclusion"] == "success")
    save()
    print(json.dumps({"模式": args.mode, "通过": result["通过"], "旧RunID": run_id,
                      "旧产物ID": artifact_id, "恢复后RunID": fresh_id}, ensure_ascii=False))
    if not result["通过"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
