// 会话数据层（承自 GCMS Pilot convo.rs）：
// conversations.json 单文件持久化，进程内互斥 + tmp/rename 原子写；
// begin_turn 在锁内完成「检查 running → push 用户消息 → 置 running → 落盘」，
// 杜绝同一会话并发开两轮的 TOCTOU。

use std::path::PathBuf;
use std::sync::{Mutex, OnceLock};

use serde::{Deserialize, Serialize};

// 进程级单锁：ConvStore 每条命令都新建，若锁是实例字段则形同虚设——
// 并发轮次会同时读改写 conversations.json 把文件写坏。所有实例共享这把锁。
fn file_lock() -> &'static Mutex<()> {
    static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
    LOCK.get_or_init(|| Mutex::new(()))
}

#[derive(Serialize, Deserialize, Clone, Default)]
pub struct ToolCall {
    pub label: String,
    pub detail: String,
}

#[derive(Serialize, Deserialize, Clone)]
pub struct Message {
    pub role: String, // user / assistant
    pub text: String,
    #[serde(default)]
    pub tools: Vec<ToolCall>,
    pub ts: i64,
    #[serde(default)]
    pub error: bool,
}

#[derive(Serialize, Deserialize, Clone)]
pub struct Conversation {
    pub id: String,
    pub title: String,
    pub brain: String,     // claude（codex 暂未接执行）
    pub model: String,     // 空 = CLI 默认
    #[serde(default)]
    pub effort: String,    // 思考强度：'' / medium / high → MAX_THINKING_TOKENS
    pub perm_mode: String, // plan（只读）/ full（全自动）
    pub task_type: String, // prospect / focus / review / free
    /// 是否绑定 CRM 连接（false = 独立模式，纯本地大脑对话）
    #[serde(default)]
    pub connected: bool,
    /// claude 的 session uuid（首轮 --session-id 指定，续轮 --resume）
    #[serde(default)]
    pub session_ref: String,
    /// 底层 claude 会话是否真正建立过。首轮失败时 session 可能没建成，
    /// 不能凭「有 session_ref」就 --resume 一个不存在的会话（会永久失败）。
    #[serde(default)]
    pub session_started: bool,
    pub messages: Vec<Message>,
    pub status: String, // idle / running
    pub created_at: i64,
    pub updated_at: i64,
}

pub enum TurnStart {
    Ok(Conversation),
    Busy,
    NotFound,
}

pub struct ConvStore {
    file: PathBuf,
}

impl ConvStore {
    pub fn new(data_dir: &std::path::Path) -> Self {
        let _ = std::fs::create_dir_all(data_dir);
        Self { file: data_dir.join("conversations.json") }
    }

    fn read_unlocked(&self) -> Vec<Conversation> {
        let mut list: Vec<Conversation> = std::fs::read_to_string(&self.file)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default();
        list.sort_by(|a, b| b.updated_at.cmp(&a.updated_at));
        list
    }

    fn save_unlocked(&self, list: &[Conversation]) {
        let tmp = self.file.with_extension("json.tmp");
        if let Ok(bytes) = serde_json::to_vec_pretty(list) {
            if std::fs::write(&tmp, bytes).is_ok() {
                let _ = std::fs::rename(&tmp, &self.file);
            }
        }
    }

    pub fn list(&self) -> Vec<Conversation> {
        let _g = file_lock().lock().unwrap();
        self.read_unlocked()
    }

    pub fn get(&self, id: &str) -> Option<Conversation> {
        let _g = file_lock().lock().unwrap();
        self.read_unlocked().into_iter().find(|c| c.id == id)
    }

    pub fn upsert(&self, conv: Conversation) {
        let _g = file_lock().lock().unwrap();
        let mut list = self.read_unlocked();
        list.retain(|c| c.id != conv.id);
        list.insert(0, conv);
        list.truncate(200); // 会话数量上限，防文件无限膨胀
        self.save_unlocked(&list);
    }

    pub fn remove(&self, id: &str) {
        let _g = file_lock().lock().unwrap();
        let mut list = self.read_unlocked();
        list.retain(|c| c.id != id);
        self.save_unlocked(&list);
    }

    /// 锁内读-改-写单个会话。
    pub fn mutate(&self, id: &str, f: impl FnOnce(&mut Conversation)) -> Option<Conversation> {
        let _g = file_lock().lock().unwrap();
        let mut list = self.read_unlocked();
        let conv = list.iter_mut().find(|c| c.id == id)?;
        f(conv);
        let out = conv.clone();
        self.save_unlocked(&list);
        Some(out)
    }

    /// 锁内原子开轮：running 拒绝、push 用户消息、置 running、落盘。
    pub fn begin_turn(&self, id: &str, now: i64, user_text: &str) -> TurnStart {
        let _g = file_lock().lock().unwrap();
        let mut list = self.read_unlocked();
        let Some(conv) = list.iter_mut().find(|c| c.id == id) else { return TurnStart::NotFound };
        if conv.status == "running" {
            return TurnStart::Busy;
        }
        conv.messages.push(Message {
            role: "user".into(),
            text: user_text.to_string(),
            tools: vec![],
            ts: now,
            error: false,
        });
        conv.status = "running".into();
        conv.updated_at = now;
        let out = conv.clone();
        self.save_unlocked(&list);
        TurnStart::Ok(out)
    }

    /// 启动时把上次异常退出残留的 running 置回 idle。
    pub fn mark_idle(&self, now: i64) {
        let _g = file_lock().lock().unwrap();
        let mut list = self.read_unlocked();
        let mut dirty = false;
        for c in list.iter_mut() {
            if c.status == "running" {
                c.status = "idle".into();
                c.updated_at = now;
                dirty = true;
            }
        }
        if dirty {
            self.save_unlocked(&list);
        }
    }
}

pub fn now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

/// 标题 = 首条用户消息前 30 个字符（与 GCMS 一致，不做 AI 生成）。
pub fn title_from(text: &str) -> String {
    let t: String = text.trim().chars().take(30).collect();
    if t.is_empty() { "新对话".into() } else { t }
}
