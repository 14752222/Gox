# Gox

> A JavaScript runtime implemented from scratch in Go: lexing → parsing → bytecode compilation →
> stack-based VM execution. Single binary, zero cgo, zero external runtime dependencies,
> with a built-in software-rasterized GUI and a script packager.

[![Go](https://img.shields.io/badge/Go-1.26.2%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![npm](https://img.shields.io/npm/v/@goxjs/goxjs)](https://www.npmjs.com/package/@goxjs/goxjs)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#requirements)
[![Release](https://github.com/14752222/Gox/actions/workflows/release.yml/badge.svg)](https://github.com/14752222/Gox/actions/workflows/release.yml)
[![test262 language](https://img.shields.io/endpoint?url=https%3A%2F%2Fraw.githubusercontent.com%2F14752222%2FGox%2Fmain%2Fdocs%2Ftest262-compliance.json)](docs/test262-t04-report.md)
[![test262 built-ins](https://img.shields.io/endpoint?url=https%3A%2F%2Fraw.githubusercontent.com%2F14752222%2FGox%2Fmain%2Fdocs%2Ftest262-compliance-builtins.json)](docs/test262-builtins-clusters.md)

📖 **[Website & tutorial](https://14752222.github.io/Gox/)** ｜ **[GUI guide](docs/gui-guide.md) (Chinese)** ｜ **[npm package](https://www.npmjs.com/package/@goxjs/goxjs)**

> 简体中文版：[README.md](README.md)。This file is the English translation; both are kept in sync
> and describe the same build.

## Table of contents

- [Overview](#overview)
- [First-screen example: adaptive single / two-pane list](#first-screen-example-adaptive-single--two-pane-list)
- [Features](#features)
- [Getting started](#getting-started)
- [Usage](#usage)
- [Configuration](#configuration)
- [Architecture](#architecture)
- [Development & testing](#development--testing)
- [Release process](#release-process)
- [Contributing](#contributing)
- [Documentation](#documentation)
- [License](#license)

## Overview

Gox is a JavaScript (ES6+ subset) runtime written in **pure Go**. The whole compilation pipeline —
lexing, parsing, bytecode generation, stack VM — is implemented from scratch: no V8, no QuickJS,
no cgo.

On top of that, Gox fills the layer scripting languages usually lack: a **home-grown desktop GUI
renderer** (pure-Go software rasterization, flex layout + JSX + signal-driven updates, win32 / X11 /
cocoa window backends), a set of host capability modules (files, HTTP, clipboard, native dialogs,
persistent storage), and a toolchain that **packs a script into a standalone executable**.

Its positioning is "small but complete": one `go build` yields one executable; you can embed JS as a
scripting layer inside a Go program, or just write a small GUI tool with it and ship it.

**Design choices**

- **Single-binary distribution** — statically compiled, no dynamic library dependencies;
  cross-platform means switching `GOOS`/`GOARCH`, no target toolchain needed.
- **Zero cgo** — backends talk to the system API directly (syscall on Windows, X protocol on Linux);
  the trade-off is that `-race` is unavailable.
- **GUI is not a bolt-on** — rendering, layout, hit testing and dirty-rectangle repainting all live
  inside the runtime; the script side sees only JSX and signals.
- **Deterministic single thread** — the VM runs single-threaded; network I/O completes in goroutines
  and is posted back to the main thread, so callbacks need no locking.

**Good fit for**: small cross-platform desktop tools in JS; embedding JS as the config / plugin layer
of a Go application; learning how a compiler and a VM are built.

## First-screen example: adaptive single / two-pane list

**Single pane below 600dp**, and it **switches to two panes at ≥ 600dp** (list on the left, detail on
the right) as you drag the window wider. Dragging the window edge is all it takes — no resize
listener, no polling of the size. Full runnable file:
[`testdata/adaptive_list_demo.js`](testdata/adaptive_list_demo.js)
(`gox testdata/adaptive_list_demo.js`).

```js
import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";
import { useBreakpoint, matchBreakpoint } from "gx/viewport";
import { Switch, Match } from "gx/view";

const bp = useBreakpoint();                 // () => "sm" | "md" | "lg" | "xl" (subscribing)
const [selected, setSelected] = createSignal(0);

const ITEMS = [
  { id: "a", title: "Pipeline", desc: "lexer -> parser -> bytecode -> stack VM, 109 opcodes." },
  { id: "b", title: "Software-rasterized GUI", desc: "flex layout + JSX + signals, three backends." },
  { id: "c", title: "Script packager", desc: "one .js in, one standalone executable out." },
];

// Layout branches are declared by breakpoint name (one sm/md/lg/xl table),
// instead of the same width check scattered across N ifs — adding a tier
// means editing the table, not the conditions.
const layout = () => {
  bp();   // <- read unconditionally = subscribe. Remove this line and the page
  return matchBreakpoint({                 //    stops adapting, silently.
    sm: { cols: 1, side: false },   // <600dp   single pane
    md: { cols: 2, side: true },    // >=600dp two panes
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
      <text font={15}>{() => `breakpoint = ${bp()}   →   ${layout().side ? "two panes" : "single pane"}`}</text>
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

**Two traps worth knowing up front** (also called out in the demo's comments):

1. **Plain reads short-circuit the subscription** — `breakpoint()` / `posture()` / `hasFold()` are
   plain reads: they give you **the value right now without subscribing**. Only the getter functions
   returned by `useBreakpoint()`-style two-stage APIs subscribe. Put a plain read in the first operand
   of a ternary (or on the left of `&&`) and the short-circuited branch is **never read at all**
   ⇒ zero dependencies ⇒ it never re-runs when the width changes, **with no warning whatsoever**.
   `gfx/adaptive_list_test.go` pins this down as an assertion: a plain-read snapshot still holds its
   first-frame value after you widen the window.
2. **Two coordinate systems** — `reservedRegions()` returns x/y/width/height in **device pixels**
   (hinge, punch-hole cutout), while `safeAreaStyle()` output and the sizes you write literally in
   scripts are **dp** (the kernel multiplies by `Display.Scale` every frame). Don't feed the former
   straight into layout props — divide by `deviceInfo().pixelRatio` first.

> Wording matters: `<Switch>` / `<Match>` *are* a conditional expression, so we don't claim
> "no ifs anywhere". The real gain is that **layout branches are declared by breakpoint name**,
> rather than the same width check scattered across N ifs.

## Features

- **Complete compilation pipeline** — home-grown lexer / parser / compiler / bytecode VM, 109
  opcodes, fixed-length 3-byte instruction encoding (`[opcode 1B][operand 2B big-endian]`),
  decoding is a direct operand fetch
- **ES6+ language subset** — `let`/`const`/`var`, functions and arrows, closures, `class`,
  `async`/`await`, destructuring, rest/default params, spread, template strings, `for...of`,
  `try`/`catch`/`throw`, optional chaining `?.`, nullish coalescing `??`, ES modules
  `import`/`export`, and **JSX** (lowered at compile time into `h(tag, props, ...children)` calls)
- **Rich built-ins** — `Array` / `String` / `Number` / `Object` / `Boolean` / `Math` / `JSON` /
  `Map` / `Set` / `WeakMap` / `WeakSet` / `Symbol` / `BigInt` / `RegExp` / `Proxy` / `Reflect` /
  `Iterator` / `Promise` / `ArrayBuffer` / `DataView` (TypedArray family) / `WeakRef` /
  `FinalizationRegistry` / the full error type family / `Temporal` (the modern date-time API —
  this runtime has no `Date`)
- **Host capability modules** — injected as global objects: `fs` (Node-style, sync + async),
  `path`, `http` (client `get`/`request` + server `createServer`), `fetch`, `process`, `stats`
- **Home-grown GUI renderer** — the `gx/gfx` module: flex-style layout (`column`/`row`/`grid`,
  percentage sizes, min-max clamping, `flexShrink`, wrapping), rounded corners / linear gradients /
  shadows, hit testing, dirty-rectangle partial repaint; win32 (pure syscall), X11 and macOS
  (cocoa, purego) window backends. See the **[GUI guide](docs/gui-guide.md)** (Chinese)
- **Media components (`<video>` / `<audio>`)** — the kernel only defines the **host contract**
  (`nativeVideoHost` / `nativeAudioHost`): mountable, event-capable, with `playing` / `muted` /
  `loop` / `volume` / `fit` fully controlled; when a backend lacks the capability it **says so**
  (cover placeholder + one `onError({code:"unsupported"})` + `canIUse("video"/"audio")` answering
  `false` honestly) — it never pretends to be playing. The default Linux build wires up the
  `gfx/softmedia` software backend and **really plays**: GIF (incl. disposal compositing) and MJPEG
  video, WAV audio (PCM 8/16/24/32-bit + IEEE float), audio output via PulseAudio `purego` dlopen
  (zero cgo), degrading to a null sink when there is no sound card (clock and events stay real).
  h264 / aac / mp3 are left to the platform media layer; boundaries in
  [media-decision.md](docs/media-decision.md)
- **Native-feeling interactive components** — form controls (`input`/`textarea`/`select`/`slider`/
  `checkbox`/`radio`/`switch`), overlays (`dialog`/`drawer`/`toast`), tabs (keep-alive page
  semantics + controlled/uncontrolled modes), scroll containers, self-drawn canvas, and a
  **self-drawn menu bar and context menu** (dropdowns / submenus / shortcuts / disabled items —
  no system menu API involved)
- **Feedback & data components** — `alert` / `tag` / `badge` / `avatar` / `empty` / `icon`
  (15 built-in pixel icons) / `spinner` / `skeleton` / `pagination` (fully controlled); loading
  states share a single animation heartbeat, so idle costs nothing. Example:
  [`testdata/feedback_demo.js`](testdata/feedback_demo.js)
- **Native capability layer** — six modules sharing one host contract (`NativeHost`): `gx/device`
  (device info / battery / network / vibration / screen brightness), `gx/app` (foreground-background /
  back key / share), `gx/geo` (location, incl. continuous watching), `gx/media` (capture / pick image /
  pick video / save), `gx/permission` (query and request), `gx/viewport` (safe area / soft keyboard
  height / split-screen and multi-window forms). On desktop the win32 host implements them directly;
  a mobile native shell only has to implement the same contract to plug in; **missing capabilities are
  reported as missing** (`canIUse` + 8 unified error codes) — never as fake data
- **Event loop** — `setTimeout` / `setInterval` / `requestIdleCallback`, plus strict-timer variants
  with controllable precision (`setStrictTimeout` and friends); in GUI mode the event loop plugs
  into the window message pump
- **Reactive programming** — Dart-GetX-style `obs` / `computed` / `ever` / `once`, plus SolidJS-style
  `gx/solid` signals (`createSignal` / `createEffect` / `createMemo` / `createResource` / `onMount` /
  `onCleanup`); declarative UI uses **element-level directives**: `model` two-way binding, `each` lists
  (keyed reuse), `show` conditional keep-alive visibility, and `Switch` + `Match` from `gx/view`
  for multi-branch
- **npm distribution** — [`@goxjs/goxjs`](https://www.npmjs.com/package/@goxjs/goxjs) bundles
  prebuilt binaries for five platforms (macOS x64/arm64, Linux x64/arm64, Windows x64);
  `npm i -g @goxjs/goxjs` gives you the `goxjs` command
- **Toolchain** — an interactive REPL, the jsbuild packager (JS → standalone executable, GUI apps and
  pure-Go cross-compilation), and `gx/dev` runtime snapshots (frame stats / cache hits / recent
  warnings, usable as a debug panel)

## Getting started

### Requirements

| Scenario | Requirement |
|---|---|
| Build from source | Go 1.26.2 or newer (zero cgo, no C toolchain needed) |
| Just run scripts | Nothing — the npm package ships prebuilt binaries per platform; Node.js ≥ 14 only |
| GUI apps | Windows (win32), Linux (X11) or macOS (cocoa) |

### Build from source

> `npm/` · `website/` · `gox-logo-concepts/` are three **separate repositories**
> ([gox-npm](https://github.com/14752222/gox-npm) · [gox-website](https://github.com/14752222/gox-website) ·
> [gox-logo-concepts](https://github.com/14752222/gox-logo-concepts)); since 2026-10-12 they are no
> longer git submodules, so cloning the main repo needs no `--recurse-submodules`. CI checks out
> `npm/` and `website/` automatically; if you want to run `scripts/check-registries.py` locally, first
> `git clone https://github.com/14752222/gox-npm npm` (otherwise `npm/package.json` is missing).

```bash
git clone https://github.com/14752222/Gox.git
cd Gox
go build ./cmd/gox   # produces the executable; gox.exe on Windows
```

> `go install github.com/14752222/Gox/cmd/gox@latest` also works (requires a published tag).

### Install via npm

```bash
npm i -g @goxjs/goxjs    # installs the goxjs command
npx goxjs app.js         # or run it without installing
```

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

### Running scripts

```bash
./gox example.js    # same usage for the goxjs command from npm
```

When a script finishes, the value of the last top-level expression is echoed (unless it is
`undefined`), and the process waits for timers and async callbacks to drain before exiting.

## Usage

### Language examples

Every example below has actually been executed.

**Basics:**

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

**class and async:**

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
Promise { <pending> }     <- top-level echo: the return value of main()
timer done                <- after the timer fires, await resumes
```

> Syntax note: async functions can be written as `async function` or async arrows `async () => {}`;
> top-level `await` is not supported.

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

**Reactivity (GetX style):**

```js
let count = obs(0)
ever(count, v => console.log("count =", v))   // fires once immediately with the current value
count.value = 1
count.value = 2
count.value = 2   // value unchanged: no notification
```

```
count = 0
count = 1
count = 2
2                  <- top-level echo: the value of the last assignment expression
```

### Host capability modules

`fs` / `path` / `http` / `fetch` / `process` / `stats` / `crypto` / `hash` are injected as **global
objects** — use them directly, no `import`:

```js
const text = fs.readFileSync("a.txt")          // sync: throws a JS exception on failure
const data = await fs.readFile("a.txt")        // async: returns a Promise when no callback is given
fs.mkdirSync("a/b", { recursive: true })

const server = http.createServer(function (req, res) {
  res.writeHead(200, { "Content-Type": "text/plain" })
  res.end("hello")
})
server.listen(0, function () { console.log(server.port) })

const res = await fetch("https://example.com")
console.log(res.status, await res.text())
```

Async I/O follows a **cross-goroutine I/O + post back to the single-threaded VM** model: network
reads/writes never touch VM state, and results come back through a zero-delay timer into the event
loop, so callbacks need no locking.

A complete HTTP example (server on port 0, routing/query params/JSON body, 404-405-500 handling,
callback-style `http.get`·`http.request` vs. Promise-style `fetch`, async responses and
`server.close`): [`testdata/http_demo.js`](testdata/http_demo.js) — run it with
`gox testdata/http_demo.js`.

### Digests and randomness: `crypto` / `hash`

`crypto` and `hash` are a global object / function (same shape as in browsers, no `import`), backed by
the Go standard library `crypto/*` — zero cgo. The same implementations are also registered as the
built-in module `gx/crypto` if you prefer `import`:

```js
hash("gox");                                 // sync -> lowercase hex (SHA-256 by default)
hash("gox", "SHA-512");                      // algorithm names are case/hyphen insensitive
hash(new Uint8Array([103, 111, 120]));       // accepts string / ArrayBuffer / TypedArray / byte array
hash([103, 111, 120]);                       // the return shape of fs.readBytesSync works too

const ab = await crypto.subtle.digest("SHA-256", "gox");   // Promise -> ArrayBuffer (32 bytes)
crypto.randomUUID();                                       // RFC 4122 v4
crypto.getRandomValues(new Uint8Array(16));                // fills in place, 64KB cap
```

Scope and boundaries:

| Item | Notes |
|---|---|
| Algorithms | `hash()` supports MD5 / SHA-1 / SHA-256 / SHA-512; `crypto.subtle.digest()` accepts **only SHA-1 / SHA-256 / SHA-512** (WebCrypto has no MD5 — code copied from the web should not silently pass here) |
| Argument order | `hash(data, algo)` but `crypto.subtle.digest(algo, data)` — the former is Gox sugar, the latter matches browsers |
| String encoding | UTF-8 (same as `TextEncoder.encode`) |
| Out of scope | Encryption / decryption / signing / KDF — those need a product decision on key management |
| Errors | Unsupported algorithms or arguments always raise `TypeError` (`digest` rejects the Promise); there is no silent fallback to another algorithm |

Full demo: [`testdata/crypto_demo.js`](testdata/crypto_demo.js) — `gox testdata/crypto_demo.js`.

### Native capability modules `gx/*`

`gx/device` / `gx/app` / `gx/geo` / `gx/media` / `gx/permission` / `gx/viewport` need `import`
(they are not globals). Three call shapes to keep apart when writing code:

```js
import { deviceInfo, battery, isOnline, canIUse } from "gx/device";   // pull-style + reported-style
import { getLocation, watchLocation } from "gx/geo";
import { takePhoto } from "gx/media";                                  // action-style

const info = deviceInfo();                    // available synchronously
console.log(info.platform, info.model, battery().level + "%", isOnline());
// battery()/isOnline() read a snapshot; useBattery() returns a getter that
// refreshes automatically when the host reports new values

if (canIUse("camera")) {                      // check the capability up front, don't rely on catch
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
open system settings), and reports `unsupported` honestly for the rest. **Mobile reuses the very same
`NativeHost` contract — no kernel changes needed**: the Android shell (Kotlin) and iOS shell (Swift)
have all six modules essentially complete — Android is verified on the emulator (display / touch /
IME / safe area / foldables), the iOS shell project and build scripts (including TestFlight packaging)
are in place with **on-device verification still to do**; the HarmonyOS shell (ArkTS) currently wires
only two pure reporting channels (safe area and fold reporting), the six capability modules are stubs.
Semantics, the 8 error codes and per-module export tables are in
[GUI guide §9.6](docs/gui-guide.md#96-原生能力层) (Chinese); the per-method (module × platform) status
table, shell build notes and first-frame self-check list are in
[`app/NATIVE-HOST.md`](app/NATIVE-HOST.md); a full demo of all three shapes:
[`testdata/native_demo.js`](testdata/native_demo.js).

**Per-platform verification scope (CI matrix wording)** — the capability matrix states only what is
actually verified, nothing more:

| Platform | Build smoke (every push) | Device regression | Foldable semantics |
|---|---|---|---|
| **Android** | `android` job in `mobile-smoke.yml`: libgox.so → assembleDebug APK | `android-fold-emulator` job: on a **pixel_fold emulator** (API 34+, the only AVD that really moves the hinge) it runs the CLOSED → HALF_FOLDED → OPENED matrix and archives screenshots; on a real device, `scripts/mobile-device-accept.sh --rounds 20` (§4 lifecycle loop) | ✅ Fold-state switching covered by the emulator (`cmd device_state`) |
| **iOS** | `ios` job in `mobile-smoke.yml`: xcodebuild simulator build | `ios-device-matrix` job: iPhone 16 / 16 Pro Max / iPad × portrait+landscape, simctl screenshots archived | ⚠️ **No foldables in the simulator**: `reservedRegions` (iOS 27.1+) reports flat only — a known boundary, not a bug; on-device verification still to do |
| **HarmonyOS** | Cross-compilation + contract tests + assembleHap build pass | — | ❌ Emulator verification still to do (**HAP signing is a hard blocker**: it must be configured with a Huawei account in the DevEco GUI; the CLI can't substitute). We do not claim "HarmonyOS supported". |

> "Run the form-factor matrix in CI and block merges on failure" is handled on the desktop side by the
> `foldable-matrix` job in `ci.yml` (fake-surface replay of 4 device forms + 20 lifecycle rounds +
> the four widths of this README's example + the three-platform permission matrix); the device side is
> covered by the two mobile-smoke jobs above. Which job is marked required is a branch-protection
> setting of the repository — code can't do it; the job keys are the names given above.

### GUI desktop apps

`gx/gfx` + `gx/solid` provide declarative JSX UI with signal-driven updates, rendered by a pure-Go
software rasterizer (no cgo, no dynamic library dependencies).

Scaffold a new project — what comes out runs out of the box, and the layout is exactly the
scaffold's default output:

```bash
gox create my-app                          # same as goxjs create / npx @goxjs/goxjs create
gox create my-app --ts                      # TypeScript/TSX flavor: .tsx runs directly, no node build
cd my-app && npm install && npm run dev
```

It generates `package.json`, the entry `src/main.js`, the root component `src/app.js`, shared state
`src/store.js`, design tokens `src/theme.js`, and three example components under `src/components/`
(counter / todo list / multi-state), demonstrating signals, element-level directives (`each` / `show`),
`model` two-way binding and `Switch` / `Match`. `--ts` generates the TypeScript flavor: the entry
becomes `src/main.tsx`, with `tsconfig.json` and `src/gox.d.ts` (IDE type hints); types are stripped
automatically at load time (embedded esbuild transform, JSX preserved as-is) — there is no node-side
build step, type checking is left to the IDE, and there is zero runtime cost.
The templates are **real files** under [`scaffold/template/`](scaffold/template/), embedded into the
binary with `go:embed` (template edits need a rebuild; `scaffold/scaffold_test.go` guarantees the
generated JS/TS still passes transform + lexer → parser → compiler, and
`gfx/scaffold_project_test.go` really mounts it and clicks through it).

The smallest hand-written version:

```js
import { h, render, createSignal } from "gox"        // aggregate entry: union of all gx/* exports
// equivalent split form: import { h, render } from "gx/gfx"; import { createSignal } from "gx/solid"

const [count, setCount] = createSignal(0)

render(
  <window title="Counter" width={400} height={300}>
    <column gap={8} padding={16}>
      <text font={20}>count: { count()}</text>
      <button onClick={() => setCount(c => c + 1)}>+1</button>
    </column>
  </window>
)
```

```bash
./gox counter.js          # run it directly: a 400x300 window pops up
```

Clicking the button → the signal updates → the props/text nodes depending on it are marked dirty →
dirty rectangles are merged and only the affected region is repainted. `render()` returns a window
handle and can be called several times for several windows (each with its own element tree and focus;
the process exits when all are closed).

Full reference — event model, built-in element property tables, flex layout and decoration drawing,
controlled components and IME, transition animations, element-level directives (`model` / `each` /
`show`) and `gx/view` multi-branch, **routing (`gx/router`: route table / parameter matching /
three-level guards / lazy loading / history stack / multi-window scope / foldable two-pane) and
screen info (`gx/screen`)**, native dialogs / clipboard / persistent storage / menu bar, multi-window
semantics and platform differences — see **[docs/gui-guide.md](docs/gui-guide.md)** (Chinese), and
runnable examples under [`testdata/`](testdata/) (`counter_demo.js`, `form_demo.js`,
`multiwindow_demo.js`, `menu_demo.js`, `view_demo.js`, `router_demo.js`, `router_window_demo.js`,
35+ in total).

Routing and screen adaptation have a dedicated manual: **[docs/gui-router.md](docs/gui-router.md)**.

### Packaging into a standalone executable

`jsbuild` (`packager/`) embeds the entry script and its relative `import`s into a generated Go
project and compiles it into a single-file executable with the full runtime inside:

```bash
go run ./packager app.js -o app.exe                    # CLI app
go run ./packager counter.js --gui -o counter.exe      # GUI app
go run ./packager app.js --gui --target linux/amd64    # pure-Go cross-compilation, no target toolchain
```

```
Usage: jsbuild <input.js> [options]

Options:
  -o, --out <path>     output file path (default: <input file name>.exe)
      --name <name>    app name (used in error messages; defaults to the input file name)
      --windowed       windowed mode: no console window (Windows only)
      --gui            GUI app: window message-pump event loop (for use with gx/gfx render)
      --target <os>/<arch>  cross-compilation target (windows|linux|darwin / amd64|arm64|386)
      --icon <path>    app icon (.png 1024 source or ready-made .ico/.icns):
                       Windows embeds icon+version resource (.syso), darwin produces an .app bundle
      --version <v>    version number (x.y.z; written into Windows version resource and .app Info.plist)
  -v, --verbose        show build progress
```

Per-platform distribution notes (Windows icons and signing, Linux packaging formats, macOS `.app`
bundles) are in [docs/desktop-distribution.md](docs/desktop-distribution.md).

> **Command-line arguments**: packaged binaries keep the same convention as `gox <file> <args>` —
> `process.argv` is always `[executable, script path, ...user args]`, so user args **always start at
> index 2** (the packaged binary has no host "script path" argument, so the runtime fills in the
> extracted entry path as a placeholder). Either way, `process.argv[2]` is the first user argument,
> both when running the source and when running the packaged build — which is what desktop tools
> expect for "drag a file onto the exe" or "launch with arguments".

### Multi-platform configuration (gox.json / permissions / icons)

A project created by `gox create` ships with `gox.json` (app name, appId, version, permissions,
per-platform sub-config) plus a full Android / iOS / desktop resource skeleton and a default icon:

- `gox sync` — idempotently injects the `permissions` from gox.json into AndroidManifest and the iOS
  Info.plist (always including the two baseline permissions `INTERNET` + `ACCESS_NETWORK_STATE`;
  undeclared logical permissions are not written);
- `gox icon` — generates Android mipmaps, the iOS AppIconSet, Windows `.ico`, macOS `.icns` and
  favicon from the single source `assets/icon.png` (1024×1024);
- `gox cert` — one-shot platform signing certificate generation (a convenience, pure Go, no
  JDK/openssl): `android` (PKCS12 keystore, picked up by gradle automatically), `windows`
  (self-signed code-signing .pfx), `harmony` (ECC P-256 debug .p12+.cer), `ios` (key pair + CSR, then
  `gox cert ios --cer x.cer` to compose the .p12). Output lands in `certs/` (gitignored); if you
  already have official certificates, configure them in the `cert` section of gox.json instead;
- `gox build windows|macos|android|ios` — unified build entry (sync → icon → platform packaging);
  `gox build android --release` produces a signed release package (certificate: gox.json `cert`
  section > `certs/` metadata > auto-generated debug certificate).

Field reference, permission list and icon spec: [docs/platform-config.md](docs/platform-config.md).

The script runtime permission model (`--allow-fs-write` etc., least privilege by default) is in
[docs/permissions.md](docs/permissions.md); measured compatibility with popular npm libraries is in
[docs/compat-matrix.md](docs/compat-matrix.md).

## Configuration

The runtime itself takes no config file: behaviour is driven entirely by **command-line arguments**, a
handful of **environment variables** and **in-script APIs**. Project-level settings (app name, appId,
version, permissions, icon) live in the `gox.json` generated by `gox create` — see
[Multi-platform configuration](#multi-platform-configuration-goxjson--permissions--icons).

### Command-line arguments

| Command | Argument | Description |
|---|---|---|
| `Gox` / `goxjs` | *(none)* | Start the interactive REPL |
| `Gox` / `goxjs` | `create <dir>` | Generate a GUI project from the default template (`--ts` for the TypeScript/TSX variant); aliases `new` / `init` |
| `Gox` / `goxjs` | `sync [dir]` | Idempotently inject the gox.json permission declarations into the Android / iOS manifests |
| `Gox` / `goxjs` | `icon [dir]` | Generate every platform's icon from a single 1024px source image |
| `Gox` / `goxjs` | `build <android\|ios\|windows\|macos>` | Unified build entry point (sync → icon → platform packaging) |
| `Gox` / `goxjs` | `<script.js>` | Run a script file; errors go to stderr and exit with a non-zero code |
| `Gox` / `goxjs` | `help` / `version` | Show usage / version |
| `go run ./packager` | see [Packaging](#packaging-into-a-standalone-executable) | jsbuild packager argument table |

`create` options: `--name <name>` (overrides the project name; defaults to the directory name), `-f` /
`--force` (only needed when the target directory already exists and is non-empty; overwriting is
refused by default). An argument is only treated as a subcommand when it **does not look like a
script path** (judged by extension and path separators), so `gox help.js` and `gox src/create.js`
still run as scripts.

Built-in REPL commands: `:help`, `:clear` (resets the global environment), `:exit` / `:quit`.

### Environment variables

| Variable | Effect | Default |
|---|---|---|
| `GOX_STORAGE_DIR` | Overrides the `gx/storage` data root; handy for tests and multi-instance isolation | empty (system config dir) |
| `GOOS` / `GOARCH` / `CGO_ENABLED` | Cross-compilation only, at build time (see `scripts/build-npm.sh`) | host platform |

### App-level settings

| Setting | How | Notes |
|---|---|---|
| App name / data dir | `setAppName("MyApp")` (`gx/storage`) | Data lands in `os.UserConfigDir()/Gox/<app name>` — `%AppData%` on Windows, `~/.config` on Linux, `~/Library/Application Support` on macOS; when unset it is derived from the script file name |
| Window title & size | JSX `<window title width height>` or `render(tree, {title, width, height})` | Defaults to `Gox` 400×300 |
| Fonts | Auto-detected per platform | Linux lazily scans system font dirs (`/usr/share/fonts`, `~/.local/share/fonts`, …) with CJK preferred, capped at 2000 entries |
| Theme / colors | Element attributes (`background` / `border` / `color`) | Named colors plus `#rgb` / `#rgba` / `#rrggbb` / `#rrggbbaa` / `rgb()` / `rgba()` |

### npm package information

| Item | Value |
|---|---|
| Package / command | `@goxjs/goxjs` → `goxjs` |
| Platforms shipped | macOS x64 / arm64, Linux x64 / arm64, Windows x64 |
| Node version | `>=14` (only used to distribute the binary; no runtime dependency on Node) |
| Version source of truth | the `version` field of [`npm/package.json`](npm/package.json) |

## Architecture

### Compile and execution pipeline

```
 .js source
    │  lexer        tokenization
    ▼
 Token stream
    │  parser       syntax analysis
    ▼
 AST (ast/)
    │  compiler     symbol table resolution + bytecode generation (constant pool)
    ▼
 Bytecode (bytecode/)   fixed-width 3 bytes: [opcode 1B][operand 2B big-endian]
    │  vm            stack-frame execution + timer event loop + module loading
    ▼
 Result
```

### Key design decisions

- **Explicit memory management, no GC** — object lifetimes are managed explicitly by the runtime, with
  dedicated handling for reference cycles.
- **Single-threaded VM + cross-goroutine scheduling** — network callbacks such as `http.createServer`
  are captured in goroutines and dispatched back onto the single VM thread, so no locking is needed.
  Pending tasks keep the event loop alive; the process may only exit once the count reaches zero.
- **Fixed-width instruction encoding** — no variable-length decode state machine, operands are read
  directly; the constant pool carries large object references.
- **Optional capabilities behind optional interfaces** — GUI backends are abstracted through
  `Surface` + `WindowFactory`, so swapping a backend needs no script changes; platform-only
  capabilities (IME, clipboard, native dialogs) are probed per backend and degrade silently instead
  of raising errors.

### Repository layout

| Directory | Responsibility |
|---|---|
| `lexer/` | Lexer (including JSX tokenization) |
| `parser/` | Parser producing the AST (including JSX desugaring) |
| `ast/` | AST node definitions |
| `compiler/` | AST → bytecode compiler (symbol table included) |
| `bytecode/` | Opcode and bytecode format definitions |
| `vm/` | Stack-based bytecode VM (call frames, module loading, timer scheduling) |
| `object/` | Runtime object system (Number / Array / Map / Promise / Observable…) |
| `runtime/` | Global Environment |
| `stdlib/` | Standard library and host API implementation (including `gx/solid` reactive signals) |
| `gfx/` | Home-grown GUI rendering layer (software rasterization, layout, hit testing, win32 / X11 / cocoa backends) |
| `packager/` | jsbuild packager (GUI apps, cross-compilation) |
| `scaffold/` | `gox create` project scaffolding: `template/` holds **real files** (`go:embed`-ed into the binary; `ts/` is the `--ts` TSX overlay), `scaffold.go` does placeholder substitution and directory validation |
| `tools/` | Dev tools and code generation (the five former root-level dirs icongen / certgen / tstransform / update / perf were consolidated here on 2026-10-12): `tstransform/` (TS/TSX → JS transpiling into the runtime, embeds the esbuild Go API, called when `vm` loads `.ts`/`.tsx`), `icongen/` (all-platform icon generation), `certgen/` (signing certificates), `update/` (self-update), `perf/` (frame-rate / GC benchmarks), `bcdump`/`initpurity`/`propdump`/`rssmon` (diagnostics) |
| `test/` | Test support: `bench/` profiling benchmarks (fib, function calls, object ops, number parsing) |
| `testdata/` | Directly runnable example scripts (language features, host APIs, GUI examples) |
| `docs/` | **Public documentation** (GUI guide, runtime API tutorial, distribution and release manuals) — process ledgers are maintained outside the repo by the maintainer and are not published with it |
| `npm/` | **Separate repo** [gox-npm](https://github.com/14752222/gox-npm) (auto-checked-out during CI builds): the **definition** of the `@goxjs/goxjs` npm package (`package.json` / `bin/gox.js` / package README); binaries are compiled on the fly by the release pipeline and never committed |
| `scripts/` | Build and check scripts: `build-npm.sh` cross-compiles all five platform binaries; `check-registries.py` verifies registry consistency (built-in components in four places / gx modules in three / version numbers / npm manifest), with `check-registries-selftest.py` as a negative self-test (`check-site.py` only accepted top-level `*.html` and became obsolete after the website moved to VitePress on 2026-09-25 — dead links are now caught by `vitepress build`); `check-imports.py` verifies that imports in docs and examples match the built-in module export tables (compile-error level + file-module silent-undefined level) |
| `.github/workflows/` | CI: `ci.yml` regular gate (registry consistency + `go build`/`vet`/`test`); `release.yml` release pipeline (push main / tag / Release → npm); `pages.yml` website publishing |
| `website/` | **Separate repo** [gox-website](https://github.com/14752222/gox-website) (auto-checked-out during CI builds): website source, published to GitHub Pages by `pages.yml` on a daily schedule or manual trigger |
| `gox-logo-concepts/` | **Separate repo** [gox-logo-concepts](https://github.com/14752222/gox-logo-concepts): logo drafts and website asset generation scripts (`make_assets.py`) |

## Development & testing

```bash
go test ./...          # run all tests (bytecode / compiler / lexer / object / parser / runtime / vm)
go run ./test/bench    # produce cpu.prof
```

> Because there is zero cgo, `go test -race` does not work here (Go requires cgo for it).

To work on the internals (adding standard library APIs, understanding the callback bridge and memory
management), read [docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md) (Chinese).

## Release process

`@goxjs/goxjs` is published automatically by GitHub Actions
([.github/workflows/release.yml](.github/workflows/release.yml)) using npm Trusted Publishing (OIDC),
so no secret or token is stored in the repo:

```bash
# 1. Bump the version — npm/package.json's version is the single source of truth; npm/ is a separate
#    repo (the local copy is its working tree):
$EDITOR npm/package.json
git -C npm commit -am "chore(release): 0.2.1" && git -C npm push
# 2. Tag and push in the main repo — the tag must match the version on gox-npm's default branch
#    (the decide gate cross-checks it)
git tag v0.2.1 && git push origin v0.2.1
# 3. CI does the rest: cross-compile the five platform binaries → smoke test → package verification
#    → npm publish --provenance, then tags v0.2.1 and creates a Release
```

- **Triggers**: pushes to `main`, `v*` tag pushes, published Releases, and manual
  `workflow_dispatch` (with an optional `dry_run` that builds and packages but does not upload)
- **Idempotent**: CI queries the registry first and skips with a notice when the version already
  exists — repeated pushes and re-runs of old workflow runs never go red and never double-publish
- **Versioning**: ordinary commits do not trigger a real publish, only versions missing from the
  registry are published; the tag must match `package.json` or the run fails outright
- **Manual publish** (emergency only, needs OTP):
  `bash scripts/build-npm.sh && cd npm && npm publish --access public`
- **Troubleshooting**: an OIDC failure and a misconfigured Trusted Publisher return the same
  misleading 404 — check the four fields on the npm side first (see the comment block at the top of
  the workflow); every run logs the npm version, whether `NODE_AUTH_TOKEN` is empty, and whether the
  OIDC endpoint is reachable

## Contributing

Issues and pull requests are welcome — the full participation guide, local gates, and this repo's
"change one place, sync several" discipline live in **[CONTRIBUTING.md](CONTRIBUTING.md)**; the code
of conduct is in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Good starting points are issues labelled
`good first issue`.

**Development environment**: Go 1.26.2+, zero cgo (no C toolchain required). Before committing:

```bash
gofmt -l .             # should print nothing
go build ./...
go vet ./...
go test ./...
python3 scripts/check-registries.py   # registry consistency (same gate as CI)
python3 scripts/check-imports.py      # imports in docs/examples match export tables (same gate as CI)
```

**Commit style**: follow the existing Conventional Commits style in the repo, with the scope set to
the affected module or topic, description in Chinese:

```
feat(gfx): 新增 <slider> 的键盘调节支持
fix(vm): 修正 SET_INDEX 对非对象类型静默丢弃的问题
docs(gui-guide): 补充多窗口事件泵的轮询语义
ci(release): 打包闸改回 tar 校验，不再解析 npm 的输出
```

**Adding a built-in GUI component requires syncing four places** (missing one fails silently, with no
compile error):

| Place | Purpose |
|---|---|
| `knownTags` in `gfx/node.go` | Register the tag name, otherwise `h()` prints an "unknown tag" warning |
| `intrinsicSize` in `gfx/layout.go` | Declare the intrinsic size |
| The layout dispatch in `gfx/layout.go` | Declare how children participate in layout |
| `drawNode` in `gfx/raster.go` | Implement drawing; tags intercepted by a `case` must draw their own background / border |

**Other conventions**:

- Event callbacks are always dispatched through `callHandler`; window-related operations resolve the
  owning window from a node via `appOfNode(n)` (multi-window aware)
- `gx/*` JSX attributes and children are evaluated **once at call time** — reactivity requires passing
  a **function** (`value: () => sig()`, and children as ``{() => sig()}`` instead of `{sig()}`);
  passing a value only takes a snapshot — and **children written as a snapshot fail totally
  silently** (attribute misuse warns, children do not), see [docs/gui-guide.md](docs/gui-guide.md)
  §8.1
- When adding standard library APIs, update
  [docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md) as well; open and unimplemented
  items are tracked by the maintainer in an out-of-repo process ledger (not published with the repo,
  contributors need not update it)
- Changes touching GUI components should also update [docs/gui-guide.md](docs/gui-guide.md) and the
  website component reference (the implementation ledger is maintained outside the repo by the
  maintainer)
- Put example scripts in `testdata/` and make sure they run directly

## Documentation

> The table below only lists documents under `docs/` that are **final and published**. Work-in-progress
> material such as technical proposals, development plans and status ledgers is maintained outside the
> repo and never pushed to the remote.

| Document | Contents |
|---|---|
| [docs/tutorial.md](docs/tutorial.md) | **Hands-on tutorial**: API calls and parameters, importing built-in modules, creating a project with `gox create`, route definition and registration (with runnable example scripts) |
| [docs/gui-guide.md](docs/gui-guide.md) | GUI guide: element / event reference, layout, animation, host capabilities, example index |
| [docs/platform-config.md](docs/platform-config.md) | Multi-platform configuration: gox.json fields, permission table, icon spec, `gox build` targets and on-device acceptance steps |
| [docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md) | Runtime API tutorial: function types, callback bridge, memory management, the full flow of adding an API |
| [docs/gui-patterns.md](docs/gui-patterns.md) | User-land pattern manual (routing, state, theming idioms) |
| [docs/gui-model-binding.md](docs/gui-model-binding.md) | `model` two-way binding: interface design, semantics table, comparison with Vue, counter-examples |
| [docs/foldable-adaptation.md](docs/foldable-adaptation.md) | **Foldable adaptation tutorial**: the pure-reader short-circuit subscription trap, why posture must not drive layout, hinge-avoidance unit conversion, Tabletop composition, three-platform verification scope |
| [docs/npm-release.md](docs/npm-release.md) | `@goxjs/goxjs` release manual: versioning policy, build steps, OIDC requirements, acceptance criteria |
| [docs/desktop-distribution.md](docs/desktop-distribution.md) | Desktop app distribution: icons, signing, per-platform package formats |
| [Website](https://14752222.github.io/Gox/) | Online tutorials for installing, running scripts, writing GUI apps and packaging |

## License

[MIT License](LICENSE)
