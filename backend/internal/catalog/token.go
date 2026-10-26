package catalog

// 访问令牌的**验签侧**：RS256（RSASSA-PKCS1-v1_5 + SHA-256）自包含 JWT。
//
// 账号拆分完成后，令牌只由账号服务（metafusion-auth）签发；目录侧保留的只有验签：
// 按来源优先级取一把公钥，在本地判定签名、时间窗、issuer 与 audience。
// 这里刻意**不提供签发、续期与注销**——目录进程拿不到也不需要签发路径，
// 少一条被误当成"第二个签发方"的可能；令牌撤销依赖 15 分钟短有效期与账号侧的会话表。
//
// 验签来源按优先级（见 NewTokenVerifierFromEnv）：
//  1. AUTH_JWT_PUBLIC_KEY —— 静态公钥（PEM 文本或其 base64）：少一条网络依赖，换钥匙要重启；
//  2. AUTH_JWKS_URL —— 账号服务的 JWKS：按 kid 选钥、10 分钟缓存、未知 kid 强制刷新；
// 两者都没有时 fail closed：验签器存在但拒绝一切令牌。
//
// 用标准库实现而不引入第三方 JWT 依赖：RS256 的验签只是 crypto/rsa 的
// VerifyPKCS1v15 加一段 base64url，少一层供应链风险。

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 验签来源标识：Source() 与启动日志用，也是排查"目录拿的是谁的钥匙"的唯一依据。
const (
	SourcePublicKey = "AUTH_JWT_PUBLIC_KEY"
	SourceJWKS      = "AUTH_JWKS_URL"
	SourceNone      = ""
)

// jwksCacheTTL 是 JWKS 公钥缓存时长：访问令牌有效期只有 15 分钟，缓存必须明显短于它，
// 否则账号侧轮换密钥后，旧公钥在目录侧还会被继续接受。
const jwksCacheTTL = 10 * time.Minute

// Claims 是访问令牌的载荷。字段名遵循 OIDC 惯例，与账号服务的签发侧逐字一致。
type Claims struct {
	Subject  string `json:"sub"`
	Username string `json:"preferred_username"`
	Email    string `json:"email,omitempty"`
	// Groups/Permissions 与账号服务的签发侧逐字一致（见 metafusion-auth 的 Claims）：
	// groups 是组码（展示与审计用），permissions 是展开后的权限码集合——授权只看它。
	Groups      []string `json:"groups,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	// TokenUse 是令牌用途标记（与签发侧对齐）：站内会话恒带 token_use=session，
	// 第三方 OAuth 带 token_use=oauth，id_token 带 token_use=id_token（aud 指向客户端，
	// audience 收口已拒）。audience 收口（Verify 的 bad audience）拒掉 aud 指向客户端的
	// 令牌，这里再标记"aud 仍是平台但带 OAuth 标记"的那一种。
	TokenUse string `json:"token_use,omitempty"`
	// TokenVersion 与签发侧 Claims.tv 对齐。
	TokenVersion int64 `json:"tv,omitempty"`
	Issuer   string `json:"iss"`
	Audience string `json:"aud"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
	JTI      string `json:"jti"`
}

// TokenVerifier 只持公钥：零值不可用，需经 NewTokenVerifierFromEnv。
type TokenVerifier struct {
	mu sync.RWMutex
	// static 是本地持有的验签公钥：来自 AUTH_JWT_PUBLIC_KEY。
	// 非空时不再请求 JWKS（静态公钥轮换要重启，换来少一条网络依赖）。
	static *rsa.PublicKey
	kid    string
	// jwksURL 为空表示不走 JWKS；keys 是按 kid 缓存的公钥，fetchedAt 记录最近一次成功拉取。
	jwksURL   string
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	client    *http.Client
	// refreshMu 只用来串行化"出站刷新"：flight 非空表示已有一次刷新在飞行中，等待者复用它的结果；
	// flightErr 是最近一次飞行的结果。缓存本身仍由 mu 保护，出站请求期间不持 mu。
	refreshMu sync.Mutex
	flight    chan struct{}
	flightErr error
	// source 是当前生效的来源，用于启动日志与排查。
	source   string
	issuer   string
	audience string
	// now 可在测试中替换，用于验证过期边界；nil 表示 time.Now。
	now func() time.Time
}

func (t *TokenVerifier) clock() time.Time {
	if t != nil && t.now != nil {
		return t.now()
	}
	return time.Now()
}

// NewTokenVerifierFromEnv 按优先级装配验签来源：静态公钥 → JWKS。
// 都未配置时返回只能拒绝一切令牌的验签器（fail closed）：目录的公开读接口照常工作，
// 写接口在验签失败时按未登录处理，不会静默放行。
func NewTokenVerifierFromEnv(issuer, audience string) (*TokenVerifier, error) {
	return newTokenVerifier(issuer, audience, os.Getenv)
}

// newTokenVerifier 是装配主体；getenv 可注入，便于测试三种来源与优先级。
func newTokenVerifier(issuer, audience string, getenv func(string) string) (*TokenVerifier, error) {
	t := &TokenVerifier{
		issuer:   issuer,
		audience: audience,
		// JWKS 拉取刻意不走 internal/upstream（超时分层+有界重试+熔断）：它有自己的单飞
		// （refreshMu + flight，同一时刻只发一次）、10 分钟缓存，以及**失败回落缓存公钥**的降级路径
		// ——失败不是"取不到身份"，只是"这次轮换没确认到"。给这条出站再加一层重试只会把
		// "密钥轮换确认"拖长，不会让验签更可靠（见 refreshJWKS 的注释）。
		client: &http.Client{Timeout: 2 * time.Second},
		keys:   map[string]*rsa.PublicKey{},
	}
	if raw := strings.TrimSpace(getenv("AUTH_JWT_PUBLIC_KEY")); raw != "" {
		pub, err := parseStaticPublicKey(raw)
		if err != nil {
			return nil, fmt.Errorf("AUTH_JWT_PUBLIC_KEY: %w", err)
		}
		t.static, t.kid, t.source = pub, keyID(pub), SourcePublicKey
		return t, nil
	}
	if jwks := strings.TrimSpace(getenv("AUTH_JWKS_URL")); jwks != "" {
		t.jwksURL, t.source = jwks, SourceJWKS
		return t, nil
	}
	return t, nil
}

// parseStaticPublicKey 解析静态验签公钥：PEM 文本或其 base64，支持 PKIX（PUBLIC KEY）与 PKCS#1（RSA PUBLIC KEY）。
// 刻意不接受私钥：目录的环境变量里出现私钥正是这次收口要消灭的东西，直接报错比默默接受更容易被发现。
func parseStaticPublicKey(raw string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(decodeMaybeBase64PEM(raw)))
	if block == nil {
		return nil, errors.New("not a valid PEM (want a public key, e.g. openssl rsa -pubout)")
	}
	if strings.Contains(block.Type, "PRIVATE KEY") {
		return nil, errors.New("must be a public key, not a private key")
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		pub, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("must be an RSA public key")
		}
		return pub, nil
	}
	if pub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return pub, nil
	}
	return nil, errors.New("must be a PKIX or PKCS#1 RSA public key")
}

// decodeMaybeBase64PEM 兼容"PEM 文本"与"base64 包裹的 PEM"两种写法（环境变量里写多行 PEM 很难）。
func decodeMaybeBase64PEM(raw string) string {
	if strings.Contains(raw, "-----BEGIN") {
		return raw
	}
	if dec, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return string(dec)
	}
	return raw
}

// Source 返回当前生效的验签来源（见 Source* 常量）；都未配置时返回空串。
func (t *TokenVerifier) Source() string {
	if t == nil {
		return SourceNone
	}
	return t.source
}

// Ephemeral 表示没有任何验签来源：所有需要身份的请求都会被当作匿名，调用方应据此告警配置缺失。
func (t *TokenVerifier) Ephemeral() bool { return t == nil || t.source == SourceNone }

// Verify 校验签名与时间窗。任何一步失败都返回错误，调用方据此按匿名处理。
func (t *TokenVerifier) Verify(token string) (*Claims, error) {
	if t == nil {
		return nil, errors.New("token verifier unavailable")
	}
	if t.static == nil && t.jwksURL == "" {
		return nil, errors.New("token verifier unavailable: no public key or JWKS configured")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed token")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("malformed header")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return nil, errors.New("malformed header")
	}
	// 只接受 RS256：拒绝 none/HS* 等算法，避免算法混淆攻击。
	if header.Alg != "RS256" {
		return nil, errors.New("unexpected algorithm")
	}
	key, err := t.signingKey(header.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("malformed signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, errors.New("bad signature")
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("malformed payload")
	}
	var claims Claims
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, errors.New("malformed payload")
	}
	now := t.clock().Unix()
	if claims.Expires == 0 || now >= claims.Expires {
		return nil, errors.New("token expired")
	}
	if claims.Issuer != t.issuer {
		return nil, errors.New("bad issuer")
	}
	// 受众只认配置的那一个：id_token（aud 指向第三方客户端）不是给本服务的凭证。
	if t.audience != "" && claims.Audience != t.audience {
		return nil, errors.New("bad audience")
	}
	if claims.Subject == "" {
		return nil, errors.New("missing subject")
	}
	// 用途隔离：未知 token_use fail closed（直接拒收，不按匿名放行）。
	if !validTokenUse(strings.TrimSpace(claims.TokenUse)) {
		return nil, errors.New("bad token_use")
	}
	return &claims, nil
}

// signingKey 按令牌头里的 kid 取验签公钥：命中未过期缓存直接返回；TTL 过期或 kid 未知时
// 刷新一次（轮换期间签名密钥可能刚换过）。三条约束来自 2026-09-19 第二轮架构报告 #22：
//
//  1. 出站请求不持缓存锁——JWKS 慢不会让本副本的验签排队（旧实现在持锁期间做 HTTP）；
//  2. 同一时刻只允许一次出站刷新（并发 Unknown kid 共享结果），出站请求数有界；
//  3. 刷新失败不直接判死：缓存里还有这个 kid 的键就继续用它验签（TTL 过期也算——公钥本身
//     没有有效期，TTL 只是"多久去确认一次轮换"），并记告警。账号服务抖动超过 TTL 不该让
//     本服务把所有已签发的有效令牌判成 401。
//
// 缓存里没有这个 kid 的键时仍然 fail closed：宁可不放行，也不退回"没有密钥也通过"。
func (t *TokenVerifier) signingKey(kid string) (*rsa.PublicKey, error) {
	if t.static != nil {
		return t.static, nil
	}
	if key, ok := t.freshKey(kid); ok {
		return key, nil
	}
	if err := t.refreshJWKS(kid); err != nil {
		if key, ok := t.cachedKey(kid); ok {
			log.Printf("catalog: JWKS 刷新失败，回落到缓存公钥（kid=%q，可能错过一次轮换确认）：%v", kid, err)
			return key, nil
		}
		return nil, fmt.Errorf("signing key unavailable: %w", err)
	}
	if key, ok := t.cachedKey(kid); ok {
		return key, nil
	}
	return nil, errors.New("unknown signing key")
}

// freshKey 返回缓存里命中且未过期的公钥。
func (t *TokenVerifier) freshKey(kid string) (*rsa.PublicKey, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if time.Since(t.fetchedAt) >= jwksCacheTTL {
		return nil, false
	}
	return lookupKey(t.keys, kid)
}

// cachedKey 返回缓存里这个 kid 的公钥，不看是否过期：刷新失败时的回落来源。
func (t *TokenVerifier) cachedKey(kid string) (*rsa.PublicKey, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return lookupKey(t.keys, kid)
}

// lookupKey 按 kid 取键；只有一个密钥且令牌头没带 kid 时也命中
// （账号服务总是带 kid，这条只是容错）。
func lookupKey(keys map[string]*rsa.PublicKey, kid string) (*rsa.PublicKey, bool) {
	if key, ok := keys[kid]; ok {
		return key, true
	}
	if kid == "" && len(keys) == 1 {
		for _, key := range keys {
			return key, true
		}
	}
	return nil, false
}

// refreshJWKS 拉一次 JWKS。出站请求不持 mu；refreshMu + flight 让同一时刻只有一次出站请求，
// 并发等待者复用同一次结果（不再各自打一次网络）。
func (t *TokenVerifier) refreshJWKS(kid string) error {
	t.refreshMu.Lock()
	if ch := t.flight; ch != nil {
		t.refreshMu.Unlock()
		<-ch
		t.refreshMu.Lock()
		err := t.flightErr
		t.refreshMu.Unlock()
		return err
	}
	// 拿到"刷新权"时缓存可能刚被刷好：命中就直接复用，不必再打一次网络。
	if key, ok := t.freshKey(kid); ok && key != nil {
		t.refreshMu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	t.flight = ch
	t.refreshMu.Unlock()

	err := t.fetch()

	t.refreshMu.Lock()
	t.flightErr, t.flight = err, nil
	t.refreshMu.Unlock()
	close(ch)
	return err
}

// fetch 拉取 JWKS 并整体替换缓存。2 秒超时；非 200、解析失败或没有任何可用 RSA 公钥
// 都算失败，此时旧缓存保持不动（调用方按 signingKey 的回落规则继续使用它）。
// 出站期间不持 mu：只有写入缓存的瞬间才加锁。
func (t *TokenVerifier) fetch() error {
	if t.jwksURL == "" {
		return errors.New("no jwks url configured")
	}
	req, err := http.NewRequest(http.MethodGet, t.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks unavailable: HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			KID string `json:"kid"`
			KTY string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if !strings.EqualFold(k.KTY, "RSA") {
			continue
		}
		key, err := rsaFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.KID] = key
	}
	if len(keys) == 0 {
		return errors.New("jwks has no usable key")
	}
	t.mu.Lock()
	t.keys = keys
	t.fetchedAt = time.Now()
	t.mu.Unlock()
	return nil
}

// rsaFromJWK 把 JWK 的 n/e 还原成 RSA 公钥。
func rsaFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, errors.New("invalid exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

// PublicJWK 返回验签公钥的 JWK 表示。目录侧不再暴露 JWKS 端点（由账号服务提供）；
// JWKS 模式下本服务不持有单一公钥，返回 nil。
func (t *TokenVerifier) PublicJWK() map[string]any {
	if t == nil || t.static == nil {
		return nil
	}
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": t.kid,
		"n":   b64(t.static.N.Bytes()),
		"e":   b64(big.NewInt(int64(t.static.E)).Bytes()),
	}
}

// 令牌用途取值，与账号服务签发侧同源：判定只认这三个值，缺键（空串）与未知取值
// 都在 Verify 直接拒收（bad token_use）——不存在"历史令牌缺键按会话放行"的兜底。
const (
	TokenUseSession = "session"
	TokenUseOAuth   = "oauth"
	TokenUseIDToken = "id_token"
)

// isThirdPartyUse 报告该用途是否为第三方（oauth 访问令牌或发给当事客户端的
// id_token）：与互动 internal/auth/auth.go 同契约，token_use=session 永不视为第三方。
func isThirdPartyUse(use string) bool {
	return use == TokenUseOAuth || use == TokenUseIDToken
}

// validTokenUse 判定载荷里的用途声明是否合法：未知取值直接拒收，防止将来新增用途的
// 令牌被当成已知用途放行（与签发侧 validTokenUse 同口径）。
func validTokenUse(use string) bool {
	switch use {
	case TokenUseSession, TokenUseOAuth, TokenUseIDToken:
		return true
	default:
		return false
	}
}

// ClaimsToUser 把已验签的载荷还原为 User（身份、角色与权限集合，不查库）。
// 第三方判定与互动 internal/auth/auth.go 同契约：仅 oauth/id_token 判第三方，
// session 永不视为第三方。未知用途 Verify 已拒收，这里按第三方收紧（直接构造
// Claims 绕过 Verify 时仍 fail closed）；permissions 键存在性原样带给 Can 做分支。
func ClaimsToUser(c *Claims) *User {
	if c == nil {
		return nil
	}
	use := strings.TrimSpace(c.TokenUse)
	thirdParty := isThirdPartyUse(use)
	if !validTokenUse(use) {
		thirdParty = true
	}
	return &User{ID: c.Subject, Username: c.Username, Email: c.Email,
		Groups: c.Groups, Permissions: c.Permissions,
		IsThirdParty: thirdParty, TokenVersion: c.TokenVersion}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// keyID 取公钥 SPKI DER 的 SHA-256 前 8 字节，作为稳定且不泄露密钥的 kid。
func keyID(pub *rsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "default"
	}
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}
