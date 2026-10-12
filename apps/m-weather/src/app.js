// 根组件：顶部城市切换 + 离线 banner + 当前卡 + 24h 曲线 + 7 天列表。
import { h } from "gox";
import { useInsets } from "gx/viewport";
import { colors, space, font } from "./theme.js";
import { city, setCity, data, online } from "./store.js";
import { cityNames, tempPoints } from "./lib/weather.js";

const insets = useInsets();

// CurrentCard: 当前天气大卡。
const CurrentCard = () => {
  return (
    <column gap={space.xs} padding={space.md} alignItems="center">
      <text font={font.xl} color={colors.text}>{() => data().current.temp + "°"}</text>
      <text font={font.lg} color={colors.text}>{() => data().current.desc}</text>
      <text font={font.sm} color={colors.muted}>
        {() => "湿度 " + data().current.humidity + "% · " + data().current.wind}
      </text>
    </column>
  );
}

// HourlyChart: 24 小时温度曲线。
const HourlyChart = () => {
  return (
    <column gap={space.xs} padding={space.sm}>
      <text font={font.sm} color={colors.muted}>24 小时</text>
      <canvas
        width={366}
        height={100}
        onDraw={(ctx) => {
          const pts = tempPoints(data().hourly, ctx.width, ctx.height);
          for (let i = 1; i < pts.length; i++) {
            ctx.line(pts[i - 1].x, pts[i - 1].y, pts[i].x, pts[i].y, colors.line);
          }
          for (let i = 0; i < pts.length; i += 3) {
            ctx.fillCircle(pts[i].x, pts[i].y, 2, colors.line);
          }
          const temps = data().hourly;
          for (let i = 0; i < pts.length; i += 6) {
            ctx.drawText(temps[i] + "°", pts[i].x - 8, ctx.height - 12, 10, colors.muted);
          }
        }}
      />
    </column>
  );
}

// DailyRow: 一天预报。
const DailyRow = (p) => {
  const d = p.d;
  return (
    <row gap={space.sm} padding={space.sm} alignItems="center">
      <text width={60} font={font.md} color={colors.text}>{d.day}</text>
      <text width={60} font={font.sm} color={colors.muted}>{d.desc}</text>
      <spacer flexGrow={1} />
      <text font={font.sm} color={colors.muted}>{d.low + "°"}</text>
      <text width={40} font={font.md} color={colors.text}>{d.high + "°"}</text>
    </row>
  );
}

export function App() {
  return (
    <column background={colors.bg}>
      <rect width={390} height={() => insets().top} background={colors.bg} />
      <view show={() => !online()}>
        <row padding={space.sm} background={colors.warnBg}>
          <text font={font.sm} color={colors.warnText}>当前离线，展示的是缓存数据</text>
        </row>
      </view>
      <row gap={space.sm} padding={space.sm} alignItems="center">
        <text font={font.lg} color={colors.text}>天气</text>
        <spacer flexGrow={1} />
        <select value={city} options={cityNames()} onChange={(e) => setCity(e.value)} />
      </row>
      <scroll width={390} height={560}>
        <column gap={space.sm} padding={space.sm}>
          <CurrentCard />
          <HourlyChart />
          <column>
            <text font={font.sm} color={colors.muted} padding={space.sm}>7 天预报</text>
            <view each={() => data().daily} key="day">
              {(d) => <DailyRow d={d} />}
            </view>
          </column>
        </column>
      </scroll>
    </column>
  );
}
