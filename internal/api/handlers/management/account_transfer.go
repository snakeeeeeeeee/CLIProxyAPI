package management

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const (
	maxSub2APIImportItems     = 1000
	maxAccountTransferBodyLen = 32 << 20
)

type accountTransferRequest struct {
	PoolID         string   `json:"pool_id"`
	IDs            []string `json:"ids"`
	IncludeProxies *bool    `json:"include_proxies"`
}

type accountTransferImportRequest struct {
	PoolID string                          `json:"pool_id"`
	Data   resourcepool.Sub2APIDataPayload `json:"data"`
}

// ExportClaudeCodeAccounts exports a sub2api-compatible OAuth account bundle.
func (h *Handler) ExportClaudeCodeAccounts(c *gin.Context) {
	setAccountTransferNoStore(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAccountTransferBodyLen)
	var body accountTransferRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "请求格式无效"})
		return
	}
	includeProxies := true
	if body.IncludeProxies != nil {
		includeProxies = *body.IncludeProxies
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	payload, err := store.ExportSub2APIData(c.Request.Context(), body.PoolID, dedupeTrimmedStrings(body.IDs), includeProxies)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "export_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, payload)
}

// ExportClaudeCodeSessionKeys exports retained plaintext SessionKeys, one per line.
func (h *Handler) ExportClaudeCodeSessionKeys(c *gin.Context) {
	setAccountTransferNoStore(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAccountTransferBodyLen)
	var body accountTransferRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "请求格式无效"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	keys, unavailable, err := store.ExportSessionKeys(c.Request.Context(), body.PoolID, dedupeTrimmedStrings(body.IDs))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "export_failed", "message": err.Error()})
		return
	}
	if len(keys) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "session_keys_unavailable", "message": "所选账号没有可导出的 SessionKey；升级前导入的原始 SessionKey 无法恢复",
			"unavailable": unavailable,
		})
		return
	}
	c.Header("Content-Disposition", `attachment; filename="claude-session-keys.txt"`)
	c.Header("X-Session-Key-Unavailable", strconv.Itoa(unavailable))
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(strings.Join(keys, "\n")+"\n"))
}

// ImportClaudeCodeAccounts imports one sub2api account bundle into an explicit pool.
func (h *Handler) ImportClaudeCodeAccounts(c *gin.Context) {
	setAccountTransferNoStore(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAccountTransferBodyLen)
	var body accountTransferImportRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "请求格式无效"})
		return
	}
	if err := validateSub2APIDataPayload(body.Data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_bundle", "message": err.Error()})
		return
	}
	body.PoolID = strings.TrimSpace(body.PoolID)
	if body.PoolID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_pool", "message": "pool_id 不能为空"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	targetPool, err := store.RequireActiveAccountPool(c.Request.Context(), body.PoolID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_pool", "message": "目标账号池不存在或已归档"})
		return
	}
	body.PoolID = targetPool.ID
	result := h.importSub2APIData(c.Request.Context(), store, body.PoolID, body.Data)
	if result.AccountCreated > 0 || result.AccountUpdated > 0 {
		h.triggerConfigReload(c.Request.Context())
		resourcepool.PublishAccountChanged("", "oauth_import")
		resourcepool.PublishStatsChanged("account")
	}
	if result.ProxyCreated > 0 {
		resourcepool.PublishProxyChanged("", "oauth_import")
	}
	c.JSON(http.StatusOK, result)
}

func setAccountTransferNoStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
}

func (h *Handler) importSub2APIData(ctx context.Context, store *resourcepool.Store, poolID string, payload resourcepool.Sub2APIDataPayload) resourcepool.Sub2APIImportResult {
	result := resourcepool.Sub2APIImportResult{Errors: []resourcepool.Sub2APIImportError{}}
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		poolID = resourcepool.DefaultAccountPoolID
	}
	proxyIDByKey := make(map[string]string)
	existingProxies, err := store.ListProxies(ctx)
	if err != nil {
		result.ProxyFailed = len(payload.Proxies)
		result.AccountFailed = len(payload.Accounts)
		result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "bundle", Message: "读取现有代理失败"})
		return result
	}
	proxyIDByURL := make(map[string]string, len(existingProxies))
	for _, proxy := range existingProxies {
		proxyIDByURL[strings.TrimSpace(proxy.ProxyURL)] = proxy.ID
		if key, ok := sub2APIProxyKeyFromURL(proxy.ProxyURL); ok {
			proxyIDByKey[key] = proxy.ID
		}
	}
	for _, item := range payload.Proxies {
		key := strings.TrimSpace(item.ProxyKey)
		proxyURL, canonicalKey, errProxy := sub2APIProxyURL(item)
		if key == "" {
			key = canonicalKey
		}
		if errProxy != nil {
			result.ProxyFailed++
			result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "proxy", Name: item.Name, Message: errProxy.Error()})
			continue
		}
		if existingID := proxyIDByURL[proxyURL]; existingID != "" {
			proxyIDByKey[key] = existingID
			proxyIDByKey[canonicalKey] = existingID
			result.ProxyReused++
			continue
		}
		status := strings.ToLower(strings.TrimSpace(item.Status))
		enabled := status != "inactive" && status != "disabled" && status != "expired"
		created, errCreate := store.CreateProxy(ctx, resourcepool.ProxyResourceSeed{Name: item.Name, ProxyURL: proxyURL, Enabled: &enabled})
		if errCreate != nil {
			result.ProxyFailed++
			result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "proxy", Name: item.Name, Message: "代理保存失败"})
			continue
		}
		proxyIDByURL[proxyURL] = created.ID
		proxyIDByKey[key] = created.ID
		proxyIDByKey[canonicalKey] = created.ID
		result.ProxyCreated++
	}
	for _, item := range payload.Accounts {
		if errAccount := validateSub2APIAccount(item); errAccount != nil {
			result.AccountFailed++
			result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: errAccount.Error()})
			continue
		}
		proxyID := ""
		if item.ProxyKey != nil && strings.TrimSpace(*item.ProxyKey) != "" {
			proxyKey := strings.TrimSpace(*item.ProxyKey)
			proxyID = proxyIDByKey[proxyKey]
			if proxyID == "" {
				result.AccountFailed++
				result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "关联代理不存在或导入失败"})
				continue
			}
		}
		record, email, errRecord := authFromSub2APIAccount(item, poolID, proxyID)
		if errRecord != nil {
			result.AccountFailed++
			result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: errRecord.Error()})
			continue
		}
		_, errExisting := store.GetAccountByAuthID(ctx, record.ID)
		if errors.Is(errExisting, sql.ErrNoRows) && email != "" {
			accountByEmail, foundByEmail, errEmail := store.FindAccountOverlay(ctx, "", email)
			if errEmail != nil {
				result.AccountFailed++
				result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "读取现有账号失败"})
				continue
			}
			if foundByEmail {
				record.ID = accountByEmail.Account.AuthID
				record.FileName = accountByEmail.Account.AuthID
				errExisting = nil
			}
		}
		updated := errExisting == nil
		if errExisting != nil && !errors.Is(errExisting, sql.ErrNoRows) {
			result.AccountFailed++
			result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "读取现有账号失败"})
			continue
		}
		if updated {
			existing, errRead := store.GetAccountByAuthID(ctx, record.ID)
			if errRead != nil {
				result.AccountFailed++
				result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "读取现有账号失败"})
				continue
			}
			if existing.PoolID != poolID {
				result.AccountFailed++
				result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "账号已属于其他账号池"})
				continue
			}
		}
		if h.postAuthHook != nil {
			if errHook := h.postAuthHook(ctx, record); errHook != nil {
				result.AccountFailed++
				result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "账号导入钩子执行失败"})
				continue
			}
		}
		account, errRegister := store.RegisterClaudeCodeAccountWithCredentialOriginInPool(ctx, poolID, record.ID, email, proxyID, record, "", "", "oauth_import", "")
		if errRegister != nil {
			result.AccountFailed++
			message := "账号保存失败"
			if errors.Is(errRegister, resourcepool.ErrAccountInOtherPool) {
				message = "账号已属于其他账号池"
			}
			result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: message})
			continue
		}
		if item.Priority >= 0 || item.Notes != nil {
			patch := resourcepool.AccountPatch{Priority: &item.Priority}
			if item.Notes != nil {
				note := strings.TrimSpace(*item.Notes)
				patch.Note = &note
			}
			if _, errPatch := store.PatchAccount(ctx, account.ID, patch); errPatch != nil {
				result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "账号已导入，但优先级或备注保存失败"})
			}
		}
		if item.Concurrency > 0 {
			capacity, errCapacity := store.GetAccountCapacity(ctx, account.ID)
			if errCapacity != nil {
				result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "账号已导入，但并发容量读取失败"})
			} else {
				capacity.ConcurrencyLimit = item.Concurrency
				if _, errCapacity = store.SaveAccountCapacity(ctx, account.ID, *capacity); errCapacity != nil {
					result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "账号已导入，但并发容量保存失败"})
				}
			}
		}
		if h.postAuthPersistHook != nil {
			if errHook := h.postAuthPersistHook(ctx, record); errHook != nil {
				result.Errors = append(result.Errors, resourcepool.Sub2APIImportError{Kind: "account", Name: item.Name, Message: "账号已导入，但外部凭据持久化失败"})
			}
		}
		if updated {
			result.AccountUpdated++
		} else {
			result.AccountCreated++
		}
	}
	return result
}

func validateSub2APIDataPayload(payload resourcepool.Sub2APIDataPayload) error {
	if payload.Type != "" && payload.Type != resourcepool.Sub2APIDataType && payload.Type != "sub2api-bundle" {
		return fmt.Errorf("不支持的数据类型 %q", payload.Type)
	}
	if payload.Version != 0 && payload.Version != resourcepool.Sub2APIDataVersion {
		return fmt.Errorf("不支持的数据版本 %d", payload.Version)
	}
	if payload.Proxies == nil || payload.Accounts == nil {
		return fmt.Errorf("proxies 和 accounts 必须是数组")
	}
	if len(payload.Proxies) > maxSub2APIImportItems || len(payload.Accounts) > maxSub2APIImportItems {
		return fmt.Errorf("单次最多导入 %d 个代理和账号", maxSub2APIImportItems)
	}
	return nil
}

func validateSub2APIAccount(item resourcepool.Sub2APIDataAccount) error {
	if strings.TrimSpace(item.Name) == "" {
		return fmt.Errorf("账号名称为空")
	}
	if !strings.EqualFold(strings.TrimSpace(item.Platform), "anthropic") {
		return fmt.Errorf("仅支持 anthropic 账号")
	}
	if !strings.EqualFold(strings.TrimSpace(item.Type), "oauth") {
		return fmt.Errorf("仅支持 oauth 账号")
	}
	if len(item.Credentials) == 0 {
		return fmt.Errorf("OAuth 凭据为空")
	}
	if item.Concurrency < 0 || item.Priority < 0 {
		return fmt.Errorf("并发和优先级不能小于 0")
	}
	return nil
}

func authFromSub2APIAccount(item resourcepool.Sub2APIDataAccount, poolID, proxyID string) (*coreauth.Auth, string, error) {
	accessToken := transferString(item.Credentials, "access_token")
	refreshToken := transferString(item.Credentials, "refresh_token")
	if accessToken == "" || refreshToken == "" {
		return nil, "", fmt.Errorf("缺少 access_token 或 refresh_token")
	}
	email := transferString(item.Credentials, "email")
	if email == "" {
		email = transferString(item.Extra, "email")
	}
	if email == "" && strings.Contains(item.Name, "@") {
		email = strings.TrimSpace(item.Name)
	}
	expiresAt := transferString(item.Credentials, "expires_at")
	if expiresAt == "" {
		expiresAt = transferString(item.Credentials, "expired")
	}
	lastRefresh := transferString(item.Credentials, "last_refresh")
	if lastRefresh == "" {
		lastRefresh = time.Now().UTC().Format(time.RFC3339)
	}
	storage := &claudeauth.ClaudeTokenStorage{
		IDToken: transferString(item.Credentials, "id_token"), AccessToken: accessToken, RefreshToken: refreshToken,
		LastRefresh: lastRefresh, Email: email, OrganizationUUID: transferString(item.Credentials, "org_uuid"),
		AccountUUID: transferString(item.Credentials, "account_uuid"), Type: "claude", Expire: expiresAt,
	}
	authID := transferString(item.Extra, "cliproxy_auth_id")
	if !validTransferredAuthID(authID) {
		identity := storage.AccountUUID
		if identity == "" {
			identity = email
		}
		if identity == "" {
			identity = refreshToken
		}
		digest := sha256.Sum256([]byte(identity))
		authID = "claude-import-" + hex.EncodeToString(digest[:8]) + ".json"
	}
	metadata := map[string]any{
		"email": email, "resource_pool_account": true, "resource_pool_type": "claude-code", "resource_pool_id": poolID,
		"org_uuid": storage.OrganizationUUID, "account_uuid": storage.AccountUUID,
	}
	if proxyID != "" {
		metadata["proxy_resource_id"] = proxyID
	}
	return &coreauth.Auth{ID: authID, Provider: "claude", FileName: authID, Storage: storage, Metadata: metadata}, email, nil
}

func validTransferredAuthID(authID string) bool {
	authID = strings.TrimSpace(authID)
	if authID == "" || authID == "." || authID == ".." || len(authID) > 512 || strings.ContainsRune(authID, '\x00') {
		return false
	}
	if strings.ContainsAny(authID, "/\\\r\n") {
		return false
	}
	return true
}

func sub2APIProxyURL(item resourcepool.Sub2APIDataProxy) (string, string, error) {
	protocol := strings.ToLower(strings.TrimSpace(item.Protocol))
	switch protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", "", fmt.Errorf("代理协议无效")
	}
	host := strings.TrimSpace(item.Host)
	if host == "" || item.Port <= 0 || item.Port > 65535 {
		return "", "", fmt.Errorf("代理地址或端口无效")
	}
	parsed := &url.URL{Scheme: protocol, Host: net.JoinHostPort(host, strconv.Itoa(item.Port))}
	if item.Username != "" || item.Password != "" {
		parsed.User = url.UserPassword(item.Username, item.Password)
	}
	key := fmt.Sprintf("%s|%s|%d|%s|%s", protocol, host, item.Port, strings.TrimSpace(item.Username), strings.TrimSpace(item.Password))
	return parsed.String(), key, nil
}

func sub2APIProxyKeyFromURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", false
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port <= 0 {
		return "", false
	}
	username, password := "", ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	return fmt.Sprintf("%s|%s|%d|%s|%s", strings.ToLower(parsed.Scheme), parsed.Hostname(), port, username, password), true
}

func transferString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
