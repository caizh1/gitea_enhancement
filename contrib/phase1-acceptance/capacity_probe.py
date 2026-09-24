#!/usr/bin/env python3
"""一期容量夹具和原始请求采集；只用于明确指定的隔离实例。"""

import argparse
import base64
import concurrent.futures
import datetime as dt
import hashlib
import hmac
import json
import math
import os
from pathlib import Path
import re
import threading
import time
import urllib.error
import urllib.parse
import urllib.request


def utc_now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def p95(values):
    if not values:
        raise ValueError("没有可计算的原始样本")
    return sorted(values)[math.ceil(len(values) * 0.95) - 1]


def file_sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def write_json(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_name(path.name + ".tmp")
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(data, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    os.replace(temp, path)


def append_jsonl(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        stream.write(json.dumps(data, ensure_ascii=False) + "\n")


def member_password(seed, prefix, index):
    digest = hmac.new(seed.encode(), f"{prefix}:成员:{index}".encode(), hashlib.sha256).hexdigest()
    return "P1!" + digest[:32]


class API:
    def __init__(self, base, admin_user, admin_password):
        self.base = base.rstrip("/") + "/api/v1"
        self.admin = (admin_user, admin_password)

    def call(self, method, path, payload=None, actor=None):
        actor = actor or self.admin
        token = base64.b64encode(f"{actor[0]}:{actor[1]}".encode()).decode()
        body = json.dumps(payload, ensure_ascii=False).encode() if payload is not None else None
        request = urllib.request.Request(self.base + path, data=body, method=method, headers={
            "Authorization": "Basic " + token,
            "Content-Type": "application/json",
        })
        started = time.perf_counter()
        try:
            response = urllib.request.urlopen(request, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        raw = response.read()
        elapsed = (time.perf_counter() - started) * 1000
        try:
            value = json.loads(raw) if raw else None
        except ValueError:
            value = None
        return response.status, value, len(raw), elapsed

    def ok(self, method, path, payload=None, actor=None):
        status, value, _, _ = self.call(method, path, payload, actor)
        if not 200 <= status < 300:
            raise RuntimeError(f"接口失败：{method} {path}，HTTP {status}；响应体不写入日志")
        return value


class Fixture:
    runner_workflow = "name: 一期 Runner 批次\non:\n  workflow_dispatch:\njobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n      - run: sleep 20\n"

    def __init__(self, args, api):
        self.args, self.api = args, api
        self.path = Path(args.state)
        config = {key: getattr(args, key) for key in
                  ("prefix", "members", "repos", "depth", "shares", "issues", "pulls", "tasks")}
        config["base_url"] = args.base_url.rstrip("/")
        if self.path.exists():
            self.state = json.loads(self.path.read_text(encoding="utf-8"))
            if self.state["config"] != config:
                raise ValueError("状态文件的固定输入与本次命令不一致")
        else:
            self.state = {"config": config, "groups": [], "users": [], "repos": [],
                          "shares": [], "issues": [], "pulls": [], "tasks": [], "runner_batches": {}}
            self.save()

    def save(self):
        write_json(self.path, self.state)

    def ensure_group(self, slug, parent_id):
        after = 0
        while True:
            rows = self.api.ok("GET", f"/governance/groups?parent_id={parent_id}&after_id={after}")
            for row in rows:
                if row["full_path"].split("/")[-1] == slug:
                    return row
            if len(rows) < 100:
                break
            after = rows[-1]["id"]
        return self.api.ok("POST", "/governance/groups", {
            "path": slug, "name": "一期容量隔离样本", "parent_id": parent_id, "visibility": 2,
        })

    def ensure_user(self, name, password):
        status, value, _, _ = self.api.call("GET", "/users/" + name)
        if status == 200:
            return value
        if status != 404:
            raise RuntimeError(f"查询成员失败：HTTP {status}")
        return self.api.ok("POST", "/admin/users", {
            "username": name, "password": password, "email": name + "@example.invalid",
            "must_change_password": False,
        })

    def ensure_repo(self, group, name):
        full_name = group["full_path"] + "/" + name
        status, value, _, _ = self.api.call("GET", "/repos/" + full_name)
        if status == 200:
            return value
        if status != 404:
            raise RuntimeError(f"查询仓库失败：HTTP {status}")
        return self.api.ok("POST", "/orgs/" + group["full_path"] + "/repos", {
            "name": name, "private": True, "auto_init": True, "default_branch": "main",
            "description": "一期容量固定样本：含 README、Issue、PR 与 Actions 子集",
        })

    def ensure_file(self, repo, path, content, branch="main"):
        encoded_path = urllib.parse.quote(path, safe="/")
        url = f"/repos/{repo['full_name']}/contents/{encoded_path}"
        status, _, _, _ = self.api.call("GET", url + "?ref=" + urllib.parse.quote(branch, safe=""))
        if status == 200:
            return
        if status != 404:
            raise RuntimeError(f"查询内容失败：{repo['full_name']} {path}，HTTP {status}")
        self.api.ok("POST", url, {
            "branch": branch, "message": "test: 建立一期容量固定内容",
            "content": base64.b64encode(content.encode()).decode(),
        })

    def seed(self):
        a, s = self.args, self.state
        root = self.ensure_group(a.prefix + "-root", 0)
        if not s["groups"]:
            s["groups"].append(root)
            self.save()
        for index in range(1, a.depth):
            group = self.ensure_group(f"{a.prefix}-d{index + 1:02d}", s["groups"][index - 1]["id"])
            if len(s["groups"]) == index:
                s["groups"].append(group)
                self.save()
        if a.shares:
            target = self.ensure_group(a.prefix + "-shared", 0)
            s["share_target"] = target
            self.save()
        for index in range(len(s["users"]), a.members):
            name = f"{a.prefix}-u{index + 1:04d}"
            user = self.ensure_user(name, member_password(a.member_seed, a.prefix, index))
            s["users"].append({"id": user["id"], "name": name})
            self.save()
        for index, user in enumerate(s["users"]):
            if user.get("member"):
                continue
            revision = self.api.ok("GET", f"/governance/groups/{root['id']}")["revision"]
            self.api.ok("PUT", f"/governance/groups/{root['id']}/members/{user['id']}",
                        {"role": 20, "revision": revision})
            user["member"] = True
            self.save()
        for index in range(len(s["repos"]), a.repos):
            group = s["groups"][index % a.depth]
            repo = self.ensure_repo(group, f"{a.prefix}-r{index + 1:04d}")
            s["repos"].append({"id": repo["id"], "full_name": repo["full_name"]})
            self.save()
        for index in range(len(s["shares"]), a.shares):
            repo, target = s["repos"][index], s["share_target"]
            current = self.api.ok("GET", f"/governance/repositories/{repo['id']}/shares")
            if not any(row["group_id"] == target["id"] for row in current["shares"]):
                self.api.ok("PUT", f"/governance/repositories/{repo['id']}/shares/{target['id']}",
                            {"max_role": 20, "revision": current["revision"]})
            s["shares"].append(repo["id"])
            self.save()
        for index in range(len(s["issues"]), a.issues):
            repo = s["repos"][index]
            title = f"{a.prefix} 固定事项 {index + 1:04d}"
            rows = self.api.ok("GET", f"/repos/{repo['full_name']}/issues?state=all&limit=50")
            issue = next((row for row in rows if row["title"] == title), None)
            if issue is None:
                issue = self.api.ok("POST", f"/repos/{repo['full_name']}/issues", {
                    "title": title, "body": "复现场景：成员授权、共享项目、审批追踪。\n\n验收：记录责任人、变更与结果。\n" * 8,
                })
            s["issues"].append({"repo": repo["full_name"], "number": issue["number"]})
            self.save()
        for index in range(len(s["pulls"]), a.pulls):
            repo = s["repos"][index]
            branch = f"{a.prefix}-pr-{index + 1:04d}"
            status, _, _, _ = self.api.call("GET", f"/repos/{repo['full_name']}/branches/{branch}")
            if status == 404:
                self.api.ok("POST", f"/repos/{repo['full_name']}/branches",
                            {"new_branch_name": branch, "old_branch_name": "main"})
            elif status != 200:
                raise RuntimeError(f"查询 PR 分支失败：HTTP {status}")
            self.ensure_file(repo, f"review-{index + 1:04d}.txt",
                             "一期容量 PR 内容：变更说明、审查理由与回归结果。\n" * 8, branch)
            pulls = self.api.ok("GET", f"/repos/{repo['full_name']}/pulls?state=all&limit=50")
            title = f"{a.prefix} 固定 PR {index + 1:04d}"
            pull = next((row for row in pulls if row["title"] == title), None)
            if pull is None:
                pull = self.api.ok("POST", f"/repos/{repo['full_name']}/pulls", {
                    "title": title, "body": "固定审查样本：代码差异、讨论与合并门禁。",
                    "head": branch, "base": "main",
                })
            s["pulls"].append({"repo": repo["full_name"], "number": pull["number"]})
            self.save()
        workflow = "name: 一期容量任务\non:\n  push:\njobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo phase1-capacity\n"
        for index in range(len(s["tasks"]), a.tasks):
            repo = s["repos"][index]
            self.ensure_file(repo, ".gitea/workflows/phase1-capacity.yaml", workflow)
            self.ensure_file(repo, f"task-{index + 1:04d}.txt", "一期容量任务触发。\n")
            branch = self.api.ok("GET", f"/repos/{repo['full_name']}/branches/main")
            commit_sha = branch["commit"]["id"]
            selected = None
            for _ in range(40):
                runs = self.api.ok("GET", f"/repos/{repo['full_name']}/actions/runs?limit=20")
                matches = [run for run in runs["workflow_runs"]
                           if run.get("path", "").startswith("phase1-capacity.yaml@")
                           and run.get("head_sha") == commit_sha]
                if matches:
                    jobs = self.api.ok("GET", f"/repos/{repo['full_name']}/actions/runs/{matches[0]['id']}/jobs")
                    if jobs.get("jobs"):
                        selected = matches[0]
                        break
                time.sleep(0.25)
            if selected is None:
                raise RuntimeError(f"触发提交在十秒内未生成 Actions Run 与 Job：{repo['full_name']}")
            s["tasks"].append({"repo": repo["full_name"], "run_id": selected["id"], "commit_sha": commit_sha})
            self.save()
        print("固定样本已建立：" + json.dumps({key: len(s[key]) for key in
              ("groups", "users", "repos", "shares", "issues", "pulls", "tasks")}, ensure_ascii=False))

    def prepare_runners(self):
        if len(self.state["repos"]) < self.args.batch_size:
            raise ValueError("现有仓库少于本轮 Runner 任务数；先建立固定样本")
        for repo in self.state["repos"][:self.args.batch_size]:
            self.ensure_file(repo, ".gitea/workflows/phase1-runner-batch.yaml", self.runner_workflow)
        print("Runner 批次工作流已准备；等待由本次提交触发的其他任务结束，再记录空闲就绪证据")

    def trigger_runners(self):
        a, s = self.args, self.state
        if len(s["repos"]) < a.batch_size:
            raise ValueError("现有仓库少于本轮 Runner 任务数；先建立固定样本")
        batches = s.setdefault("runner_batches", {})
        readiness = Path(a.runner_readiness)
        repos = s["repos"][:a.batch_size]
        for repo in repos:
            entry = self.api.ok("GET", f"/repos/{repo['full_name']}/contents/.gitea/workflows/phase1-runner-batch.yaml?ref=main")
            if base64.b64decode(entry.get("content", "")).decode() != self.runner_workflow:
                raise ValueError("批次工作流不匹配；先执行 prepare-runners，再重新记录 Runner 空闲状态")
        if a.batch_id not in batches:
            batches[a.batch_id] = {"就绪证据SHA256": file_sha256(readiness),
                                   "就绪证据修改时间": dt.datetime.fromtimestamp(readiness.stat().st_mtime, dt.timezone.utc).isoformat(),
                                   "触发开始": utc_now(), "tasks": []}
            self.save()
        else:
            raise ValueError("批次编号已使用；先保留并核对原 Run，再以新编号开始新批次")
        batch = batches[a.batch_id]
        def dispatch(repo):
            name = repo["full_name"]
            details = self.api.ok("POST", f"/repos/{name}/actions/workflows/phase1-runner-batch.yaml/dispatches?return_run_details=true",
                                  {"ref": "refs/heads/main"})
            run_id = details["workflow_run_id"]
            selected = None
            for _ in range(40):
                jobs = self.api.ok("GET", f"/repos/{name}/actions/runs/{run_id}/jobs")
                if jobs.get("jobs"):
                    selected = jobs["jobs"][0]
                    break
                time.sleep(0.25)
            if selected is None:
                raise RuntimeError(f"独立批次在十秒内未生成真实 Job：{name} Run {run_id}")
            return {"repo": name, "run_id": run_id, "job_id": selected["id"]}
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(repos)) as pool:
            futures = [pool.submit(dispatch, repo) for repo in repos]
            for future in concurrent.futures.as_completed(futures):
                batch["tasks"].append(future.result())
                self.save()
            self.save()
        print("Runner 新批次真实 Run／Job 已创建：" + json.dumps({"批次": a.batch_id,
              "任务数": len(batch["tasks"])}, ensure_ascii=False))


def measure(args, api, state):
    if any(len(state[key]) != state["config"][expected] for key, expected in
           (("groups", "depth"), ("users", "members"), ("repos", "repos"),
            ("shares", "shares"), ("issues", "issues"), ("pulls", "pulls"), ("tasks", "tasks"))):
        raise ValueError("固定样本尚未完整建立，不能采集容量指标")
    if len(state["users"]) < args.concurrency:
        raise ValueError("成员样本不足以提供指定数量的不同并发身份")
    first = state["users"][0]
    first_actor = (first["name"], member_password(args.member_seed, state["config"]["prefix"], 0))
    visible = []
    for page in range(1, math.ceil(len(state["repos"]) / 50) + 2):
        entries = api.ok("GET", f"/user/repos?limit=50&page={page}", actor=first_actor)
        visible.extend(entry["id"] for entry in entries)
        if len(entries) < 50:
            break
    expected = {repo["id"] for repo in state["repos"]}
    if set(visible) != expected or len(visible) != len(expected):
        raise ValueError("首名样本成员可见仓库集合与固定夹具不一致；不能采集容量指标")
    candidate_hash = file_sha256(args.candidate_binary) if args.candidate_binary else args.candidate_sha256
    if args.candidate_binary and args.candidate_sha256 != "未提供" and candidate_hash != args.candidate_sha256:
        raise ValueError("候选二进制实算哈希与声明哈希不一致")
    output = Path(args.output)
    if output.exists() or output.with_suffix(".summary.json").exists():
        raise FileExistsError("原始采集文件已存在；每轮必须使用独立文件名")
    root, deep = state["groups"][0], state["groups"][-1]
    deep_repo_index = len(state["groups"]) - 1
    if len(state["repos"]) <= deep_repo_index:
        raise ValueError("最深群组没有固定仓库，不能采集深层容量指标")
    repo = state["repos"][deep_repo_index]
    paths = {
        "用户仓库列表首屏": "/user/repos?limit=50&page=1",
        "深层群组仓库列表": f"/governance/navigation/groups/{deep['id']}?limit=50",
        "深层仓库资料": f"/repos/{repo['full_name']}",
    }
    if state["issues"]:
        issue = state["issues"][min(len(state["issues"]), len(state["groups"])) - 1]
        paths["代表性Issue列表"] = f"/repos/{issue['repo']}/issues?state=all&limit=50"
    if state["pulls"]:
        pull = state["pulls"][min(len(state["pulls"]), len(state["groups"])) - 1]
        paths["代表性PR列表"] = f"/repos/{pull['repo']}/pulls?state=all&limit=50"
    navigation = api.ok("GET", paths["深层群组仓库列表"], actor=first_actor)
    if not any(item.get("type") == "repository" and item.get("id") == repo["id"]
               for item in navigation.get("items", [])):
        raise ValueError("深层群组列表没有固定的代表性仓库，不能采集容量指标")
    if state["issues"] and not any(row.get("number") == issue["number"]
                                    for row in api.ok("GET", paths["代表性Issue列表"], actor=first_actor)):
        raise ValueError("代表性 Issue 未出现在实际列表中")
    if state["pulls"] and not any(row.get("number") == pull["number"]
                                   for row in api.ok("GET", paths["代表性PR列表"], actor=first_actor)):
        raise ValueError("代表性 PR 未出现在实际列表中")
    config_path = f"/governance/approval-settings/group/{root['id']}"
    config = api.ok("GET", config_path)
    if not isinstance(config, dict) or not isinstance(config.get("effective"), dict):
        raise ValueError("管理身份读取审批有效配置未返回 effective，不能采集容量指标")
    barrier = threading.Barrier(args.concurrency)
    rows = []
    lock = threading.Lock()

    def worker(index):
        user = state["users"][index]
        actor = (user["name"], member_password(args.member_seed, state["config"]["prefix"], index))
        barrier.wait(timeout=30)
        local = []
        for repeat in range(args.repeats):
            for name, path in {**paths, "审批有效配置（管理员）": config_path}.items():
                started = utc_now()
                try:
                    selected_actor = api.admin if name == "审批有效配置（管理员）" else actor
                    status, _, size, duration = api.call("GET", path, actor=selected_actor)
                    error = "" if status == 200 else f"HTTP {status}"
                except Exception as exc:
                    status, size, duration, error = 0, 0, 0, type(exc).__name__
                local.append({"时间": started, "身份序号": index + 1, "轮次": repeat + 1,
                              "入口": name, "身份类型": "同一管理账号" if name == "审批有效配置（管理员）" else "不同成员账号",
                              "HTTP": status, "字节": size, "耗时毫秒": round(duration, 3), "错误": error})
        with lock:
            rows.extend(local)

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as pool:
        list(pool.map(worker, range(args.concurrency)))
    for row in rows:
        append_jsonl(output, row)
    summary = {"采集时间": utc_now(), "候选SHA256": candidate_hash,
               "并发身份": args.concurrency, "每身份轮次": args.repeats,
               "数据量": {key: len(state[key]) for key in
                         ("groups", "users", "repos", "shares", "issues", "pulls", "tasks")},
               "首名成员实际可见仓库数": len(visible),
               "入口": {}, "首个可操作页面": "待浏览器单独采集", "10Runner": "待独立采集",
               "采集边界": "审批有效配置使用同一管理账号；其他入口使用不同成员账号；零数量的 Issue／PR 不采集对应入口"}
    for name in {**paths, "审批有效配置（管理员）": config_path}:
        samples = [row for row in rows if row["入口"] == name]
        good = [row["耗时毫秒"] for row in samples if row["HTTP"] == 200]
        percentile = p95(good) if good else None
        summary["入口"][name] = {"请求数": len(samples), "成功数": len(good),
                                "失败数": len(samples) - len(good),
                                "成功请求P95毫秒": percentile,
                                "P95阈值毫秒": args.p95_ms,
                                "P95达标": percentile is not None and percentile <= args.p95_ms}
    write_json(output.with_suffix(".summary.json"), summary)
    print(json.dumps(summary, ensure_ascii=False))
    if any(item["失败数"] or not item["P95达标"] for item in summary["入口"].values()):
        raise RuntimeError("请求失败或 P95 超出阈值，不能判定容量通过")


def observe_runners(args, api, state):
    batch = state.get("runner_batches", {}).get(args.batch_id)
    if not batch or len(batch["tasks"]) != args.batch_size:
        raise ValueError("先在十个 Runner 就绪后用 trigger-runners 创建完整的新批次")
    if len(batch["tasks"]) < args.expected_runners:
        raise ValueError("新批次任务少于预期 Runner 数，无法覆盖全部 Runner")
    if batch["就绪证据SHA256"] != file_sha256(args.runner_readiness):
        raise ValueError("观察使用的 Runner 就绪证据与触发时不同")
    if dt.datetime.fromisoformat(batch["就绪证据修改时间"]) > dt.datetime.fromisoformat(batch["触发开始"]):
        raise ValueError("Runner 就绪证据晚于批次触发，不能判定发现延迟")
    output = Path(args.output)
    if output.exists() or output.with_suffix(".summary.json").exists():
        raise FileExistsError("Runner 采集文件已存在；每轮必须使用独立文件名")
    deadline = time.monotonic() + args.timeout
    jobs = {}
    assignments = {}
    while time.monotonic() < deadline:
        for task in batch["tasks"]:
            path = f"/repos/{task['repo']}/actions/runs/{task['run_id']}/jobs?limit=100"
            value = api.ok("GET", path)
            for job in value.get("jobs", []):
                key = f"{task['repo']}:{task['run_id']}:{job['id']}"
                row = {"时间": utc_now(), "任务键": key, "RunnerID": job.get("runner_id", 0),
                       "状态": job.get("status"), "创建": job.get("created_at"),
                       "开始": job.get("started_at")}
                append_jsonl(output, row)
                jobs[key] = row
                if row["RunnerID"]:
                    assignments.setdefault(key, set()).add(row["RunnerID"])
        if len(jobs) >= len(batch["tasks"]) and all(row["RunnerID"] and row["开始"]
                                                      and not row["开始"].startswith("1970-") for row in jobs.values()):
            break
        time.sleep(args.sample_interval)
    delays = []
    for row in jobs.values():
        if row["RunnerID"] and row["开始"] and not row["开始"].startswith("1970-") and row["创建"]:
            created = dt.datetime.fromisoformat(row["创建"].replace("Z", "+00:00"))
            started = dt.datetime.fromisoformat(row["开始"].replace("Z", "+00:00"))
            delays.append((started - created).total_seconds())
    runners = sorted({row["RunnerID"] for row in jobs.values() if row["RunnerID"]})
    summary = {"采集时间": utc_now(), "批次": args.batch_id, "批次触发开始": batch["触发开始"],
               "任务数": len(jobs), "已开始任务数": len(delays),
               "实际领取RunnerID": runners, "预期Runner数": args.expected_runners,
               "多个Runner曾分配同一Job": sorted(key for key, ids in assignments.items() if len(ids) > 1),
               "资格与空闲前置证据": args.runner_readiness or "未提供，不能判定两轮询周期目标",
               "就绪证据SHA256": file_sha256(args.runner_readiness) if args.runner_readiness else None,
               "配置轮询周期秒": args.poll_seconds, "两周期阈值秒": 2 * args.poll_seconds,
               "创建至开始P95秒": p95(delays) if delays else None,
               "创建至开始最长秒": max(delays) if delays else None,
               "全部两周期内": len(delays) == len(jobs) == len(batch["tasks"]) and all(x <= 2 * args.poll_seconds for x in delays),
               "计时边界": "仅对就绪后新触发的同批真实任务计时；创建至开始含调度开销，若期间有排队或其他任务抢占须结合服务端日志剔除，不能冒称纯发现延迟",
               "10Runner覆盖": len(runners) >= args.expected_runners}
    write_json(output.with_suffix(".summary.json"), summary)
    print(json.dumps(summary, ensure_ascii=False))
    if (not summary["全部两周期内"] or not summary["10Runner覆盖"] or
            summary["多个Runner曾分配同一Job"] or not args.runner_readiness):
        raise RuntimeError("Runner 固定断言未通过；保留原始观察记录")


def main():
    parser = argparse.ArgumentParser(description="一期隔离容量夹具与原始指标采集")
    parser.add_argument("command", choices=("self-test", "seed", "measure", "prepare-runners", "trigger-runners", "observe-runners"))
    parser.add_argument("--base-url")
    parser.add_argument("--state")
    parser.add_argument("--prefix")
    parser.add_argument("--members", type=int, default=1000)
    parser.add_argument("--repos", type=int, default=1000)
    parser.add_argument("--depth", type=int, default=20)
    parser.add_argument("--shares", type=int, default=100)
    parser.add_argument("--issues", type=int, default=100)
    parser.add_argument("--pulls", type=int, default=50)
    parser.add_argument("--tasks", type=int, default=50)
    parser.add_argument("--concurrency", type=int, default=50)
    parser.add_argument("--repeats", type=int, default=5)
    parser.add_argument("--p95-ms", type=float, default=1000)
    parser.add_argument("--expected-runners", type=int, default=10)
    parser.add_argument("--batch-id", help="就绪后新触发的 Runner 批次编号")
    parser.add_argument("--batch-size", type=int, default=10)
    parser.add_argument("--runner-readiness", help="10 Runner 已注册、空闲、标签及轮询周期的独立证据路径")
    parser.add_argument("--poll-seconds", type=float, default=2)
    parser.add_argument("--sample-interval", type=float, default=0.5)
    parser.add_argument("--timeout", type=float, default=120)
    parser.add_argument("--candidate-sha256", default="未提供")
    parser.add_argument("--candidate-binary", help="采集时实际使用的 Gitea 二进制；自动计算 SHA-256")
    parser.add_argument("--output")
    args = parser.parse_args()
    if args.command == "self-test":
        assert p95([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]) == 10
        assert member_password("固定自测", "p1", 0) == member_password("固定自测", "p1", 0)
        assert member_password("固定自测", "p1", 0) != member_password("固定自测", "p1", 1)
        print("纯函数自测通过；未连接实例，也未验证真实容量")
        return
    if not args.base_url or not args.state or not args.prefix:
        parser.error("实例地址、私有状态路径和唯一前缀均为必填")
    if not re.fullmatch(r"[a-z][a-z0-9-]{2,16}", args.prefix):
        parser.error("前缀须为 3—17 位英文小写字母、数字或连字符")
    if not args.base_url.startswith(("http://", "https://")):
        parser.error("实例地址必须包含 HTTP 协议")
    if not all((1 <= args.depth <= 20, args.members > 0, args.repos > 0,
                0 <= args.shares <= args.repos, 0 <= args.issues <= args.repos,
                0 <= args.pulls <= args.repos, 0 <= args.tasks <= args.repos,
                args.p95_ms > 0, args.poll_seconds > 0, args.sample_interval > 0,
                args.batch_size > 0)):
        parser.error("固定规模参数超出支持边界")
    if args.command in ("prepare-runners", "trigger-runners", "observe-runners") and args.batch_size > args.repos:
        parser.error("Runner 批次任务数不能大于固定样本仓库数")
    if args.command in ("trigger-runners", "observe-runners") and args.batch_size != args.expected_runners:
        parser.error("两周期发现批次须每个就绪 Runner 对应一个任务，避免人为排队")
    if args.command in ("trigger-runners", "observe-runners") and (
            not args.batch_id or not re.fullmatch(r"[a-z][a-z0-9-]{2,20}", args.batch_id) or
            not args.runner_readiness):
        parser.error("Runner 批次必须提供英文批次编号和预先保存的就绪证据")
    if args.runner_readiness and not Path(args.runner_readiness).is_file():
        parser.error("Runner 就绪证据文件不存在")
    admin_user = os.environ.get("PHASE1_ADMIN_USER", "")
    admin_password = os.environ.get("PHASE1_ADMIN_PASSWORD", "")
    member_seed = os.environ.get("PHASE1_MEMBER_SEED", "")
    if not admin_user or not admin_password or not member_seed:
        parser.error("需通过环境变量传入专用管理员账号、密码和成员密码种子")
    args.member_seed = member_seed
    api = API(args.base_url, admin_user, admin_password)
    fixture = Fixture(args, api)
    if args.command == "seed":
        fixture.seed()
    elif args.command == "prepare-runners":
        fixture.prepare_runners()
    elif args.command == "trigger-runners":
        fixture.trigger_runners()
    else:
        if not args.output:
            parser.error("采集命令必须指定新输出路径")
        if args.command == "measure":
            measure(args, api, fixture.state)
        else:
            observe_runners(args, api, fixture.state)


if __name__ == "__main__":
    main()
