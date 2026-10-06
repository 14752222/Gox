#!/usr/bin/env python3
"""从 packaging/npm-mobile/<dirname>/metadata.json 生成子包的 package.json + README.md。

单一真源 = **主仓** `packaging/npm-mobile/<dirname>/metadata.json`（可版本管理、不走
gox-npm 子模块）。本脚本把它展开成 npm 真正需要的两个 committed 文件：

    npm/packages/<dirname>/package.json   ← name/version/files/… （身份，npm publish 认这个）
    npm/packages/<dirname>/README.md      ← 包页面文案（npm 自动收录）

为什么要有这一层（而不是让 build-npm-mobile.sh 直接读 metadata.json）：
    npm publish 读的是**包目录里的 package.json**，它是 npm 的硬契约，不能省。
    所以「定义」与「npm 定义的落点」之间必须有一步生成。这一步就是本脚本。
    `scripts/build-npm-mobile.sh` 只**消费**生成结果（npm/packages/<dirname>/package.json），
    不自己拼字段 —— 它拿到的就是 npm 真正会发布的那份身份。

`version` 取主仓 npm/package.json 的版本（子包与主包同号是 M11 的硬约定，
check-registries.py 检查5 会把不一致判红）。

用法：
    python3 scripts/gen-npm-mobile-pkgs.py            # 生成/刷新到 npm/packages/
    python3 scripts/gen-npm-mobile-pkgs.py --check    # 只校验是否已同步（CI 用，不改文件）

退出码非 0 表示失败。
"""
from __future__ import annotations

import json
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFS = os.path.join(ROOT, "packaging", "npm-mobile")
OUT_ROOT = os.path.join(ROOT, "npm", "packages")


def read_version():
    """子包版本 = 主包版本（同号约定）。"""
    with open(os.path.join(ROOT, "npm", "package.json"), encoding="utf-8") as f:
        return json.load(f)["version"]


def load_defs():
    """返回 [(metadata_dict, source_rel)]，按 dirname 排序。"""
    if not os.path.isdir(DEFS):
        raise SystemExit("error: 定义目录不存在：%s" % os.path.relpath(DEFS, ROOT))
    out = []
    for name in sorted(os.listdir(DEFS)):
        d = os.path.join(DEFS, name)
        if not os.path.isdir(d):
            continue
        mf = os.path.join(d, "metadata.json")
        if not os.path.isfile(mf):
            raise SystemExit("error: %s 缺 metadata.json" % os.path.relpath(d, ROOT))
        with open(mf, encoding="utf-8") as f:
            meta = json.load(f)
        out.append((meta, os.path.relpath(mf, ROOT).replace("\\", "/")))
    if not out:
        raise SystemExit("error: %s 下没有任何子包定义" % os.path.relpath(DEFS, ROOT))
    return out


def render_package_json(meta, version):
    """由 metadata.json 渲染子包 package.json。

    字段刻意与既有 npm/packages/*/package.json 逐字一致（同一 key 顺序、同一缩进），
    这样「生成物」与「手写物」可逐字节比对 —— 迁移期不产生噪音 diff。
    """
    pkg = {
        "name": meta["name"],
        "version": version,
        "description": meta["description"],
        "license": "Apache-2.0",
        "repository": {
            "type": "git",
            "url": "git+https://github.com/14752222/Gox.git",
        },
        # files = 该平台的产物 + manifest.json（白名单，决定包里进什么）
        "files": sorted(meta["artifacts"]) + ["manifest.json"],
        "publishConfig": {"access": "public"},
        "keywords": meta["keywords"],
    }
    return json.dumps(pkg, ensure_ascii=False, indent=2) + "\n"


def render_readme(meta, version):
    arts = "\n".join(meta["readmeArtifacts"])
    return f"""# {meta["readmeTitle"]}

{meta["readmeIntro"]}
本包是 [`@goxjs/goxjs`](https://www.npmjs.com/package/@goxjs/goxjs) 的**平台子包（M11）**——
主包只发桌面二进制，移动端按「平台 × ABI」拆成独立子包，避免把几十 MB 的 `.so` 塞给每个桌面用户。

## 里面是什么

| 文件 | 说明 |
|---|---|
{arts}

## 怎么用

{meta["readmeHost"]}不直接 `require` 本包，而是用取库脚本把它落进
{meta["readmeDest"]}：

```bash
npm i {meta["name"]}
bash scripts/fetch-mobile-libs.sh     # 校验 goxVersion + sha256 后拷贝
```

- 版本必须与引擎一致（壳工程 ↔ 引擎错配是运行时才崩，编译期拦不住）。
- 完整分发策略见 Gox 仓库的 `app/MOBILE-DISTRIBUTION.md`。
"""


def main():
    check = "--check" in sys.argv
    version = read_version()
    defs = load_defs()

    problems = []
    written = []
    for meta, src in defs:
        dirname = meta["dirname"]
        out_dir = os.path.join(OUT_ROOT, dirname)
        want_pj = render_package_json(meta, version)
        want_rd = render_readme(meta, version)

        for rel, want in (("package.json", want_pj), ("README.md", want_rd)):
            path = os.path.join(out_dir, rel)
            if check:
                if not os.path.isfile(path):
                    problems.append("npm/packages/%s/%s 不存在（跑 gen-npm-mobile-pkgs.py 生成）"
                                    % (dirname, rel))
                    continue
                with open(path, encoding="utf-8") as f:
                    got = f.read()
                # README 允许包维护者手改；只有 package.json 是硬生成物，必须逐字一致。
                if rel == "package.json" and got != want:
                    problems.append(
                        "npm/packages/%s/package.json 与 %s 不同步（跑 gen-npm-mobile-pkgs.py 刷新）"
                        % (dirname, src))
            else:
                os.makedirs(out_dir, exist_ok=True)
                with open(path, "w", encoding="utf-8", newline="\n") as f:
                    f.write(want)
                written.append("npm/packages/%s/%s" % (dirname, rel))

    if check:
        if problems:
            print("FAIL 移动端子包定义同步")
            for p in problems:
                print("  - " + p)
            return 1
        print("ok   %d 个移动端子包定义与 packaging/npm-mobile/ 同步" % len(defs))
        return 0

    for w in written:
        print("gen  " + w)
    print("共 %d 个子包，版本 %s" % (len(defs), version))
    return 0


if __name__ == "__main__":
    sys.exit(main())
