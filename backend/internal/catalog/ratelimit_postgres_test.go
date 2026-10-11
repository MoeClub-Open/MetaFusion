package catalog

// 限流策略的真库往返：单例读写、etag 覆盖保护、证据要求与权限闸门。
// 无 MF_V2_TEST_DSN 时 newFixture 里的 testutil.Database 自动跳过。

import (
	"context"
	"errors"
	"testing"
)

func TestSaveRateLimitPolicyRoundTripAndETag(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// 迁移 000018 播下的空文档必须可读，且语义是"全部沿用内置额度"。
	base, err := f.s.RateLimitPolicy(ctx)
	if err != nil {
		t.Fatalf("读取初始限流策略失败：%v", err)
	}
	if base.ETag == "" {
		t.Fatal("初始策略缺少 etag：覆盖保护没有基准")
	}
	if got, unlimited := base.Policy.Resolve([]string{"u"}, []string{"member"}, false, DefaultRateLimitPerMinute); unlimited || got != DefaultRateLimitPerMinute {
		t.Fatalf("空文档应沿用路由内置额度，实际 (%d, unlimited=%v)", got, unlimited)
	}

	policy := RateLimitPolicy{
		DefaultPerMinute: 240,
		Groups:           map[string]RateLimitRule{"admin": {Unlimited: true}, "anonymous": {PerMinute: 20}},
		Accounts:         map[string]RateLimitRule{f.u.ID: {PerMinute: 6000}},
	}
	saved, err := f.s.SaveRateLimitPolicy(ctx, policy, base.ETag, f.u, "限流策略往返用例", fixtureSources())
	if err != nil {
		t.Fatalf("保存限流策略失败：%v", err)
	}
	if saved.ETag == base.ETag || saved.ETag == "" {
		t.Fatalf("保存后 etag 应换新，实际 %q（原 %q）", saved.ETag, base.ETag)
	}

	// 回读：进程内快照与数据库都必须给出同一份策略。
	read, err := f.s.RateLimitPolicy(ctx)
	if err != nil {
		t.Fatalf("回读限流策略失败：%v", err)
	}
	if read.ETag != saved.ETag {
		t.Fatalf("回读 etag = %q，期望 %q", read.ETag, saved.ETag)
	}
	if got, unlimited := read.Policy.Resolve([]string{f.u.ID}, nil, false, 120); unlimited || got != 6000 {
		t.Fatalf("账号覆盖未生效：(%d, unlimited=%v)", got, unlimited)
	}
	if got, unlimited := read.Policy.Resolve(nil, nil, true, 120); unlimited || got != 20 {
		t.Fatalf("匿名组未生效：(%d, unlimited=%v)", got, unlimited)
	}

	// 热路径读的是进程内快照，不能是只在数据库里生效。
	if got, unlimited := currentRateLimitPolicy().Resolve([]string{f.u.ID}, nil, false, 120); unlimited || got != 6000 {
		t.Fatalf("进程内快照未随保存刷新：(%d, unlimited=%v)", got, unlimited)
	}

	// etag 覆盖保护：用旧 etag 再存一次必须 409，而不是静默覆盖别人的改动。
	if _, err := f.s.SaveRateLimitPolicy(ctx, RateLimitPolicy{DefaultPerMinute: 1}, base.ETag, f.u, "过期 etag", fixtureSources()); !errors.Is(err, errVersionConflict) {
		t.Fatalf("过期 etag 应报 version_conflict，实际 %v", err)
	}
	// 缺 etag 同样拒绝：没有基准就不是一次可判定的编辑。
	if _, err := f.s.SaveRateLimitPolicy(ctx, RateLimitPolicy{DefaultPerMinute: 1}, "", f.u, "缺 etag", fixtureSources()); !errors.Is(err, errVersionConflict) {
		t.Fatalf("缺 etag 应报 version_conflict，实际 %v", err)
	}
	// 冲突写不得改动已发布策略。
	if after, err := f.s.RateLimitPolicy(ctx); err != nil || after.ETag != saved.ETag || after.Policy.DefaultPerMinute != 240 {
		t.Fatalf("冲突写改动了已发布策略：etag=%v err=%v", after.ETag, err)
	}
}

func TestSaveRateLimitPolicyGuards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	base, err := f.s.RateLimitPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 运维策略需要决策说明；不伪造不存在的事实证据。
	if _, err := f.s.SaveRateLimitPolicy(ctx, RateLimitPolicy{}, base.ETag, f.u, "", nil); err == nil || err.Error() != "edit_note_required" {
		t.Fatalf("缺决策说明应报 edit_note_required，实际 %v", err)
	}
	// 非法额度只回稳定码 invalid_rate_limit。
	if _, err := f.s.SaveRateLimitPolicy(ctx, RateLimitPolicy{DefaultPerMinute: -1}, base.ETag, f.u, "非法额度", fixtureSources()); err == nil || err.Error() != "invalid_rate_limit" {
		t.Fatalf("非法额度应报 invalid_rate_limit，实际 %v", err)
	}
	// 权限闸门：只有 catalog.definitions.manage 能改限流。
	editor := fixtureUser("editor")
	if _, err := f.s.SaveRateLimitPolicy(ctx, RateLimitPolicy{DefaultPerMinute: 9}, base.ETag, editor, "无权限", fixtureSources()); !errors.Is(err, errForbidden) {
		t.Fatalf("缺少 catalog.definitions.manage 应报 forbidden，实际 %v", err)
	}
	// 第三方 OAuth 身份在治理码上一律拒绝（见 permission.go 的 Can）。
	thirdParty := fixtureUser("admin")
	thirdParty.IsThirdParty = true
	if _, err := f.s.SaveRateLimitPolicy(ctx, RateLimitPolicy{DefaultPerMinute: 9}, base.ETag, thirdParty, "第三方身份", fixtureSources()); !errors.Is(err, errForbidden) {
		t.Fatalf("第三方身份应报 forbidden，实际 %v", err)
	}
	// 失败路径都不得改动已发布策略。
	if after, err := f.s.RateLimitPolicy(ctx); err != nil || after.ETag != base.ETag {
		t.Fatalf("被拒的写改动了策略：etag=%v err=%v", after.ETag, err)
	}
}
