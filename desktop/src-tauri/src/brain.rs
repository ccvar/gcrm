// 本地大脑（承自 GCMS Pilot brains.rs / agent.rs）：
// 检测并驱动本机已登录的 claude CLI 做深度分析，跑在用户自己的订阅上，零 API 计费。
//
// 与 GCMS 的关键差异：分析数据由 Rust 侧先从 CRM API 拉好、嵌进 prompt——
// 子进程不需要任何密钥（GCMS 是把密钥注入子进程 env 让 agent 自己调 API）。
// 密钥连子进程都不进，取消时杀进程组只是防资源泄漏，不再有密钥外泄面。

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

use serde::Serialize;
use tauri::ipc::Channel;
use tauri::{AppHandle, Manager, State};
use tokio::io::AsyncBufReadExt;
use tokio::sync::oneshot;

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

fn resolve_bin(bin: &str) -> String {
    which(bin).map(|p| p.to_string_lossy().into_owned()).unwrap_or_else(|| bin.to_string())
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
            "#!/bin/zsh -il\nclear\necho \"== CRM Pilot · {brain} 授权 ==\"\necho \"即将运行：{login_cmd}\"\necho\n{login_cmd}\necho\nif {status_cmd} 2>/dev/null | grep -qi '{marker}'; then\n  echo \"✅ 已登录，可以回到 CRM Pilot 点「重新检测」。\"\nelse\n  echo \"❌ 看起来还没登录成功，可重新运行本窗口的命令。\"\nfi\necho \"按任意键关闭窗口…\"\nread -s -k 1\n"
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
            "Write-Host '== CRM Pilot - {brain} 授权 =='\r\nWrite-Host '即将运行：{login_cmd}'\r\n{login_cmd}\r\n$out = ({status_cmd}) 2>&1 | Out-String\r\nif ($out -match '{marker}') {{ Write-Host '已登录，可以回到 CRM Pilot 点「重新检测」。' }} else {{ Write-Host '看起来还没登录成功。' }}\r\nRead-Host '按回车关闭'\r\n"
        );
        let mut bytes = vec![0xEF, 0xBB, 0xBF];
        bytes.extend_from_slice(script.as_bytes());
        std::fs::write(&file, bytes).map_err(|e| e.to_string())?;
        std::process::Command::new("cmd")
            .args(["/c", "start", "", "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File"])
            .arg(&file)
            .spawn()
            .map_err(|e| e.to_string())?;
    }

    let _ = marker; // 非 mac/win 平台未使用
    Ok(())
}

// ---------- 分析执行 ----------

#[derive(Serialize, Clone)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum TurnEvent {
    Delta { text: String },
    Done { ok: bool, error: String },
}

struct RunHandle {
    cancel: Arc<AtomicBool>,
    kill_tx: Option<oneshot::Sender<()>>,
    pid: Option<u32>,
}

/// 同一时刻只允许一个分析在跑（每个 CLI 进程吃几百 MB 内存）。
#[derive(Default)]
pub struct BrainRunState(Mutex<Option<RunHandle>>);

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

/// 应用退出前的清理：托盘「退出」是唯一正常退出路径，清理必须挂在那里。
pub fn kill_running(state: &BrainRunState) {
    if let Ok(mut guard) = state.0.lock() {
        if let Some(h) = guard.take() {
            h.cancel.store(true, Ordering::SeqCst);
            kill_tree(h.pid);
        }
    }
}

#[tauri::command]
pub fn cancel_analysis(state: State<'_, BrainRunState>) -> Result<(), String> {
    let mut guard = state.0.lock().map_err(|_| "状态锁失败")?;
    if let Some(h) = guard.as_mut() {
        h.cancel.store(true, Ordering::SeqCst);
        if let Some(tx) = h.kill_tx.take() {
            let _ = tx.send(());
        }
        Ok(())
    } else {
        Err("没有进行中的分析".into())
    }
}

/// 组装分析 prompt：数据由 Rust 拉好嵌入，子进程零密钥。
async fn build_prompt(app: &AppHandle, kind: &str, custom: &str) -> Result<String, String> {
    // 单个数据集截断，防 prompt 失控
    const CAP: usize = 60_000;
    let fetch = |path: &'static str| {
        let app = app.clone();
        async move {
            let (status, body) = crate::api_request(&app, "GET", path, None).await?;
            if status >= 400 {
                return Err(format!("拉取 {path} 失败（HTTP {status}）"));
            }
            let mut b = body;
            if b.chars().count() > CAP {
                b = b.chars().take(CAP).collect::<String>() + "…（已截断）";
            }
            Ok::<String, String>(b)
        }
    };
    let header = "你是一名资深销售总监，正在分析一家公司的 CRM 数据。数据以 JSON 给出，\
时间字段是 unix 秒，金额字段 amount_cents 是分。直接输出 Markdown 报告正文，不要客套。\n\n";
    match kind {
        "lost_review" => {
            let deals = fetch("/api/v1/deals").await?;
            Ok(format!(
                "{header}## 任务：赢单/丢单归因月报\n\n已关单商机数据（含逐单 AI 复盘）：\n```json\n{deals}\n```\n\n\
请输出：1) 总体胜率与金额概览；2) 丢单的共性归因（按频次排序）；3) 赢单的可复制动作；\
4) 给销售团队的 3 条最优先改进建议（具体到话术或流程节点）。"
            ))
        }
        "today_focus" => {
            let tasks = fetch("/api/v1/tasks").await?;
            let customers = fetch("/api/v1/customers").await?;
            Ok(format!(
                "{header}## 任务：今日作战重点\n\n待办任务：\n```json\n{tasks}\n```\n\n客户列表（含意向评级）：\n```json\n{customers}\n```\n\n\
请输出：1) 今天最该打的 3 个客户及理由（结合意向、阶段与任务紧迫度）；2) 每个客户的开场话术建议；\
3) 有没有被遗漏的高意向客户（有意向但没有任何待办）。"
            ))
        }
        "custom" => {
            if custom.trim().is_empty() {
                return Err("自定义分析需要填写问题".into());
            }
            let tasks = fetch("/api/v1/tasks").await?;
            let deals = fetch("/api/v1/deals").await?;
            let customers = fetch("/api/v1/customers").await?;
            Ok(format!(
                "{header}## 任务（用户提问）：{q}\n\n客户：\n```json\n{customers}\n```\n\n待办：\n```json\n{tasks}\n```\n\n已关单商机（含 AI 复盘）：\n```json\n{deals}\n```",
                q = custom.trim()
            ))
        }
        _ => Err("未知的分析类型".into()),
    }
}

#[tauri::command]
pub async fn run_analysis(
    app: AppHandle,
    state: State<'_, BrainRunState>,
    kind: String,
    custom: Option<String>,
    on_event: Channel<TurnEvent>,
) -> Result<String, String> {
    // 占坑：同一时刻只跑一个
    let cancel = Arc::new(AtomicBool::new(false));
    let (kill_tx, mut kill_rx) = oneshot::channel::<()>();
    {
        let mut guard = state.0.lock().map_err(|_| "状态锁失败")?;
        if guard.is_some() {
            return Err("已有分析在进行中，请先停止".into());
        }
        *guard = Some(RunHandle { cancel: cancel.clone(), kill_tx: Some(kill_tx), pid: None });
    }
    // 无论成败都要释放坑位
    let result = run_analysis_inner(&app, &state, &kind, custom.as_deref().unwrap_or(""), &on_event, cancel.clone(), &mut kill_rx).await;
    if let Ok(mut guard) = state.0.lock() {
        *guard = None;
    }
    match &result {
        Ok(_) => {
            let _ = on_event.send(TurnEvent::Done { ok: true, error: String::new() });
        }
        Err(e) => {
            let msg = if cancel.load(Ordering::SeqCst) { "已停止".to_string() } else { e.clone() };
            let _ = on_event.send(TurnEvent::Done { ok: false, error: msg });
        }
    }
    result
}

async fn run_analysis_inner(
    app: &AppHandle,
    state: &State<'_, BrainRunState>,
    kind: &str,
    custom: &str,
    on_event: &Channel<TurnEvent>,
    cancel: Arc<AtomicBool>,
    kill_rx: &mut oneshot::Receiver<()>,
) -> Result<String, String> {
    // 拉数据阶段也要能停：custom 分析顺序拉 3 个 API，最坏 90 秒，不 select 的话
    // 「停止」要等拉取跑完才生效。
    let prompt = tokio::select! {
        p = build_prompt(app, kind, custom) => p?,
        _ = &mut *kill_rx => return Err("已停止".into()),
    };

    let claude = resolve_bin("claude");
    let mut cmd = tokio::process::Command::new(&claude);
    cmd.arg("-p")
        .arg(&prompt)
        .args(["--output-format", "stream-json", "--verbose", "--include-partial-messages"])
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .kill_on_drop(true);
    #[cfg(unix)]
    cmd.process_group(0); // 取消时 kill -9 -PID 才能带走整组
    #[cfg(windows)]
    cmd.creation_flags(0x0800_0000);

    let mut child = cmd
        .spawn()
        .map_err(|e| format!("启动 claude 失败（确认已安装并登录）: {e}"))?;
    let pid = child.id();
    if let Ok(mut guard) = state.0.lock() {
        if let Some(h) = guard.as_mut() {
            h.pid = pid;
        }
    }

    // stdout：逐行 NDJSON，token 级 text_delta 即时转发
    let text = Arc::new(Mutex::new(String::new()));
    let is_error = Arc::new(AtomicBool::new(false));
    let stdout = child.stdout.take().ok_or("无法读取 stdout")?;
    let stderr = child.stderr.take();
    let text2 = text.clone();
    let err2 = is_error.clone();
    let ch = on_event.clone();
    let reader_task = tauri::async_runtime::spawn(async move {
        let mut reader = tokio::io::BufReader::new(stdout);
        let mut buf = Vec::new();
        loop {
            buf.clear();
            match reader.read_until(b'\n', &mut buf).await {
                Ok(0) | Err(_) => break,
                Ok(_) => {}
            }
            let line = String::from_utf8_lossy(&buf);
            let line = line.trim();
            if line.is_empty() {
                continue;
            }
            let Ok(ev) = serde_json::from_str::<serde_json::Value>(line) else { continue };
            match ev.get("type").and_then(|t| t.as_str()) {
                Some("stream_event") => {
                    let e = &ev["event"];
                    if e.get("type").and_then(|t| t.as_str()) == Some("content_block_delta")
                        && e["delta"].get("type").and_then(|t| t.as_str()) == Some("text_delta")
                    {
                        if let Some(t) = e["delta"].get("text").and_then(|t| t.as_str()) {
                            text2.lock().unwrap().push_str(t);
                            let _ = ch.send(TurnEvent::Delta { text: t.to_string() });
                        }
                    }
                }
                Some("result") => {
                    if ev.get("is_error").and_then(|b| b.as_bool()).unwrap_or(false) {
                        err2.store(true, Ordering::SeqCst);
                    }
                    let mut t = text2.lock().unwrap();
                    if t.trim().is_empty() {
                        if let Some(r) = ev.get("result").and_then(|r| r.as_str()) {
                            t.push_str(r);
                        }
                    }
                }
                _ => {}
            }
        }
    });
    // stderr 单独攒着供错误诊断
    let stderr_buf = Arc::new(Mutex::new(String::new()));
    let stderr_task = stderr.map(|se| {
        let sb = stderr_buf.clone();
        tauri::async_runtime::spawn(async move {
            let mut reader = tokio::io::BufReader::new(se);
            let mut line = String::new();
            loop {
                line.clear();
                match reader.read_line(&mut line).await {
                    Ok(0) | Err(_) => break,
                    Ok(_) => sb.lock().unwrap().push_str(&line),
                }
            }
        })
    });

    // 等进程结束，或被取消时对整组 SIGKILL
    let status = tokio::select! {
        s = child.wait() => s.map_err(|e| e.to_string())?,
        _ = &mut *kill_rx => {
            kill_tree(pid);
            let _ = child.kill().await;
            let _ = child.wait().await;
            // 等 reader 排空，否则残余 Delta 会在 Done 之后继续到达前端
            let _ = reader_task.await;
            return Err("已停止".into());
        }
    };
    // 两个读取任务都要等完再取缓冲：stderr 没读完就取，错误诊断会拿到空串
    let _ = reader_task.await;
    if let Some(t) = stderr_task {
        let _ = t.await;
    }

    let final_text = text.lock().unwrap().clone();
    if cancel.load(Ordering::SeqCst) {
        return Err("已停止".into());
    }
    if !status.success() || is_error.load(Ordering::SeqCst) {
        let se = stderr_buf.lock().unwrap();
        let last = se.lines().rev().find(|l| !l.trim().is_empty()).unwrap_or("").to_string();
        let msg = if !last.is_empty() {
            last
        } else if !final_text.trim().is_empty() {
            final_text.chars().take(300).collect()
        } else {
            format!("模型没有产生输出（进程退出码：{:?}）", status.code())
        };
        return Err(msg);
    }
    Ok(final_text)
}
