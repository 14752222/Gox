// testdata/ts/decorator.ts —— legacy 装饰器 (需 esbuild 降级成 JS, parser 不认 @)。

function tag(target: any): any {
  target.tagged = true;
  return target;
}

@tag
class Widget {
  label: string = "w";
}

new Widget().label;
