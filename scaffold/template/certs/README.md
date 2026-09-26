# certs/ —— 平台签名证书目录

这个目录存放各平台签名证书（`gox cert` 一键生成，或自备证书放这里）：

```
gox cert android   →  android.keystore + android-cert.json（别名/密码元数据）
gox cert windows   →  windows.pfx + windows-cert.json
gox cert harmony   →  harmony.p12 + harmony.cer + harmony-cert.json
gox cert ios       →  ios.key.pem + ios.csr（上传 Apple 换 .cer 后:
                      gox cert ios --cer <下载的.cer> 合成 ios.p12）
```

- **整个目录已被 .gitignore 排除** —— 证书与 `*-cert.json` 元数据含私钥/密码，
  严禁提交仓库
- `gox build android --release` 会自动读取这里的签名配置（自备证书也可在
  gox.json 的 `cert` 段指定路径，见 docs/platform-config.md）
- 换证书 = 安卓应用无法覆盖安装，正式发布前请确定最终 keystore

各平台证书的适用边界（自签只够调试，上架要求见 README / docs/platform-config.md）。
