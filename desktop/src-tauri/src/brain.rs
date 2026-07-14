// 本地大脑基础设施（承自 GCMS Pilot brains.rs）：
// CLI 检测、登录状态、去授权引导、进程组击杀。对话执行在 chat.rs。

use serde::Serialize;
use tauri::{AppHandle, Manager};

// ---------- CLI 检测 ----------

#[derive(Serialize, Clone)]
pub struct BrainStatus {
    pub id: String,
    pub found: bool,
    pub path: String,
    pub version: String,
    pub logged_in: Option<bool>,
    pub detail: String,
}

/// 自实现 which：GUI 环境不能信任外部 which 命令（PATH 同样是坏的）。
/// Windows 上 npm 装的是 .cmd，裸名 CreateProcess 只补 .exe 永远找不到，
/// 必须处理 PATHEXT 并返回完整路径供 spawn 使用。
fn which(bin: &str) -> Option<std::path::PathBuf> {
    let path = std::env::var("PATH").ok()?;
    let sep = if cfg!(windows) { ';' } else { ':' };
    let exts: Vec<String> = if cfg!(windows) {
        std::env::var("PATHEXT")
            .unwrap_or_else(|_| ".EXE;.CMD;.BAT;.COM".into())
            .split(';')
            .map(|e| e.to_lowercase())
            .collect()
    } else {
        vec![String::new()]
    };
    for dir in path.split(sep).filter(|d| !d.is_empty()) {
        for ext in &exts {
            let p = std::path::Path::new(dir).join(format!("{bin}{ext}"));
            if p.is_file() {
                return Some(p);
            }
        }
    }
    None
}

pub(crate) fn resolve_bin(bin: &str) -> String {
    which(bin).map(|p| p.to_string_lossy().into_owned()).unwrap_or_else(|| bin.to_string())
}

/// 杀整棵进程树：spawn 前已设 process_group(0)（unix，组 id == pid），
/// kill -9 -PID 对整组 SIGKILL；Windows 用 taskkill /T 杀树。
pub(crate) fn kill_tree(pid: Option<u32>) {
    #[cfg(unix)]
    if let Some(pid) = pid {
        let _ = std::process::Command::new("kill").args(["-9", &format!("-{pid}")]).status();
    }
    #[cfg(windows)]
    if let Some(pid) = pid {
        use std::os::windows::process::CommandExt;
        let _ = std::process::Command::new("taskkill")
            .args(["/T", "/F", "/PID", &pid.to_string()])
            .creation_flags(0x0800_0000)
            .status();
    }
}

/// 带超时的子进程捕获：stdin 关死 + kill_on_drop 防交互式 CLI 挂死检测流程；
/// Windows 加 CREATE_NO_WINDOW 防闪黑窗；stdout 空时回退 stderr。
async fn run_capture(program: &str, args: &[&str], timeout_secs: u64) -> Option<(bool, String)> {
    let mut cmd = tokio::process::Command::new(program);
    cmd.args(args)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .kill_on_drop(true);
    #[cfg(windows)]
    cmd.creation_flags(0x0800_0000);
    let fut = cmd.output();
    let out = tokio::time::timeout(std::time::Duration::from_secs(timeout_secs), fut)
        .await
        .ok()?
        .ok()?;
    let stdout = String::from_utf8_lossy(&out.stdout).trim().to_string();
    let stderr = String::from_utf8_lossy(&out.stderr).trim().to_string();
    let text = if stdout.is_empty() { stderr } else { stdout };
    Some((out.status.success(), text))
}

/// 取首个 '{' 到末个 '}'：claude 的 JSON 输出可能混入升级提示等非 JSON 行。
fn extract_json(s: &str) -> Option<serde_json::Value> {
    let start = s.find('{')?;
    let end = s.rfind('}')?;
    serde_json::from_str(&s[start..=end]).ok()
}

async fn detect_claude() -> BrainStatus {
    let mut st = BrainStatus {
        id: "claude".into(),
        found: false,
        path: String::new(),
        version: String::new(),
        logged_in: None,
        detail: String::new(),
    };
    let Some(p) = which("claude") else { return st };
    st.found = true;
    st.path = p.to_string_lossy().into_owned();
    if let Some((_, ver)) = run_capture(&st.path, &["--version"], 10).await {
        st.version = ver.lines().find(|l| !l.trim().is_empty()).unwrap_or_default().to_string();
    }
    // 登出时退出码是 1 但 stdout 仍是合法 JSON —— 必须解析 stdout，不能看退出码
    if let Some((_, out)) = run_capture(&st.path, &["auth", "status", "--json"], 15).await {
        match extract_json(&out) {
            Some(v) => {
                let logged = v
                    .get("loggedIn")
                    .or_else(|| v.get("logged_in"))
                    .and_then(|b| b.as_bool())
                    .unwrap_or(false);
                st.logged_in = Some(logged);
                if let Some(email) = v.get("email").or_else(|| v.get("account")).and_then(|e| e.as_str()) {
                    st.detail = email.to_string();
                }
            }
            None => st.detail = out.chars().take(200).collect(),
        }
    }
    st
}

async fn detect_codex() -> BrainStatus {
    let mut st = BrainStatus {
        id: "codex".into(),
        found: false,
        path: String::new(),
        version: String::new(),
        logged_in: None,
        detail: String::new(),
    };
    let Some(p) = which("codex") else { return st };
    st.found = true;
    st.path = p.to_string_lossy().into_owned();
    if let Some((_, ver)) = run_capture(&st.path, &["--version"], 10).await {
        st.version = ver.lines().find(|l| !l.trim().is_empty()).unwrap_or_default().to_string();
    }
    // codex 与 claude 判据方向相反：exit 0 且输出含 "logged in" 才算已登录
    if let Some((ok, out)) = run_capture(&st.path, &["login", "status"], 15).await {
        st.logged_in = Some(ok && out.to_lowercase().contains("logged in"));
        st.detail = out.chars().take(200).collect();
    }
    st
}

#[tauri::command]
pub async fn detect_brains() -> Vec<BrainStatus> {
    crate::path_env::fix(); // 手动重检时顺带修复 PATH（装完 CLI 后进程快照是旧的）
    let (claude, codex) = tokio::join!(detect_claude(), detect_codex());
    vec![claude, codex]
}

// ---------- 去授权：写登录脚本用系统终端打开 ----------

#[tauri::command]
pub fn open_brain_login(app: AppHandle, brain: String) -> Result<(), String> {
    let (login_cmd, status_cmd, marker) = match brain.as_str() {
        "claude" => ("claude auth login", "claude auth status --json", "\"loggedIn\": true"),
        "codex" => ("codex login", "codex login status", "logged in"),
        _ => return Err("未知的大脑类型".into()),
    };
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| e.to_string())?
        .join("login");
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;

    #[cfg(target_os = "macos")]
    {
        // .command 文件 + open 是 macOS 免 AppleScript 打开终端跑脚本的最简方式；
        // #!/bin/zsh -il 让脚本天然继承用户完整 PATH。
        let file = dir.join(format!("{brain}-login.command"));
        let script = format!(
            "#!/bin/zsh -il\nclear\necho \"== GCRM Pilot · {brain} 授权 ==\"\necho \"即将运行：{login_cmd}\"\necho\n{login_cmd}\necho\nif {status_cmd} 2>/dev/null | grep -qi '{marker}'; then\n  echo \"✅ 已登录，可以回到 GCRM Pilot 点「重新检测」。\"\nelse\n  echo \"❌ 看起来还没登录成功，可重新运行本窗口的命令。\"\nfi\necho \"按任意键关闭窗口…\"\nread -s -k 1\n"
        );
        std::fs::write(&file, script).map_err(|e| e.to_string())?;
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&file, std::fs::Permissions::from_mode(0o755)).map_err(|e| e.to_string())?;
        std::process::Command::new("open").arg(&file).spawn().map_err(|e| e.to_string())?;
    }

    #[cfg(target_os = "windows")]
    {
        // .ps1 必须带 UTF-8 BOM，否则 PowerShell 5.1 按 ANSI 解析中文导致闪退
        let file = dir.join(format!("{brain}-login.ps1"));
        let script = format!(
            "Write-Host '== GCRM Pilot - {brain} 授权 =='\r\nWrite-Host '即将运行：{login_cmd}'\r\n{login_cmd}\r\n$out = ({status_cmd}) 2>&1 | Out-String\r\nif ($out -match '{marker}') {{ Write-Host '已登录，可以回到 GCRM Pilot 点「重新检测」。' }} else {{ Write-Host '看起来还没登录成功。' }}\r\nRead-Host '按回车关闭'\r\n"
        );
        let mut bytes = vec![0xEF, 0xBB, 0xBF];
        bytes.extend_from_slice(script.as_bytes());
        std::fs::write(&file, bytes).map_err(|e| e.to_string())?;
        // 中转 cmd 自身不弹窗（否则授权窗口前先闪一个空黑窗）；start 仍会为
        // powershell 开一个可见的新控制台，授权交互不受影响
        use std::os::windows::process::CommandExt;
        std::process::Command::new("cmd")
            .args(["/c", "start", "", "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File"])
            .arg(&file)
            .creation_flags(0x0800_0000)
            .spawn()
            .map_err(|e| e.to_string())?;
    }

    let _ = marker; // 非 mac/win 平台未使用
    Ok(())
}
