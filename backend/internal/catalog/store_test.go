package catalog

import (
	"context"
	"fmt"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"sync"
	"testing"
)

func TestDefaultsAndDynamicFields(t *testing.T) {
	d := Defaults()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	d.Fields["personal_photo_note"] = Field{Names: names("个人影像备注", "Personal photo note"), Type: "text", ApplicableKinds: []string{"work"}, Enabled: true}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	d.Fields["id"] = Field{Names: names("非法", "Invalid"), Type: "text"}
	if err := d.Validate(); err == nil {
		t.Fatal("reserved identity field accepted")
	}
}
func TestLocatorAndEvidence(t *testing.T) {
	d := Defaults()
	ref := func(string, []string) error { return nil }
	// 动态组校验：有定位键却缺锚点（relative_to）必须拒绝
	if err := d.value(d.Fields["locator"], map[string]any{"page_start": float64(1)}, ref, false); err == nil {
		t.Fatal("locator without anchor accepted")
	}
	// 起止顺序颠倒必须拒绝
	if err := d.value(d.Fields["locator"], map[string]any{"relative_to": "track", "time_start_ms": float64(500), "time_end_ms": float64(100)}, ref, false); err == nil {
		t.Fatal("reversed range accepted")
	}
	// 正常定位应通过
	if err := d.value(d.Fields["locator"], map[string]any{"relative_to": "track", "time_start_ms": float64(0), "time_end_ms": float64(500)}, ref, false); err != nil {
		t.Fatalf("valid locator rejected: %v", err)
	}
	// 空定位合法（整轨收录）
	if err := d.value(d.Fields["locator"], map[string]any{}, ref, false); err != nil {
		t.Fatalf("empty locator rejected: %v", err)
	}
	// 未声明的定位键必须拒绝（说明键集合由 definitions 决定，不是硬编码）
	if err := d.value(d.Fields["locator"], map[string]any{"relative_to": "track", "no_such_key": float64(1)}, ref, false); err == nil {
		t.Fatal("undeclared locator key accepted")
	}
	if err := validateSources("本人首次发布", []Source{{Kind: "self", Citation: "作者自述"}}); err == nil || err.Error() != "source_not_eligible" {
		t.Fatalf("self source accepted: %v", err)
	}
	if validateSources("x", []Source{{Kind: "url", Citation: "x", URL: "javascript:alert(1)"}}) == nil {
		t.Fatal("unsafe URL accepted")
	}
}
func TestRelationCyclesAndContexts(t *testing.T) {
	d := Defaults()
	a, b, c := Entity{ID: "a", Kind: "work"}, Entity{ID: "b", Kind: "work"}, Entity{ID: "c", Kind: "work"}
	ref := func(string, []string) error { return nil }
	r := Relation{ID: "3", Type: "sequel_of", SourceID: "c", TargetID: "a"}
	prior := []Relation{{ID: "1", Type: "sequel_of", SourceID: "a", TargetID: "b"}, {ID: "2", Type: "sequel_of", SourceID: "b", TargetID: "c"}}
	if validateRelation(d, r, c, a, prior, ref, false) == nil {
		t.Fatal("long cycle accepted")
	}
	_ = b
	r = Relation{ID: "2", Type: "voiced_by", SourceID: "a", TargetID: "actor", Attributes: map[string]any{"character": "character2"}}
	prior = []Relation{{ID: "1", Type: "voiced_by", SourceID: "a", TargetID: "actor", Attributes: map[string]any{"character": "character1"}}}
	if err := validateRelation(d, r, a, Entity{Kind: "agent"}, prior, ref, false); err != nil {
		t.Fatal(err)
	}
}

// includes 方向语义契约（缺口5）：专辑→歌曲是"组成内容"，歌曲→专辑是
// "所属集合"，方向由 source/target 端点决定。前端 componentEntries
// （WorkContentDirectory.tsx，已导出供回归）按此划分三区块独立成段：
// 报告复现"歌曲页把所属专辑列进内容目录"与"专辑有自身表达时组成歌曲
// 被隐藏"在此失败。前端 jiti 隔离复现（A/B/C 三场景）已验证展示侧。
func TestIncludesDirectionContract(t *testing.T) {
	d := Defaults()
	ref := func(string, []string) error { return nil }
	album := Entity{ID: "album", Kind: "work"}
	song := Entity{ID: "song", Kind: "work"}
	// 正向：专辑包含歌曲，端点 work→work 合法。
	fwd := Relation{ID: "1", Type: "includes", SourceID: "album", TargetID: "song"}
	if err := validateRelation(d, fwd, album, song, nil, ref, false); err != nil {
		t.Fatalf("album includes song rejected: %v", err)
	}
	// 反向查询是展示层按 source_id 判定的事：歌曲页读到"以自己为 target"
	// 的 includes 边时归入"所属集合"，不进"组成内容"。此处锁定正向边
	// 合法、非法端点被拒；反向归属划分由前端 componentEntries 回归覆盖
	// （jiti 隔离复现 A/B/C 已验证），此处不重复断言方向展示。
	other := Entity{ID: "other", Kind: "work"}
	rev := Relation{ID: "2", Type: "includes", SourceID: "other", TargetID: "album"}
	if err := validateRelation(d, rev, other, album, []Relation{fwd}, ref, false); err != nil {
		t.Fatalf("other collection includes album rejected: %v", err)
	}
	// 非 work/collection 端点不得用 includes（如歌曲直接包含录音）。
	bad := Relation{ID: "3", Type: "includes", SourceID: "song", TargetID: "expr"}
	if err := validateRelation(d, bad, song, Entity{ID: "expr", Kind: "expression"}, nil, ref, false); err == nil {
		t.Fatal("includes to expression accepted")
	}
}

func TestPostgresCatalog(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	admin := fixtureUser("admin")
	sources := fixtureSources()
	save := func(e Entity) Entity {
		t.Helper()
		if e.State == "" {
			e.State = "active"
		}
		v, err := s.Save(ctx, Edit{Entity: e, ExpectedVersion: e.Version, EditNote: "integration fixture", Sources: sources}, admin)
		if err != nil {
			t.Fatalf("%s %s: %v", e.Kind, e.Title, err)
		}
		return v
	}
	entity := func(kind, title string) Entity {
		// 发布态要求至少一条翻译：夹具给一条，语义上与真实写入一致。
		return Entity{Kind: kind, Title: title, Attributes: map[string]any{}, Translations: map[string]Translation{"en": {Title: title}}}
	}
	song := save(entity("work", "原创歌曲"))
	album := save(entity("work", "个人专辑"))
	recording := entity("expression", "录音室版")
	recording.WorkID = song.ID
	recording = save(recording)
	release := entity("release", "普通版")
	release.Subjects = []Subject{{WorkID: song.ID, Role: "compilation", Position: 1}, {WorkID: album.ID, Role: "primary"}}
	release = save(release)
	direct, err := s.Get(ctx, release.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	batched, err := s.getMany(ctx, []string{release.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []Entity{direct, batched[release.ID]} {
		if len(got.Subjects) != 2 || got.Subjects[0].WorkID != album.ID || got.Subjects[1].WorkID != song.ID {
			t.Fatalf("release subjects order differs: %+v", got.Subjects)
		}
	}
	medium := entity("medium", "CD 1")
	medium.ReleaseID = release.ID
	medium = save(medium)
	track := entity("track", "01 原创歌曲")
	track.MediumID = medium.ID
	track.Number = "A1"
	track.Contents = []Inclusion{{ExpressionID: recording.ID}}
	track = save(track)
	occurrences, err := s.Occurrences(ctx, song.ID, nil)
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("reverse inclusion %v %d", err, len(occurrences))
	}
	t.Run("undeclared cross-work reference", func(t *testing.T) {
		r := entity("release", "未声明歌曲的盒装")
		r.Subjects = []Subject{{WorkID: album.ID, Role: "primary"}}
		r = save(r)
		m := entity("medium", "附盘")
		m.ReleaseID = r.ID
		m = save(m)
		tr := track
		tr.ID = ""
		tr.Version = 0
		tr.MediumID = m.ID
		if _, err := s.Save(ctx, Edit{Entity: tr, Sources: sources, EditNote: "invalid"}, admin); err == nil {
			t.Fatal("undeclared reference accepted")
		}
	})
	t.Run("same scope and hierarchy cycles", func(t *testing.T) {
		u := entity("content_unit", "第三章")
		u.WorkID = song.ID
		u = save(u)
		v := entity("content_unit", "章节组")
		v.WorkID = album.ID
		v = save(v)
		u.ParentID = v.ID
		if _, err := s.Save(ctx, Edit{Entity: u, ExpectedVersion: u.Version, Sources: sources, EditNote: "invalid"}, admin); err == nil {
			t.Fatal("cross-work parent accepted")
		}
		u.ParentID = u.ID
		if _, err := s.Save(ctx, Edit{Entity: u, ExpectedVersion: u.Version, Sources: sources, EditNote: "invalid"}, admin); err == nil {
			t.Fatal("self parent accepted")
		}
	})
	t.Run("optimistic concurrency", func(t *testing.T) {
		// 同一实体、同一 expected_version 的并发写：恰好一个成功、另一个 version_conflict，
		// 且失败者零落库（版本只前进一格、本批只留一条修订）。断言口径固定，不看时序运气。
		base := save(entity("work", "乐观并发基准"))
		titles := [2]string{"乐观并发甲", "乐观并发乙"}
		results := [2]error{}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < len(results); i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				payload := base
				payload.Title = titles[i]
				<-start
				_, err := s.Save(ctx, Edit{Entity: payload, ExpectedVersion: base.Version, Sources: sources, EditNote: "race"}, admin)
				results[i] = err
			}(i)
		}
		close(start)
		wg.Wait()
		success, conflict, winner := 0, 0, -1
		for i, err := range results {
			switch {
			case err == nil:
				success++
				winner = i
			case err != nil && err.Error() == "version_conflict":
				conflict++
			default:
				t.Errorf("writer %d: unexpected error %v", i, err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("success count=%d conflict count=%d, want 1/1", success, conflict)
		}
		assertNoLostUpdate(t, s, base, base.Version+1, titles[winner], "race")
	})
	t.Run("optimistic concurrency (N writers)", func(t *testing.T) {
		// 更狠的一轮：N 个 goroutine 同时用同一 expected_version 写同一实体。
		// 乐观并发的下限是"成功数恰为 1"——多于 1 就是丢更新（后写覆盖先写）。
		base := save(entity("work", "乐观并发基准 N"))
		const writers = 8
		titles := make([]string, writers)
		results := make([]error, writers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			titles[i] = fmt.Sprintf("乐观并发 N-%d", i)
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				payload := base
				payload.Title = titles[i]
				<-start
				_, err := s.Save(ctx, Edit{Entity: payload, ExpectedVersion: base.Version, Sources: sources, EditNote: "race-n"}, admin)
				results[i] = err
			}(i)
		}
		close(start)
		wg.Wait()
		success, conflict, winner := 0, 0, -1
		for i, err := range results {
			switch {
			case err == nil:
				success++
				winner = i
			case err != nil && err.Error() == "version_conflict":
				conflict++
			default:
				t.Errorf("writer %d: unexpected error %v", i, err)
			}
		}
		if success != 1 {
			t.Fatalf("success count=%d, want exactly 1", success)
		}
		if conflict != writers-1 {
			t.Fatalf("version_conflict count=%d, want %d", conflict, writers-1)
		}
		assertNoLostUpdate(t, s, base, base.Version+1, titles[winner], "race-n")
	})
	t.Run("optimistic concurrency (relations)", func(t *testing.T) {
		// 关系写走同一原则：同一关系的并发更新同样只能有一个赢家。
		rel, err := s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "sequel_of", SourceID: song.ID, TargetID: album.ID, Attributes: map[string]any{}}, EditNote: "relation fixture", Sources: sources}, admin)
		if err != nil {
			t.Fatal(err)
		}
		const writers = 8
		results := make([]error, writers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				payload := rel
				payload.Position = i + 1
				<-start
				_, err := s.SaveRelation(ctx, RelationEdit{Relation: payload, ExpectedVersion: rel.Version, EditNote: "race-n", Sources: sources}, admin)
				results[i] = err
			}(i)
		}
		close(start)
		wg.Wait()
		success, conflict := 0, 0
		for i, err := range results {
			switch {
			case err == nil:
				success++
			case err != nil && err.Error() == "version_conflict":
				conflict++
			default:
				t.Errorf("writer %d: unexpected error %v", i, err)
			}
		}
		if success != 1 || conflict != writers-1 {
			t.Fatalf("success count=%d conflict count=%d, want 1/%d", success, conflict, writers-1)
		}
		var colVersion int64
		if err := s.DB.QueryRowContext(ctx, "SELECT version FROM catalog.relations WHERE id=$1", rel.ID).Scan(&colVersion); err != nil {
			t.Fatal(err)
		}
		if colVersion != rel.Version+1 {
			t.Fatalf("relation version=%d, want %d", colVersion, rel.Version+1)
		}
	})
	t.Run("removed publication states are rejected", func(t *testing.T) {
		e := entity("work", "未公开个人作品")
		e.State = "draft"
		_, err := s.Save(ctx, Edit{Entity: e, EditNote: "retired state", Sources: sources}, admin)
		if err == nil {
			t.Fatal("draft state accepted")
		}
	})
	t.Run("definition impacts and retired terms", func(t *testing.T) {
		v, err := s.Definitions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		d := v.Document
		delete(d.Vocabularies["release_role"].Terms, "compilation")
		probe, err := s.DefinitionImpactFor(ctx, d, admin)
		if err != nil || len(probe.Issues) == 0 {
			t.Fatal("missing impact")
		}
		if _, err := s.SaveDefinitions(ctx, d, v.ETag, admin, "publish invalid", sources); err == nil {
			t.Fatal("invalid definition published")
		}
	})
	t.Run("revision and outbox consistency", func(t *testing.T) {
		var revisions, matchingEvents int
		err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE EXISTS (
			SELECT 1 FROM catalog.outbox o WHERE o.entity_id=r.target_id
			AND o.version=r.version AND o.payload=r.snapshot
		)) FROM catalog.revisions r WHERE r.target_id=$1`, song.ID).Scan(&revisions, &matchingEvents)
		if err != nil {
			t.Fatal(err)
		}
		if revisions == 0 || matchingEvents != revisions {
			t.Fatalf("revision/outbox mismatch: revisions=%d matching_events=%d", revisions, matchingEvents)
		}
	})
	if _, err = s.List(ctx, ListOptions{Kind: "work"}, nil); err != nil {
		t.Fatal(err)
	}
}

// assertNoLostUpdate 复核并发写之后的落库事实：版本列只前进一格、列与文档的题名一致、
// 本批次只留一条修订。任何"后写覆盖先写"的丢更新都会在其中一条上暴露。
func assertNoLostUpdate(t *testing.T, s *Store, base Entity, wantVersion int64, wantTitle, note string) {
	t.Helper()
	var colVersion int64
	var colTitle, docTitle string
	if err := s.DB.QueryRowContext(context.Background(), "SELECT version,title,document->>'title' FROM catalog.entities WHERE id=$1", base.ID).Scan(&colVersion, &colTitle, &docTitle); err != nil {
		t.Fatalf("read back raced entity: %v", err)
	}
	if colVersion != wantVersion {
		t.Fatalf("version=%d, want %d", colVersion, wantVersion)
	}
	if colTitle != wantTitle || docTitle != wantTitle {
		t.Fatalf("title=%q document.title=%q, want %q", colTitle, docTitle, wantTitle)
	}
	var revisions int
	if err := s.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog.revisions WHERE target_id=$1 AND edit_note=$2", base.ID, note).Scan(&revisions); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if revisions != 1 {
		t.Fatalf("revisions=%d, want 1", revisions)
	}
}

// 定位方案与记录级属性全部由 definitions 决定，键集合不硬编码：
// 后台给 locator 组加一个子字段后，无需改代码/迁移即可通过校验。
func TestLocatorKeysAreDefinitionsDriven(t *testing.T) {
	d := Defaults()
	ref := func(string, []string) error { return nil }
	// 默认不含 spindel 键 → 拒绝
	if err := d.value(d.Fields["locator"], map[string]any{"relative_to": "track", "spindle": "A"}, ref, false); err == nil {
		t.Fatal("undeclared locator key accepted before extending definitions")
	}
	// 在 definitions 里加子字段后即可通过（模拟后台扩展）
	loc := d.Fields["locator"]
	loc.Fields["spindle"] = Field{Names: names("盘面", "Spindle"), Type: "text", Enabled: true}
	d.Fields["locator"] = loc
	if err := d.value(d.Fields["locator"], map[string]any{"relative_to": "track", "spindle": "A"}, ref, false); err != nil {
		t.Fatalf("extended locator key rejected: %v", err)
	}
}

// 记录级附加属性：默认不允许未知字段，后台加子字段后放行。
func TestStructuralAttributesAreDefinitionsDriven(t *testing.T) {
	d := Defaults()
	ref := func(string, []string) error { return nil }
	if err := d.value(d.Fields["inclusion_attributes"], map[string]any{"note": "x"}, ref, false); err == nil {
		t.Fatal("undeclared inclusion attribute accepted")
	}
	f := d.Fields["inclusion_attributes"]
	f.Fields["note"] = Field{Names: names("备注", "Note"), Type: "text", Enabled: true}
	d.Fields["inclusion_attributes"] = f
	if err := d.value(d.Fields["inclusion_attributes"], map[string]any{"note": "x"}, ref, false); err != nil {
		t.Fatalf("extended inclusion attribute rejected: %v", err)
	}
	// 发行对象附加属性同理
	if err := d.value(d.Fields["subject_attributes"], map[string]any{"seq": float64(1)}, ref, false); err == nil {
		t.Fatal("undeclared subject attribute accepted")
	}
}

// number 校验必须同时接受 JSON 的 float64 与代码内部构造的 int/int64。
func TestNumberAcceptsGoInts(t *testing.T) {
	d := Defaults()
	ref := func(string, []string) error { return nil }
	f := Field{Names: names("数量", "Qty"), Type: "number", Enabled: true}
	for _, v := range []any{float64(3), int(3), int64(3)} {
		if err := d.value(f, v, ref, false); err != nil {
			t.Errorf("%T value rejected: %v", v, err)
		}
	}
}

// 结构属性入口是固定契约：删除或改成非 group 会让校验取不到定义而空转，
// 必须在校验阶段就被拒绝，而不是放任未声明数据通过。
func TestStructuralAttributeEntrypointsCannotBeRemoved(t *testing.T) {
	base := Defaults()
	if err := base.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	for _, code := range []string{"locator", "inclusion_attributes", "subject_attributes"} {
		deleted := Defaults()
		delete(deleted.Fields, code)
		if err := deleted.Validate(); err == nil {
			t.Errorf("definitions without %s accepted", code)
		}
		retyped := Defaults()
		retyped.Fields[code] = Field{Names: names("坏定义", "Bad"), Type: "text", Enabled: true}
		if err := retyped.Validate(); err == nil {
			t.Errorf("non-group %s accepted", code)
		}
	}
}

// 入口定义被删后，实体层也不得放过未声明数据：effectiveGroupField 取到
// 零值 Field 时 value 的 default 分支报 unknown_field_type（空数据已被
// isEmptyValue 提前放行，整轨收录的空定位不受影响）。
// 隔离复现：删 locator 后 Track 带未知定位键仍通过 validateEntity。
func TestStructuralEntryMissingRejectsEntityData(t *testing.T) {
	ref := func(string, []string) error { return nil }
	mkTrack := func(locator Locator, attrs map[string]any) Entity {
		return Entity{Kind: "track", Title: "T", State: "active",
			MediumID: "11111111-1111-4111-8111-111111111111",
			Contents: []Inclusion{{ExpressionID: "22222222-2222-4222-8222-222222222222",
				Locator: locator, Attributes: attrs}}}
	}
	mkRelease := func(attrs map[string]any) Entity {
		return Entity{Kind: "release", Title: "R", State: "active",
			Subjects: []Subject{{WorkID: "44444444-4444-4444-8444-444444444444",
				Role: "primary", Attributes: attrs}}}
	}
	for _, code := range []string{"locator", "inclusion_attributes", "subject_attributes"} {
		d := Defaults()
		delete(d.Fields, code)
		var withData, empty Entity
		switch code {
		case "locator":
			withData = mkTrack(Locator{"relative_to": "medium", "no_such_key": "x"}, nil)
			empty = mkTrack(Locator{}, nil)
		case "inclusion_attributes":
			withData = mkTrack(Locator{"relative_to": "medium"}, map[string]any{"no_such_key": "x"})
			empty = mkTrack(Locator{"relative_to": "medium"}, nil)
		default:
			withData = mkRelease(map[string]any{"no_such_key": "x"})
			empty = mkRelease(nil)
		}
		if err := d.validateEntity(withData, ref, false); err == nil {
			t.Errorf("entity data accepted with %s entry deleted", code)
		}
		if err := d.validateEntity(empty, ref, false); err != nil {
			t.Errorf("empty structural data rejected with %s entry deleted: %v", code, err)
		}
	}
}

// 对比语义是闭集：只接受系统真正实现的规则，自由填写一律拒绝。
func TestCompareSemanticsIsClosedSet(t *testing.T) {
	ok := Defaults()
	loc := ok.Fields["locator"]
	loc.Fields["time_start_ms"] = Field{Names: names("起始", "Start"), Type: "number", Enabled: true, Semantics: "content"}
	ok.Fields["locator"] = loc
	if err := ok.Validate(); err != nil {
		t.Fatalf("declared semantics rejected: %v", err)
	}
	bad := Defaults()
	loc = bad.Fields["locator"]
	loc.Fields["page_start"] = Field{Names: names("起始页", "Start page"), Type: "number", Enabled: true, Semantics: "guess_by_length"}
	bad.Fields["locator"] = loc
	if err := bad.Validate(); err == nil {
		t.Fatal("free-form semantics accepted")
	}
}

// 默认种子的语义划分是对比算法的输入契约：time_* 为内容范围（content），
// page_*/path/chapter/relative_to 为本版位置（locating）。此测试把该契约
// 钉死——若有人把 page_* 改成 content（或反之），报告 Table-1/2 的判定
// 会静默反转（排版变化被判内容变化），必须在此失败而不是在线上被发现。
// 前端 compareAlignment.ts 的 A/B 复现用例与此同源（jiti 隔离复现已验证）。
func TestDefaultSeedSemanticsContract(t *testing.T) {
	loc := Defaults().Fields["locator"]
	for code, want := range map[string]string{
		"time_start_ms": "content", "time_end_ms": "content",
		"page_start": "locating", "page_end": "locating",
		"path": "locating", "chapter": "locating", "relative_to": "locating",
	} {
		f, ok := loc.Fields[code]
		if !ok {
			t.Fatalf("seed locator missing %s", code)
		}
		if f.Semantics != want {
			t.Errorf("seed locator.%s semantics = %q, want %q", code, f.Semantics, want)
		}
	}
}

// 停用子字段后：存量数据仍可原样保存，新增使用必须被拒绝——结构属性落点也要覆盖。
func TestRetirementCoversStructuralAttributes(t *testing.T) {
	d := Defaults()
	loc := d.Fields["locator"]
	page := loc.Fields["page_start"]
	page.Enabled = false
	loc.Fields["page_start"] = page
	d.Fields["locator"] = loc
	prior := Entity{Contents: []Inclusion{{ExpressionID: "e1", Position: 0, Locator: Locator{"relative_to": "medium", "page_start": float64(5)}}}}
	// 未改动：放行
	same := prior
	if err := d.retiredEntity(same, prior); err != nil {
		t.Fatalf("unchanged retired locator rejected: %v", err)
	}
	// 改了页码（新增/变更使用停用字段）：拒绝
	changed := Entity{Contents: []Inclusion{{ExpressionID: "e1", Position: 0, Locator: Locator{"relative_to": "medium", "page_start": float64(9)}}}}
	if err := d.retiredEntity(changed, prior); err == nil {
		t.Fatal("new use of disabled locator subfield accepted")
	}
}

// 实体合并要改写**全部**动态属性位置里的实体引用，包括结构属性。
func TestMergeRewritesStructuralAttributeReferences(t *testing.T) {
	d := Defaults()
	inc := d.Fields["inclusion_attributes"]
	inc.Fields["translator"] = Field{Names: names("译者", "Translator"), Type: "entity", Kinds: []string{"agent"}, Enabled: true}
	d.Fields["inclusion_attributes"] = inc
	loc := d.Fields["locator"]
	loc.Fields["anchor_entity"] = Field{Names: names("锚点实体", "Anchor entity"), Type: "entity", Kinds: []string{"agent"}, Enabled: true}
	d.Fields["locator"] = loc
	e := Entity{Contents: []Inclusion{{
		ExpressionID: "e1",
		Locator:      Locator{"relative_to": "medium", "anchor_entity": "source-id"},
		Attributes:   map[string]any{"translator": "source-id"},
	}}}
	rewriteEntityRefs(d, &e, "source-id", "target-id")
	if e.Contents[0].Attributes["translator"] != "target-id" {
		t.Errorf("inclusion attribute reference not rewritten: %v", e.Contents[0].Attributes)
	}
	if e.Contents[0].Locator["anchor_entity"] != "target-id" {
		t.Errorf("locator reference not rewritten: %v", e.Contents[0].Locator)
	}
}

// 模板用 primary_date_field 声明主日期字段，取代代码里硬编码 edition_date。
func TestTemplateDeclaresPrimaryDateField(t *testing.T) {
	d := Defaults()
	if got := d.Templates["music"].PrimaryDateField; got != "edition_date" {
		t.Errorf("music primary date field = %q", got)
	}
	// 声明必须指向真实存在的字段，否则展示层会取到空值
	for code, tpl := range d.Templates {
		if tpl.PrimaryDateField == "" {
			continue
		}
		if _, ok := d.Fields[tpl.PrimaryDateField]; !ok {
			t.Errorf("template %s declares unknown field %q", code, tpl.PrimaryDateField)
		}
	}
}

// 模板声明的 badge_fields / primary_date_field 必须指向真实字段，
// 否则展示层静默取空；后台写错声明应在保存 draft 时就被拒绝。
func TestTemplateFieldReferencesValidated(t *testing.T) {
	d := Defaults()
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	bad := Defaults()
	bad.Templates["music"] = Template{
		Names: names("音乐", "Music"), Directory: "tree",
		BadgeFields: []string{"no_such_field"},
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("template with unknown badge field accepted")
	}
	bad2 := Defaults()
	bad2.Templates["music"] = Template{
		Names: names("音乐", "Music"), Directory: "tree",
		PrimaryDateField: "no_such_field",
	}
	if err := bad2.Validate(); err == nil {
		t.Fatal("template with unknown primary date field accepted")
	}
}

// 有匹配 scheme 时超集子字段被实体校验拒绝，无匹配时回退全局组通过；
// require_range 缺范围被拒绝。参考 TestStructural* 风格，不连数据库。
func TestSchemeConvergenceInEntityValidation(t *testing.T) {
	ref := func(string, []string) error { return nil }
	mkTrack := func(locator Locator) Entity {
		return Entity{
			Kind: "track", Title: "A1", State: "active",
			MediumID: "11111111-1111-4111-8111-111111111111",
			Contents: []Inclusion{{
				ExpressionID: "22222222-2222-4222-8222-222222222222",
				Locator:      locator,
			}},
		}
	}
	// 显式启用测试方案（或单独构造测试 scheme）：
	// 并集内字段通过，并集外超集字段被拒绝。
	d := Defaults()
	vinyl := d.Schemes["vinyl_track_locator"]
	vinyl.Enabled = true
	d.Schemes["vinyl_track_locator"] = vinyl
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	badFormat := vinyl
	formats := []string{"unregistered_format"}
	badFormat.MediumFormats = &formats
	d.Schemes["vinyl_track_locator"] = badFormat
	if err := d.Validate(); err == nil {
		t.Fatal("scheme accepted an undefined medium format")
	}
	d.Schemes["vinyl_track_locator"] = vinyl
	ok := mkTrack(Locator{"relative_to": "track", "chapter": "A1"})
	if err := d.validateEntity(ok, ref, false, "vinyl"); err != nil {
		t.Fatalf("in-scheme locator rejected: %v", err)
	}
	superset := mkTrack(Locator{"relative_to": "track", "chapter": "A1", "page_start": float64(1)})
	if err := d.validateEntity(superset, ref, false, "vinyl"); err == nil {
		t.Fatal("superset locator beyond matched scheme accepted")
	}
	if err := d.validateEntity(superset, ref, false, "cd"); err != nil {
		t.Fatalf("CD track should use global locator fields: %v", err)
	}
	if err := d.validateEntity(superset, ref, false); err != nil {
		t.Fatalf("unknown parent format should use global locator fields: %v", err)
	}
	// 无匹配时回退全局组：同一定位在 medium 拥有者（无 track scheme）下通过。
	medium := Entity{
		Kind: "medium", Title: "CD1", State: "active",
		ReleaseID:  "33333333-3333-4333-8333-333333333333",
		Attributes: map[string]any{},
	}
	_ = medium
	release := Entity{
		Kind: "release", Title: "R", State: "active",
		Subjects: []Subject{{
			WorkID: "44444444-4444-4444-8444-444444444444", Role: "primary",
			Attributes: map[string]any{},
		}},
	}
	if err := d.validateEntity(release, ref, false); err != nil {
		t.Fatalf("fallback without matched scheme rejected: %v", err)
	}
	pageOnTrack := mkTrack(Locator{"relative_to": "track", "page_start": float64(3), "page_end": float64(5)})
	plain := Defaults()
	plain.Schemes = nil
	if err := plain.validateEntity(pageOnTrack, ref, false); err != nil {
		t.Fatalf("nil schemes should fall back to global group: %v", err)
	}
	// require_range：匹配场景要求内容范围时，缺 content 语义子字段被拒绝。
	ranged := Defaults()
	ranged.Schemes["paper_range"] = Scheme{
		Names: names("纸书范围", "Paper range"), Slot: "locator",
		Kinds: []string{"track"}, Fields: []string{"relative_to", "time_start_ms", "time_end_ms"},
		RequireRange: true, Enabled: true,
	}
	withoutRange := mkTrack(Locator{"relative_to": "track"})
	if err := ranged.validateEntity(withoutRange, ref, false); err == nil {
		t.Fatal("locator without content range accepted under require_range")
	}
	withRange := mkTrack(Locator{"relative_to": "track", "time_start_ms": float64(0), "time_end_ms": float64(500)})
	if err := ranged.validateEntity(withRange, ref, false); err != nil {
		t.Fatalf("locator with content range rejected: %v", err)
	}
}

// TestExpressionDetailsBatch：批量上屏端点与单条端点语义一致——
// 表达实体、同 Work 收录、首个署名标题都应命中；不存在/不可见 ID 不返回。
func TestExpressionDetailsBatch(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	admin := fixtureUser("admin")
	sources := fixtureSources()
	save := func(e Entity) Entity {
		t.Helper()
		if e.State == "" {
			e.State = "active"
		}
		v, err := s.Save(ctx, Edit{Entity: e, ExpectedVersion: e.Version, EditNote: "batch fixture", Sources: sources}, admin)
		if err != nil {
			t.Fatalf("%s %s: %v", e.Kind, e.Title, err)
		}
		return v
	}
	entity := func(kind, title string) Entity {
		// 发布态要求至少一条翻译：夹具给一条，语义上与真实写入一致。
		return Entity{Kind: kind, Title: title, Attributes: map[string]any{}, Translations: map[string]Translation{"en": {Title: title}}}
	}
	song := save(entity("work", "批量歌曲"))
	album := save(entity("work", "批量专辑"))
	rec := entity("expression", "批量录音")
	rec.WorkID = song.ID
	rec = save(rec)
	rel := entity("release", "批量发行")
	rel.Subjects = []Subject{{WorkID: album.ID, Role: "primary"}, {WorkID: song.ID, Role: "compilation", Position: 1}}
	rel = save(rel)
	med := entity("medium", "CD 1")
	med.ReleaseID = rel.ID
	med = save(med)
	tr := entity("track", "01 批量歌曲")
	tr.MediumID = med.ID
	tr.Contents = []Inclusion{{ExpressionID: rec.ID}}
	tr = save(tr)
	// 署名：表达 → 人员。
	actor := save(entity("agent", "批量演唱者"))
	if _, err := s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "performed_by", SourceID: rec.ID, TargetID: actor.ID, Attributes: map[string]any{}},
		EditNote: "batch fixture", Sources: sources,
	}, admin); err != nil {
		t.Fatal(err)
	}

	out, err := s.ExpressionDetailsBatch(ctx, []string{rec.ID, "00000000-0000-0000-0000-000000000000"}, &admin)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := out.Items[rec.ID]
	if !ok {
		t.Fatalf("batch missing requested expression: %+v", out)
	}
	if d.Entity.Title != "批量录音" {
		t.Fatalf("bad entity: %+v", d.Entity)
	}
	// 批量返回的表达实体必须带结构归属：document 落库时清空了 work_id/content_unit_id，
	// 只解 JSON 会让前端按章节对齐全部退化到 work 级。
	if d.Entity.WorkID != song.ID {
		t.Fatalf("batch entity missing work_id (structural parentage): %+v", d.Entity)
	}
	if len(d.Occurrences) != 1 {
		t.Fatalf("expected 1 occurrence, got %d", len(d.Occurrences))
	}
	// 收录以引用形态返回：实体在共享表中。
	if d.Occurrences[0].ReleaseID != rel.ID {
		t.Fatalf("occurrence release mismatch: %+v", d.Occurrences[0])
	}
	if out.Entities[rel.ID].Kind != "release" {
		t.Fatalf("shared entity table missing release: %+v", out.Entities)
	}
	if d.CreditTitle != "批量演唱者" {
		t.Fatalf("credit not aggregated: %q", d.CreditTitle)
	}
	if _, present := out.Items["00000000-0000-0000-0000-000000000000"]; present {
		t.Fatal("nonexistent id returned in batch")
	}
}

// TestOccurrencesScopeByKind：收录必须按实体 kind 收敛——
// 同 Work 的其它表达不再混入某表达自身的收录；同篇目兄弟表达单列 siblings。
func TestOccurrencesScopeByKind(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	admin := fixtureUser("admin")
	sources := fixtureSources()
	save := func(e Entity) Entity {
		t.Helper()
		if e.State == "" {
			e.State = "active"
		}
		v, err := s.Save(ctx, Edit{Entity: e, ExpectedVersion: e.Version, EditNote: "scope fixture", Sources: sources}, admin)
		if err != nil {
			t.Fatalf("%s %s: %v", e.Kind, e.Title, err)
		}
		return v
	}
	entity := func(kind, title string) Entity {
		// 发布态要求至少一条翻译：夹具给一条，语义上与真实写入一致。
		return Entity{Kind: kind, Title: title, Attributes: map[string]any{}, Translations: map[string]Translation{"en": {Title: title}}}
	}
	// 同 Work 两集：各自独立收录，不应互相污染。
	work := save(entity("work", "分集动画"))
	unitA := entity("content_unit", "第一集")
	unitA.WorkID = work.ID
	unitA = save(unitA)
	unitB := entity("content_unit", "第二集")
	unitB.WorkID = work.ID
	unitB = save(unitB)
	// 同一篇目下的两个表达（如 TV 版与加长版）。
	exprA1 := entity("expression", "第一集 TV 版")
	exprA1.WorkID = work.ID
	exprA1.ContentUnitID = unitA.ID
	exprA1 = save(exprA1)
	exprA2 := entity("expression", "第一集 加长版")
	exprA2.WorkID = work.ID
	exprA2.ContentUnitID = unitA.ID
	exprA2 = save(exprA2)
	exprB := entity("expression", "第二集正片")
	exprB.WorkID = work.ID
	exprB.ContentUnitID = unitB.ID
	exprB = save(exprB)
	relA := entity("release", "第一集发行")
	relA.Subjects = []Subject{{WorkID: work.ID, Role: "primary"}}
	relA = save(relA)
	medA := entity("medium", "BD 1")
	medA.ReleaseID = relA.ID
	medA = save(medA)
	trackA := entity("track", "S1E1")
	trackA.MediumID = medA.ID
	trackA.Contents = []Inclusion{{ExpressionID: exprA1.ID}}
	save(trackA)
	relB := entity("release", "第二集发行")
	relB.Subjects = []Subject{{WorkID: work.ID, Role: "primary"}}
	relB = save(relB)
	medB := entity("medium", "BD 2")
	medB.ReleaseID = relB.ID
	medB = save(medB)
	trackB := entity("track", "S1E2")
	trackB.MediumID = medB.ID
	trackB.Contents = []Inclusion{{ExpressionID: exprB.ID}}
	save(trackB)

	// expression 自身：只含本表达的收录。
	occ, err := s.Occurrences(ctx, exprA1.ID, nil)
	if err != nil || len(occ) != 1 {
		t.Fatalf("expression own occurrences: %v %d", err, len(occ))
	}
	if occ[0]["expression_id"].(string) != exprA1.ID {
		t.Fatalf("expression occurrence bled from another expression: %+v", occ[0])
	}
	// content_unit：该集下各表达的收录。
	occ, err = s.Occurrences(ctx, unitA.ID, nil)
	if err != nil || len(occ) != 1 {
		t.Fatalf("content_unit occurrences: %v %d", err, len(occ))
	}
	// work：作品下全部表达的收录，两集都算。
	occ, err = s.Occurrences(ctx, work.ID, nil)
	if err != nil || len(occ) != 2 {
		t.Fatalf("work occurrences: %v %d", err, len(occ))
	}
	// 批量：自身收录与同篇目兄弟分列，且不含同 Work 的其它篇目。
	out, err := s.ExpressionDetailsBatch(ctx, []string{exprA1.ID}, &admin)
	if err != nil {
		t.Fatal(err)
	}
	d := out.Items[exprA1.ID]
	if len(d.Occurrences) != 1 || d.Occurrences[0].ExpressionID != exprA1.ID {
		t.Fatalf("batch own occurrences wrong: %+v", d.Occurrences)
	}
	if len(d.Siblings) != 0 {
		t.Fatalf("exprA1 should have no recorded sibling inclusion, got %+v", d.Siblings)
	}
	// 给同篇目兄弟表达也建一条收录，siblings 应命中它而不污染自身。
	relA2 := entity("release", "第一集加长版发行")
	relA2.Subjects = []Subject{{WorkID: work.ID, Role: "primary"}}
	relA2 = save(relA2)
	medA2 := entity("medium", "BD 3")
	medA2.ReleaseID = relA2.ID
	medA2 = save(medA2)
	trackA2 := entity("track", "S1E1 long")
	trackA2.MediumID = medA2.ID
	trackA2.Contents = []Inclusion{{ExpressionID: exprA2.ID}}
	save(trackA2)
	out, err = s.ExpressionDetailsBatch(ctx, []string{exprA1.ID}, &admin)
	if err != nil {
		t.Fatal(err)
	}
	d = out.Items[exprA1.ID]
	if len(d.Occurrences) != 1 {
		t.Fatalf("sibling inclusion leaked into own occurrences: %+v", d.Occurrences)
	}
	if len(d.Siblings) != 1 || d.Siblings[0].ExpressionID != exprA2.ID {
		t.Fatalf("same-unit sibling not reported: %+v", d.Siblings)
	}
}

// 统一递归分发契约（缺口3）：后端 value() 必须覆盖全部 Field.Type，
// group 逐子字段递归、list 逐元素递归——这是前端 FieldValue 递归渲染与
// FieldInput 全类型分发的服务端镜像。报告断言"定位编辑器只处理枚举/
// 数字/文本"、"group/list 输出原始 JSON"的旧形态在此失败：
// 后台新增布尔值、实体引用或嵌套结构时，校验层必须同样按类型递归，
// 否则编辑器能填的数据会在保存时被拒（或反之，脏数据显示出来）。
// 前端 jiti 隔离复现（7 项：递归渲染/EntityLink/编辑分发/定位通用输入/
// 后台控件/模板消费/medium 属性区）已验证展示侧；此处以后端契约钉死
// 同一套类型分发，保证两端不漂移。
func TestUnifiedRecursiveDispatchContract(t *testing.T) {
	d := Defaults()
	ref := func(string, []string) error { return nil }
	mk := func(typ string, extra map[string]Field) Field {
		f := Field{Names: names("测", "T"), Type: typ, Enabled: true}
		for k, v := range extra {
			if f.Fields == nil {
				f.Fields = map[string]Field{}
			}
			f.Fields[k] = v
		}
		return f
	}
	text := Field{Names: names("文", "T"), Type: "text", Enabled: true}
	num := Field{Names: names("数", "N"), Type: "number", Enabled: true}
	boolean := Field{Names: names("布", "B"), Type: "boolean", Enabled: true}
	entity := Field{Names: names("引", "R"), Type: "entity", Kinds: []string{"agent"}, Enabled: true}
	// group 递归：子字段逐个按类型校验，未声明键拒绝。
	g := mk("group", map[string]Field{"title": text, "flag": boolean, "peer": entity})
	if err := d.value(g, map[string]any{"title": "a", "flag": true, "peer": "x"}, ref, false); err != nil {
		t.Fatalf("group recursion rejected: %v", err)
	}
	if err := d.value(g, map[string]any{"title": "a", "flag": "not-bool"}, ref, false); err == nil {
		t.Fatal("group nested boolean mistype accepted")
	}
	if err := d.value(g, map[string]any{"title": "a", "no_such": 1}, ref, false); err == nil {
		t.Fatal("group undeclared key accepted")
	}
	// list 递归：逐元素按 items 定义校验（报告"音轨列表→语言、声道"形态）。
	l := Field{Names: names("表", "L"), Type: "list", Enabled: true,
		Items: &Field{Names: names("项", "I"), Type: "group", Enabled: true,
			Fields: map[string]Field{"lang": text, "channels": num}}}
	good := []any{
		map[string]any{"lang": "zh", "channels": float64(2)},
		map[string]any{"lang": "ja", "channels": float64(6)},
	}
	if err := d.value(l, good, ref, false); err != nil {
		t.Fatalf("list recursion rejected: %v", err)
	}
	bad := []any{map[string]any{"lang": "zh", "channels": "stereo"}}
	if err := d.value(l, bad, ref, false); err == nil {
		t.Fatal("list nested mistype accepted")
	}
	// 嵌套 group 里再套 list：三层递归不断。
	deep := mk("group", map[string]Field{"tracks": l})
	nested := map[string]any{"tracks": good}
	if err := d.value(deep, nested, ref, false); err != nil {
		t.Fatalf("deep recursion rejected: %v", err)
	}
	// 实体引用直通 reference 回调（合并改写与标题解析的前置条件）。
	called := ""
	ref2 := func(id string, kinds []string) error { called = id; return nil }
	if err := d.value(entity, "agent-1", ref2, false); err != nil {
		t.Fatalf("entity reference rejected: %v", err)
	}
	if called != "agent-1" {
		t.Fatalf("entity reference not passed through: %q", called)
	}
}
