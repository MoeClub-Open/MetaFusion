package catalog

// 身份闸门集中在这里：/api 组的身份中间件（只验签，身份只来自账号服务签发的 RS256 令牌）
// 与各路由组复用的权限闸门 required()。

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// patRejection 是 PAT 请求**不能按匿名继续**时的结论：调用方必须直接结束请求，
// 不能放行到处理器再让它以 401 authentication_required 或 403 收场。
//
// 401 只用于"无效/已吊销/已过期"（三者共用一个码，避免把内省变成探测口）；
// 503 用于账号服务不可达（含未配置 AUTH_URL）——回 401 会让 bot/CI 以为凭据有问题去换令牌，
// 而实际是下游依赖故障，重试等待才是对的。
type patRejection struct {
	status int
	code   string
}

// attachIdentity 按 PAT → Bearer → Cookie 顺序解析身份并把结果放进上下文：身份只来自账号服务
// （JWT 是它的 RS256 签发、PAT 是它内省判定），目录侧**不查库、不签发**，所以这里没有"会话表
// 兜底"分支——账号数据归账号服务，目录不读它的表。Bearer 与 Cookie 各试一次：前端可能带着刚
// 过期的 Bearer 令牌，而 HttpOnly Cookie 里是刷新后的新令牌（或反之），不能互相顶掉。
// JWT/会话验签失败按匿名处理（fail closed）；PAT 失败则返回 patRejection 由调用方结束请求。
// 返回 nil 表示"身份已解决或按匿名继续"。不清除上下文里已有的身份（http_gate_test 这类夹具会
// 先注入 catalog_user）。
func attachIdentity(c *gin.Context, s *Store) *patRejection {
	if user(c) != nil {
		return nil
	}
	bearer := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if IsPAT(bearer) {
		// PAT 请求以 Bearer 为准，**不回落到 cookie**：浏览器里可能同时存在另一个用户的
		// mf_session，回落到它会把"机器身份"悄悄变成"浏览器登录身份"。
		u, err := s.introspectPAT(c.Request.Context(), bearer)
		switch {
		case err != nil:
			return &patRejection{status: http.StatusServiceUnavailable, code: CodeAuthUnavailable}
		case u == nil:
			return &patRejection{status: http.StatusUnauthorized, code: CodeInvalidToken}
		}
		c.Set("catalog_user", u)
		return nil
	}
	cookie, _ := c.Cookie("mf_session")
	for _, token := range []string{bearer, cookie} {
		if token == "" {
			continue
		}
		u, err := s.Authenticate(token)
		if err != nil {
			continue
		}
		ok, unavailable := s.checkCredentials(c.Request.Context(), u)
		if unavailable {
			return &patRejection{status: http.StatusServiceUnavailable, code: CodeAuthUnavailable}
		}
		if !ok {
			continue // 世代不匹配或已封禁：试下一凭据 / 按匿名
		}
		c.Set("catalog_user", u)
		break
	}
	return nil
}

// checkCredentials 在 JWKS 验签后核对账号库世代。未配置核对器时放行（ok=true）。
func (s *Store) checkCredentials(ctx context.Context, u *User) (ok bool, unavailable bool) {
	if s == nil || s.Creds == nil || !s.Creds.Enabled() || u == nil {
		return true, false
	}
	st, err := s.Creds.Lookup(ctx, u.ID)
	if err != nil {
		return false, true
	}
	if !st.Match(u.TokenVersion) {
		return false, false
	}
	return true, false
}

// introspectPAT 是 PAT 的内省入口：内省器未注入（未配置 AUTH_URL）时按不可用处理。
// 身份只能问账号服务，本侧不查 auth 库，也不在本地缓存明文。
func (s *Store) introspectPAT(ctx context.Context, token string) (*User, error) {
	if s == nil || s.PAT == nil {
		return nil, errPATUnavailable
	}
	ident, err := s.PAT.Introspect(ctx, token)
	if err != nil {
		return nil, err
	}
	if ident == nil {
		return nil, nil
	}
	return ident.catalogUser(), nil
}

// rejectPAT 把 patRejection 写成响应（错误体只回稳定机器码）。
func rejectPAT(c *gin.Context, r *patRejection) {
	c.AbortWithStatusJSON(r.status, gin.H{"error": r.code})
}

// attachUser 是 /api 组的身份中间件（组内所有端点共用一次解析结果）。
func attachUser(s *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if r := attachIdentity(c, s); r != nil {
			rejectPAT(c, r)
			return
		}
		c.Next()
	}
}
