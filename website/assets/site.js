/* Gox 官网脚本 —— 迷你 JS/Bash 高亮、代码复制、移动端菜单、目录滚动监听。零依赖。 */
(function () {
  "use strict";

  /* ---------- HTML 转义 ---------- */
  function esc(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  /* ---------- JS 迷你高亮 ----------
     用一条有序分支正则做词法切分(注释 > 字符串 > 数字 > 关键字...)，
     命中的片段包 span,其余原样转义。JSX 标签按 <tag 前缀粗略着色。 */
  var JS_KEYWORDS = new Set((
    "let const function return if else for of in while do break continue class extends new this super " +
    "async await try catch finally throw typeof instanceof import export from default switch case " +
    "yield delete void static get set"
  ).split(" "));

  var JS_LITERALS = new Set("true false null undefined NaN Infinity".split(" "));

  var JS_BUILTINS = new Set((
    "console Math JSON Object Array String Number Boolean Map Set WeakMap WeakSet Symbol BigInt RegExp " +
    "Proxy Reflect Iterator Promise ArrayBuffer DataView WeakRef FinalizationRegistry Temporal Intl " +
    "Error TypeError RangeError SyntaxError ReferenceError AggregateError " +
    "fs http path process stats fetch setTimeout setInterval clearTimeout clearInterval " +
    "setStrictTimeout setStrictInterval clearStrictTimeout clearStrictInterval setStrictIntervalMode " +
    "requestIdleCallback cancelIdleCallback delay obs computed ever once " +
    "createSignal createEffect createMemo createResource onMount onCleanup untrack devStats " +
    "h window render requestAnimationFrame animate " +
    "clipboardReadText clipboardWriteText openContextMenu " +
    "Switch Match createRouter RouterView RouterLink lazy useRoute useRouter useRouteState " +
    "screens primaryScreen screen screenOf useScreen useScreens windowInfo useWindowInfo " +
    "posture usePosture hinge regions reportPosture resetDisplays onDisplayChange offDisplayChange " +
    "setAppName appDataDir setStorage getStorage removeStorage clearStorage getStorageInfo " +
    "alert confirm openFile devSnapshot"
  ).split(" "));

  /* JSX 内置标签:JSX 分支只会把 <tag 前缀标成 tk-tag,这里再把已知标签名
     细分成 tk-tag(已知) —— 未知标签保持同色,不额外制造视觉噪音。 */
  var JSX_TAGS = new Set((
    "column row text rect button checkbox radio switch progress separator spacer " +
    "select input textarea scroll image canvas slider dialog toast " +
    "menubar menu menuitem window view"
  ).split(" "));

  var JS_RE = new RegExp(
    [
      "(\\/\\/[^\\n]*|\\/\\*[\\s\\S]*?\\*\\/)",                                  // 1 注释
      "(`(?:\\\\.|[^`\\\\])*`|'(?:\\\\.|[^'\\\\])*'|\"(?:\\\\.|[^\"\\\\])*\")", // 2 字符串
      "(<\\/?[A-Za-z][\\w.-]*)",                                                  // 3 JSX 标签名
      "(\\b0[xX][0-9a-fA-F]+\\b|\\b\\d[\\d_]*(?:\\.\\d+)?(?:[eE][+-]?\\d+)?\\b)", // 4 数字
      "([A-Za-z_$][\\w$]*)(?=\\s*\\()",                                          // 5 函数调用名
      "([A-Za-z_$][\\w$]*)"                                                       // 6 标识符
    ].join("|"),
    "g"
  );

  function highlightJS(code) {
    var out = "", last = 0, m;
    JS_RE.lastIndex = 0;
    while ((m = JS_RE.exec(code)) !== null) {
      out += esc(code.slice(last, m.index));
      var cls = null, text = m[0];
      if (m[1] !== undefined) cls = "tk-cmt";
      else if (m[2] !== undefined) cls = "tk-str";
      else if (m[3] !== undefined) {
        // 已知 JSX 标签名单独着色,未知标签保持默认(不制造噪音)
        var tagName = text.replace(/^<\/?/, "");
        cls = JSX_TAGS.has(tagName) ? "tk-tag" : null;
      }
      else if (m[4] !== undefined) cls = "tk-num";
      else if (m[5] !== undefined) cls = JS_KEYWORDS.has(text) ? "tk-kw" : "tk-fn";
      else if (m[6] !== undefined) {
        if (JS_KEYWORDS.has(text)) cls = "tk-kw";
        else if (JS_LITERALS.has(text)) cls = "tk-lit";
        else if (JS_BUILTINS.has(text)) cls = "tk-blt";
      }
      out += cls ? '<span class="' + cls + '">' + esc(text) + "</span>" : esc(text);
      last = m.index + text.length;
    }
    out += esc(code.slice(last));
    return out;
  }

  /* ---------- Bash 迷你高亮:注释行 + "$ " 提示符后的首个命令 ---------- */
  function highlightBash(code) {
    return code.split("\n").map(function (line) {
      if (/^\s*#/.test(line)) return '<span class="tk-cmt">' + esc(line) + "</span>";
      var mm = line.match(/^(\s*\$\s+)([A-Za-z_][\w.]*)([\s\S]*)$/);
      if (mm) {
        return '<span class="tk-blt">' + esc(mm[1]) + '</span><span class="tk-fn">' + esc(mm[2]) + "</span>" + esc(mm[3]);
      }
      return esc(line);
    }).join("\n");
  }

  /* ---------- 应用到页面上的 pre>code ---------- */
  function highlightAll(root) {
  (root || document).querySelectorAll("pre.code > code").forEach(function (code) {
    var pre = code.parentElement;
    var lang = (code.getAttribute("data-lang") || pre.getAttribute("data-lang") || "js").toLowerCase();
    var src = code.textContent;
    var html;
    if (lang === "bash" || lang === "sh") html = highlightBash(src);
    else if (lang === "text" || lang === "plain" || lang === "output") html = esc(src);
    else html = highlightJS(src);
    code.innerHTML = html;
    addCopyButton(pre, code, src);
  });
  }

  /* ---------- 复制按钮 ---------- */
  function addCopyButton(pre, code, rawText) {
    if (pre.querySelector(".copy-btn")) return;
    var btn = document.createElement("button");
    btn.className = "copy-btn";
    btn.type = "button";
    btn.textContent = "复制";
    btn.addEventListener("click", function () {
      var done = function () {
        btn.textContent = "已复制 ✓";
        btn.classList.add("done");
        setTimeout(function () { btn.textContent = "复制"; btn.classList.remove("done"); }, 1600);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(rawText).then(done, done);
      } else {
        var ta = document.createElement("textarea");
        ta.value = rawText;
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand("copy"); } catch (e) { /* 忽略 */ }
        document.body.removeChild(ta);
        done();
      }
    });
    pre.appendChild(btn);
  }

  /* ---------- 移动端菜单 ---------- */
  var toggle = document.querySelector(".nav-toggle");
  var links = document.querySelector(".nav-links");
  if (toggle && links) {
    toggle.addEventListener("click", function () { links.classList.toggle("open"); });
    links.addEventListener("click", function (e) {
      if (e.target.tagName === "A") links.classList.remove("open");
    });
  }

  /* ---------- 教程页:目录滚动监听 + h2 锚点 ---------- */
  var tocLinks = Array.prototype.slice.call(document.querySelectorAll(".toc a"));
  if (tocLinks.length) {
    var targets = tocLinks.map(function (a) {
      return document.getElementById(a.getAttribute("href").slice(1));
    }).filter(Boolean);

    // h2 追加悬浮锚点链接
    document.querySelectorAll(".doc h2[id]").forEach(function (h2) {
      var a = document.createElement("a");
      a.className = "anchor";
      a.href = "#" + h2.id;
      a.textContent = "#";
      h2.appendChild(a);
    });

    var activate = function (id) {
      tocLinks.forEach(function (a) {
        a.classList.toggle("active", a.getAttribute("href") === "#" + id);
      });
    };

    // 滚动时直接计算:取最后一个已越过标题带的章节,避免
    // IntersectionObserver 平滑滚动下只报"变化"导致的旧值残留
    var spyUpdate = function () {
      var band = 120; // 标题越过视口顶部 120px 即视为"当前章节"(锚点定位在 84px)
      var currentId = targets[0] ? targets[0].id : null;
      for (var i = 0; i < targets.length; i++) {
        if (targets[i].getBoundingClientRect().top <= band) currentId = targets[i].id;
      }
      if (currentId) activate(currentId);
    };

    var ticking = false;
    window.addEventListener("scroll", function () {
      if (!ticking) {
        ticking = true;
        requestAnimationFrame(function () { spyUpdate(); ticking = false; });
      }
    }, { passive: true });
    spyUpdate();
  }

  /* ---------- 导航当前页高亮 ---------- */
  var page = (location.pathname.split("/").pop() || "index.html").toLowerCase();
  document.querySelectorAll(".nav-links a[data-page]").forEach(function (a) {
    if (a.getAttribute("data-page") === page) a.classList.add("active");
  });

  document.addEventListener("DOMContentLoaded", function () { highlightAll(); });
  if (document.readyState !== "loading") highlightAll();
})();
