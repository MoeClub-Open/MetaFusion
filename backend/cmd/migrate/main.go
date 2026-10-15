package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
	"github.com/metafusion/metafusion-app/internal/catalog"
	"github.com/metafusion/metafusion-app/internal/config"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/migrations"
)

func main() {
	cfg := config.Load()
	operationTimeout := flag.Duration("timeout", 15*time.Minute, "单次迁移、种子或检查操作的超时，必须大于 0（例如 30m）")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "MetaFusion 独立版本化数据库迁移管理工具 (Database Schema Migrator)\n\n")
		fmt.Fprintf(os.Stderr, "用法:\n")
		fmt.Fprintf(os.Stderr, "  mf-migrate [flags] [command] [args]\n\n")
		fmt.Fprintf(os.Stderr, "全局参数（必须放在 command 前）:\n")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "命令:\n")
		fmt.Fprintf(os.Stderr, "  up              执行所有待处理的数据库迁移 (默认)\n")
		fmt.Fprintf(os.Stderr, "  down            回滚上一版本的数据库迁移\n")
		fmt.Fprintf(os.Stderr, "  status          查看数据库当前版本与全部迁移状态\n")
		fmt.Fprintf(os.Stderr, "  force <version> 强制解除指定版本的脏迁移 (dirty) 标记\n")
		fmt.Fprintf(os.Stderr, "  seed            补齐内容种子（无 DDL：空库发布内置定义，存量定义/外部库/货架只增不改）\n")
		fmt.Fprintf(os.Stderr, "  check-refs      悬挂引用体检：列出指向不存在行的引用（部署前置检查，有则非零退出）\n")
		fmt.Fprintf(os.Stderr, "                  可选 -json 输出机器可读报告\n\n")
	}
	flag.Parse()
	if *operationTimeout <= 0 {
		log.Fatal("-timeout 必须大于 0")
	}

	args := flag.Args()
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}

	// 连接串只有 config.DSN() 一份（含 DB_SSLMODE）：内联第二份会让迁移连接与
	// 服务连接的加密开关各自漂移（2026-09-19 第二轮审计 #5）。
	dsn := cfg.DSN()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("无法连接数据库: %v", err)
	}
	defer db.Close()

	// 重试等待数据库就绪 (针对生产启动容器等待场景)
	var pingErr error
	for i := 0; i < 15; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pingErr = db.PingContext(ctx)
		cancel()
		if pingErr == nil {
			break
		}
		log.Printf("等待数据库连接就绪... (尝试 %d/15): %v", i+1, pingErr)
		time.Sleep(2 * time.Second)
	}
	if pingErr != nil {
		log.Fatalf("数据库连接超时失败: %v", pingErr)
	}

	m := migrator.New(db, migrations.FS)
	ctx, cancel := context.WithTimeout(context.Background(), *operationTimeout)
	defer cancel()

	switch cmd {
	case "seed":
		// 显式内容种子入口（无 DDL）：空库发布内置定义，存量只增不改
		// （定义/外部库/货架，后台自定义不被覆盖），最后再合并种子新增的定义键。
		// 结构迁移用 up，HTTP 进程只做只读兼容检查（见 CheckSchemaContract），
		// 空库安装顺序：mf-migrate up → mf-migrate seed → mf-migrate check-refs。
		log.Println("开始补齐内容种子（无 DDL，只增不改）...")
		s, err := catalog.Open(ctx, dsn)
		if err != nil {
			log.Fatalf("打开目录库失败: %v", err)
		}
		defer s.DB.Close()
		if err := s.SeedContent(ctx); err != nil {
			// 显式命令是运维主动要结果的地方：没生效就必须红，别让"跑过了"被误读成"合并成功"。
			log.Fatalf("内容种子补齐失败: %v", err)
		}
		log.Println("内容种子补齐完成")

	case "check-refs":
		// 部署前置检查：一次性列出库中所有悬挂引用（entity 型属性字段、结构与记录级引用、
		// 关系端点与关系属性），有则非零退出，便于直接卡进流水线。
		// 判定基准是"当前已发布定义 + 种子新增项"合并后的文档，即下一次启动会发布的定义：
		// 只按旧文档扫描会漏掉本次新增的 entity 型字段，"升级前体检"就失去意义。
		asJSON := false
		for _, a := range args[1:] {
			switch a {
			case "-json", "--json":
				asJSON = true
			default:
				log.Fatalf("未知参数 %q（用法: mf-migrate check-refs [-json]）", a)
			}
		}
		s, err := catalog.Open(ctx, dsn)
		if err != nil {
			log.Fatalf("打开目录库失败: %v", err)
		}
		defer s.DB.Close()
		report, err := s.DanglingReferences(ctx)
		if err != nil {
			log.Fatalf("悬挂引用体检失败: %v", err)
		}
		if report.DefinitionETag == "" {
			log.Println("提示: catalog.definition_config 尚无生效文档（库尚未初始化？），本次按内置种子判定")
		} else {
			log.Printf("判定基准: 生效定义 etag=%s + 种子新增 %d 项", report.DefinitionETag, len(report.SeedAdded))
		}
		if asJSON {
			out, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				log.Fatalf("序列化体检报告失败: %v", err)
			}
			fmt.Println(string(out))
		}
		for _, x := range report.Items {
			if x.Scope == "relation" {
				log.Printf("悬挂引用 [%s] relation=%s type=%s field=%s value=%s (%s)", x.Kind, x.ID, x.RelationType, x.Field, x.Value, x.Reason)
				continue
			}
			log.Printf("悬挂引用 [%s] entity=%s field=%s value=%s (%s)", x.Kind, x.ID, x.Field, x.Value, x.Reason)
		}
		if len(report.Items) > 0 {
			// 非零退出：体检的判据是"数据是否干净"，与定义发布的判据（定义是否非法）分开——
			// 部署流程保留停写状态，处理数据并重验通过后才能启动新版。
			log.Fatalf("发现 %d 条悬挂引用：引用目标行已不存在，新定义回放时会报 invalid_reference；部署检查未通过，请处理后重验", len(report.Items))
		}
		log.Println("悬挂引用体检通过: 0 条")

	case "up":
		log.Println("开始执行数据库版本迁移 (Migrate Up)...")
		if err := m.Up(ctx); err != nil {
			log.Fatalf("迁移执行失败: %v", err)
		}
		log.Println("数据库迁移全部执行成功！")

	case "down":
		log.Println("开始回滚最新版本迁移 (Migrate Down)...")
		if err := m.Down(ctx); err != nil {
			log.Fatalf("迁移回滚失败: %v", err)
		}
		log.Println("数据库迁移回滚成功！")

	case "status":
		if err := m.Status(ctx); err != nil {
			log.Fatalf("查询迁移状态失败: %v", err)
		}

	case "force":
		if len(args) < 2 {
			log.Fatal("缺少版本号参数。用法: mf-migrate force <version>")
		}
		ver, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			log.Fatalf("无效的版本号: %s", args[1])
		}
		if err := m.Force(ctx, ver); err != nil {
			log.Fatalf("强制解除脏状态失败: %v", err)
		}
		log.Printf("已成功解除版本 %d 的脏状态标记。", ver)

	default:
		flag.Usage()
		os.Exit(1)
	}
}
