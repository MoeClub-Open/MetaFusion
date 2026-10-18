# 资源存储运行约定

资源文件、哈希与实体绑定由独立存储服务（`metafusion-storage`）管理；目录只保存作品、表达与发行元数据，
通过实体 UUID 与文件建立引用。ISRC、MBID 是内容身份标识，**文件 SHA-256 不是**录音或作品身份。

## 部署与持久化

- 存储服务在 `deploy/docker-compose.yml` 中作为独立服务运行：`STORAGE_S3_*` 连接 RustFS，
  `STORAGE_ROOT=/app/storage-data` 挂载 `storage_data` 命名卷（上传暂存与本地对象模式）。
  归档变量与归档卷由存储服务持有。
- 对象存储端口**不发布到宿主机**，只在 compose 网络内可达；浏览器直传需要让对象存储经反代对外可达，
  再设 `STORAGE_S3_PUBLIC_ENDPOINT` 指向该地址（见 `.env.example`）。
- **桶由存储服务启动时自己创建**（`internal/objects` 的 `ensureBucket`：先探测再建、并发下按已存在容忍）。
- 未配置 S3 端点时存储服务走**本地对象模式**：文件落在 `storage_data` 卷里，直传改由服务端流式接收。
- 目录侧与存储侧各用自有 schema（`catalog` / `storage`），**不跨 schema 建外键、不互相 JOIN**；
  实体可见性由存储服务向目录查询（`GET /api/catalog/entities/{id}`）后自行判定。
- 若旧容器可写层里还有资产，需先备份再按哈希核验后迁移，新增卷不会自动搬运。

## 契约与权限口径

- 上传：`POST /api/storage/upload/initiate`（命中 sha256 即秒传，否则签发分片预签名地址）→ 客户端直传 →
  `POST /api/storage/upload/complete`；不可直传时用 `PUT /api/storage/upload/stream/{asset_id}`（服务端流式接收并边收边算哈希）。
- 绑定：`POST /api/storage/bind` 用 `binding_role` 表达用途（`track_audio`/`disc_image`/`video`/`scans`/`cover_image`…），
  `DELETE /api/storage/bindings/{id}` 解绑纠错；收录位置（页码、时间码）留在目录侧的 `locator`，两处不重复。
- 读取可见性只有一条口径：**上传者本人或管理员直通，其余人只要任一绑定目标实体可见即可读**；
  下载、元数据读取与哈希校验共用该判定（`GET /api/storage/download/{asset_id}`、`GET /api/storage/entities/{id}/files`）。
- 下载在 S3 模式下返回预签名地址，本地模式由服务端流式下发；`/storage/preview/` 仍返回 404，不直代私有桶。
- **稳定引用**：目录数据里需要长期指向某份文件的地址（如实体 `pictures[].url`）用
  `GET /api/storage/assets/{id}/content`：按请求重新鉴权后把对象**原样**内联发出（不转码、不裁剪），
  与 `download` 共用同一读取判定与 404 口径，响应只进私有缓存（可见性按请求判定），
  并带 `ETag`（资产 sha256）与 `If-None-Match` → 304，避免每次加载都整份回源对象存储。
  预签名地址会过期、签名 Host 又是对象存储端点（未配 `STORAGE_S3_PUBLIC_ENDPOINT` 时浏览器不可达），
  只能当一次性取件用，不能当稳定地址。
  自托管封面走这条：一张图一条 `binding_role=cover_image` 的绑定，目录侧在该 `Picture` 上写
  `asset_id`（同一个资产 UUID）+ `url`（该 `content` 地址）。**顺序与用途都在目录侧**——
  哪张是封面由 `pictures[]` 的数组顺序决定（首张即封面），`storage.bindings` 没有 position 列，
  不在这里再造一套顺序；目录侧也不跨服务校验 `asset_id` 是否存在或已被封禁（无跨服务事务），
  取不到对象时前端退化成程序封面而不是画破图。

## 当前能力边界

- 媒体分析与预览：存储服务只收原始文件、按权限分发，**不做转码与媒体分析**。
- BT 种子、分享率治理、零拷贝（对象存储内部拷贝）等仍属未确认需求，不作为前提。

## 绑定生命周期与回收

- 目录配置 `STORAGE_URL` 与双方一致的 `STORAGE_CATALOG_LIFECYCLE_SECRET` 后启用
  `storage-lifecycle-v1` 消费者；空密钥关闭，启用时无效 URL 在目录启动期明确失败。
  存储的 `GET /internal/catalog/binding-targets` 只列当前有绑定的实体 UUID，每页至多 100，按 UUID 游标前进。
  消费者启动先复核该库存，随后消费 `entity.saved/deleted/merged`；长期关闭后重新启用不依赖旧事件仍在。
- 内部 `POST /internal/catalog/lifecycle` 仅接受专用服务凭据，外部网关必须拒绝 `/internal/`。
  生命周期消费者读取目录当前 canonical、完整 aliases、实体版本与图片资产集合；实际存储应用后才记录独立 delivery ACK。
  目录 outbox 保留清理等待所有启用消费者 ACK。新增自托管封面在副本保存和合入前同步验证资产授权及可用性，存储不可用时返回503；普通无资产变更不依赖该调用。
- 删除撤 canonical 与历史 aliases 的全部文件绑定；合并迁移全部用途并去重，保留历史别名图片资产。
  TrackContent 只有 Expression 收录引用，替换收录不会撤 Track 或 Expression 文件。
  工作副本保存图片时建立私有引用，不增加公开绑定；合入前按保存者的真实身份验证授权，合入后的生命周期投递才创建 `cover_image`，并撤已移除的封面绑定。其他用途不变，公开绑定接口禁止直接新增或删除 `cover_image`。
- 存储011迁移保存工作副本的私有资产引用；未决保存暂保留48小时，GC到期后通过目录内部接口精确核对 workspace/entity/asset 的当前值及不可变提交。确认无引用才删除，目录不可用时保留；已提交历史的原图持续受保护。
  副本、PR和提交图片分别经目录上下文鉴权代理读取，PR只包含推送时冻结的图片；代理响应 `private, no-store`，不改变权威图片地址。共享服务密钥与 `STORAGE_URL` 是自托管封面协作的必要配置。
- 重复 Bind 保留绑定 ID、创建者与创建时间，并刷新内部确认时间；旧快照只撤确认时间不晚于该次目录写入的封面。
  存储迁移 `000008` 将存量确认时间从迁移时刻保守起算；旧实体图片绑定在下一次目录新写入后才按新快照撤销。
  读取身份后、落绑定前实体已合并/删除时，存储返回 `409 binding_target_changed`，客户端重新读取身份再写。
- 已完成资产从最后解绑（从未绑定则从完成）开始保留，`STORAGE_UNBOUND_RETENTION_DAYS` 默认 **7 天**；
  重新绑定清空计时，存量无绑定资产从迁移 `000006` 时刻保守起算，禁发或仍有任何绑定的资产不回收。
  `storage-server worker` 同时处理过期 pending 与到期 complete；需要部署侧定期执行该 worker。
- 回收共享对象键锁与资产行锁，复检引用并持久登记删除门禁后，最多 15 秒操作对象并删除资产行；共享对象只删当前行。
  对象失败或删字节后数据库提交失败保留门禁，worker 重试；绑定、续租、完成与禁发返回 `409 asset_reclaiming`，读取不再分发该资产。
  对账隔离同样共享对象键锁，避免隔离刚被新资产引用的对象。隔离保留字节，不等于物理删除。

## 变更纪律

- 存储结构由 `internal/store/migrations/*.sql` 的版本化迁移负责，记账到 `storage.schema_migrations`，迁移期取事务级 advisory lock **740204**。目录迁移在 `backend/migrations`，使用锁 **88481001**；目录结构写入锁 **740202** 是不同职责。
- 存储统一调用 `GET /api/catalog/entities/{id}/identity`，获取当前可见实体的 canonical、kind 与完整别名。`404 not_found` 是不可见，缺接口与上游故障不能折为空列表；不再回退旧实体/resolve 组合，也不直接读目录库。
- 用户会话 JWT 在存储服务本地验签，PAT 通过账号服务（`AUTH_URL`）内省；JWKS 由账号服务提供。目录身份与账号身份分别由对应服务解析。
