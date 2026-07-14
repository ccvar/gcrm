// 本地客户池（线索）：找客户产出的线索先落本地 leads.json（独立模式也能用），
// 连上 CRM 后由用户在池子里「推入 GCRM」——这一步就是半自动的「关键动作人确认」。
//
// 存储与 convo.rs 同构：单文件 + 进程级共享锁 + tmp/rename 原子写。

use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Mutex, OnceLock};

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager};

fn file_lock() -> &'static Mutex<()> {
    static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
    LOCK.get_or_init(|| Mutex::new(()))
}

#[derive(Serialize, Deserialize, Clone)]
pub struct Lead {
    pub id: String,
    pub company: String,
    #[serde(default)]
    pub contact: String, // 对接人/部门
    #[serde(default)]
    pub phone: String,
    #[serde(default)]
    pub email: String,
    #[serde(default)]
    pub wechat: String,
    #[serde(default)]
    pub source: String,
    #[serde(default)]
    pub source_url: String,
    #[serde(default)]
    pub reason: String, // 为什么是潜客
    pub status: String, // new / contacted / pushed
    #[serde(default)]
    pub crm_id: i64, // 推入 CRM 后的客户 id（0 = 未推）
    pub created_at: i64,
    #[serde(default)]
    pub pushed_at: i64,
}

/// 前端「入池」传入的原始线索（AI 产出，字段可能缺）。
#[derive(Deserialize, Default)]
pub struct LeadInput {
    #[serde(default)]
    pub company: String,
    #[serde(default)]
    pub contact: String,
    #[serde(default)]
    pub phone: String,
    #[serde(default)]
    pub email: String,
    #[serde(default)]
    pub wechat: String,
    #[serde(default)]
    pub source: String,
    #[serde(default)]
    pub source_url: String,
    #[serde(default)]
    pub reason: String,
}

fn leads_path(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app.path().app_data_dir().map_err(|e| e.to_string())?;
    let _ = std::fs::create_dir_all(&dir);
    Ok(dir.join("leads.json"))
}

fn read_unlocked(path: &PathBuf) -> Vec<Lead> {
    let mut list: Vec<Lead> = std::fs::read_to_string(path)
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default();
    list.sort_by(|a, b| b.created_at.cmp(&a.created_at));
    list
}

fn save_unlocked(path: &PathBuf, list: &[Lead]) -> Result<(), String> {
    let tmp = path.with_extension("json.tmp");
    let bytes = serde_json::to_vec_pretty(list).map_err(|e| format!("序列化失败: {e}"))?;
    std::fs::write(&tmp, bytes).map_err(|e| format!("写入失败: {e}"))?;
    std::fs::rename(&tmp, path).map_err(|e| format!("落盘失败: {e}"))?;
    Ok(())
}

fn now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

#[tauri::command]
pub fn list_leads(app: AppHandle) -> Result<Vec<Lead>, String> {
    let path = leads_path(&app)?;
    let _g = file_lock().lock().map_err(|_| "锁失败")?;
    Ok(read_unlocked(&path))
}

/// 批量入池，按公司名去重（已在池中的公司跳过）。返回实际新增条数。
#[tauri::command]
pub fn add_leads(app: AppHandle, leads: Vec<LeadInput>) -> Result<usize, String> {
    let path = leads_path(&app)?;
    let _g = file_lock().lock().map_err(|_| "锁失败")?;
    let mut list = read_unlocked(&path);
    let existing: std::collections::HashSet<String> =
        list.iter().map(|l| l.company.trim().to_lowercase()).collect();
    let mut seen = existing;
    let t = now();
    let mut added = 0;
    for inp in leads.into_iter() {
        let company = inp.company.trim().to_string();
        if company.is_empty() {
            continue;
        }
        let key = company.to_lowercase();
        if seen.contains(&key) {
            continue; // 去重
        }
        seen.insert(key);
        // 进程级自增计数器保证 id 全局唯一（同一秒多次入池也不撞）
        static SEQ: AtomicU64 = AtomicU64::new(0);
        list.push(Lead {
            id: format!("lead-{}-{}", t, SEQ.fetch_add(1, Ordering::Relaxed)),
            company,
            contact: inp.contact.trim().to_string(),
            phone: inp.phone.trim().to_string(),
            email: inp.email.trim().to_string(),
            wechat: inp.wechat.trim().to_string(),
            source: inp.source.trim().to_string(),
            source_url: inp.source_url.trim().to_string(),
            reason: inp.reason.trim().to_string(),
            status: "new".into(),
            crm_id: 0,
            created_at: t,
            pushed_at: 0,
        });
        added += 1;
    }
    if added > 0 {
        save_unlocked(&path, &list)?; // 写失败要如实报错，不能假装入池成功
    }
    Ok(added)
}

#[tauri::command]
pub fn update_lead_status(app: AppHandle, id: String, status: String) -> Result<(), String> {
    let path = leads_path(&app)?;
    let _g = file_lock().lock().map_err(|_| "锁失败")?;
    let mut list = read_unlocked(&path);
    if let Some(l) = list.iter_mut().find(|l| l.id == id) {
        l.status = status;
        save_unlocked(&path, &list)?;
    }
    Ok(())
}

#[tauri::command]
pub fn delete_lead(app: AppHandle, id: String) -> Result<(), String> {
    let path = leads_path(&app)?;
    let _g = file_lock().lock().map_err(|_| "锁失败")?;
    let mut list = read_unlocked(&path);
    list.retain(|l| l.id != id);
    save_unlocked(&path, &list)
}

/// 推入 GCRM：把线索建成客户（POST /api/v1/customers），成功后标记 pushed + crm_id。
/// 需已连接（否则报错）。这是本地池 → 服务端的「关键动作」，由用户在池子里发起。
#[tauri::command]
pub async fn push_lead(app: AppHandle, id: String) -> Result<Lead, String> {
    let path = leads_path(&app)?;
    // 锁内：查重 + 抢占标记 pushing（防两次并发推入建出重复客户），随即释放锁再发网络请求。
    let (lead, prev_status) = {
        let _g = file_lock().lock().map_err(|_| "锁失败")?;
        let mut list = read_unlocked(&path);
        let l = list.iter_mut().find(|l| l.id == id).ok_or("线索不存在")?;
        if l.status == "pushed" && l.crm_id > 0 {
            return Err("该线索已入库".into());
        }
        if l.status == "pushing" {
            return Err("正在推入中，请稍候".into());
        }
        let prev = l.status.clone();
        l.status = "pushing".into();
        let snap = l.clone();
        save_unlocked(&path, &list)?; // 抢占失败（写不进）就直接报错，还没发网络请求
        (snap, prev)
    };

    let name = if lead.contact.is_empty() { lead.company.clone() } else { lead.contact.clone() };
    let mut notes = lead.reason.clone();
    if !lead.source_url.is_empty() {
        if !notes.is_empty() {
            notes.push_str("；");
        }
        notes.push_str("来源：");
        notes.push_str(&lead.source_url);
    }
    let body = serde_json::json!({
        "name": name,
        "company": lead.company,
        "phone": lead.phone,
        "email": lead.email,
        "wechat": lead.wechat,
        "source": if lead.source.is_empty() { "AI获客".to_string() } else { lead.source.clone() },
        "notes": notes,
    });

    // 把 pushing 标记复位成 prev（推入失败/网络异常时用），best-effort。
    let revert = |prev: &str| {
        if let Ok(_g) = file_lock().lock() {
            let mut list = read_unlocked(&path);
            if let Some(l) = list.iter_mut().find(|l| l.id == id) {
                if l.status == "pushing" {
                    l.status = prev.to_string();
                    let _ = save_unlocked(&path, &list);
                }
            }
        }
    };

    let (status, text) = match crate::api_request(&app, "POST", "/api/v1/customers", Some(body)).await {
        Ok(r) => r,
        Err(e) => { revert(&prev_status); return Err(e); }
    };
    if status >= 400 {
        let msg = serde_json::from_str::<serde_json::Value>(&text)
            .ok()
            .and_then(|v| v.get("error").and_then(|e| e.as_str()).map(String::from))
            .unwrap_or_else(|| format!("HTTP {status}"));
        revert(&prev_status);
        return Err(format!("推入失败：{msg}"));
    }
    let crm_id = serde_json::from_str::<serde_json::Value>(&text)
        .ok()
        .and_then(|v| v.get("id").and_then(|i| i.as_i64()))
        .unwrap_or(0);

    // 回写 pushed 状态
    let _g = file_lock().lock().map_err(|_| "锁失败")?;
    let mut list = read_unlocked(&path);
    let l = list.iter_mut().find(|l| l.id == id).ok_or("线索已被删除")?;
    l.status = "pushed".into();
    l.crm_id = crm_id;
    l.pushed_at = now();
    let out = l.clone();
    // 客户已在 CRM 建档，若本地写回失败要明确告知，避免用户重复推入
    if let Err(e) = save_unlocked(&path, &list) {
        return Err(format!("已在 CRM 建档（id {crm_id}），但本地状态未能更新，请勿重复推入：{e}"));
    }
    Ok(out)
}
