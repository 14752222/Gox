// 画廊截图: <form> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/form.js
//
// form 是"回车提交 + 整表取值"的归属节点; 只收带 name 的字段。
// 焦点落在 input 里按 Enter 即提交 —— 截图拍不到交互, 拍的是排版:
// 标签列 (label width 对齐) + 字段列 + 提交按钮。
import { createSignal, render } from "gox";

const [name, setName] = createSignal("植球");
const [city, setCity] = createSignal("sh");
const [note, setNote] = createSignal("");
const [saved, setSaved] = createSignal("(还没提交)");

render(
  <window title="form" width={400} height={360}>
    <column gap={10} padding={16}>
      <form gap={10} onSubmit={(e) => setSaved(JSON.stringify(e.values))}>
        <row gap={8} alignItems="center">
          <label width={72} required>姓名</label>
          <input width={180} name="name" model={name} />
        </row>

        <row gap={8} alignItems="center">
          <label width={72}>城市</label>
          <select
            width={180}
            name="city"
            options={[{ value: "sh", label: "Shanghai" }, { value: "bj", label: "Beijing" }]}
            model={city}
          />
        </row>

        <row gap={8} alignItems="center">
          <label width={72}>备注</label>
          <textarea width={180} rows={2} name="note" model={note} />
        </row>

        <row gap={8}>
          <label width={72}> </label>
          <button onClick={() => {}}>保存</button>
        </row>
      </form>

      <text font={12} color="#4a5560">{() => "提交结果: " + saved()}</text>
    </column>
  </window>
);
