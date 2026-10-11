// 应用状态：当前城市 + 网络状态。signal 用索引取值（引擎纪律）。
import { createSignal } from "gox";
import { useNetwork } from "gx/device";
import { cityByName } from "./lib/weather.js";

const cityP = createSignal("深圳");
const city = cityP[0];

const network = useNetwork();

const setCity = (name) => {
  cityP[1](name);
}

// data 当前城市的整组天气数据（派生）。
const data = () => {
  return cityByName(city());
}

// online 离线判定：宿主不支持 network 上报时按在线处理（不吓用户）。
const online = () => {
  const n = network() || {};
  return n.online !== false;
}

export { city, setCity, data, online };
