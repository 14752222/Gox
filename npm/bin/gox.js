#!/usr/bin/env node
// goxjs 命令入口: 检测平台后把参数原样转发给对应架构的 Gox 二进制。
var spawn = require('child_process').spawn;
var path = require('path');

// 二进制路径: 若未来拆出平台分包 (goxjs-darwin-arm64 等, 其 index.js 导出二进制路径)
// 则优先从分包取; 否则用本包自带的 binaries/ 目录。
var osName = process.platform === 'win32' ? 'windows' : process.platform;
var bin;
try {
  bin = require('goxjs-' + process.platform + '-' + process.arch);
} catch (_) {
  bin = path.join(__dirname, '..', 'binaries',
    osName + '-' + process.arch,
    process.platform === 'win32' ? 'gox.exe' : 'gox');
}

var child = spawn(bin, process.argv.slice(2), { stdio: 'inherit' });
child.on('error', function (err) {
  console.error('goxjs: 无法启动平台二进制 (' + err.message + ')');
  console.error('请尝试重新安装: npm i -g @goxjs/goxjs');
  process.exit(1);
});
child.on('close', function (code, signal) {
  if (signal) process.kill(process.pid, signal);
  else process.exit(code == null ? 0 : code);
});
