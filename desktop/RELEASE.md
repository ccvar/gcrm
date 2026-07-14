# GCRM Pilot 发布手册

## 发一个新版本

1. 同步版本号：`desktop/package.json` 和 `desktop/src-tauri/tauri.conf.json` 的 `version`
   改成同一个值（如 `0.2.1`）。CI 不校验一致性，忘改的话应用「关于」里版本会对不上。
2. 打 tag 推送：

```bash
git tag pilot-v0.2.1 && git push origin pilot-v0.2.1
```

流水线（.github/workflows/pilot-release.yml）会在 macOS + Windows runner 各自原生打包，
发布到本仓库 Release：`GCRM-Pilot_<ver>_aarch64.dmg` + `GCRM-Pilot_<ver>_x64-setup.exe`。

## 在线更新（应用内自动升级）

架构与 GCMS Pilot 相同（ed25519 签名 + latest.json），但为单仓库方案：
updater 端点是固定滚动 tag **`pilot-latest`** 的 `latest.json` ——
不能用 GitHub 的 `releases/latest`，那会被服务端 `v*` 发布（make_latest: true）顶掉，
这正是 GCMS 拆两个仓库的原因，单仓库用固定 tag 绕开。

### 一次性配置（不做则发布只有安装包、无在线更新）

1. 私钥在 `~/.ccvar/crm-pilot-updater.key`（生成于 2026-07-14，**务必备份**——
   丢了老用户就永远收不到自动更新，只能换公钥重装）。
2. 仓库 Settings → Secrets and variables → Actions → New repository secret：
   - Name：`TAURI_SIGNING_PRIVATE_KEY`
   - Value：`~/.ccvar/crm-pilot-updater.key` 的**文件内容**（整段 base64 文本）
3. 之后每次 `pilot-v*` 发布会自动：产出签名的更新工件（macOS `.app.tar.gz`、
   Windows `-setup.exe`）→ 合成双平台 `latest.json` → 更新到 `pilot-latest`。
   已装用户启动时静默检查（之后每 6 小时一次），顶栏出现「更新到 x.y.z」按钮，
   点击下载显示进度，装完自动重启。

公钥内置在 `tauri.conf.json` 的 `plugins.updater.pubkey`，与私钥配对。
未配置 secret 时流水线打 `::warning` 并跳过更新工件，安装包发布不受影响。

## macOS 首装放行

未做 Apple 公证（ad-hoc 签名），从网上下载的 dmg 首次打开会报「已损坏」，放行一次即可：

```bash
xattr -cr "/Applications/GCRM Pilot.app"
```

updater 安装的后续升级不带 quarantine 属性，无需再放行。

## 本地打包

```bash
./scripts/gcrm.sh pilot-build   # 或 Windows: .\scripts\gcrm.ps1 pilot-build
```
