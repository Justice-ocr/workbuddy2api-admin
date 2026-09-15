package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ownedPath rejects symbolic links and requires a direct child of the configured directory.
// Configured directories are trusted and must not be writable by other local users.
func ownedPath(dir, path string, missing bool) bool {
	root, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	full, err := filepath.Abs(path)
	if err != nil || filepath.Dir(full) != root {
		return false
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return false
	}
	st, err := os.Lstat(full)
	if os.IsNotExist(err) {
		return missing
	}
	return err == nil && st.Mode().IsRegular()
}

func (a *adminServer) accountAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UID          string `json:"uid"`
		Action       string `json:"action"`
		Confirmation string `json:"confirmation,omitempty"`
	}
	if !decodeAdmin(w, r, &req) {
		return
	}
	if !validAdminUID(req.UID) {
		adminError(w, 400, "无效的账号标识")
		return
	}
	acct := a.pool.AuthByUID(req.UID)
	if acct == nil {
		adminError(w, 404, "账号不存在")
		return
	}
	if !ownedPath(a.authDir, acct.FilePath, false) {
		adminError(w, 409, "账号文件路径不安全或不存在")
		return
	}
	switch req.Action {
	case "disable":
		a.pool.Disable(req.UID, "manual disable (admin)")
		a.pool.Flush()
		a.event("账号停用", "成功")
	case "enable":
		a.pool.ReviveDisabled(req.UID)
		a.pool.Flush()
		a.event("账号恢复", "成功")
	case "refresh":
		if acct.RealmStored() == "global" && !a.up.GlobalEnabled {
			adminError(w, 409, "国际版路由未启用")
			return
		}
		if err := a.up.RefreshToken(acct); err != nil {
			a.event("账号刷新", "上游失败")
			adminError(w, 502, "凭据刷新失败，原凭据文件未变更")
			return
		}
		if err := acct.SaveAtomic(); err != nil {
			adminError(w, 500, "刷新完成但保存失败，请检查数据目录")
			return
		}
		a.pool.ClearSessionDead(req.UID)
		credits, err := a.up.UserResource(acct)
		if err == nil {
			a.pool.SetCredits(req.UID, credits)
			a.pool.Flush()
		}
		a.event("账号刷新", "凭据已更新")
		adminJSON(w, map[string]any{"ok": true, "balance_updated": err == nil})
		return
	case "delete-confirm":
		st, _ := a.pool.Status(req.UID)
		if !st.Disabled || st.InFlight != 0 {
			adminError(w, 409, "请先停用账号并等待在途请求结束")
			return
		}
		for id, c := range a.confirms {
			if time.Now().After(c.Expires) {
				delete(a.confirms, id)
			}
		}
		if len(a.confirms) >= 32 {
			adminError(w, 429, "待确认操作过多")
			return
		}
		id := randomID()
		a.confirms[id] = confirmation{req.UID, time.Now().Add(2 * time.Minute)}
		adminJSON(w, map[string]string{"confirmation": id})
		return
	case "delete":
		c, ok := a.confirms[req.Confirmation]
		delete(a.confirms, req.Confirmation)
		if !ok || c.UID != req.UID || time.Now().After(c.Expires) {
			adminError(w, 409, "删除确认无效或已过期")
			return
		}
		if err := a.pool.RemoveManaged(req.UID, acct); err != nil {
			adminError(w, 409, "账号未停用、仍有请求或文件删除失败")
			return
		}
		a.event("账号删除", "成功")
	default:
		adminError(w, 400, "未知账号操作")
		return
	}
	adminJSON(w, map[string]bool{"ok": true})
}

func (a *adminServer) rotateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key     string `json:"key"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeAdmin(w, r, &req) {
		return
	}
	if !req.Confirm || len(req.Key) < 32 || len(req.Key) > 256 || strings.TrimSpace(req.Key) != req.Key || strings.ContainsAny(req.Key, "\r\n\t ") {
		adminError(w, 400, "新密钥至少 32 位且不含空白，并需确认修改")
		return
	}
	if sha256.Sum256([]byte(req.Key)) == a.digest {
		adminError(w, 400, "API 密钥不能与管理凭证相同")
		return
	}
	if os.Getenv("WB2A_API_KEY") != "" {
		adminError(w, 409, "API 密钥由环境变量管理，本页面不可修改")
		return
	}
	if err := backupAndRotate(a.configPath, req.Key); err != nil {
		adminError(w, 500, "配置备份或保存失败，当前 API 密钥未变更")
		return
	}
	a.api.SetAPIKey(req.Key)
	a.event("API 密钥", "已备份并更新")
	adminJSON(w, map[string]bool{"ok": true, "backup_created": true})
}

func backupAndRotate(path, key string) error {
	if !ownedPath(filepath.Dir(path), path, false) {
		return errors.New("unsafe config path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil || doc == nil {
		return errors.New("invalid config")
	}
	backupDir := filepath.Join(filepath.Dir(path), "admin-backups")
	if err = os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	backup := filepath.Join(backupDir, "config-"+time.Now().UTC().Format("20060102T150405")+"-"+randomID()+".json")
	if !ownedPath(backupDir, backup, true) {
		return errors.New("unsafe backup path")
	}
	f, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	doc["api_key"], _ = json.Marshal(key)
	updated, _ := json.MarshalIndent(doc, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(updated)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr = tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), path)
}
