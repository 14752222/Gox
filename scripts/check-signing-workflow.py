#!/usr/bin/env python3
"""桌面签名「跳过逻辑」静态闸门（看板 r8ErgP）。

背景：release.yml 里现在有三处签名（publish 里的 Windows Authenticode、sign-macos
作业给 npm 的 darwin 二进制、universal 作业给 Release Assets），每一处都依赖 secrets。
它们共同守一条约定 —— **缺凭证必须优雅跳过，发布不得被阻塞**。这条约定写在注释里
没人会违反，但它会在一次普通的「顺手改一下」里被悄悄改坏：比如给某个步骤加一个
`env: SECRET: ${{ secrets.X }}` 而忘了 `if:` 闸门，于是缺凭证的那次跑就会拿到空串、
签名失败、把发版判红。更糟的是这只在**没配凭证的仓库**上才复现，配齐了凭证的
维护者本地怎么跑都是绿的。

所以这个脚本把那条约定变成可静态校验的判据（不需要真跑 workflow，也不需要凭证）：

  1. 凡引用 `secrets.*`（GITHUB_TOKEN 除外）的步骤，必须有
     `if: steps.<probe>.outputs.enabled == 'true'` 这样的闸门；
  2. 那个 `<probe>` 步骤必须真的存在于同一作业里，且调的是
     scripts/check-signing-creds.sh（判据单一真源）；
  3. 探测步骤本身只允许**判空**（`secrets.X != ''`），不许把凭证值取出来；
  4. 凭证只出现在步骤级 `env:` 里，不许挂到作业级 `env:`（那等于发给所有步骤）；
  5. publish 里消费 sign-macos 产物的步骤必须挂在
     `needs.sign-macos.outputs.signed == 'true'` 上 —— 不能用
     `needs.sign-macos.result == 'success'`：缺凭证时那个作业照样 success（只是内部
     步骤全跳过、不传 artifact），拿 result 判会去下载一个不存在的 artifact、
     把发布搞挂 —— 这正是「跳过逻辑」最容易改坏的一种形态；
  6. workflow 里引用的 scripts/*.sh 必须真实存在（改名了要连这里一起改）。

退出码：有问题 → 1（CI 判红）；全通过 → 0。

用法：
    python3 scripts/check-signing-workflow.py
    python3 scripts/check-signing-workflow.py --github     # 失败写成 ::error:: 注解
    python3 scripts/check-signing-workflow.py --workflow .github/workflows/release.yml
"""
from __future__ import annotations

import argparse
import os
import re
import sys

try:
    import yaml
except ImportError:  # CI 的 ubuntu 镜像自带 pyyaml；本地没有就直说，别装作通过
    sys.exit("::error::需要 pyyaml：pip install pyyaml（或 apt install python3-yaml）")

DEFAULT_WORKFLOW = os.path.join(".github", "workflows", "release.yml")
PROBE_SCRIPT = "check-signing-creds.sh"
# GITHUB_TOKEN 是 GitHub 自动注入的，不是「签名凭证」，不参与闸门判据
EXEMPT_SECRETS = {"GITHUB_TOKEN"}
SECRET_REF = re.compile(r"secrets\.([A-Za-z_][A-Za-z0-9_]*)")
SECRET_CMP = re.compile(r"secrets\.([A-Za-z_][A-Za-z0-9_]*)\s*!=")
GATE = re.compile(r"steps\.([A-Za-z0-9_-]+)\.outputs\.enabled\s*==\s*'true'")


def dump(step: object) -> str:
    """把步骤序列化回文本再搜：省得逐个字段分支处理 env / run / with。"""
    return yaml.safe_dump(step, allow_unicode=True, default_flow_style=False)


def check(root: str, workflow_rel: str) -> list[str]:
    errs: list[str] = []
    wf_path = os.path.join(root, workflow_rel)
    try:
        with open(wf_path, encoding="utf-8") as f:
            wf = yaml.safe_load(f)
    except OSError as e:
        return ["读不到 %s：%s" % (workflow_rel, e)]
    except yaml.YAMLError as e:
        return ["%s 不是合法 YAML：%s" % (workflow_rel, e)]

    jobs = wf.get("jobs") or {}
    for want in ("publish", "sign-macos", "universal"):
        if want not in jobs:
            errs.append("%s 里没有 %s 作业 —— 签名链路缺了一环" % (workflow_rel, want))
    if errs:
        return errs

    # 5. publish 必须等 sign-macos（签好的 darwin 产物要在 npm pack 之前到位）
    needs = jobs["publish"].get("needs") or []
    if "sign-macos" not in needs:
        errs.append("publish.needs 里没有 sign-macos（%s）—— 签名后的 darwin 二进制赶不上 npm pack" % needs)
    # 5b. 那个作业必须把探测结论暴露成 outputs.signed：丢了它，publish 的闸门永远
    #     为假 —— 不报错、不判红，只是**永远发未签名包**，是最难发现的一种坏法。
    outs = jobs["sign-macos"].get("outputs") or {}
    signed_expr = str(outs.get("signed", ""))
    if "outputs.enabled" not in signed_expr:
        errs.append("sign-macos 缺少 outputs.signed = ${{ steps.<probe>.outputs.enabled }}（当前：%r）—— publish 会永远走未签名路径且不报错"
                    % (signed_expr or "<无>"))

    # 4. 作业级 env 里出现凭证 = 全作业可见，比步骤级难审计
    for jname, job in jobs.items():
        job_env = dump(job.get("env") or {})
        for name in set(SECRET_REF.findall(job_env)) - EXEMPT_SECRETS:
            errs.append("作业 %s 在**作业级** env 里引用了 secrets.%s；凭证只能挂在步骤级 env 上" % (jname, name))

    for jname, job in jobs.items():
        steps = job.get("steps") or []
        probe_ids = {
            s.get("id")
            for s in steps
            if s.get("id") and PROBE_SCRIPT in dump(s)
        }
        for step in steps:
            text = dump(step)
            name = step.get("name") or step.get("uses") or step.get("id") or "<无名步骤>"
            refs = set(SECRET_REF.findall(text)) - EXEMPT_SECRETS

            if PROBE_SCRIPT in text:
                # 3. 探测步骤只许判空，不许把值取出来
                cmps = set(SECRET_CMP.findall(text))
                leaked = refs - cmps
                if leaked:
                    errs.append("作业 %s 的探测步骤「%s」把凭证值取出来了（%s）；只允许判空：secrets.X != ''"
                                % (jname, name, ", ".join(sorted(leaked))))
                continue
            if not refs:
                continue

            cond = step.get("if") or ""
            gate = GATE.search(cond) if cond else None
            if not gate:
                errs.append("作业 %s 的步骤「%s」用了 %s，却没有 `if: steps.<probe>.outputs.enabled == 'true'` 闸门 —— 缺凭证的那次跑会因此失败"
                            % (jname, name, ", ".join("secrets.%s" % r for r in sorted(refs))))
                continue
            if gate.group(1) not in probe_ids:
                errs.append("作业 %s 的步骤「%s」闸门指向 steps.%s，但该作业里没有这个探测步骤（现有：%s）"
                            % (jname, name, gate.group(1), ", ".join(sorted(str(p) for p in probe_ids)) or "<无>"))

        # 5. 消费 sign-macos 产物的步骤必须挂在 outputs.signed 上（见文件头第 5 条）
        if jname == "publish":
            for step in steps:
                text = dump(step)
                cond = step.get("if") or ""
                if "gox-darwin-signed" in text and "needs.sign-macos.outputs.signed" not in cond:
                    errs.append("publish 的步骤「%s」用了 gox-darwin-signed 产物，却没挂在 `needs.sign-macos.outputs.signed == 'true'` 上（缺凭证时该作业无 artifact，会下载失败）"
                                % (step.get("name") or step.get("uses")))

    # 6. workflow 里点到的脚本必须存在：改名/删文件时这条会先响
    for jname, job in jobs.items():
        for step in job.get("steps") or []:
            for m in re.findall(r"scripts/([A-Za-z0-9_.-]+\.sh)", dump(step)):
                if not os.path.isfile(os.path.join(root, "scripts", m)):
                    errs.append("作业 %s 引用了不存在的 scripts/%s" % (jname, m))

    return errs


def main() -> int:
    ap = argparse.ArgumentParser(description="校验 release.yml 的桌面签名跳过逻辑")
    ap.add_argument("--root", default=".", help="仓库根（默认当前目录）")
    ap.add_argument("--workflow", default=DEFAULT_WORKFLOW, help="待校验的 workflow（相对仓库根）")
    ap.add_argument("--github", action="store_true", help="把失败写成 ::error:: 注解")
    args = ap.parse_args()

    errs = check(args.root, args.workflow)
    if errs:
        for e in errs:
            print(("::error::" if args.github else "[fail] ") + e)
        print("桌面签名跳过逻辑校验未通过（%d 条）—— 见 docs/desktop-distribution.md §签名与公证自动化" % len(errs))
        return 1
    print("[ok] %s 的桌面签名跳过逻辑完好：凭证只在带闸门的步骤里出现，缺凭证即跳过" % args.workflow)
    return 0


if __name__ == "__main__":
    sys.exit(main())
