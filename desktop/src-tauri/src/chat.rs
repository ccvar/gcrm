// 对话引擎（承自 GCMS Pilot agent.rs 的 run_turn 模型）：
// 一轮对话 = 一个 claude 子进程。首轮 `--session-id <uuid>` + `--append-system-prompt`，
// 续轮 `--resume <uuid>` 靠 CLI 的会话状态延续；全程 stream-json 流式回传。
//
// 凭据边界：连接模式下把 CRM_BASE_URL / CRM_API_KEY 注入子进程 env（agent 按
// SKILL.md 用 curl 调 GCRM 接口），密钥不进 WebView；取消时对整个进程组 SIGKILL，
// 不留带着密钥的孙进程。独立模式零凭据零 cwd 约束。

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

use serde::Serialize;
use tauri::ipc::Channel;
use tauri::{AppHandle, Manager, State};
use tokio::io::AsyncBufReadExt;
use tokio::sync::oneshot;

use crate::brain::{kill_tree, resolve_bin};
use crate::convo::{self, Conversation, Message, ToolCall, TurnStart};

#[derive(Serialize, Clone)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum TurnEvent {
    /// 首轮开始即回传新建的会话 id，让前端能在首轮就取消（否则要等 send_chat 返回，
    /// 而那时回合已经结束了）。
    Started { conv_id: String },
    Delta { text: String },
    Tool { label: String, detail: String },
    Done { ok: bool, error: String },
}

struct RunHandle {
    cancel: Arc<AtomicBool>,
    kill_tx: Option<oneshot::Sender<()>>,
    pid: Option<u32>,
}

/// conv_id → 运行句柄。不同会话可并发，同一会话由 begin_turn 挡住。
#[derive(Default)]
pub struct ChatRuns(Mutex<HashMap<String, RunHandle>>);

/// 应用退出前清理全部子进程（托盘「退出」是唯一正常退出路径）。
pub fn kill_all(runs: &ChatRuns) {
    if let Ok(mut map) = runs.0.lock() {
        for (_, h) in map.drain() {
            h.cancel.store(true, Ordering::SeqCst);
            kill_tree(h.pid);
        }
    }
}

// ---------- 系统提示 ----------

fn system_prompt(task_type: &str, connected: bool) -> String {
    let mut s = String::from(
        "你是 GCRM Pilot 的销售参谋，嵌在一家公司的 CRM 桌面端里，帮销售把线索转化成成交。\
回答用中文，直接、具体、可执行，不说空话。输出用 Markdown。\n",
    );
    if connected {
        s.push_str(
            "\n当前目录是 GCRM 技能包：先读 SKILL.md 了解自动化接口，再用 Bash 的 curl 调接口取数。\
鉴权头 `Authorization: Bearer $CRM_API_KEY`，基址 `$CRM_BASE_URL`（都已在环境变量里，\
绝不要把密钥明文写进任何输出或文件）。改写数据（新建客户/记录沟通/完成任务）前先向用户确认。\n",
        );
    } else {
        s.push_str(
            "\n当前是独立模式（未连接 CRM 服务端），你拿不到实际客户数据——基于用户描述给建议，\
需要数据时提醒用户在左下角连接 GCRM 或导入技能包。\n",
        );
    }
    match task_type {
        "focus" => s.push_str("\n本次会话主题：今日作战。帮用户从待办和客户中找出今天最该打的仗、给出具体话术。\n"),
        "review" => s.push_str("\n本次会话主题：复盘归因。帮用户分析赢单/丢单的原因，沉淀可复用的 playbook。\n"),
        "prospect" => {
            s.push_str(
                "\n本次会话主题：找客户（获客）。用户会给你理想客户画像（行业/地域/规模/关键词）。\
你的工作流严格如下：\n\
1) 用 WebSearch / WebFetch 联网检索：优先公开来源——企业官网、行业名录、招投标/中标公告、招聘信息、\
新闻与展会名单。只取公开可获得的信息，不要伪造，不要抓取需要登录的社媒平台的私密数据。\n\
2) 整理成结构化线索清单，用 Markdown 表格列出：公司名｜对接人/部门｜联系方式(公开)｜来源链接｜为什么是潜客(一句话)。\n\
3) 先把清单给用户看，等用户明确说「导入」哪些（可全部或挑选），再动手写入——这是关键动作，必须人确认。\n",
            );
            if connected {
                s.push_str(
                    "4) 导入时按 SKILL.md 的 POST /api/v1/customers 逐条建客户：source 填来源（如「AI获客·行业名录」），\
notes 填线索依据与来源链接。写入前先 GET /api/v1/customers?q= 查重，避免重复建档。每导入一批报告结果。\n",
                );
            } else {
                s.push_str(
                    "4) 当前未连接 CRM，你只能把线索清单交给用户，无法直接写入。提示用户在左下角连接 GCRM 或导入技能包后即可一键导入。\n",
                );
            }
        }
        _ => {}
    }
    s
}

fn perm_flags(perm_mode: &str) -> Vec<String> {
    match perm_mode {
        // 全自动：无人值守直通（ask/auto 的逐命令批准钩子后续再接）
        "full" => vec!["--dangerously-skip-permissions".into()],
        // 默认只读（fail-safe）：空串/未知值都落到只读，不能默默放开全部权限
        _ => vec!["--permission-mode".into(), "plan".into()],
    }
}

// ---------- 命令 ----------

fn store(app: &AppHandle) -> Result<convo::ConvStore, String> {
    let dir = app.path().app_data_dir().map_err(|e| e.to_string())?;
    Ok(convo::ConvStore::new(&dir))
}

#[tauri::command]
pub fn list_convos(app: AppHandle) -> Result<Vec<Conversation>, String> {
    Ok(store(&app)?.list())
}

#[tauri::command]
pub fn get_convo(app: AppHandle, id: String) -> Result<Option<Conversation>, String> {
    Ok(store(&app)?.get(&id))
}

#[tauri::command]
pub fn delete_convo(app: AppHandle, runs: State<'_, ChatRuns>, id: String) -> Result<(), String> {
    // 运行中先杀
    if let Ok(mut map) = runs.0.lock() {
        if let Some(h) = map.remove(&id) {
            h.cancel.store(true, Ordering::SeqCst);
            kill_tree(h.pid);
        }
    }
    store(&app)?.remove(&id);
    Ok(())
}

#[tauri::command]
pub fn cancel_chat(runs: State<'_, ChatRuns>, id: String) -> Result<(), String> {
    let mut map = runs.0.lock().map_err(|_| "状态锁失败")?;
    if let Some(h) = map.get_mut(&id) {
        h.cancel.store(true, Ordering::SeqCst);
        if let Some(tx) = h.kill_tx.take() {
            let _ = tx.send(());
        }
        Ok(())
    } else {
        Err("该会话没有进行中的回合".into())
    }
}

#[derive(Serialize)]
pub struct ChatOut {
    pub conv: Conversation,
    pub ok: bool,
    pub error: String,
}

/// 发送一轮：conv_id 为空则新建会话（首轮），否则续轮。
/// 事件经 on_event 流式回传，最终以落库后的 Conversation 返回。
#[tauri::command]
#[allow(clippy::too_many_arguments)]
pub async fn send_chat(
    app: AppHandle,
    runs: State<'_, ChatRuns>,
    conv_id: Option<String>,
    text: String,
    brain: Option<String>,
    model: Option<String>,
    perm_mode: Option<String>,
    task_type: Option<String>,
    on_event: Channel<TurnEvent>,
) -> Result<ChatOut, String> {
    let text = text.trim().to_string();
    if text.is_empty() {
        return Err("消息不能为空".into());
    }
    let st = store(&app)?;
    let now = convo::now();
    let connected = crate::has_connection(&app);

    // ---- 建会话 / 开轮（锁内原语防并发）----
    let (mut conv, is_first) = match conv_id {
        None => {
            let brain = brain.unwrap_or_else(|| "claude".into());
            if brain != "claude" {
                return Err("暂只支持 Claude Code 执行对话（Codex 已检测但未接入）".into());
            }
            let conv = Conversation {
                id: uuid::Uuid::new_v4().to_string(),
                title: convo::title_from(&text),
                brain,
                model: model.clone().unwrap_or_default(),
                perm_mode: perm_mode.clone().unwrap_or_else(|| "plan".into()),
                task_type: task_type.unwrap_or_else(|| "free".into()),
                connected,
                // claude 的 session id 由客户端预生成，首轮 --session-id 指定
                session_ref: uuid::Uuid::new_v4().to_string(),
                session_started: false,
                messages: vec![Message { role: "user".into(), text: text.clone(), tools: vec![], ts: now, error: false }],
                status: "running".into(),
                created_at: now,
                updated_at: now,
            };
            st.upsert(conv.clone());
            (conv, true)
        }
        Some(id) => match st.begin_turn(&id, now, &text) {
            // 底层会话没真正建立过（首轮失败等）就当首轮重开，并换一把新 session id
            // ——避免 --resume 一个不存在的会话导致永久失败。
            TurnStart::Ok(mut c) => {
                if c.session_started {
                    (c, false)
                } else {
                    c.session_ref = uuid::Uuid::new_v4().to_string();
                    (c, true)
                }
            }
            TurnStart::Busy => return Err("该会话有回合在进行中，请先停止".into()),
            TurnStart::NotFound => return Err("会话不存在".into()),
        },
    };

    // 首轮：立刻回传 conv_id，前端凭它在首轮就能取消
    let _ = on_event.send(TurnEvent::Started { conv_id: conv.id.clone() });

    // 更新档位/模型（续轮时用户可能改过，下一轮生效）
    if let Some(pm) = perm_mode {
        if !pm.is_empty() {
            conv.perm_mode = pm;
        }
    }
    if let Some(m) = model {
        conv.model = m;
    }

    // ---- 注册取消句柄 ----
    let cancel = Arc::new(AtomicBool::new(false));
    let (kill_tx, mut kill_rx) = oneshot::channel::<()>();
    {
        let mut map = runs.0.lock().map_err(|_| "状态锁失败")?;
        map.insert(conv.id.clone(), RunHandle { cancel: cancel.clone(), kill_tx: Some(kill_tx), pid: None });
    }

    let result = run_claude_turn(&app, &runs, &mut conv, is_first, &text, &on_event, cancel.clone(), &mut kill_rx).await;

    // ---- 收尾：注销句柄（先读 cancel 再摘除）、落库 ----
    let canceled = cancel.load(Ordering::SeqCst);
    if let Ok(mut map) = runs.0.lock() {
        map.remove(&conv.id);
    }
    let now2 = convo::now();
    let (ok, err_msg, final_text, tools) = match result {
        Ok((t, tools)) => (true, String::new(), t, tools),
        Err(e) => {
            let msg = if canceled { "已停止".to_string() } else { e };
            (false, msg, String::new(), vec![])
        }
    };
    let assistant = Message {
        role: "assistant".into(),
        text: if ok { final_text } else { err_msg.clone() },
        tools,
        ts: now2,
        error: !ok,
    };
    // 用 mutate 而非 upsert 落库：若会话在跑的过程中被删掉，mutate 返回 None、
    // 不写任何东西，避免把已删除的会话「复活」成僵尸。
    let persisted = st.mutate(&conv.id, |c| {
        c.messages.push(assistant.clone());
        if ok {
            c.session_started = true; // 底层 claude 会话确认建立，续轮才能安全 --resume
        }
        c.status = "idle".into();
        c.updated_at = now2;
    });
    let out_conv = persisted.unwrap_or_else(|| {
        // 会话已被删除：返回本地快照给前端收尾，但不落库
        conv.messages.push(assistant);
        conv.status = "idle".into();
        conv
    });
    let _ = on_event.send(TurnEvent::Done { ok, error: err_msg.clone() });
    Ok(ChatOut { conv: out_conv, ok, error: err_msg })
}

#[allow(clippy::too_many_arguments)]
async fn run_claude_turn(
    app: &AppHandle,
    runs: &State<'_, ChatRuns>,
    conv: &mut Conversation,
    is_first: bool,
    message: &str,
    on_event: &Channel<TurnEvent>,
    cancel: Arc<AtomicBool>,
    kill_rx: &mut oneshot::Receiver<()>,
) -> Result<(String, Vec<ToolCall>), String> {
    let claude = resolve_bin("claude");
    let mut cmd = tokio::process::Command::new(&claude);

    // 首轮系统提示折进 stdin，而不是 --append-system-prompt：系统提示必含换行，
    // 作为 argv 传给 Windows 上 npm 装的 claude.cmd 会被 Rust std 直接拒绝
    // （CVE-2024-24576 修复行为）。折进 stdin 跨平台都稳。
    let stdin_content = if is_first {
        format!("{}\n\n———\n用户：{}", system_prompt(&conv.task_type, conv.connected), message)
    } else {
        message.to_string()
    };

    // prompt 走 stdin（Windows .cmd 换行拒绝 + 32K 命令行上限）
    cmd.arg("-p");
    if is_first {
        cmd.args(["--session-id", &conv.session_ref]);
    } else {
        cmd.args(["--resume", &conv.session_ref]);
    }
    cmd.args(["--output-format", "stream-json", "--verbose", "--include-partial-messages"]);
    if !conv.model.trim().is_empty() {
        let m = conv.model.trim();
        if m.starts_with('-') || m.contains(char::is_whitespace) {
            return Err("模型名不合法".into());
        }
        cmd.args(["--model", m]);
    }
    for f in perm_flags(&conv.perm_mode) {
        cmd.arg(f);
    }

    // 连接模式：cwd = 技能包目录（若有），注入 CRM_BASE_URL / CRM_API_KEY
    if conv.connected {
        if let Some(dir) = crate::skill_dir(app) {
            cmd.current_dir(dir);
        }
        match crate::connection_env(app) {
            Ok(envs) => {
                for (k, v) in envs {
                    cmd.env(k, v);
                }
            }
            Err(e) => return Err(format!("读取连接凭据失败: {e}")),
        }
    }

    cmd.stdin(std::process::Stdio::piped())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .kill_on_drop(true);
    #[cfg(unix)]
    cmd.process_group(0);
    #[cfg(windows)]
    cmd.creation_flags(0x0800_0000);

    let mut child = cmd.spawn().map_err(|e| format!("启动 claude 失败（确认已安装并登录）: {e}"))?;
    let pid = child.id();
    if let Ok(mut map) = runs.0.lock() {
        if let Some(h) = map.get_mut(&conv.id) {
            h.pid = pid;
        }
    }

    // 写 prompt 到 stdin（独立任务防管道死锁）
    if let Some(mut si) = child.stdin.take() {
        let msg = stdin_content;
        tauri::async_runtime::spawn(async move {
            use tokio::io::AsyncWriteExt;
            let _ = si.write_all(msg.as_bytes()).await;
            let _ = si.shutdown().await;
        });
    }

    // stdout：NDJSON 流 → Delta / Tool
    let text = Arc::new(Mutex::new(String::new()));
    let tools = Arc::new(Mutex::new(Vec::<ToolCall>::new()));
    let is_error = Arc::new(AtomicBool::new(false));
    let stdout = child.stdout.take().ok_or("无法读取 stdout")?;
    let stderr = child.stderr.take();
    let (t2, tl2, e2, ch) = (text.clone(), tools.clone(), is_error.clone(), on_event.clone());
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
                            t2.lock().unwrap().push_str(t);
                            let _ = ch.send(TurnEvent::Delta { text: t.to_string() });
                        }
                    }
                }
                Some("assistant") => {
                    if let Some(blocks) = ev["message"]["content"].as_array() {
                        for b in blocks {
                            if b.get("type").and_then(|t| t.as_str()) == Some("tool_use") {
                                let name = b.get("name").and_then(|n| n.as_str()).unwrap_or("tool");
                                // Bash 显示命令本体，其余显示入参 JSON，都截 200 字符
                                let detail = if name == "Bash" {
                                    b["input"].get("command").and_then(|c| c.as_str()).unwrap_or("").to_string()
                                } else {
                                    b["input"].to_string()
                                };
                                let detail: String = detail.chars().take(200).collect();
                                let tc = ToolCall { label: name.to_string(), detail: detail.clone() };
                                tl2.lock().unwrap().push(tc);
                                let _ = ch.send(TurnEvent::Tool { label: name.to_string(), detail });
                            }
                        }
                    }
                }
                Some("result") => {
                    if ev.get("is_error").and_then(|b| b.as_bool()).unwrap_or(false) {
                        e2.store(true, Ordering::SeqCst);
                    }
                    let mut t = t2.lock().unwrap();
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

    let status = tokio::select! {
        s = child.wait() => s.map_err(|e| e.to_string())?,
        _ = &mut *kill_rx => {
            kill_tree(pid);
            let _ = child.kill().await;
            let _ = child.wait().await;
            let _ = reader_task.await;
            return Err("已停止".into());
        }
    };
    let _ = reader_task.await;
    if let Some(t) = stderr_task {
        let _ = t.await;
    }

    let final_text = text.lock().unwrap().clone();
    let final_tools = tools.lock().unwrap().clone();
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
    Ok((final_text, final_tools))
}
