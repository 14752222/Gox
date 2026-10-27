# Gox

> A JavaScript runtime written from scratch in Go: lexing → parsing → bytecode compilation →
> stack-based VM execution. Single binary, zero cgo, zero external runtime dependencies —
> with a built-in software-rasterised GUI and a script packager.

[![Go](https://img.shields.io/badge/Go-1.26.2%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![npm](https://img.shields.io/npm/v/@goxjs/goxjs)](https://www.npmjs.com/package/@goxjs/goxjs)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#requirements)
[![Release](https://github.com/14752222/Gox/actions/workflows/release.yml/badge.svg)](https://github.com/14752222/Gox/actions/workflows/release.yml)
[![test262 language](https://img.shields.io/endpoint?url=https%3A%2F%2Fraw.githubusercontent.com%2F14752222%2FGox%2Fmain%2Fdocs%2Ftest262-compliance.json)](docs/test262-t04-report.md)
[![test262 built-ins](https://img.shields.io/endpoint?url=https%3A%2F%2Fraw.githubusercontent.com%2F14752222%2FGox%2Fmain%2Fdocs%2Ftest262-compliance-builtins.json)](docs/test262-builtins-clusters.md)

📖 **[Website & tutorials](https://14752222.github.io/Gox/)** ｜ **[GUI guide](docs/gui-guide.md)** ｜ **[npm package](https://www.npmjs.com/package/@goxjs/goxjs)**

> 中文版：[README.md](README.md)。Both READMEs are maintained in sync and describe the same build.

---

## Contents

- [Overview](#overview)
- [Project status](#project-status)
- [Quick start](#quick-start)
- [Hero example: adaptive one/two-column list](#hero-example-adaptive-onetwo-column-list)
- [Capability map](#capability-map)
- [Language and built-ins](#language-and-built-ins)
- [GUI desktop apps](#gui-desktop-apps)
- [Host capability modules](#host-capability-modules)
- [Packaging and toolchain](#packaging-and-toolchain)
- [Architecture](#architecture)
- [Performance](#performance)
- [Documentation map](#documentation-map)
- [Development and testing](#development-and-testing)
- [Release process](#release-process)
- [Contributing](#contributing)
- [License](#license)

---

## Overview

Gox is a JavaScript (ES6+ subset) runtime implemented in **pure Go**. The entire compile
pipeline — lexing, parsing, bytecode generation and the stack-based VM — is built from scratch.
It does not depend on V8, QuickJS or any other existing engine, and it does not use cgo.

On top of that, Gox adds the layer scripting languages usually lack: a **self-written desktop GUI
rendering layer** (pure-Go software rasterisation, flex layout + JSX + signal-driven updates, with
win32 / X11 / cocoa window backends), a set of host capability modules (files, HTTP, clipboard,
native dialogs, persistent storage), and a toolchain that **packages a script into a standalone
executable**.

The positioning is "small but complete": one `go build` yields one executable. You can embed JS as
a scripting layer inside a Go program, or write small GUI tools with it and ship them.

### Design choices

| Choice | How | Cost |
|---|---|---|
| **Single-binary distribution** | Static build, no shared-library deps; cross-platform is just `GOOS`/`GOARCH`, no target toolchain needed | Larger binaries |
| **Zero cgo** | Backends talk to system APIs directly (syscall on Windows, the X protocol on Linux, cocoa/purego on macOS) | `-race` detection unavailable |
| **GUI is not bolted on** | Rendering, layout, hit testing and dirty-rectangle redraws all live inside the runtime; scripts see only JSX and signals | Rendering power bounded by software rasterisation |
| **Deterministic single thread** | Single-threaded VM; network I/O completes on goroutines and is dispatched back to the main thread, so callbacks need no locking | No true parallel execution |
| **Missing capabilities say so** | Host capabilities expose `canIUse()` plus 8 unified error codes — no fake data, no pretending to play | Surfaces problems earlier than a "best effort" approach would |

### What Gox is not for

Stating the boundary up front saves you the round trip:

- **Not a Node.js replacement for web backends** — no CommonJS (`require`) and most of the npm ecosystem won't run; see the [compatibility matrix](docs/compat-matrix.md).
- **Not for CPU-bound computation** — a pure interpreter with no JIT; `fib28` is ~3× slower than goja and an order of magnitude slower than V8. See [Performance](#performance).
- **Not for production systems needing full ES conformance** — test262 `built-ins` compliance is 62%, with `Temporal` / `RegExp` and others still missing whole blocks.
- **Not a "write once, run anywhere" fantasy for GUI** — three-platform form factors (foldables, safe areas, permissions) differ for real and need per-platform adaptation.

**Good fits**: small cross-platform desktop tools in JS; JS as a config/plugin layer inside Go apps;
learning how compilers and virtual machines are built.

---

## Project status

Gox is in **active 0.x development** — public APIs may see breaking changes between minor
versions (under 0.x, minor *is* the breaking position).

### test262 compliance

The official ECMAScript conformance suite is run on a weekly CI schedule, which writes the badges
back automatically (`docs/test262-*.json`):

| Suite | Compliance | Scope |
|---|---|---|
| `language` | **97.0%** | Language semantics (statements / expressions / modules / functions / classes …) |
| `built-ins` | **62.1%** | Built-in objects (`Array` / `String` / `Temporal` / `TypedArray` …), 14804 / 23823 |

> The two suites have **different denominators and completely different composition**, so each
> guards its own lower-bound baseline. `intl402` (needs i18n) is not implemented and is outside the
> runner's suite list. Compliance is a **trend signal, not an acceptance gate** — the failure
> surface is clustered into 502 clusters of "directory family × failure mode", and work is
> scheduled per cluster. See the [website · Performance and compliance](https://14752222.github.io/Gox/guide/performance)
> for the full methodology.

### Platform support matrix

| Platform | Runtime | GUI window backend | CI coverage |
|---|---|---|---|
| Windows x64 | ✅ | win32 (pure syscall) | `ci.yml` build + test |
| Linux x64 / arm64 | ✅ | X11 (pure protocol) | `ci.yml` build + test |
| macOS x64 / arm64 | ✅ | cocoa (purego) | `ci.yml` build + test |
| Android arm64 | ✅ prebuilt library | Shell project (Kotlin host) | `mobile-smoke.yml` + foldable emulator regression |
| iOS arm64 | ✅ prebuilt library | Shell project (Swift host) | `mobile-smoke.yml`, three simulator models; **on-device verification pending** |
| HarmonyOS arm64 | ⚠️ cross-compiles | Shell project (ArkTS); the six capability modules are still stubs | Build passes; **HAP signing is a hard blocker**, emulator verification pending |

### Known gaps

- Rich text (inline mixed styling) is not yet wrapped; virtualised long lists landed as `<scroll vlist>`
- Parts of the native-capability layer that need real hardware (camera / location / photo library / permissions) honestly report `unsupported` on desktop
- CommonJS / `require` is explicitly out of scope; only a handful of npm packages such as `uuid` partially work
- Itemised in [docs/gui-guide.md](docs/gui-guide.md) and the website's [Limitations](https://14752222.github.io/Gox/components/limits)

---

## Quick start

### Requirements

| Scenario | Requirement |
|---|---|
| Build from source | Go 1.26.2 or newer (zero cgo, no C toolchain) |
| Just run scripts | Nothing — the npm package ships prebuilt binaries; Node.js ≥ 14 only |
| GUI apps | Windows (win32), Linux (X11) or macOS (cocoa) |

### Three ways to install

```bash
# 1) npm — easiest; prebuilt binaries for five platforms ship with the package
npm i -g @goxjs/goxjs    # gives you the `goxjs` command
npx goxjs app.js         # or run it without installing

# 2) Build from source — needs a Go toolchain
git clone https://github.com/14752222/Gox.git
cd Gox
go build ./cmd/gox       # produces the executable (gox.exe on Windows)

# 3) go install — straight into GOBIN (requires a published tag)
go install github.com/14752222/Gox/cmd/gox@latest
```

> `npm/` · `website/` · `gox-logo-concepts/` are three **separate repositories**
> ([gox-npm](https://github.com/14752222/gox-npm) · [gox-website](https://github.com/14752222/gox-website) ·
> [gox-logo-concepts](https://github.com/14752222/gox-logo-concepts)). Since 2026-10-12 they are no longer
> git submodules, so cloning the main repo does not need `--recurse-submodules`. CI checks out `npm/` and
> `website/` automatically; to run `scripts/check-registries.py` locally, first
> `git clone https://github.com/14752222/gox-npm npm` (otherwise `npm/package.json` is missing).

### Interactive REPL

```bash
./gox                # Unix; gox.exe on Windows
```

```
Gox REPL (ES6 subset)
Type :exit to quit, :help for help

> let x = 10
> let y = 20
> x + y
  30
> [1, 2, 3].map(v => v * 2)
  [2, 4, 6]
```

REPL commands: `:help` for help, `:clear` to reset the environment, `:exit` to quit.

### Running a script

```bash
./gox example.js    # the npm-installed goxjs command works the same way
```

After the script finishes, the value of the last top-level expression is echoed (`undefined`
excepted), and the process waits for all timers and async callbacks before exiting.

---

## Hero example: adaptive one/two-column list

Below **600dp window width** it is a single column; drag it wider to **≥ 600dp** and it becomes two
columns (list on the left, detail on the right). Dragging the window edge triggers it — no resize
listener, no size polling. Full runnable file:
[`testdata/adaptive_list_demo.js`](testdata/adaptive_list_demo.js) (`gox testdata/adaptive_list_demo.js`).

```js
import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";
import { useBreakpoint, matchBreakpoint } from "gx/viewport";
import { Switch, Match } from "gx/view";

const bp = useBreakpoint();                 // () => "sm" | "md" | "lg" | "xl" (subscribing)
const [selected, setSelected] = createSignal(0);

const ITEMS = [
  { id: "a", title: "Compile pipeline", desc: "Lexer → parser → bytecode → stack VM, 160 opcodes." },
  { id: "b", title: "Software-rasterised GUI", desc: "flex layout + JSX + signals, three backends." },
  { id: "c", title: "Script packager", desc: "Turn .js into a single executable." },
];

// Layout branches are declared by breakpoint name (a sm/md/lg/xl table) instead of
// scattering the same width check across N if statements — add a tier, edit only the table.
const layout = () => {
  bp();   // ← read unconditionally = subscription registration. Delete this line and the
          //   page stops adapting (with no warning)
  return matchBreakpoint({
    sm: { cols: 1, side: false },   // <600dp  single column
    md: { cols: 2, side: true },    // >=600dp two columns
    lg: { cols: 2, side: true },
    xl: { cols: 2, side: true },
  });
};

const List = () => (
  <column gap={6}>
    <view each={ITEMS} key="id">
      {(it, i) => (
        <row gap={6} padding={8} radius={6}
             background={selected() === i ? "#dce8ff" : "#f2f5fa"}
             onClick={() => setSelected(i)}>
          <text font={13} color="#334">{it.title}</text>
        </row>
      )}
    </view>
  </column>
);

const Detail = () => (
  <column gap={6} padding={12} radius={8} background="#ffffff">
    <text font={14} color="#223">{ITEMS[selected()].title}</text>
    <text font={12} color="#667">{ITEMS[selected()].desc}</text>
  </column>
);

render(
  <window title="Adaptive list" width={520} height={420}>
    <column gap={10} padding={14}>
      <text font={15}>{() => `breakpoint = ${bp()}   →   ${layout().side ? "two columns" : "one column"}`}</text>
      <Switch>
        <Match when={() => layout().side}>
          <row gap={10}>
            <column width={210} gap={6}><List /></column>
            <column flex={1}><Detail /></column>
          </row>
        </Match>
        <Match when={() => true}>
          <column gap={8}>
            <List />
            <Detail />
          </column>
        </Match>
      </Switch>
    </column>
  </window>
);
```

**The two easiest pitfalls** (also called out in the demo's own comments):

1. **Pure reads short-circuit the subscription** — `breakpoint()` / `posture()` / `hasFold()` are pure
   reads that do **not** subscribe; only two-stage accessors like `useBreakpoint()` subscribe. Put a
   pure read in the first operand of a ternary or on the left of `&&`, and the short-circuited branch
   is never read ⇒ zero dependencies ⇒ it never re-runs when the width changes, **with no warning**.
   `gfx/adaptive_list_test.go` pins this as an assertion: a pure-read snapshot stays at its first-frame
   value after widening.
2. **Two coordinate systems** — `reservedRegions()` reports x/y/width/height in **device pixels**
   (hinge, punch-hole cutouts), while `safeAreaStyle()` and explicitly written size attributes are in
   **dp** (the kernel multiplies by `Display.Scale` every frame). Don't feed the former straight into
   layout attributes when avoiding the hinge — divide by `pixelRatio` first.

---

## Capability map

| Area | Contents | Docs |
|---|---|---|
| **Language runtime** | 160 opcodes, fixed 3-byte instructions; ES6+ language subset; full built-in object family | [Language and built-ins](#language-and-built-ins) |
| **GUI rendering layer** | 63 built-in elements, flex layout, JSX, dirty-rectangle partial redraw, win32 / X11 / cocoa backends | [docs/gui-guide.md](docs/gui-guide.md) |
| **Host capabilities** | Globals `fs` / `path` / `http` / `fetch` / `process` / `stats` / `crypto` / `hash`; 23 `gx/*` built-in modules | [Host capability modules](#host-capability-modules) |
| **Mobile and form factors** | Android / iOS / HarmonyOS shell projects plus foldable and safe-area semantics in `gx/screen` | [docs/foldable-adaptation.md](docs/foldable-adaptation.md) |
| **Toolchain** | REPL, `gox create` scaffolding, `gox dev` hot reload, `gox cert` / `gox build` / `gox sync` / `gox icon`, jsbuild packaging | [Packaging and toolchain](#packaging-and-toolchain) |

---

## Language and built-ins

### Syntax subset

`let`/`const`/`var`, functions and arrow functions, closures, `class` (including private fields
`#name`), `async`/`await`, destructuring assignment, rest/default parameters, spread, template
literals, `for...of`, `for...in`, `try`/`catch`/`throw`, optional chaining `?.`, nullish coalescing
`??`, ES modules `import`/`export`, and **JSX syntax** (lowered at compile time into
`h(tag, props, ...children)` calls).

```js
let x = 10
let y = 20
console.log(x + y)                                                 // 30
console.log([1, 2, 3].map(v => v * 2))                             // [2, 4, 6]
console.log([1, 2, 3].filter(v => v > 1).reduce((a, b) => a + b))  // 5
console.log(Math.hypot(3, 4))                                      // 5

let name = "Gox"
console.log(`Hello, ${name}!`)                                     // Hello, Gox!

let [a, b] = [1, 2]                                                // destructuring
console.log(a + b)                                                 // 3
```

```js
class Point {
  constructor(x, y) { this.x = x; this.y = y }
  norm() { return Math.hypot(this.x, this.y) }
}
console.log(new Point(3, 4).norm())       // 5

async function main() {
  let v = await delay(10, "timer done")   // delay(ms, value) returns a Promise
  console.log(v)
}
main()
```

```
5
Promise { <pending> }     ← top-level echo: the return value of main()
timer done                ← after the timer fires, await resumes
```

> Syntax note: `async function` and async arrows `async () => {}` both work; top-level `await` does not.

**ES modules** — `lib.js`:

```js
export const PI = 3.14
export function double(x) { return x * 2 }
```

`main.js`:

```js
import { PI, double } from "./lib.js"
setTimeout(() => console.log("tick"), 10)
console.log(double(PI))
```

```
6.28
tick
```

### Built-in objects

`Array` / `String` / `Number` / `Object` / `Boolean` / `Math` / `JSON` /
`Map` / `Set` / `WeakMap` / `WeakSet` / `Symbol` / `BigInt` / `RegExp` / `Proxy` / `Reflect` /
`Iterator` / `Promise` / `ArrayBuffer` / `DataView` (the TypedArray family) / `WeakRef` /
`FinalizationRegistry` / the full error type family / `Temporal` (the modern date-time API —
this runtime has no `Date`).

### Reactive programming

GetX-style (Dart) `obs` / `computed` / `ever` / `once`:

```js
let count = obs(0)
ever(count, v => console.log("count =", v))   // fires immediately with the current value
count.value = 1
count.value = 2
count.value = 2   // unchanged, no notification
```

```
count = 0
count = 1
count = 2
2                  ← top-level echo: the value of the last assignment expression
```

Plus SolidJS-style `gx/solid` signals (`createSignal` / `createEffect` / `createMemo` /
`createResource` / `onMount` / `onCleanup`).

---

## GUI desktop apps

`gx/gfx` + `gx/solid` provide declarative JSX UI with signal-driven reactive updates; the renderer
is pure-Go software rasterisation (no cgo, no shared-library dependencies).

### Start a project with the scaffolder

```bash
gox create my-app                          # also goxjs create / npx @goxjs/goxjs create
gox create my-app --ts                     # TypeScript/TSX: .tsx runs directly, no node build
cd my-app && npm install && npm run dev
```

This generates `package.json`, entry `src/main.js`, root component `src/app.js`, shared state
`src/store.js`, design tokens `src/theme.js` and three sample components under `src/components/`
(counter / todo list / multi-state), demonstrating signals, element-level directives (`each` /
`show`), `model` two-way binding and `Switch` / `Match`. `--ts` generates the TypeScript variant:
entry becomes `src/main.tsx`, with `tsconfig.json` and `src/gox.d.ts` (IDE type hints). Types are
stripped automatically at load time (embedded esbuild transform, JSX preserved) — there is no
node-side build step; type checking is left to the IDE and runtime cost is zero.

The templates are **real files** under [`scaffold/template/`](scaffold/template/), embedded into the
binary with `go:embed` (change a template and you must rebuild; `scaffold/scaffold_test.go` keeps the
generated JS/TS passing transform + lexer → parser → compiler, and `gfx/scaffold_project_test.go`
actually mounts it and clicks through it).

### Minimal hand-written version

```js
import { h, render, createSignal } from "gox"        // aggregate entry: union of all gx/* exports
// equivalent split form: import { h, render } from "gx/gfx"; import { createSignal } from "gx/solid"

const [count, setCount] = createSignal(0)

render(
  <window title="Counter" width={400} height={300}>
    <column gap={8} padding={16}>
      <text font={20}>Count: { count()}</text>
      <button onClick={() => setCount(c => c + 1)}>Add one</button>
    </column>
  </window>
)
```

```bash
./gox counter.js          # run it directly; a 400x300 window opens
```

Click the button → the signal updates → dependent attributes/text nodes are marked dirty → dirty
rectangles are merged so only the affected region is redrawn. `render()` returns a window handle and
can be called multiple times to open several windows (each with its own element tree and focus; the
process exits only when all are closed).

### GUI capability list

- **63 built-in elements** — layout (`column` / `row` / `grid` / `scroll` / `view`), form
  (`input` / `textarea` / `select` / `slider` / `checkbox` / `radio` / `switch`), data display
  (`table` / `tree` / `list-item` / `tabs` / `pagination`), feedback (`alert` / `tag` / `badge` /
  `avatar` / `empty` / `icon` / `spinner` / `skeleton` / `progress` / `tooltip`), overlays
  (`dialog` / `drawer` / `toast`), media (`<video>` / `<audio>`), a custom-draw canvas, and
  self-drawn menu bars and context menus
- **Declarative UI as element-level directives** — `model` two-way binding, `each` lists (keyed
  reuse), `show` conditional keep-alive visibility, and `Switch` + `Match` from `gx/view` for
  multi-branch
- **Routing and screens** — `gx/router` (route table / parameter matching / three-level guards /
  lazy loading / history stack / multi-window scope / foldable two-column) and `gx/screen`
  (multi-monitor / per-window display info)
- **Media tags that actually play** — the kernel only defines the host contract
  (`nativeVideoHost` / `nativeAudioHost`): it can mount, receive events, and `playing` / `muted` /
  `loop` / `volume` / `fit` are fully controlled, and **when the backend lacks the capability it says
  so** (placeholder poster + a single `onError({code:"unsupported"})` + `canIUse("video"/"audio")`
  answering `false` honestly). Default Linux builds mount `gfx/softmedia`: GIF (with disposal
  compositing) and MJPEG video, WAV audio (PCM 8/16/24/32-bit + IEEE float) output through
  PulseAudio via `purego` dlopen (zero cgo). h264 / aac / mp3 are left to platform media layers —
  see [docs/media-decision.md](docs/media-decision.md)

Full reference — event model, built-in element attribute tables, flex layout and decorative drawing,
controlled components and IME, transitions, native dialogs / clipboard / persistent storage / menu
bars, multi-window semantics and platform differences — lives in
**[docs/gui-guide.md](docs/gui-guide.md)**; runnable samples live in [`testdata/`](testdata/)
(`counter_demo.js`, `form_demo.js`, `multiwindow_demo.js`, `menu_demo.js`, `view_demo.js`,
`router_demo.js`, `router_window_demo.js` and 108 more).

Routing and screen adaptation have a dedicated manual: **[docs/gui-router.md](docs/gui-router.md)**.

---

## Host capability modules

### Globals (no import needed)

`fs` / `path` / `http` / `fetch` / `process` / `stats` / `crypto` / `hash`:

```js
const text = fs.readFileSync("a.txt")          // sync: throws a JS error on failure
const data = await fs.readFile("a.txt")        // async: returns a Promise without a callback
fs.mkdirSync("a/b", { recursive: true })

const server = http.createServer(function (req, res) {
  res.writeHead(200, { "Content-Type": "text/plain" })
  res.end("hello")
})
server.listen(0, function () { console.log(server.port) })

const res = await fetch("https://example.com")
console.log(res.status, await res.text())
```

The async I/O model is **cross-goroutine I/O dispatched back onto the single-threaded VM**: network
reads and writes never touch VM state, and results return to the event loop through a zero-delay
timer, so callbacks need no locking when they touch the VM.

A complete HTTP sample (server on port 0, routing/query params/JSON body/404-405-500 statuses,
callback-style `http.get` · `http.request` vs Promise-style `fetch`, async responses and
`server.close` teardown): [`testdata/http_demo.js`](testdata/http_demo.js) —
`gox testdata/http_demo.js` runs it.

**Digests and randomness** — `crypto` and `hash` sit on the Go standard library's `crypto/*`, zero cgo:

```js
hash("gox");                                 // sync → lowercase hex (SHA-256 by default)
hash("gox", "SHA-512");                      // algorithm name is case/hyphen insensitive
const ab = await crypto.subtle.digest("SHA-256", "gox");   // Promise → ArrayBuffer (32 bytes)
crypto.randomUUID();                                       // RFC 4122 v4
crypto.getRandomValues(new Uint8Array(16));                // fills in place, 64KB cap
```

| Item | Note |
|---|---|
| Algorithms | `hash()` supports MD5 / SHA-1 / SHA-256 / SHA-512; `crypto.subtle.digest()` **only takes SHA-1 / SHA-256 / SHA-512** |
| Argument order | `hash(data, algo)` but `crypto.subtle.digest(algo, data)` |
| Not included | Encryption / signing / KDF |
| Errors | Unsupported algorithms or arguments always raise `TypeError` — never a silent fallback |

### `gx/*` built-in modules (import required)

**23 modules** in the registry: `gox` (aggregate entry), `gx/solid`, `gx/gfx`, `gx/view`,
`gx/router`, `gx/screen`, `gx/viewport`, `gx/dialog`, `gx/storage`, `gx/update`, `gx/crypto`,
`gx/theme`, `gx/dev`, `gx/a11y`, `gx/device`, `gx/app`, `gx/geo`, `gx/media`, `gx/permission`, plus
`fs` / `http` / `path` / `process`, which are also importable from the global side.

The six native-capability modules share one host contract (`NativeHost`). Three call shapes need to
be told apart when writing code:

```js
import { deviceInfo, battery, isOnline, canIUse } from "gx/device";   // pull-type + report-type
import { getLocation, watchLocation } from "gx/geo";
import { takePhoto } from "gx/media";                                  // action-type

const info = deviceInfo();                    // available synchronously
console.log(info.platform, info.model, battery().level + "%", isOnline());

if (canIUse("camera")) {                      // probe capability up front, don't rely on catch
  const photo = await takePhoto({ count: 1 });      // rejects on failure, with errCode
}

const stop = watchLocation((loc) => console.log(loc.latitude, loc.longitude));
try {
  await getLocation({ highAccuracy: true });
} catch (e) {
  if (e.errCode === "permission-denied") console.warn(e.message);
}
```

The desktop backend implements whatever it can (battery / network / brightness / keep-screen-on /
opening system settings) and honestly reports `unsupported` for the rest. **Mobile implements the same
`NativeHost` contract without touching the kernel** — the Android (Kotlin) and iOS (Swift) shells have
essentially all six modules: Android is verified on an emulator (first paint / touch / IME / safe
area / foldables), while the iOS shell project and build scripts (including TestFlight packaging) are
complete but **on-device verification is pending**; the HarmonyOS (ArkTS) shell currently wires only
the two pure-report channels (safe area and fold state) and the six capability modules are still
stubs. For the per-method (module × platform) status table, shell build notes and first-frame
self-check list, see [`app/NATIVE-HOST.md`](app/NATIVE-HOST.md); a demo of all three shapes:
[`testdata/native_demo.js`](testdata/native_demo.js).

**Three-platform verification scope (CI matrix) — the capability matrix states what was actually verified, nothing more**:

| Platform | Build smoke (every push) | Device regression | Fold semantics |
|---|---|---|---|
| **Android** | `mobile-smoke.yml` android job: libgox.so → assembleDebug APK | `android-fold-emulator` job: on a **pixel_fold emulator** (API 34+, real hinge switching) runs CLOSED → HALF_FOLDED → OPENED matrix + screenshot archiving; on-device `scripts/mobile-device-accept.sh --rounds 20` | ✅ fold-state switching covered by the emulator |
| **iOS** | `mobile-smoke.yml` ios job: xcodebuild simulator build | `ios-device-matrix` job: iPhone 16 / 16 Pro Max / iPad × portrait/landscape, simctl screenshots archived | ⚠️ no foldable simulator: `reservedRegions` (iOS 27.1+) only reports flat; on-device verification pending |
| **HarmonyOS** | Cross-compile + host-contract tests + assembleHap build | — | ❌ emulator verification pending (**HAP signing is a hard blocker**); we do not claim "HarmonyOS supported" |

---

## Packaging and toolchain

### jsbuild: script → standalone executable

`jsbuild` (`packager/`) embeds the entry script and its relatively-imported modules into a generated
Go project and compiles it into a single self-contained executable:

```bash
go run ./packager app.js -o app.exe                    # CLI app
go run ./packager counter.js --gui -o counter.exe      # GUI app
go run ./packager app.js --gui --target linux/amd64    # pure-Go cross-compile, no target toolchain
```

Per-platform distribution notes (Windows icons and signing, Linux packaging formats, macOS `.app`
bundles) are in [docs/desktop-distribution.md](docs/desktop-distribution.md).

> **Command-line arguments**: packaged output and `gox <file> <args>` share one convention —
> `process.argv` is always `[executable, script path, ...user args]`, so user arguments **always start
> at index 2**. The same script can therefore use `process.argv[2]` for the first user argument whether
> run from source or packaged.

### Multi-platform config (gox.json / permissions / icons / certificates)

`gox create` generates a project with `gox.json` (app name, appId, version, permissions, per-platform
sub-config) plus complete Android / iOS / desktop resource skeletons and default icons:

| Command | Purpose |
|---|---|
| `gox sync` | Idempotently injects `permissions` from gox.json into AndroidManifest and iOS Info.plist |
| `gox icon` | Generates Android mipmaps, iOS AppIconSet, Windows `.ico`, macOS `.icns` and favicon from a single `assets/icon.png` (1024×1024) |
| `gox cert` | One-step platform signing certificates (pure Go, no JDK/openssl): `android` / `windows` / `harmony` / `ios` |
| `gox build <android\|ios\|windows\|macos>` | Unified build entry (sync → icon → platform packaging); `--release` for signed release builds |

Field reference, permission lists and icon specs: [docs/platform-config.md](docs/platform-config.md).
The script runtime permission model (`--allow-fs-write` etc., least privilege by default):
[docs/permissions.md](docs/permissions.md).

### npm package info

| Item | Value |
|---|---|
| Package / command | `@goxjs/goxjs` → `goxjs` |
| Platforms included | macOS x64 / arm64, Linux x64 / arm64, Windows x64 |
| Node version | `>=14` (only used to distribute the binary; runtime does not depend on Node) |
| Version source of truth | `version` in [gox-npm's `package.json`](https://github.com/14752222/gox-npm/blob/main/package.json) |
| Mobile sub-packages | `@goxjs/goxjs-mobile-<platform>-<abi>` (4 of them, installed on demand, not in the main package's dependencies) |

---

## Architecture

### Compile and execution pipeline

```
 .js source
    │  lexer        tokenisation
    ▼
 Token stream
    │  parser       parsing (incl. JSX lowering)
    ▼
 AST (ast/)
    │  compiler     symbol table resolution + bytecode generation (constant pool)
    ▼
 Bytecode (bytecode/)   fixed 3 bytes: [opcode 1B][operand 2B big-endian], 160 opcodes
    │  vm           stack call-frame execution + timer event loop + module loading
    ▼
 Result
```

### Key design decisions

- **Fixed-length instruction encoding** — no variable-length decoding state machine; operands are
  read directly, and the constant pool carries large object references.
- **Single-threaded VM + cross-goroutine scheduling** — network callbacks such as
  `http.createServer` are captured on goroutines and scheduled back onto the VM thread, so no locks
  are needed. Pending tasks keep the event loop alive; the process may exit only when the count hits zero.
- **Optional capabilities go through optional interfaces** — GUI backends are abstracted via
  `Surface` + `WindowFactory`, so swapping backends needs zero script changes; platform-unique
  capabilities (IME, clipboard, native dialogs, video / audio) are probed per backend and **degrade
  honestly** instead of erroring when absent.
- **Fixed shards keep compliance reproducible** — the test262 runner pins the shard count at 128 and
  **decouples it from `-jobs`**: the engine has package-level mutable state, so if the shard count
  tracked concurrency, each test's "same-process neighbours" would change and the pass count would
  flip with `-jobs`.

### Directory layout

| Directory | Responsibility |
|---|---|
| `lexer/` | Tokeniser (incl. JSX lexical support) |
| `parser/` | Parser producing the AST (incl. JSX lowering) |
| `ast/` | AST node definitions |
| `compiler/` | AST → bytecode compiler (incl. symbol table) |
| `bytecode/` | Opcode and bytecode format definitions |
| `vm/` | Stack-based bytecode VM (call frames, module loading, timer scheduling) |
| `object/` | Runtime object system (Number / Array / Map / Promise / Observable …) |
| `runtime/` | Global Environment |
| `stdlib/` | Standard library and host API implementations (incl. `gx/solid` reactive signals) |
| `gfx/` | Self-written GUI rendering layer (software rasterisation, layout, hit testing, win32 / X11 / cocoa backends) |
| `packager/` | jsbuild packager (GUI apps, cross-compilation) |
| `scaffold/` | `gox create` project scaffolding: `template/` holds **real files** (`go:embed`ed into the binary) |
| `tools/` | Dev tools: `tstransform/` (TS/TSX → JS), `icongen/` (icons), `certgen/` (certificates), `update/` (self-update), `perf/` (benchmarks) |
| `test/` | Testing: `bench/` profiling benchmarks |
| `testdata/` | Directly runnable sample scripts (108 of them) |
| `app/` | Mobile shell projects and the `NATIVE-HOST.md` contract doc |
| `docs/` | Public-facing documentation |
| `scripts/` | Build and check scripts: `build-npm.sh`, `check-registries.py`, `check-imports.py`, `test262-cluster.py` and more |
| `.github/workflows/` | CI: `ci.yml` general gate, `release.yml` releases, `pages.yml` website, `test262.yml` compliance, `mobile-smoke.yml` three-platform matrix |

---

## Performance

> Figures come from public benchmarks run on CI runners (`bench-results/`, AMD EPYC 7763) and the
> website's own measurements (Apple M2). **Including the one we miss** — methodology and thresholds
> are fully published.

| Metric | gox | goja (pure interpreter reference) | node / V8 (JIT reference) |
|---|---:|---:|---:|
| Startup | 4 ms | 3 ms | 25 ms |
| `fib28` pure compute | 631 ms | 200 ms | 34 ms |
| 10k timers | 168 ms | 116 ms | 139 ms |
| Resident RSS | 25 MB | 10 MB | 42 MB |

**Verdict**: startup and memory are on par with comparable interpreters; pure compute throughput is
**not on par with goja** (`fib28` ~3× slower), the direct cost of a pure interpreter with no JIT, and
not an optimisation target. **UI is free of GC pauses** (stressed-run aggregate p95 frame interval
5.95ms ≤ 16.7ms; single GC pause max 1.382ms ≤ 5ms).

The virtualised long list (`<scroll vlist>`) is decoupled from row count: first frame for 100k rows is
89ms vs 2187ms for a full render (~24×), and scroll frames stay at 0.11–0.17ms vs 999ms for a full render.

Full figures, how to run them and the pass/fail thresholds:
[website · Performance and compliance](https://14752222.github.io/Gox/guide/performance).

---

## Documentation map

| Document | Contents |
|---|---|
| [docs/tutorial.md](docs/tutorial.md) | **Hands-on tutorial**: API calls and parameters, importing built-in modules, `gox create`, route definition and registration |
| [docs/gui-guide.md](docs/gui-guide.md) | GUI guide: element/event reference, layout, animation, host capabilities, sample index |
| [docs/gui-router.md](docs/gui-router.md) | Routing and screen adaptation manual |
| [docs/gui-patterns.md](docs/gui-patterns.md) | Userland patterns manual (routing, state, theming idioms) |
| [docs/gui-model-binding.md](docs/gui-model-binding.md) | `model` two-way binding: interface design, semantics table, comparison with Vue, counter-examples |
| [docs/foldable-adaptation.md](docs/foldable-adaptation.md) | Foldable adaptation tutorial: the pure-read short-circuit pitfall, hinge-avoidance unit conversion, three-platform verification scope |
| [docs/platform-config.md](docs/platform-config.md) | Multi-platform config: gox.json fields, permission list, icon specs, `gox build` targets |
| [docs/desktop-distribution.md](docs/desktop-distribution.md) | Desktop distribution: icons, signing, per-platform packaging |
| [docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md) | Runtime API tutorial: function types, callback bridge, memory management, how to add an API |
| [docs/permissions.md](docs/permissions.md) | Script runtime permission model |
| [docs/compat-matrix.md](docs/compat-matrix.md) | Measured compatibility with popular npm packages |
| [docs/npm-release.md](docs/npm-release.md) | `@goxjs/goxjs` release manual |
| [Website](https://14752222.github.io/Gox/) | Online tutorials for installing, running scripts, writing GUI apps and packaging (bilingual) |

---

## Development and testing

```bash
go test ./...          # run all tests (308 test files / 2419 test functions)
go run ./test/bench    # generate cpu.prof
```

> Because there is no cgo, `go test -race` is unavailable in this project (Go requires cgo for it).

To go deeper (adding standard-library APIs, understanding the callback bridge and memory
management), read [docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md).

---

## Release process

`@goxjs/goxjs` is published automatically by GitHub Actions
([.github/workflows/release.yml](.github/workflows/release.yml)), authenticating with npm Trusted
Publishing (OIDC) — no secret or token lives in the repo:

```bash
# 1. Bump the version — npm/package.json's `version` is the single source of truth,
#    and npm/ is a separate repository:
$EDITOR npm/package.json
git -C npm commit -am "chore(release): 0.15.0" && git -C npm push
# 2. Tag the main repo and push — the tag must match gox-npm's default-branch version
git tag v0.15.0 && git push Gox v0.15.0
# 3. CI does the rest: cross-compile five platforms → package verification → OIDC publish
#    → create the tag and the Release automatically
```

- **Triggers**: push to `main`, pushing a `v*` tag, publishing a Release, and manual
  `workflow_dispatch`
- **Idempotent**: CI checks the registry first and skips with a notice if the version already exists —
  repeated pushes never double-publish
- **Versioning**: ordinary commits never trigger a real publish; `const version` in
  `cmd/gox/main.go` and `npm/package.json` must stay in sync
  ([`scripts/check-registries.py`](scripts/check-registries.py) enforces it)
- **Full manual**: [docs/npm-release.md](docs/npm-release.md)

---

## Contributing

Issues and pull requests are welcome — the full participation guide, local gates and this repo's
particular "change one place, sync several" discipline are in **[CONTRIBUTING.md](CONTRIBUTING.md)**;
the code of conduct is in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). First-time contributors can start
with issues labelled `good first issue`.

**Dev environment**: Go 1.26.2+, zero cgo (no C toolchain). Before submitting:

```bash
gofmt -l .             # should print nothing
go build ./...
go vet ./...
go test ./...
python3 scripts/check-registries.py   # registry consistency (same gate as CI)
python3 scripts/check-imports.py      # doc/sample imports vs export table (same gate as CI)
```

**Commit style**: follow the repo's existing Conventional Commits style, with a scope naming the
affected module or topic:

```
feat(gfx): add keyboard adjustment support to <slider>
fix(vm): stop SET_INDEX from silently dropping non-object types
docs(gui-guide): document the multi-window event pump polling semantics
ci(release): switch the packaging gate back to tar verification
```

**Adding a built-in GUI component requires syncing four places** (missing one fails silently, with no
compile error):

| Place | Purpose |
|---|---|
| `knownTags` in `gfx/node.go` | Registers the tag name; otherwise `h()` prints an "unknown tag" warning |
| `intrinsicSize` in `gfx/layout.go` | Declares the intrinsic size |
| layout dispatch in `gfx/layout.go` | Declares how children participate in layout |
| `drawNode` in `gfx/raster.go` | Implements drawing; tags intercepted by a `case` must draw their own background / border |

**Other conventions**:

- Event callbacks are always dispatched through `callHandler`; window-related operations go from the
  node through `appOfNode(n)` to get the owning window (multi-window safe)
- JSX attributes and children of `gx/*` are evaluated once at call time — reactivity requires passing
  a **function** (`value: () => sig()`; children as ``{() => sig()}`` not `{sig()}`). Passing a value
  is just a snapshot — and **snapshot children fail completely silently** (misused attributes warn,
  children do not). See [docs/gui-guide.md](docs/gui-guide.md) §8.1
- Put sample scripts in `testdata/` and make sure they run directly

---

## License

[MIT License](LICENSE)
