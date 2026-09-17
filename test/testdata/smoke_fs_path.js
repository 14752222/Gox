// fs / path / process 冒烟测试
const FILE = "smoke_test.txt";

fs.writeFileSync(FILE, "hello fs\nsecond line");
console.log("read:", JSON.stringify(fs.readFileSync(FILE)));
console.log("read base64:", fs.readFileSync(FILE, "base64"));
console.log("exists:", fs.existsSync(FILE), "missing:", fs.existsSync("no_such.txt"));

fs.appendFileSync(FILE, "\nappended");
console.log("after append:", fs.readFileSync(FILE).split("\n").length, "lines");

fs.writeFileSync(FILE, [0x41, 0x42, 0x43]);
console.log("bytes roundtrip:", fs.readFileSync(FILE), "byte[0]:", fs.readBytesSync(FILE)[0]);

fs.mkdirSync("smoke_dir/nested", { recursive: true });
fs.writeFileSync("smoke_dir/nested/a.txt", "A");
fs.writeFileSync("smoke_dir/b.txt", "B");
console.log("readdir:", fs.readdirSync("smoke_dir").sort().join(","));
console.log("stat file:", fs.statSync("smoke_dir/b.txt").isFile, "size:", fs.statSync("smoke_dir/b.txt").size);
console.log("stat dir:", fs.statSync("smoke_dir").isDirectory);

fs.renameSync("smoke_dir/b.txt", "smoke_dir/nested/b2.txt");
fs.copyFileSync("smoke_dir/nested/a.txt", "smoke_dir/a_copy.txt");
console.log("after rename/copy:", fs.readdirSync("smoke_dir").sort().join(","));

// 异步 API: 回调风格
fs.writeFile("smoke_async.txt", "async data", function (err) {
  if (err) { console.log("write error:", err.message); return; }
  fs.readFile("smoke_async.txt", function (err2, data) {
    console.log("cb read:", data, "err is null:", err2 === null);
    fs.unlink("smoke_async.txt", function () {
      console.log("async unlink ok");
      finish();
    });
  });
});

// 异步 API: Promise / await 风格
let asyncDone = false;
async function asyncPart() {
  const data = await fs.readFile(FILE);
  const st = await fs.stat("smoke_dir");
  try {
    await fs.readFile("definitely_missing.txt");
  } catch (e) {
    console.log("await reject caught:", e.name);
  }
  asyncDone = data.length > 0 && st.isDirectory;
  console.log("await fs.readFile len:", data.length, "stat dir:", st.isDirectory);
}

// path / process
console.log("path.join:", path.join("a", "b", "c.txt"));
console.log("path.dirname:", path.dirname("/x/y/z.js"), "basename:", path.basename("/x/y/z.js"));
console.log("path.extname:", path.extname("archive.tar.gz"), "|", path.extname(".bashrc"));
console.log("path.isAbsolute:", path.isAbsolute(path.resolve("rel.txt")));
console.log("process.platform:", process.platform, "argv len:", process.argv.length >= 1);
console.log("process.env has PATH:", "PATH" in process.env || "Path" in process.env);

asyncPart();

let finished = false;
function finish() {
  if (finished) { return; }
  finished = true;
  fs.rmSync("smoke_dir", { recursive: true, force: true });
  fs.rmSync(FILE, { force: true });
  console.log("cleanup done; asyncDone:", asyncDone);
  console.log("FS-SMOKE-OK");
}
