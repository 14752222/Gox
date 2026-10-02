// testdata/ts/annotations.ts —— 类型注解 / interface / 泛型 / as 断言的基本面。

interface Point {
  x: number;
  y: number;
}

type Vec = Point;

function dist(p: Point): number {
  return Math.sqrt(p.x * p.x + p.y * p.y);
}

const p: Vec = { x: 3, y: 4 };
dist(p);
