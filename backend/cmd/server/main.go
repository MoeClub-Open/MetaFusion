package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/assistant"
	"github.com/metafusion/metafusion-app/internal/audit"
	"github.com/metafusion/metafusion-app/internal/capabilities"
	"github.com/metafusion/metafusion-app/internal/catalog"
	"github.com/metafusion/metafusion-app/internal/httplog"
	"github.com/metafusion/metafusion-app/internal/nettrust"
	"github.com/metafusion/metafusion-app/internal/upstream"
)

func main() {
	env := func(k, v string) string {
		if x := os.Getenv(k); x != "" {
			return x
		}
		return v
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required for the catalog runtime")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := catalog.Open(ctx, dsn)
	if err != nil {
		log.Fatalf("catalog database connection failed: %v", err)
	}
	defer s.DB.Close()

	// S1：HTTP 进程启动只做兼容版本检查（只读，见 catalog startup.go）。
	// 结构迁移（mf-migrate up）、种子升级（mf-migrate seed）、完整性扫描（mf-migrate check-refs）
	// 是显式运维任务，不再由每次启动/扩容执行。检查失败即 Fatal：库没准备好时拒绝服务。
	// 2026-09 CrashLoop 的根治：启动路径不再合并种子，无降级分支；种子状态仍由 /health 暴露。
	if err = s.CheckSchemaContract(ctx); err != nil {
		log.Fatalf("catalog schema incompatible: %v", err)
	}
	log.Print("catalog schema compatible; seed upgrades run via mf-migrate seed, integrity scans via mf-migrate check-refs")
	// 限流策略已在上面的只读检查里载入快照。这里再起一个每分钟的刷新：
	// 策略只有一处写入入口，但服务是多副本的，保存方只刷新自己的快照，
	// 其余副本靠 etag 轮询收敛（没有变化就一次索引查询，不反序列化）。
	go s.RunRateLimitPolicyRefresher(ctx)
	search, searchErr := catalog.NewOpenSearchClient(os.Getenv("OPENSEARCH_URL"), os.Getenv("OPENSEARCH_USERNAME"), os.Getenv("OPENSEARCH_PASSWORD"))
	if searchErr != nil {
		log.Fatalf("OpenSearch configuration invalid: %v", searchErr)
	}
	s.OpenSearch = search
	go s.RunOpenSearchIndexer(ctx)
	log.Print("OpenSearch entity search enabled; PostgreSQL supplies authoritative content and authorization")
	retention, err := catalog.RetentionPolicyFromEnv(os.Getenv)
	if err != nil {
		log.Fatalf("catalog retention configuration invalid: %v", err)
	}
	storageURL := strings.TrimSpace(os.Getenv("STORAGE_URL"))
	storageSecret := strings.TrimSpace(os.Getenv("STORAGE_CATALOG_LIFECYCLE_SECRET"))
	if storageSecret != "" {
		if err := catalog.ValidateStorageLifecycleConfig(storageURL, storageSecret); err != nil {
			log.Fatalf("storage lifecycle configuration invalid: %v", err)
		}
	} else {
		log.Print("storage lifecycle consumer disabled: STORAGE_CATALOG_LIFECYCLE_SECRET is not configured")
	}
	if storageURL != "" && storageSecret != "" {
		retention.RequiredConsumers = append(retention.RequiredConsumers, catalog.StorageLifecycleConsumer)
		go s.RunStorageLifecycle(ctx, storageURL, storageSecret)
	}
	go s.RunRetention(ctx, retention)

	// 审计写入器（跨服务契约 §3）：一个后台 goroutine + 有界队列，挂在 Store 上供
	// registerGroup 的中间件使用。关停时排空队列——进程直接退会把队列里最后一批行丢掉。
	s.Audit = audit.NewRecorder(s.DB, audit.ServiceName)
	defer s.Audit.Close()

	// 目录侧只验签，不签发：按 AUTH_JWT_PUBLIC_KEY（静态公钥）→ AUTH_JWKS_URL（账号服务的 JWKS）
	// 取公钥；私钥始终留在账号服务，目录的环境变量里出现私钥会被拒绝启动。
	// 三者都未配置时验签器不可用，需要身份的写接口会按未登录处理——这是有意的 fail closed，
	// 只影响写与个性化，公开读不受影响。
	verifier, terr := catalog.NewTokenVerifierFromEnv(env("AUTH_JWT_ISSUER", "https://findverse.cc/api"), env("AUTH_JWT_AUDIENCE", "metafusion"))
	if terr != nil {
		log.Fatalf("token verifier initialization failed: %v", terr)
	}
	s.Verifier = verifier
	if verifier.Ephemeral() {
		log.Print("no token signing key material configured (AUTH_JWT_PUBLIC_KEY / AUTH_JWKS_URL): catalog cannot verify tokens, authenticated writes will be rejected as anonymous")
	} else {
		log.Printf("catalog verifies RS256 tokens using %s", verifier.Source())
	}

	// PAT（个人访问令牌，mfp_ 前缀）的消费侧：目录不签发、不读 auth 库，带 mfp_ 的请求交给
	// 账号服务的内省端点判定（结果进程内缓存 60 秒 → 吊销与过期最长 60 秒后在下游生效）。
	// AUTH_URL 未配置时不装配内省器：这类请求回 503 auth_unavailable（依赖不可用），
	// 而不是 401——bot/CI 拿到 401 会以为凭据有问题去换令牌。
	if authURL := strings.TrimSpace(os.Getenv("AUTH_URL")); authURL != "" {
		s.PAT = catalog.NewPATIntrospector(authURL)
		s.Creds = catalog.NewCredentialsChecker(authURL)
		log.Printf("catalog accepts personal access tokens via %s (introspection cached for %s)", authURL, catalog.PATCacheTTL)
		// 实际生效的出站策略要能被运维看见：账号服务慢到什么程度算故障、会重试几次、熔断阈值多少，
		// 只写在代码里的话，"为什么 PAT 请求 503 了"只能靠读源码猜。
		if pol := catalog.PATIntrospectPolicy(); true {
			log.Printf("auth upstream policy: attempts=%d attempt_timeout=%s budget=%s backoff=%s..%s jitter=%.2f breaker=%d consecutive failures / %s open",
				pol.Attempts, pol.AttemptTimeout, pol.Budget, pol.BaseBackoff, pol.MaxBackoff, pol.Jitter, pol.BreakerThreshold, pol.BreakerOpenFor)
		}
	} else {
		log.Print("AUTH_URL is not configured: personal access tokens (mfp_ prefix) will be rejected with 503 auth_unavailable")
	}

	r := gin.New()
	// request_id 必须在访问日志之前：接受 X-Request-ID 或自行生成，写入上下文与响应头；
	// AccessLogger 每一行都带上它，方便用返回的 request_id 在容器 stdout 里对账。
	r.Use(httplog.RequestID(), httplog.AccessLogger(nil), gin.Recovery())
	// 真实客户端 IP：只信任显式声明的来源（TRUSTED_PROXIES，默认回环 + RFC1918 私网 = 网关容器所在网段）。
	// 此前这里是 SetTrustedProxies(nil)（谁都不是代理），于是 XFF 被整段忽略、ClientIP() 恒等于网关
	// 容器 IP：routeLimiter 的桶键退化成"全站共享一个桶"（线上实测第 11 次 /api/catalog/compare 即 429），
	// 审计行的 actor_ip 也全是网关地址。配置非法直接拒绝启动——静默退化回共享桶在功能上看不出来。
	trustedProxies, perr := nettrust.Apply(r, os.Getenv(nettrust.EnvVar))
	if perr != nil {
		log.Fatalf("trusted proxies configuration invalid: %v", perr)
	}
	log.Printf("trusted proxies for X-Forwarded-For: %s", trustedProxies)
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		// 切流自检用：标明本次响应来自哪个上游，便于确认网关前缀是否已切到目标服务
		// （metafusion-auth / -community / -storage 返回同名头，拆分后能逐前缀核对）。
		c.Header("X-MetaFusion-Service", "metafusion-catalog")
		c.Next()
	})

	if origins := os.Getenv("CORS_ALLOWED_ORIGINS"); origins != "" {
		r.Use(cors.New(cors.Config{
			AllowOrigins:     strings.Split(origins, ","),
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Authorization", "Content-Type", "Accept-Language", "X-Request-ID", "Idempotency-Key"},
			AllowCredentials: true,
		}))
	}

	// INTERNAL_API_TOKEN 是跨服务投递站内通知的共享密钥（唯一一个服务间写端点）。
	// 未配置即该端点关闭：评论回复照常成功，只是不产生通知（行为可见、可排查），
	// 而不是"任何登录用户都能给任何人塞通知"。密钥只从环境读取，不进日志。
	internalToken := strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN"))
	if internalToken == "" {
		log.Print("INTERNAL_API_TOKEN is not configured: cross-service notification delivery is disabled (POST /api/notifications/internal returns 503 internal_api_disabled)")
	}
	catalogHTTP := catalog.HTTP{Store: s, InternalToken: internalToken}
	aiKey, err := assistant.ParseEncryptionKey(os.Getenv("AI_ENCRYPTION_KEY"))
	if err != nil {
		log.Fatal("AI_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	if len(aiKey) == 0 {
		log.Print("AI_ENCRYPTION_KEY is not configured: LLM credential writes and calls are disabled")
	}
	aiStore := assistant.NewStore(s.DB, aiKey)
	go func() {
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				recoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				_, recoveryErr := aiStore.RecoverReservations(recoveryCtx, time.Now().Add(-10*time.Minute))
				cancel()
				if recoveryErr != nil && ctx.Err() == nil {
					log.Print("assistant: abandoned usage reservation recovery failed")
				}
			}
		}
	}()
	catalog.RegisterOpenAPIModule("assistant", assistant.AddOpenAPI)
	catalogHTTP.Register(r)
	catalogHTTP.RegisterModule(r, assistant.HTTP{Store: aiStore, Catalog: s}.Register)

	// 能力清单是**部署态声明**：子系统拆出去之后，能力由部署配置声明（见 capabilities 包），
	// 目录不探测上游、不发任何出站请求，清单只有 {id, enabled}。它是唯一的公开只读端点，
	// 注册在 /api 组之外（匿名可读，不需要身份也不需要审计）。
	capabilities.New(os.Getenv).Register(r)

	// /healthz 是进程存活；/health 与其它服务同形（status+service），供网关/运维面聚合探针统一读取。
	// /health 另外带 definitions（见 catalog.DefinitionStatus）：etag 标记当前生效文档，
	// degraded/pending_error 表示"这次种子合并没生效，站点仍按上一个
	// 已发布定义服务"。状态码刻意保持 200——降级可用不是"不健康"，回 503 会把编排器拉回
	// "重启到好为止"的循环，那正是要根除的 CrashLoop。探针判据：definitions.degraded == true。
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "live"}) })
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "live", "service": "metafusion-catalog", "definitions": s.DefinitionStatus(), "search_ready": s.OpenSearch.Ready(), "search_indexing_delayed": s.OpenSearch.IndexingDelayed()})
	})
	// 浅探针只探本进程自己的依赖（PG），必须保持毫秒级：编排的 healthcheck 每 10 秒打它一次。
	// 跨服务依赖另开 deep=1（编排与常规探针都不带这个参数），因此不会把探针拖慢。
	r.GET("/ready", func(c *gin.Context) {
		check, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if s.DB.PingContext(check) != nil {
			c.JSON(503, gin.H{"status": "unavailable"})
			return
		}
		body := gin.H{"status": "ready", "dependencies": []string{"postgres"}}
		if deep := c.Query("deep"); deep == "1" || strings.EqualFold(deep, "true") {
			// 深探：并发探关键上游（账号服务 = PAT 内省与 JWKS 的依赖），总时长被 ProbeAll 封在 3s。
			// 上游不可用时回 503 + status=degraded：这是"依赖不全、别往上切流"的信号，
			// 与浅探针的语义（本进程连不上库）刻意区分开。
			results := upstream.ProbeAll(c.Request.Context(), 3*time.Second, []upstream.ProbeTarget{s.PAT.ProbeTarget()})
			body["upstreams"] = results
			for _, r := range results {
				if r.Status != upstream.ProbeReady {
					body["status"] = "degraded"
					c.JSON(503, body)
					return
				}
			}
		}
		c.JSON(200, body)
	})

	server := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		log.Print("MetaFusion catalog core ready (PostgreSQL baseline)")
		done <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Fatalf("server shutdown error: %v", err)
		}
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		log.Fatalf("server error: %v", err)
	}
}
