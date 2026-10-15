package catalog

import (
	"context"
)

// ExternalSourceDefinition 是外部来源查询可选的来源项。
// id / category / 展示元数据三者都不是前端或后端各抄一份的常量：
// id 来自代码里的适配器集合（真实实现），元数据来自 external databases 注册表，
// 后台改名、换图标或调整适用范围后，查询端点下一次请求即生效。
type ExternalSourceDefinition struct {
	ID          string            `json:"id"`
	Names       map[string]string `json:"names"`
	Category    string            `json:"category"`
	Icon        string            `json:"icon"`
	Description string            `json:"description"`
	URLPattern  string            `json:"url_pattern,omitempty"`
}

// externalSourceAdapterCodes 是**真正实现了适配器**的来源 id，与 normalizeExternalSourceDefinition
// 接受的具体来源一一对应。新增一个可查询来源时，必须同时在
// externalDatabaseSeeds 里有对应的注册表行（externalSource_sources_test 直接断言这一点），
// 否则下方合成会丢掉它——界面不撒谎的前提是这份清单只增真实条目。
//
// "auto" 只用于按 URL/ID 探测具体适配器；无法判定时默认 bangumi。
var externalSourceAdapterCodes = []string{"bangumi", "dlsite", "dmm"}

// externalSourceAdapterSet 供 O(1) 判定使用。
func externalSourceAdapterSet() map[string]bool {
	out := make(map[string]bool, len(externalSourceAdapterCodes))
	for _, code := range externalSourceAdapterCodes {
		out[code] = true
	}
	return out
}

// buildExternalSourceDefinitions 把适配器集合与注册表元数据合成为响应项。
// 以种子顺序为输出顺序，注册表行缺失（未播种/被手工删）时回落到种子元数据：
// 来源的**可用性**由代码决定，元数据缺失不该让一个真能抓取的来源从界面消失。
// 与 externalDatabaseSeeds 是同一份数据，不另立一份展示常量。
func buildExternalSourceDefinitions(rows []ExternalDatabase) []ExternalSourceDefinition {
	adapters := externalSourceAdapterSet()
	byCode := make(map[string]ExternalDatabase, len(rows))
	for _, r := range rows {
		byCode[r.Code] = r
	}
	out := make([]ExternalSourceDefinition, 0, len(externalSourceAdapterCodes))
	for _, seed := range externalDatabaseSeeds() {
		if !adapters[seed.Code] {
			continue
		}
		row, ok := byCode[seed.Code]
		if !ok {
			row = seed
		}
		out = append(out, ExternalSourceDefinition{
			ID:          row.Code,
			Names:       row.Names,
			Category:    row.Category,
			Icon:        row.Icon,
			Description: row.Description,
			URLPattern:  row.URLPattern,
		})
	}
	return out
}

// ExternalSourceDefinitions 返回可查询外部来源清单（ExternalSources 同实现）。
//
// 为什么读全表（含停用行）：注册表的 is_enabled 管的是"外链字段是否出现"
// （GET /catalog/external-databases 只回启用项），而"这个库有没有导入适配器"是代码事实；
// 把一个适配器仍在的来源从清单抹掉只会让查询功能无源可选。停用与适配器的关系
// 由管理台那一列呈现给管理员，不在读取侧静默改语义。
func (s *Store) ExternalSourceDefinitions(ctx context.Context) ([]ExternalSourceDefinition, error) {
	// 纯映射单测用的 Store{} 没有库：回落到种子元数据，不引入新的失败形态。
	if s.DB == nil {
		return buildExternalSourceDefinitions(nil), nil
	}
	rows, err := s.ListExternalDatabases(ctx, "", false)
	if err != nil {
		// 真实读库失败照旧上抛：把"库挂了"显示成"没有可用来源"会掩盖故障。
		return nil, err
	}
	return buildExternalSourceDefinitions(rows), nil
}
