# MetaFusion 核心实现与模块边界

本文维护目录实现、读取边界与查询容量。产品约束见[架构基准](./spec-driven-requirements.md)，编目例子见[媒体编目与展示](./media-catalog.md)，完整请求格式见[公开 API 文档](https://github.com/MoeclubM/metafusion-docs)。

## 权威事实与事务

核心位于 `backend/internal/catalog`，包含八种实体、结构侧表、动态定义、修订、outbox 与通知收件箱。账号、会话和令牌归账号服务，目录不保存账号数据。API 统一使用 `/api`，实体详情统一为 `/catalog/[id]`。

| 事实 | 唯一存储来源 | 写入入口 |
| --- | --- | --- |
| 归属与目录位置 | 结构侧表的 `work_id`、`release_id`、`medium_id`、`parent_id`、`content_unit_id` | 所属实体的结构字段 |
| 发行收录 | `release_subjects` | Release.subjects |
| 轨位收录与定位 | `track_contents` | Track.contents 或单条收录编辑 |
| 署名、改编、聚合等语义 | `catalog.relations` | 关系写入口 |
| 实体属性中的声明引用 | `catalog.entities.document` 的 `attributes` 与当前 definitions | 实体属性写入口 |

目录事实、生命周期和定义发布统一走 `writeCatalog` 的 SERIALIZABLE 事务。结构与关系并行校验，不取全目录结构锁；数据库拒绝跨事务的依赖异常，服务端只在明确回滚后有界重试。乐观版本防止静默覆盖，复合外键和延迟触发器拒绝跨域父子与循环。修订和 outbox 与事实同事务写入，事件按 event_id 幂等消费。

安装和升级只按[部署与恢复手册](./deployment-runbook.md)执行。HTTP 启动只读检查结构契约，迁移与种子显式发布；种子只补缺失项，保留人工配置、停用状态及已有模板规则。

## AI 助手与计费边界

`backend/internal/assistant` 与目录共用 backend 进程及数据库连接，独立 `assistant` schema 由目录迁移工具管理。Provider 保存接口地址、协议、加密密钥与供应方预算；Model 独立关联 Provider，保存上游模型名、输入/缓存输入/输出价格、积分费率及上下文限制。平台金额以整数 `microusd` 保存，1 USD = 1,000,000 microusd；每百万 token 单价与用户积分是不同账本。调用预留额度后按上游用量结算，无法确认消耗时按上界保守记为 unknown/usage_estimated；记录保留价格快照、token、金额和失败状态，防止修改当前价格后重算历史账单。

Model 的 `max_tokens_parameter` 明确选择上游输出上限字段 `max_tokens` 或 `max_completion_tokens`。用户自备 API 分为 `assistant.byo_providers`（接口地址、协议与加密密钥）与 `assistant.byo_models`（所属自备服务商、上游模型名、上下文窗口、最大输出与参数名）；聊天、介绍生成和自动维护的 `model_id` 均可指向平台模型或本人启用的自备模型，不依赖平台预先登记同名模型。`POST /api/assistant/chat` 支持 `background=true` 后台执行：请求确认封存后立即返回 `status=pending`，独立后台上下文最多执行5分钟并在每轮模型与工具完成后 checkpoint 到 `assistant.conversations`（过滤 `system` 消息）及 `assistant.chat_requests`；会话返回 `active_request_id`，用户可通过 `GET /api/assistant/requests` 与 `GET /api/assistant/requests/:id` 查询进度和最终结果。后台可查看封存请求及结果，仅能取消创建超过10分钟的 pending 请求；取消保留既有回执、用量和幂等 ID，不撤销已完成目录修改。backend 每分钟回收超过10分钟的预留，已调用但消耗未知的请求按预留上界保守计费。

注册赠分在用户首次使用助手账户时唯一记账；实际合入的目录编辑提交按合入时间和奖励生效时间唯一奖励，副本保存、未合入提交及重基操作不计采纳。`byo_providers` 与 `byo_models` 仅由所属用户使用，调用记录保存 `byo_provider_id` 与模型快照并计 token，不扣平台积分；赞助支付未实现，管理员可通过有唯一操作 ID 的积分调整记账。平台与自备服务商密钥均使用 `AI_ENCRYPTION_KEY` 加密，API 仅返回存在标记及掩码提示。未配置加密密钥时保留配置和记录读取，禁止密钥写入与模型调用。

问答工具读取现有目录投影，编辑统一保存至服务器副本，再 commit、push 或发起 PR，不引入另一套实体写入器；用户不必为每次工具调用再次授权，已有写权限与免审资格仍由目录校验。来源及查证记录不作为操作门槛。AI 生成介绍作为派生内容缓存，不覆盖条目正文或结构事实；图表、表格与时间线只表达可追溯数据。缓存读取必须重新核对当前公开事实快照及依赖，事实变化后重新生成需要额度。公开百科接入统一走只读来源 API，保留原文、页面来源、内容哈希及署名和许可信息。

## 自动维护 Agent

`backend/internal/curator` 复用助手运行器和目录副本协议。迁移 041 在既有 settings/findings/runs 上增加执行配置、审核 PR 输入、父子运行关系和消息，不复制第二套任务队列、目录事实或模型账本。原候选、回执、运行和暂停状态均保留；旧运行没有父节点，不虚构主 Agent 历史。读取需要 `assistant.manage`，变更还需要 `catalog.review.manage`。

PostgreSQL advisory 锁 740211 保证只有一个定时调度器。持锁连接每5秒探活，丢失连接即取消运行；每次模型调用、委派及工具执行前重读启停和范围配置。定时器先用数据库查询发现待审核 PR，再按游标轮转已有质量检测器。没有到期待办时不创建主 Agent 运行、不调用模型；空扫描不保留重复运行行。启用范围内积压达到200条后暂停新质量扫描。`interval_seconds` 是两轮检查间隔，`max_runs_per_hour` 限制子任务执行数，`execution.max_children` 限制单个主 Agent 的子 Agent 数。

存在待办时调用主 Agent，主 Agent 通过受控 `curator_delegate` 工具按任务类型创建独立上下文的子 Agent，并接收结果继续分工。内置 review（冻结 PR 审核）、research（查证与资料补全）、repair（机械修复）三类。工具只能委派本轮列出的任务，服务器校验类型、范围、小时额度与子 Agent 数；重复委派同一父运行和任务时回放原子运行结果。管理员也可通过 `/api/curator/tasks` 布置任务，任务 UUID 封存原输入。无实体的任务先查询定位对象，仍不得批量灌入未经阅读的数据。

任务按到期时间和优先级认领。运行结束与候选状态同事务提交，messages、工具结果和用量在模型轮次及工具执行时持久保存。副本写入前另外保存稳定 ID 与原始参数，写入后保存回执。重启获得独占锁后，将未完成运行明确记为中断并重新排队未完成任务；完成任务不重派。再次执行前读取原副本、提交和 PR 恢复结果，已推送的提交不重复创建。旧消息保留于对应尝试，新尝试使用新的模型请求 ID。关闭或暂停阻止后续调用和写入，已发出的请求照常结算。

目录写入作者与复核人仍是两个固定服务身份。主模型 `model_id` 与子模型 `execution.child_model_id` 均可选择平台模型或管理员本人的 `byo_models`，搜索可通过 `execution.search_byo_provider_id` 选择管理员本人的 `byo_providers`；`billing_owner_id` 由保存配置时的实际管理员绑定，模型和请求体不能冒用其他人的配置。平台费用记服务账号；BYO 费用记绑定所有者的零平台积分用量。全部使用 BYO 模型及免费或 BYO 搜索时，月积分额度可为0；使用平台付费能力时须设置额度。

资料补全与修复沿用 checkout → stage → commit → push。仅 tags 精确去重与 pictures 完全重复对象去重可在 `auto_merge_low_risk` 开启时经机械差异检查自动合入，曲序、名称和其他修复保留 PR。审核子 Agent 只提供只读工具；每个 PR 冻结 head/version 单独排队，排除服务身份自己提交的 PR。默认只记录审核建议。仅显式开启 `execution.auto_merge_reviews`、模型实际读取冻结差异且给出高置信 approve 时，服务器才按原 head/version 与当前权限原子合入；模型不能提供或替换合入参数。

月硬额度在账户行锁内合计同一服务账号的 `curator:` 模型已花费、未结算预留，以及搜索已花费、预留和未知结果；赠分与人工充值不放宽额度。失败退避重试，预算不足不计故障熔断。每轮最多执行8个工具，未执行的工具逐项返回限制结果，等待后续轮次。

### 网页搜索

`assistant.search_providers` 独立保存搜索类型、默认服务、加密密钥和每次成功请求积分，默认启用免费 DuckDuckGo。普通助手和后台 Agent 共用 `web_search` / `web_fetch`；目录公开百科设置仍只负责百科接入，不混入搜索凭据。

DuckDuckGo 使用公开 HTML 搜索，解析真实网页结果；遇验证码或上游失败明确报错。DeepSeek 使用原生 `POST /v1/tools/search` 与 `/v1/tools/fetch`。BYO 搜索通过 `byo_provider_id` 复用本人 `assistant.byo_providers` 的公开 HTTPS 地址与 Bearer 密钥；DeepSeek 的 `/v1` 前缀会统一处理。所有请求使用公共网络客户端校验 DNS，禁止私网目标和转发密钥到重定向地址。

搜索记录保存在 `assistant.search_requests`，与模型 token 用量分开；积分共用 accounts/credit_entries。先预留后结算，200且有结果才按配置收费，空结果与明确失败释放预留。超时、无法解析的200及进程中断的已预留请求记 unknown、保守保留该次积分，并封存请求防止重复调用。后台可查看状态与费用。DeepSeek 官方成功单次价格按当前文档为0.005元，原币整数 `upstream_cost_microcny` 单独记录，不能当美元合入 `microusd` 预算；BYO 上游账单由用户自行结算，平台账本仍保留请求和零积分记录。完整消息保存实际提供给模型的有限正文，用于对话回读，不保存独立全网抓取库。

## 完整保存的能力边界与演进方向

八种实体、动态 group/list 字段、带上下文的语义关系和跨作品收录可表达主要媒体编目场景；这不等于已经无损保全全部原始权威资料。正文可以保留补充说明，但文本可保存不等于对应事实已能结构化查询。以下为当前边界，后面的调整方向尚未实现，也不是编辑前置条件。

单条结构化记录使用 group，重复记录使用 list；可选 group 在省略时不触发其子字段的必填要求，提供对象后才递归校验，避免仅为可选信息增加一层列表。

| 范围 | 当前实现与边界 | 调整方向 |
| --- | --- | --- |
| 原始来源 | Source 仅保存 kind/citation/url/key/pointers/scope；外部查询返回 raw、抓取时间与哈希，不自动持久化原文。存储服务能保存文件，但目录来源没有指向不可变资料版本的结构引用 | 在目录核心增加来源文档及不可变快照记录，字节仍由现有存储服务保存；引用可定位具体快照、页码或原文片段，未映射信息保留在原件中 |
| 历史语义 | definitions 只有一份生效文档，etag 用于并发检查；SaveDefinitions 覆盖全文，审计只留规模摘要，不能按旧 etag 取回旧定义 | 保存不可变 definitions 版本并让提交/修订引用，后台仍直接保存，不增加草稿或审批流程 |
| 名称与事实限定 | Translation 是每语言一项 title/summary/body/aliases，别名为字符串；没有统一的名称级官方性、地区、时期与来源。署名已有 context/角色/语言/期间，额外限定可用动态字段扩展 | 先用定义补足具体署名等事实的字段；名称若需一语言多名称、nonofficial 和逐名称证据，再统一引入结构化名称记录，不把标记拼进字符串 |
| 历史读取与导出 | 实体/关系修订已按 limit/offset 分页，并可按版本、作者及时间筛选；exchange 只导出当前可见实体，不含完整历史、关系、定义和来源原件 | 完整保存包仍需明确导出范围与依赖清单；复用现有历史读取协议，公开详情投影不作为数据库或原件备份 |

Release.subjects 是发行声明覆盖的作品，Work 的组成关系表达创作编排，edition_of 表达版本组，Track.contents 表达实际收录位置。它们并非同一事实的四份副本，不能直接互相替换；可以在一个编目操作中维护必要声明，并从权威收录事实派生浏览投影，减少重复填写与展示。作品组成、尚未定位的已知收录和显式版本组仍需保留各自语义。

优先顺序是来源快照与定义版本，其次按真实场景补名称/事实限定和完整导出。现有字段与关系能表达的事实先通过 definitions 扩展；既有 Go/PostgreSQL 目录核心和独立存储足以承载这些扩展。来源与定义留存可在保存时自动处理，保持普通编辑流程简短。

详情、目录、关系编辑器和 MCP 图谱共用 links/relationships-query 的读取投影，不另维护仅含普通关系的实体集合读接口。语义 link 返回原关系 ID 与真实 version，属性引用仍由所属字段维护；正反向读取不生成重复关系。Agent 同时承载个人和组织，参与关系文案不能预设为人员。当前 publisher 是单值字段：联合发行、多方职责、地区或期间应通过有上下文的动态关系表达；若将其统一为职责关系，须一次迁移权威事实，不能长期同时维护属性与同义边。

## 表达组合、版本组与收录编辑

整体译本或整季剪辑用 `usage=expression_composition` 的普通关系引用同 Work 的部分 Expression，种子码为 `expression_part`。声明必须包含 `scope=work`、`acyclic=true`、`cycle_group=expression_composition`、`unique_position=true`、`aggregate=true`。全部同用途关系共用部分去重、顺序唯一与无环约束，部分表达可复用于多个整体；组合不代替实际 Track.contents。

普通、限定、地区发行用 `usage=release_group` 关系显式指向共同 Work 或 Collection，种子码为 `edition_of`。一个 Release 在全部同用途码中只属于一个版本组。专辑 Work 可直接作组，仅为发行组织建立的 Collection 不制造创作身份；共同 subjects 只表示相关收录，不推断版本组。

`relation_constraints.go` 共用于保存、定义影响检查和合并回放。固定单值作用域为：work 支持 Work/ContentUnit/Expression，release 支持 Release/Medium/Track，medium 支持 Medium/Track；复合发行的 subjects 不作为单 Work 归属。`reference_scopes: {"context":"source_work"}` 等声明约束 entity 属性引用；引用端与字段目标均须支持相应作用域。`cycle_group` 合并多码的存活边检查无环，未分组时按单码检查。

| 只读投影 | 返回 |
| --- | --- |
| `GET /api/catalog/expressions/:id/composition` | 有序 parts/wholes |
| `GET /api/catalog/releases/:id/editions` | 显式 group/editions；未分组时为 null/[] |

两个投影均在 RepeatableRead 事务内读取，带 `definition_etag` 并裁剪不可见或终态对端，不另建图谱事实表。

单条收录在工作副本中使用 `action=content` 增加、替换或删除，替换可用 inclusion.position 重排；与其他实体和关系变更一起提交，合入时才产生 Track 修订及 outbox。`track_contents.sources` 是可选的直接来源，不从提交说明自动补造。单条编辑保留其余不可见历史收录；普通编辑者不能用裁剪后的公开视图删改隐藏事实。

## 关系读取与 Agent 遍历

`GET /api/catalog/definitions` 的 `relationship_rules` 登记可查询规则，`GET /api/catalog/entities/{id}/links` 返回单主体的关系投影。固定规则只读，语义规则及属性引用来自当前 definitions；读取不复制边。

| 规则码 | 事实来源 |
| --- | --- |
| `structure:` | 归属、位置与两种收录 |
| `relation:` | 普通语义关系 |
| `attribute:<path>` | 按 entity/group/list 声明递归生成的实体属性引用 |

`field` 标出具体数组位置。关系、subjects、contents 的属性及 locator 中声明的第三方引用随边返回 `references`，以引用实体反查时用 `via` 标记；不可见端点或引用使整条边不可见。当前 links 不提供逐边证据或历史规则版本；新增结构写入的判据见[架构基准](./spec-driven-requirements.md#3-实体骨架与复杂媒体映射规则)。

Agent 使用只读 `POST /api/catalog/relationships/query`：

- ids 为 1–20 个主体，direction 为 both/outgoing/incoming；rule_codes 取当前完整规则码，peer_kinds 过滤相对主体的对端。
- limit 默认 25、最大 100，offset 为非负整数；逐主体独立分页，可见性和筛选先于分页。
- 响应包含 definition_etag、pages、去重 entities 摘要与 unavailable_ids；不存在与不可见不区分。
- 摘要覆盖主体、端点和第三方引用，只含 id、kind、version、title、original_language、translations；完整载荷另取实体。

定义、主体和直接边在同一 RepeatableRead 只读事务中装配，多次请求不共享快照。客户端按 has_more 翻完每个主体，再用已访问 ID 集合逐层遍历；接口自身不递归。默认读写额度各为每分钟 180 次，可由现行账户或组策略调整。

它提供调用者可见且由模型或 definitions 声明的当前关系，包括结构归属、跨作品收录、语义关系及嵌套引用。文本中的隐含关联、跨服务论坛/资源数据和历史规则图不属于自动遍历范围，单请求不能导出全站图谱。

## 搜索与索引同步

`cmd/server/main.go` 要求 `DATABASE_URL` 与 `OPENSEARCH_URL`。q 文本查询由 OpenSearch 2.14 匹配、过滤、排序和分页，PostgreSQL 按当页 ID 批量回读权威实体并复核当前可见性及筛选。未就绪或不可用返回 503 search_unavailable，不执行 ILIKE 回退。

无 q 浏览由 PostgreSQL 执行，total_relation=eq。搜索为 total_relation=index_snapshot，计数属于索引快照；当前权限变化可能导致 items 短页或空页。搜索使用一分钟 PIT 与 search_after 游标：offset+limit 仅允许前 10,000 条，深页沿 next_cursor；has_more=true 时即使 items=[] 也继续。游标绑定身份、查询、筛选、排序、语言和 limit，过期后重新开始。

索引覆盖题名、翻译、摘要、别名、标签、外部 ID、结构 ID 与可检索动态属性。三字符及以上子串通过 ngram 预筛，短查询仍可能命中很大范围。

索引器通过 advisory lock 保证单写入者，全量重建包含结构侧表，完成后原子切换 `metafusion-entities` 别名至 v3。outbox 只作变更通知：消费读取当前数据库事实，同一批次的相同 ID 只处理一次；行存在时使用当前实体版本，行已物理删除时删除索引文档，不从历史 payload 复活实体。bulk 成功后才确认 deliveries，丢失确认可重投；删除不存在文档可幂等成功，缺索引等错误不能冒充成功。

读取副本确认完成标记后可提供搜索。`/health.search_ready` 表示索引读取就绪，`search_indexing_delayed` 单独表示最近的增量同步失败；完整索引存在时，增量投递失败不关闭读取，实体可见性仍由 PostgreSQL 回读复核。首次索引未完成或索引检查不可用时仍关闭读取。`/ready` 只检查 PostgreSQL；发布验收必须另验搜索，见部署手册。

## 并发编目查重

`POST /api/catalog/entities/candidates` 在同一个 PostgreSQL repeatable-read 只读事务中查匹配原始实体、补结构作用域并解析 canonical。024 迁移发布 `identity_title`、`identity_candidate_terms` 与 GIN 表达式索引，实体 document 写入同步维护索引；启动须确认函数和有效索引存在，不能靠异步 OpenSearch 索引的零命中推断实体不存在。

题名及多语言别名按大小写、空白归一；外部 ID 和标量/标量数组属性精确匹配，条件 OR，Expression 可限 Work。返回当前调用者可见的摘要、快照命中总数与 complete；超上限、不可解析 canonical、数据库或协议故障均阻断依赖写入。工具 `mf-find-identity` 与 `mf-platform entity.create` 只走此接口，不再跨 offset 页扫描同 kind 的全量数据；接口缺失不回退旧扫描。

快照只覆盖一次查重，不预留后续创建身份。同一对象的任务仍需避免重复分派，创建幂等键只保障同一载荷重放，不保证不同任务键的语义去重。冲突或未知写结果先回读，不能更换键或把 pending 宣称为未写入。

## 并发编辑与提交

目录使用服务器工作副本、不可变提交与 PR（均在 `/api/catalog`）。工作副本按每个实体/关系保存正式基线、最近提交和当前工作树，以全副本 version 防并发覆盖；每个新对象提前分配稳定 UUID，可在后续提交引用。副本 owner 可邀请协作者，每次 commit 分别记录原作者、实际贡献者和提交操作人。PG 仍是结构约束和事务事实源，不建立物理 Git 仓。公开协议见[工作副本与提交](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/api-commits.md)，本地工具在 metafusion-skills。

保存变更与 commit 均只修改副本；同一副本支持多次操作合成一次提交，以及多次提交后 push。有未提交变更时不能 push。免审用户直接合入，普通用户 push 创建或更新 PR；PR 固定源 head，合并人必须提交实际查看过的 PR version 和 source head。两个入口共用同一事务合并器及 saveEntityTx/saveRelationTx，不保留直接实体/关系/收录写接口或旧提交审核实现。

合并比较副本基线到源 head 的净变更，再与当前正式事实做三方比较；撤销到基线的字段不覆盖他人修改。对象成员顺序差异不构成事实变化或删除冲突。数组原子，正文使用有预算的 Myers 行级三方合并；不同字段及不相交正文修改自动合并，重叠且不同值返回字段冲突。解决请求携带本次看到的 current_version，明确选择当前、副本或自定义值；重基生成新提交，不改写旧提交。来源与候选查证均可选；身份候选查询仍用于人工判断，不要求提交 reviewed_candidate_ids，也不按同名自动合并。

局部锁顺序为创建身份 term（稳定排序）→definitions 共享→Release（稳定排序）→实体行（UUID 排序）。同发行和同身份条件仍协调，互不关联的结构与关系不会等待全库写锁。实体用 NO KEY UPDATE 行锁；关系保存只读取端点附近的计数、重复/位置依赖及从目标可达的 cycle_group 图。定义影响检查预先按端点整理计数、重复与位置依赖，并复用同规则的完整无环关系集合，避免每条普通关系遍历全库；与合并回放继续使用同一校验规则。

提交期限15秒、锁等待3秒，最多16次事务尝试，带短暂随机退避。仅 SQLSTATE 40001、40P01、55P03 确认回滚时重试整笔事务；每次重建 refs/items 与输入副本，成功回执只来自最终事务。耗尽返回503 transaction_busy，提交响应 applied=false、Retry-After:1。网络断开、未知 COMMIT 和业务冲突不自动重试；此前未知推送不能被后一次忙碌响应判为失败。definitions 每批只读一次，checkout 批量补齐实体与结构。

合入在单一事务中更新事实、修订、原始提交关联、outbox 与通知；任何冲突或校验失败都不部分生效。原作者与合并人分别保留，公开贡献按 UTC 合入日期统计实际编辑提交，不按修订行计数，私人未合入活动不进入公开主页。提交、父链和贡献关联永久保留。分支成员独立于原始创作副本，fork 与 merge 沿两个父提交保留原历史；PR 冻结明确提交集合。变基逐提交重放，允许选择顺序和丢弃；amend/squash/reset 仅整理未合入历史，原提交及替代链接保留。支持 cherry-pick、revert、restore、标签、stash 与不可变 reflog，所有操作先改变副本，正式写入仍经过原合并器。

037 新建明确的正式目录基线，包含实体、结构侧表、subjects、contents 与关系；旧提交只回填实际保留的记录，不伪造全库历史。随后每次事务追加变化记录和正式 head，快照按序重建并在分页前检查当前可见性。历史快照、对比与字段归属经同一 API 提供；字段归属沿保留的两父图查找编辑提交，不把系统合入节点当成编辑者。账号、配置和文件字节由各服务负责，不进入目录版本树。

正式合入在同一 SERIALIZABLE 事务内批量读取变化对象、结构和前一快照，并在取得 head 行锁前完成比较、序列化与作者查询；锁内批量追加版本记录和提交对象。正式版本对比先取两个序列之间变过的对象，再按索引读取两端状态，计数与分页共用一次可见差异计算。按记录读取快照时将筛选下推到版本查询；历史轨位的收录可见性按一批文档共同查询，仍以当前权限为准。工作副本提交继续保存完整已跟踪对象快照。

## 查询规模、费用与接口边界

当前已有搜索与事实存储分离、索引增量更新、按页批量回读和关系批量查询。默认编排仍是起步配置：OpenSearch 单节点、1 主分片、0 副本、512m heap，PostgreSQL 每进程连接池默认上限64（CATALOG_DB_MAX_OPEN_CONNS，1–256；idle 默认32，可设0–maxOpen），生产业务限流已通过 PostgreSQL 固定分钟窗口共享计数；增加后端副本仍须核对全服务连接总量、索引写入者与搜索高可用。

| 路径 | 已有能力 | 扩容前需验证 |
| --- | --- | --- |
| 文本搜索 | 索引组合过滤、深页游标、当页权限复核 | 文档体积与索引膨胀、短查询/动态字段过滤、P95/P99、索引滞后、heap 与磁盘 |
| 无关键词浏览 | 结构/JSONB 索引、批量读取 | 大集合精确 COUNT、深 OFFSET、池等待、热点缓存需求 |
| 关系遍历 | 声明引用双向查询、端点和引用共享缓存 | 固定无附加引用和纯属性边直接 SQL 分页；混合语义页仍需扫描可见边计算 offset，高扇出重复翻页成本需压测 |
| 多副本 | 单索引写入者、读取副本可就绪、共享限流窗口 | 共享计数的竞争与故障恢复、索引积压、主分片/副本布局、数据库连接总预算 |

目标按上万在线用户、查询为主、部分编辑；在线数须转换为实际请求速率与批次大小。例如10000人平均10秒一次请求约1000 RPS，只是容量场景假设，页面一次动作可能发多个请求。当前生产共享主机4核、约8GiB、OpenSearch 512MiB heap，不能据功能测试承诺该场景。全目录结构锁已移除，热点行/发行、事务重试成本和真实业务批次仍需容量压测。

验收在独立容量环境按50→100→250→500→1000只读RPS递增，浏览/搜索/关系按真实日志配比，同时加入部分编辑。初始目标可设搜索P95<500ms、P99<1500ms、错误率<1%、搜索滞后<5秒，需真实负载校准。scripts/bench_catalog_reads.ts 是有限时长的无写入开放速率探针，输出实际速率、客户端丢弃、错误率和P50/P95/P99，不自动改配额，不把一次测试当容量达标。示例：`bun scripts/bench_catalog_reads.ts --base https://staging.example --rps 50 --seconds 60 --concurrency 32 --entity <UUID>`；凭据用 MF_PAT 或已忽略的 --credentials 文件，专用账号预算须覆盖场景。

扩容按测量决策：先分离数据库、搜索与业务进程的CPU/IO资源；热公共详情/定义用版本键缓存，私有投影不能共享；大集合浏览避免无界精确COUNT和深OFFSET；搜索按索引/heap及副本布局扩容；高扇出关系改用稳定游标；多后端副本前验证共享限流的竞争开销与总连接预算。数据库连接总量=副本数×每副本上限+其他服务+迁移/运维预留，不能按在线人数开连接。继续调整局部锁或依赖查询须用对抗并发测试验证父子环、关系环、基数与生命周期竞态。

尚无足以承诺大规模容量的负载证据，供应商月费未提供。应按浏览、短查询、高扇出关系和写入同步的真实比例测错误率、尾延迟、池等待、CPU/IO、heap 及索引滞后；费用计入搜索节点及副本、数据库、持久盘、备份和网络，再计算每百万查询成本。功能回归通过不能证明容量达标。

## 图片与封面

pictures[] 是有序数组，数组顺序决定展示顺序，首张为当前封面，服务端不重排。taken_at 记录图本身的拍摄或发布时间；version_label 为四语图像版本名称；usage_period={begin?,end?} 表达已知使用期，允许单端、部分日期和重叠，不自动选择封面或新增目录实体。旧图保留在同一图集中，版别图片归对应 Release，跨期作品主视觉归 Work；借图应标明来源。

每张图可选 role（picture_role 词表）与 asset_id（存储资产 UUID，配 cover_image 绑定）。`validation.go` 拒绝重复 URL、超过 40 张、非法用途、非 UUID 资产及非法或确定倒置的使用期。目录不跨服务校验资产存在或封禁，读取失败时前端使用程序封面。前端封面判定集中于 `src/lib/cover.ts` 的 coverPicture/coverUrl，稳定资产地址与绑定见[存储运行约定](./storage-operations.md)。

外部来源查询仅返回预览中的图片 URL，不补图、不写入实体；编辑者通过常规编辑或 commit 工作流追加并重排。上游 URL 原地换像素时，需将旧版保存为独立资产才能留存；日期和版本名不能代替字节保存。

## 模板与管理工作面

模板 match 使用 exists/equals/contains 的 AND，priority 决定顺序，同最高优先级冲突回到通用布局。缺少 match 不参与自动选择但可手工选用，显式 [] 是该 kind 的兜底模板。blocks 是受支持区块的有序列表，缺省继承布局，[] 隐藏可选区块；固定事实和修订入口保持可读。creation_form 仅描述内容与选择模板，不改变身份或字段权限。

目录后台按权限展示工作面：definitions、extdb、ratelimits 需 catalog.definitions.manage，shelves 需 catalog.shelves.manage，reviews、merge 需 catalog.lifecycle.manage。无权 hash 不挂载对应组件；统计只由生命周期管理员请求。其它工作面仍按管理台准入与具体端点授权。

审核使用实际查看过的版本，回读发现变化先报冲突；合并先展示双方名称、标识、层级、版本和方向，再调用 lifecycle，源版本仍受乐观锁约束。成功、失败与空列表分开显示，提交期间阻止重复操作。

主前端 `/catalog` 使用 CatalogProvider，根布局不挂全局播放器；资源、社区和个人记录动态导入，按能力声明再请求自己的服务。外围服务的数据与调用边界只维护于[拆分契约](./service-split-migration.md)。
