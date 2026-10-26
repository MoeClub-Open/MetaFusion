package catalog

// 凭据世代核对：本地 JWKS 验签之后，向账号服务读取 auth.users.token_version / banned。
// 共享源是 Postgres（经 GET /api/auth/credentials-status/:user_id），不是进程内 jti map。
// 改密 / 全量登出 / 重置密码 / 封禁会 bump token_version，旧会话 JWT 的 tv 对不上即作废。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/metafusion/metafusion-app/internal/upstream"
)

const (
	credentialsStatusPath = "/api/auth/credentials-status/"
	// credentialsCacheTTL 只是读穿缓存：权威仍是账号库。窗口远短于 AccessTokenTTL，
	// 避免把进程内 map 当成吊销源；多实例共享可在 Medium 换成 Redis。
	credentialsCacheTTL  = 2 * time.Second
	credentialsRespLimit = 1 << 12
)

// CredentialsStatus 与账号服务响应逐字对齐。
type CredentialsStatus struct {
	UserID       string `json:"user_id"`
	TokenVersion int64  `json:"token_version"`
	Banned       bool   `json:"banned"`
}

// CredentialsChecker 拉取账号凭据快照。baseURL 空时 Enabled=false，验签路径跳过核对
// （部署未配 AUTH_URL 时与 PAT 一样留下缺口，启动日志已警告）。
type CredentialsChecker struct {
	baseURL string
	up      *upstream.Client
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]credentialsCacheEntry
}

type credentialsCacheEntry struct {
	status CredentialsStatus
	until  time.Time
}

// NewCredentialsChecker 用账号服务基址建核对器。
func NewCredentialsChecker(baseURL string) *CredentialsChecker {
	policy := upstream.DefaultPolicy("credentials")
	policy.Attempts = 2
	policy.AttemptTimeout = 1500 * time.Millisecond
	policy.Budget = 4 * time.Second
	return &CredentialsChecker{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		up:      upstream.New(policy),
		now:     time.Now,
		cache:   map[string]credentialsCacheEntry{},
	}
}

// Enabled 报告是否配置了 AUTH_URL。
func (c *CredentialsChecker) Enabled() bool { return c != nil && c.baseURL != "" }

// Lookup 返回该用户当前凭据世代。错误表示账号服务不可达/非预期响应。
func (c *CredentialsChecker) Lookup(ctx context.Context, userID string) (CredentialsStatus, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return CredentialsStatus{}, errors.New("missing user id")
	}
	if !c.Enabled() {
		return CredentialsStatus{}, errPATUnavailable
	}
	c.mu.Lock()
	if e, ok := c.cache[userID]; ok && c.now().Before(e.until) {
		c.mu.Unlock()
		return e.status, nil
	}
	c.mu.Unlock()

	st, err := c.fetch(ctx, userID)
	if err != nil {
		return CredentialsStatus{}, err
	}
	c.mu.Lock()
	c.cache[userID] = credentialsCacheEntry{status: st, until: c.now().Add(credentialsCacheTTL)}
	c.mu.Unlock()
	return st, nil
}

func (c *CredentialsChecker) fetch(ctx context.Context, userID string) (CredentialsStatus, error) {
	u := c.baseURL + credentialsStatusPath + url.PathEscape(userID)
	header := http.Header{}
	header.Set("Accept", "application/json")
	resp, err := c.up.Do(ctx, upstream.Request{Method: http.MethodGet, URL: u, Header: header})
	if err != nil {
		return CredentialsStatus{}, fmt.Errorf("credentials status request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return CredentialsStatus{}, errors.New("user_not_found")
	}
	if resp.StatusCode != http.StatusOK {
		return CredentialsStatus{}, fmt.Errorf("credentials status status %d", resp.StatusCode)
	}
	var st CredentialsStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, credentialsRespLimit)).Decode(&st); err != nil {
		return CredentialsStatus{}, fmt.Errorf("credentials status malformed: %w", err)
	}
	if strings.TrimSpace(st.UserID) == "" {
		st.UserID = userID
	}
	return st, nil
}

// Match 报告 JWT 内 tv 是否仍与账号库一致，且账号未被封禁。
func (st CredentialsStatus) Match(tokenVersion int64) bool {
	return !st.Banned && st.TokenVersion == tokenVersion
}
