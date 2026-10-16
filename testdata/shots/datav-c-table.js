// 画廊截图: gx-datav 单组件 —— ScrollTable（轮播表格 (无缝循环)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { ScrollTable, palette } from "../datav/index.js";

const COLUMNS = [
  { key: "id", label: "订单号", width: 1.2 },
  { key: "region", label: "大区", width: 0.8 },
  { key: "ch", label: "渠道", width: 1 },
  { key: "amt", label: "金额 (元)", width: 1, align: "right" },
  { key: "st", label: "状态", width: 0.8, align: "right" },
];
const ROWS = [
  { id: "A-10241", region: "华南", ch: "线上商城", amt: 128900, st: "已完成" },
  { id: "A-10242", region: "华东", ch: "APP 推送", amt: 56200, st: "进行中" },
  { id: "A-10243", region: "华北", ch: "线下门店", amt: 93400, st: "已完成" },
  { id: "A-10244", region: "西南", ch: "第三方", amt: 27800, st: "待付款" },
  { id: "A-10245", region: "西北", ch: "线上商城", amt: 76100, st: "已完成" },
];

render(
  <window title="gx-datav · ScrollTable" width={760} height={340} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <ScrollTable width={680} height={220} rowHeight={28} columns={COLUMNS} rows={ROWS}/>
      </row>
    </column>
  </window>
);
