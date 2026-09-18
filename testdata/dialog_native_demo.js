// P3-4 演示: 原生系统对话框 (alert / confirm / openFile)。
// 运行: go run . testdata/dialog_native_demo.js
//
// 现象:
//   - 点 "Alert" → 弹系统消息框 (只有一个"确定"), 关掉后日志追加一行;
//   - 点 "Confirm" → 弹"确定/取消", 日志按你的选择写 yes/no;
//   - 点 "Open file" → 弹系统"打开文件"对话框 (带类型下拉框),
//     取消得到 null, 选中得到完整路径;
//   - 三个都是 **await** 的: 对话框关掉之前, 下面那行代码不会执行。//
// 四个容易踩的点:
//   - **这三个 API 是 async 的** (返回 Promise), 要用 await 或 .then。
//     与剪贴板那两个同步函数不同 —— 模态对话框会挂住等到用户作答,
//     而剪贴板是即时调用。
//   - **模态期间界面仍然响应**: 消息框以主窗口为 owner, Windows 会自动
//     替我们泵消息 (重绘、拖动都正常), 所以不需要自己写循环。
//   - **取消不是错误**: openFile 取消返回 null (与浏览器 File System
//     Access API 一致), 不是抛异常 —— 不必写 try/catch。
//   - **非 Windows 后端会降级**: 没有原生对话框能力时, 内容打到 stderr
//     并立刻返回 (confirm 取"确定", openFile 取"取消"), 不会挂住。
//
// 注意: 这个演示在自动化测试里由**假后端**应答 (见 gfx/dialog_test.go),
// 不会真的弹框。
//
// 语法提示: 运行时**只支持 `async function`**, 不支持 `async () => {}`
// (parser/parser.go 的 parseAsyncExpression 显式降级报错)。所以下面写成
// `async function () { ... }` —— 匿名 async 函数表达式, 等价可用。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { alert, confirm, openFile } from "gx/dialog";

const [log, setLog] = createSignal("(nothing yet)");
const push = (line) => setLog((prev) => (prev === "(nothing yet)" ? line : prev + "\n" + line));

render(
  h("column", { gap: 10, padding: 16 },
    h("text", { font: 14, width: 420 }, "P3-4 native dialogs (async)"),

    h("row", { gap: 8 },
      h("button", {
        onClick: async function () {
          await alert("This is a native message box.", "Hello");
          push("alert closed");
        }
      }, "Alert"),

      h("button", {
        onClick: async function () {
          const yes = await confirm("Proceed with the operation?", "Please confirm");
          push("confirm -> " + (yes ? "yes" : "no"));
        }
      }, "Confirm"),

      h("button", {
        onClick: async function () {
          const path = await openFile({
            title: "Pick a file",
            filter: [
              { name: "Text files", pattern: "*.txt;*.md" },
              { name: "All files", pattern: "*.*" }
            ]
          });
          if (path === null) {
            push("openFile cancelled");
          } else {
            push("openFile -> " + path);
          }
        }
      }, "Open file")
    ),

    h("separator", { height: 1 }),
    h("text", { font: 12, width: 460 }, "log:"),
    h("text", { font: 12, width: 460, wrap: true, color: "#555555" }, () => log())
  ),
  { title: "Native dialog demo", width: 480, height: 260 }
);
