// 技能包导入（承自 GCMS Pilot pack.rs，裁剪为单连接模型）：
// zip 解压到 <app_data_dir>/packs/<uuid>/，以 SKILL.md 定位技能目录，
// .env(.example) 里解析 CRM_BASE_URL / CRM_API_KEY；密钥进钥匙串后
// 重写 .env 只留基址（绝不让密钥以明文躺在磁盘上）。

use std::path::{Path, PathBuf};

use serde::Serialize;
use tauri::{AppHandle, Manager};

#[derive(Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum ImportOutcome {
    /// 导入完成，连接已建立
    Imported { api_base: String, skill_dir: String },
    /// 包里没有可用密钥（原始包）：让 UI 弹密钥输入框后带 key 重试
    NeedsKey { api_base: String },
}

/// ccrm_ 密钥合法性：前缀 + 40 位十六进制（.env.example 的中文占位符自然不过）。
fn key_ok(key: &str) -> bool {
    key.strip_prefix("ccrm_")
        .map(|rest| rest.len() >= 32 && rest.chars().all(|c| c.is_ascii_hexdigit()))
        .unwrap_or(false)
}

/// 兼容 BOM / CRLF / `export ` 前缀 / 引号。
fn parse_env(content: &str) -> (Option<String>, Option<String>) {
    let mut base = None;
    let mut key = None;
    for line in content.lines() {
        let line = line.trim_start_matches('\u{feff}').trim();
        let line = line.strip_prefix("export ").unwrap_or(line);
        if let Some((k, v)) = line.split_once('=') {
            let v = v.trim().trim_matches('"').trim_matches('\'').trim_end_matches('/');
            match k.trim() {
                "CRM_BASE_URL" if !v.is_empty() => base = Some(v.to_string()),
                "CRM_API_KEY" if !v.is_empty() => key = Some(v.to_string()),
                _ => {}
            }
        }
    }
    (base, key)
}

/// 以 SKILL.md 为判据定位技能目录：根目录不中则向下两层 BFS
/// （兼容 zip 顶层是 README.md + <skill-folder>/ 的嵌套）。
fn find_skill_dir(root: &Path) -> Option<PathBuf> {
    let has_skill = |d: &Path| d.join("SKILL.md").is_file();
    if has_skill(root) {
        return Some(root.to_path_buf());
    }
    let mut level = vec![root.to_path_buf()];
    for _ in 0..2 {
        let mut next = Vec::new();
        for dir in &level {
            let Ok(rd) = std::fs::read_dir(dir) else { continue };
            for entry in rd.flatten() {
                let p = entry.path();
                if p.is_dir() {
                    if has_skill(&p) {
                        return Some(p);
                    }
                    next.push(p);
                }
            }
        }
        level = next;
    }
    None
}

/// 递归拒收符号链接：解压后若包里带任何符号链接，直接拒绝导入。
fn reject_symlinks(dir: &Path) -> Result<(), String> {
    let rd = std::fs::read_dir(dir).map_err(|e| e.to_string())?;
    for entry in rd.flatten() {
        let p = entry.path();
        let meta = std::fs::symlink_metadata(&p).map_err(|e| e.to_string())?;
        if meta.file_type().is_symlink() {
            return Err("技能包含符号链接，出于安全拒绝导入".into());
        }
        if meta.is_dir() {
            reject_symlinks(&p)?;
        }
    }
    Ok(())
}

/// 导入失败时把钥匙串/连接回滚到导入前：有旧密钥则恢复，无则清掉本次写入的。
fn rollback_key(prev_key: Option<&str>, prev_server: Option<&str>, app: &AppHandle) {
    if let Ok(entry) = crate::key_entry() {
        match prev_key {
            Some(k) => {
                let _ = entry.set_password(k);
            }
            None => {
                let _ = entry.delete_credential();
            }
        }
    }
    if let Some(s) = prev_server {
        let _ = crate::write_connection(app, s, crate::skill_dir(app).and_then(|p| p.to_str().map(String::from)).as_deref());
    }
}

fn read_env_file(dir: &Path) -> (Option<String>, Option<String>) {
    for name in [".env", ".env.example"] {
        if let Ok(s) = std::fs::read_to_string(dir.join(name)) {
            let (base, key) = parse_env(&s);
            if base.is_some() || key.is_some() {
                return (base, key);
            }
        }
    }
    (None, None)
}

#[tauri::command]
pub fn import_pack(app: AppHandle, zip_path: String, key: Option<String>) -> Result<ImportOutcome, String> {
    let data_dir = app.path().app_data_dir().map_err(|e| e.to_string())?;
    let packs = data_dir.join("packs");
    std::fs::create_dir_all(&packs).map_err(|e| e.to_string())?;
    let dest = packs.join(uuid::Uuid::new_v4().to_string());

    let result = import_inner(&app, &zip_path, &dest, key.as_deref());
    if !matches!(result, Ok(ImportOutcome::Imported { .. })) {
        // 失败或 NeedsKey：回滚本次解压
        let _ = std::fs::remove_dir_all(&dest);
    }
    result
}

fn import_inner(app: &AppHandle, zip_path: &str, dest: &Path, provided_key: Option<&str>) -> Result<ImportOutcome, String> {
    let file = std::fs::File::open(zip_path).map_err(|e| format!("打开 zip 失败: {e}"))?;
    let mut archive = zip::ZipArchive::new(file).map_err(|e| format!("解析 zip 失败: {e}"))?;
    archive.extract(dest).map_err(|e| format!("解压失败: {e}"))?;
    // 拒收含符号链接的包：恶意 zip 可放一个 .env → /Users/x/.zshenv 的符号链接，
    // 后续 std::fs::write(.env) 会跟随它越权覆盖任意文件（潜在 RCE）。
    reject_symlinks(dest)?;

    let skill = find_skill_dir(dest).ok_or("包里没找到 SKILL.md，不是 GCRM 技能包")?;
    let (base, pack_key) = read_env_file(&skill);
    let api_base = base.ok_or("包里没有 CRM_BASE_URL（.env / .env.example）")?;

    // 密钥来源优先级：包内嵌 → 用户粘贴 → 都没有则要密钥
    let key = match (pack_key.filter(|k| key_ok(k)), provided_key) {
        (Some(k), _) => k,
        (None, Some(k)) if key_ok(k.trim()) => k.trim().to_string(),
        (None, Some(_)) => return Err("密钥格式不对（应为 ccrm_ 前缀）".into()),
        (None, None) => return Ok(ImportOutcome::NeedsKey { api_base }),
    };

    // 记下旧连接凭据，导入失败时回滚（不能把用户原有可用密钥清掉）
    let had_prev_key = crate::key_entry().ok().and_then(|e| e.get_password().ok());
    let prev_server = crate::current_server(app);

    // 密钥进钥匙串；.env 重写只留基址
    crate::key_entry()?
        .set_password(&key)
        .map_err(|e| format!("写入钥匙串失败: {e}"))?;
    let env_body = format!(
        "CRM_BASE_URL={api_base}\n# CRM_API_KEY 已由 GCRM Pilot 保管在系统钥匙串，运行时自动注入\n"
    );
    // 先删旧 .env（断掉可能的符号链接）再写全新普通文件，杜绝跟随符号链接写盘
    let env_path = skill.join(".env");
    let _ = std::fs::remove_file(&env_path);
    if let Err(e) = std::fs::write(&env_path, env_body) {
        // 回滚钥匙串到导入前状态
        rollback_key(had_prev_key.as_deref(), prev_server.as_deref(), app);
        return Err(format!("重写 .env 失败: {e}"));
    }
    let _ = std::fs::remove_file(skill.join(".env.example"));

    // 单连接模型：替换旧技能包目录（若在 packs/ 下则清掉）
    let old_skill = crate::skill_dir(app);
    crate::write_connection(app, &api_base, Some(skill.to_string_lossy().as_ref()))?;
    if let Some(old) = old_skill {
        if old.starts_with(app.path().app_data_dir().map_err(|e| e.to_string())?.join("packs")) && old != skill {
            // 只清 packs/<uuid> 这一层
            if let Some(pack_root) = old.ancestors().find(|a| a.parent().map(|p| p.ends_with("packs")).unwrap_or(false)) {
                let _ = std::fs::remove_dir_all(pack_root);
            }
        }
    }

    Ok(ImportOutcome::Imported {
        api_base,
        skill_dir: skill.to_string_lossy().into_owned(),
    })
}
