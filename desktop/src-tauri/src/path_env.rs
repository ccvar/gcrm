// GUI 进程的 PATH / 代理环境修复（承自 GCMS Pilot path_env.rs）。
//
// macOS GUI 应用拿到的是登录时的裸 PATH，看不到 Homebrew / ~/.local/bin 里的
// claude/codex CLI；Windows GUI 进程持有的是启动时的 PATH 快照。必须在应用
// 启动最早期修复一次，手动「重新检测」时再修复一次（登录 shell 有成本，不轮询）。

#[cfg(target_os = "macos")]
const MARK: &str = "__CRM_PILOT_ENV__";

/// 白名单导入：只要 PATH 和代理变量，整包导入会把 shell 私货带进 GUI 进程。
#[cfg(target_os = "macos")]
const IMPORT_KEYS: &[&str] = &[
    "PATH",
    "HTTP_PROXY", "http_proxy",
    "HTTPS_PROXY", "https_proxy",
    "ALL_PROXY", "all_proxy",
    "NO_PROXY", "no_proxy",
];

pub fn fix() {
    #[cfg(target_os = "macos")]
    fix_macos();
    #[cfg(target_os = "windows")]
    fix_windows();
}

#[cfg(target_os = "macos")]
fn fix_macos() {
    let Some(env) = login_shell_env() else { return };
    for (k, v) in env {
        if IMPORT_KEYS.contains(&k.as_str()) && !v.is_empty() {
            std::env::set_var(&k, &v);
        }
    }
}

/// 起一次交互登录 shell（-l 读 .zprofile，-i 读 .zshrc），用前后标记行
/// 夹逼截取 env 输出 —— rc 文件可能自带 echo，直接解析整个 stdout 会被污染。
#[cfg(target_os = "macos")]
fn login_shell_env() -> Option<Vec<(String, String)>> {
    let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".into());
    let cmd = format!("printf '%s\\n' \"{MARK}\"; env; printf '%s\\n' \"{MARK}\"");
    let out = std::process::Command::new(shell)
        .args(["-l", "-i", "-c", &cmd])
        .output()
        .ok()?;
    let stdout = String::from_utf8_lossy(&out.stdout);
    let start = stdout.find(MARK)? + MARK.len();
    let end = stdout.rfind(MARK)?;
    if end <= start {
        return None;
    }
    let mut pairs = Vec::new();
    for line in stdout[start..end].lines() {
        if let Some((k, v)) = line.split_once('=') {
            pairs.push((k.to_string(), v.to_string()));
        }
    }
    Some(pairs)
}

/// Windows：GUI 进程看不到安装 CLI 后更新的系统 PATH，从注册表读
/// User + Machine 两份合并，再补 npm 全局目录。
#[cfg(target_os = "windows")]
fn fix_windows() {
    let read = |scope: &str| -> String {
        use std::os::windows::process::CommandExt;
        std::process::Command::new("powershell")
            .args([
                "-NoProfile",
                "-Command",
                // 先切 UTF-8 输出再打印：PowerShell 5.1 管道输出默认走 OEM 代码页（中文系统
                // GBK），按 UTF-8 解码会把含中文的 PATH（如 C:\Users\张三\.local\bin）变乱码
                &format!("[Console]::OutputEncoding=[Text.Encoding]::UTF8; [Environment]::GetEnvironmentVariable('Path','{scope}')"),
            ])
            .creation_flags(0x0800_0000) // CREATE_NO_WINDOW：别在启动/重检时闪黑窗
            .output()
            .ok()
            .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
            .unwrap_or_default()
    };
    let mut parts: Vec<String> = Vec::new();
    for chunk in [read("User"), read("Machine")] {
        for p in chunk.split(';') {
            let p = p.trim();
            if !p.is_empty() {
                parts.push(p.to_string());
            }
        }
    }
    if let Ok(appdata) = std::env::var("APPDATA") {
        parts.push(format!("{appdata}\\npm"));
    }
    if let Ok(cur) = std::env::var("PATH") {
        for p in cur.split(';') {
            if !p.trim().is_empty() {
                parts.push(p.trim().to_string());
            }
        }
    }
    // 新目录在前，按小写去重
    let mut seen = std::collections::HashSet::new();
    let merged: Vec<String> = parts
        .into_iter()
        .filter(|p| seen.insert(p.to_lowercase()))
        .collect();
    std::env::set_var("PATH", merged.join(";"));
}
