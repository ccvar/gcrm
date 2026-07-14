// Stub module：把 desktop/（CRM Pilot 桌面客户端，Tauri/Rust/JS）
// 从根模块的 go build ./... / go test ./... 中隔离出去。
// 本目录没有任何 Go 代码，此文件仅作为模块边界存在（与 GCMS 同一约定）。
module pilot.local

go 1.23
