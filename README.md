# <img src="frontend/public/mark.svg" width="28" height="28" alt="MetaFusion"/> MetaFusion

MetaFusion 是开放的多媒介元数据目录，记录作品、内容表达、发行、载体与参与者之间的关系，支持协作编目、检索和关联浏览。

公开元数据可匿名读取；资源文件由独立存储服务按绑定实体的可见性控制访问。目录条目可以不附带文件。当前提供原始文件上传与读取，不提供转码、HLS 或流媒体处理。

## 使用与编目

- [快速开始](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/quickstart.md)：浏览、搜索和参与编辑。
- [元数据目录](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/catalog.md)：区分作品、表达、发行与实际收录。
- [词条编辑与协作](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/editing-guide.md)：副本保存、批量提交、PR 与冲突处理。
- [资源上传与下载](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/upload-download.md)：文件、绑定与访问范围。

正式题名与内容身份以来源为准；季数、Vol、OST 等不能机械删除。发行版、包装和载体规格记录在相应层级。同一内容表达可被多个发行收录，收录不会改变其作品归属。

## 开发者与 Agent

统一 API 入口为 `/api`。接入时读取目标实例的 `/api/openapi.json` 和 `/api/catalog/definitions`，按当前协议和启用定义查询；认证、分页、副本版本和合入规则集中维护于独立文档仓。来源与查证记录可选，不作为编辑门槛。

| 入口 | 内容 |
| --- | --- |
| [API 概览](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/api-overview.md) | 接入顺序、能力索引与公共响应约定 |
| [实体查询](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/api-entities.md) | 详情、身份解析、目录与关系分页 |
| [检索](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/api-search.md) | OpenSearch 查询、筛选、计数和深页游标 |
| [Agent 接入](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/api-agent.md) | 技能选择、工具组织、查询完整性与恢复决策 |
| [技能仓库](https://github.com/MoeclubM/metafusion-skills) | 编目、层级规范和只读关系查询工具 |

## 代码与服务边界

目录模型固定为 Agent、Collection、Work、ContentUnit、Expression、Release、Medium、Track。字段、词项、关系和展示名称由服务端 definitions 动态加载，支持简体中文、繁体中文、日语与英语；实体详情共用 `/catalog/[id]`。

| 组件 | 位置与职责 |
| --- | --- |
| 目录后端 | `backend/internal/catalog/`：实体、动态定义、结构约束、修订与索引同步 |
| 主站与目录后台 | `frontend/`：Next.js 页面、分区编辑与比较 |
| 部署编排 | `deploy/`：Compose、网关、发布清单和版本锁 |
| 账号 / 互动 / 存储 | 独立仓库 [auth](https://github.com/MoeclubM/metafusion-auth)、[community](https://github.com/MoeclubM/metafusion-community)、[storage](https://github.com/MoeclubM/metafusion-storage) |
| 公共文档 | 独立仓库 [metafusion-docs](https://github.com/MoeclubM/metafusion-docs)，是用户教程与 API 文档的唯一源 |

后端使用 Go，主站使用 Next.js / Bun，事实存储使用 PostgreSQL，搜索使用 OpenSearch，文件存储使用 RustFS（S3）。具体版本以各 manifest、锁文件和 CI 为准。服务各自维护所属 schema，通过 HTTP 查询跨服务身份与可见性；目录不保存文件物理路径或论坛记录。

架构、约束与尚未实现的需求见 [内部文档索引](docs/README.md)，当前边界见 [服务拆分契约](docs/architecture/service-split-migration.md)。

本地启动、配置分工、集成测试条件与生成物检查见 [开发指南](docs/development.md)。

## 自建与部署

主仓与 auth、community、storage、gateway、docs 仓库须按 `deploy/versions.lock` 并列检出。配置由 `.env.example` 建立；四个业务服务必须显式提供各自 DATABASE_URL，签发私钥仅归账号服务。真实配置、凭据与数据库导出不提交。

- 源码部署入口：`deploy/deploy.sh prod`；预构建发布入口：`deploy/deploy.sh pull`。
- 预构建发布使用同 SHA 的成功 CI/CodeQL/Release 清单，后端、前端和迁移器按摘要运行，兄弟服务按版本锁构建。
- 目录 HTTP 启动只校验数据库，迁移与种子由 `mf-migrate` 显式执行。既有实例升级须停写、备份、恢复演练和验收；不能靠重启推断迁移完成。
- 新实例通过 `/setup` 创建首个管理员；没有预置账号。站点文档位于 `/docs/`。

完整步骤、首次安装、独立迁移和回退边界只维护于 [部署与恢复手册](docs/architecture/deployment-runbook.md)；角色授权见 [数据库最小权限](docs/architecture/database-roles.md)，文件与备份见 [存储运行约定](docs/architecture/storage-operations.md)。默认单节点搜索配置不构成大规模并发承诺，评估方法见 [查询规模与费用](docs/architecture/catalog-core-implementation.md#查询规模费用与接口边界)。

## 贡献

协作、验证和提交规则见 [AGENTS.md](AGENTS.md)。提交使用 Conventional Commits；动态元数据名称从 definitions 获取，界面文案走四语字典。

项目使用 [Apache-2.0 许可证](LICENSE)。
