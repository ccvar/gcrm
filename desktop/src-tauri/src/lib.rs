// GCRM Pilot 核心原则（承自 GCMS Pilot）：
//  1. 密钥只进系统钥匙串，绝不落盘、绝不进 WebView —— 前端只知道「有没有密钥」；
//  2. API 请求与 CLI 子进程凭据注入都在 Rust 侧完成，前端拿到的只是响应；
//  3. 连接是可选项：没有连接也能用本地大脑对话（独立模式），
//     连接方式 = 导入技能包 zip 或手动填服务器 + 密钥；
//  4. 关窗隐藏到托盘，后台任务继续跑；托盘「退出」是唯一正常退出路径，清理挂在那里。

mod brain;
mod chat;
mod convo;
mod pack;
mod path_env;
mod pool;

use std::fs;
use std::path::PathBuf;

use serde::Serialize;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Emitter, Manager, WindowEvent};

const KEYRING_SERVICE: &str = "com.ccvar.crm.pilot";
const KEYRING_USER: &str = "api_key";

pub(crate) fn key_entry() -> Result<keyring::Entry, String> {
    keyring::Entry::new(KEYRING_SERVICE, KEYRING_USER).map_err(|e| format!("钥匙串不可用: {e}"))
}

fn config_path(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_config_dir()
        .map_err(|e| format!("配置目录不可用: {e}"))?;
    fs::create_dir_all(&dir).map_err(|e| format!("创建配置目录失败: {e}"))?;
    Ok(dir.join("pilot.json"))
}

fn read_config(app: &AppHandle) -> serde_json::Value {
    config_path(app)
        .ok()
        .and_then(|p| fs::read_to_string(p).ok())
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_else(|| serde_json::json!({}))
}

fn read_server(app: &AppHandle) -> Result<String, String> {
    let v = read_config(app);
    let server = v["server"].as_str().unwrap_or_default().trim_end_matches('/');
    if server.is_empty() {
        return Err("尚未配置服务器地址".into());
    }
    Ok(server.to_string())
}

/// 技能包目录（导入过则有；手动连接没有，对话时 cwd 不设）。
pub(crate) fn skill_dir(app: &AppHandle) -> Option<PathBuf> {
    let v = read_config(app);
    let d = v["skill_dir"].as_str()?;
    let p = PathBuf::from(d);
    if p.is_dir() { Some(p) } else { None }
}

/// 当前配置的服务器地址（未配置返回 None），供导入回滚用。
pub(crate) fn current_server(app: &AppHandle) -> Option<String> {
    read_server(app).ok()
}

pub(crate) fn has_connection(app: &AppHandle) -> bool {
    read_server(app).is_ok()
        && key_entry().and_then(|e| e.get_password().map_err(|_| String::new())).is_ok()
}

/// 对话子进程的凭据注入（SKILL.md 用的就是这两个变量名）。
pub(crate) fn connection_env(app: &AppHandle) -> Result<Vec<(String, String)>, String> {
    let server = read_server(app)?;
    let key = key_entry()?.get_password().map_err(|_| "尚未配置密钥".to_string())?;
    Ok(vec![("CRM_BASE_URL".into(), server), ("CRM_API_KEY".into(), key)])
}

pub(crate) fn write_connection(app: &AppHandle, server: &str, skill_dir: Option<&str>) -> Result<(), String> {
    let p = config_path(app)?;
    let mut cfg = serde_json::json!({ "server": server.trim_end_matches('/') });
    if let Some(d) = skill_dir {
        cfg["skill_dir"] = serde_json::Value::String(d.to_string());
    }
    fs::write(p, cfg.to_string()).map_err(|e| format!("写配置失败: {e}"))
}

#[derive(Serialize)]
struct Setup {
    server: String,
    has_key: bool,
    skill_dir: String,
    key_prefix: String,
}

#[tauri::command]
fn get_setup(app: AppHandle) -> Setup {
    let server = read_server(&app).unwrap_or_default();
    let key = key_entry().and_then(|e| e.get_password().map_err(|_| String::new())).ok();
    // 只暴露前 13 位供辨认，完整密钥不进 WebView
    let key_prefix = key.as_deref().map(|k| k.chars().take(13).collect::<String>()).unwrap_or_default();
    Setup {
        server,
        has_key: key.is_some(),
        skill_dir: skill_dir(&app).map(|p| p.to_string_lossy().into_owned()).unwrap_or_default(),
        key_prefix,
    }
}

#[tauri::command]
fn save_setup(app: AppHandle, server: String, key: String) -> Result<(), String> {
    let server = server.trim().trim_end_matches('/').to_string();
    if !server.starts_with("http://") && !server.starts_with("https://") {
        return Err("服务器地址需以 http:// 或 https:// 开头".into());
    }
    // 手动配置不带技能包目录；保留已导入的 skill_dir（若 server 未变）
    let keep_skill = skill_dir(&app)
        .filter(|_| read_server(&app).map(|s| s == server).unwrap_or(false))
        .map(|p| p.to_string_lossy().into_owned());
    write_connection(&app, &server, keep_skill.as_deref())?;
    let key = key.trim();
    if !key.is_empty() {
        if !key.starts_with("ccrm_") {
            return Err("密钥应为 ccrm_ 前缀".into());
        }
        key_entry()?
            .set_password(key)
            .map_err(|e| format!("写入钥匙串失败: {e}"))?;
    }
    Ok(())
}

#[tauri::command]
fn clear_setup(app: AppHandle) -> Result<(), String> {
    if let Ok(entry) = key_entry() {
        let _ = entry.delete_credential();
    }
    if let Ok(p) = config_path(&app) {
        let _ = fs::remove_file(p);
    }
    Ok(())
}

/// Rust 侧统一出口：注入密钥调 GCRM API（行动队列等前端数据面）。
pub(crate) async fn api_request(
    app: &AppHandle,
    method: &str,
    path: &str,
    body: Option<serde_json::Value>,
) -> Result<(u16, String), String> {
    let server = read_server(app)?;
    let key = key_entry()?
        .get_password()
        .map_err(|_| "尚未配置密钥".to_string())?;
    // 白名单校验必须在 URL 规范化之后：/api/v1/../../x 能过 starts_with 但会被
    // reqwest 归一成 /x，携密钥打到任意同源端点。先 parse 再看规范化后的 path。
    let url = reqwest::Url::parse(&format!("{server}{path}")).map_err(|e| format!("非法地址: {e}"))?;
    if !url.path().starts_with("/api/v1/") {
        return Err("仅允许调用 /api/v1 下的接口".into());
    }
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(30))
        .build()
        .map_err(|e| e.to_string())?;
    let mut req = match method {
        "GET" => client.get(url),
        "POST" => client.post(url),
        _ => return Err(format!("不支持的方法 {method}")),
    };
    req = req.header("Authorization", format!("Bearer {key}"));
    if let Some(b) = body {
        req = req.json(&b);
    }
    let resp = req.send().await.map_err(|e| format!("请求失败: {e}"))?;
    let status = resp.status().as_u16();
    let text = resp.text().await.map_err(|e| format!("读取响应失败: {e}"))?;
    Ok((status, text))
}

#[derive(Serialize)]
struct ApiResp {
    status: u16,
    body: String,
}

#[tauri::command]
async fn api(
    app: AppHandle,
    method: String,
    path: String,
    body: Option<serde_json::Value>,
) -> Result<ApiResp, String> {
    let (status, text) = api_request(&app, &method, &path, body).await?;
    Ok(ApiResp { status, body: text })
}

// ---------- 托盘 ----------

fn show_main(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.set_focus();
    }
}

fn setup_tray(app: &AppHandle) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, "show", "显示主窗口", true, None::<&str>)?;
    let refresh = MenuItem::with_id(app, "refresh", "立即刷新行动队列", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "退出 GCRM Pilot", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &refresh, &quit])?;
    let mut builder = TrayIconBuilder::new()
        .menu(&menu)
        .tooltip("GCRM Pilot")
        .on_menu_event(|app, event| match event.id.as_ref() {
            "show" => show_main(app),
            "refresh" => {
                show_main(app);
                let _ = app.emit("pilot://refresh", ());
            }
            "quit" => {
                // 唯一正常退出路径：先杀所有对话子进程，别留孤儿
                chat::kill_all(&app.state::<chat::ChatRuns>());
                app.exit(0);
            }
            _ => {}
        });
    if let Some(icon) = app.default_window_icon().cloned() {
        builder = builder.icon(icon);
    }
    builder.build(app)?;
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // GUI 进程的 PATH/代理是裸的，必须最先修复（否则找不到 claude CLI）
    path_env::fix();

    tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .manage(chat::ChatRuns::default())
        .invoke_handler(tauri::generate_handler![
            get_setup,
            save_setup,
            clear_setup,
            api,
            brain::detect_brains,
            brain::open_brain_login,
            chat::list_convos,
            chat::get_convo,
            chat::delete_convo,
            chat::send_chat,
            chat::cancel_chat,
            pack::import_pack,
            pool::list_leads,
            pool::add_leads,
            pool::update_lead_status,
            pool::delete_lead,
            pool::push_lead
        ])
        .setup(|app| {
            setup_tray(app.handle())?;
            show_main(app.handle()); // 启动无条件前置，修 macOS relaunch 后窗口不激活
            // 上次异常退出残留的 running 会话置回 idle
            if let Ok(dir) = app.path().app_data_dir() {
                convo::ConvStore::new(&dir).mark_idle(convo::now());
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                if window.label() == "main" {
                    // 关窗隐藏到托盘，后台任务继续跑
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
        })
        .run(tauri::generate_context!())
        .expect("GCRM Pilot 启动失败");
}
