// 小柯 —— 用户自建的网页版智能体。
// Go 纯标准库后端：静态托管 Vue 前端 + SSE 流式聊天（工具调用循环）+
// MCP 客户端 + 项目工作区 + 运行时设置。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"xiaoke/internal/agents"
	"xiaoke/internal/api"
	"xiaoke/internal/chat"
	"xiaoke/internal/config"
	"xiaoke/internal/mcp"
	"xiaoke/internal/memory"
	"xiaoke/internal/persona"
	"xiaoke/internal/projects"
	"xiaoke/internal/rules"
	"xiaoke/internal/settings"
	"xiaoke/internal/skills"
	"xiaoke/internal/store"
)

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径（不存在则使用默认配置）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 运行时设置（data/settings.json，可热更新）
	setStore, err := settings.Open(filepath.Join(cfg.DataDir, "settings.json"), cfg.LLM)
	if err != nil {
		log.Fatalf("加载设置失败: %v", err)
	}

	// 项目工作区（索引在 data/projects.json；根目录可被设置热更新）
	projectsMgr, err := projects.New(filepath.Join(cfg.DataDir, "projects.json"), func() string {
		if r := strings.TrimSpace(setStore.Get().ProjectsRoot); r != "" {
			return r
		}
		return "projects" // 默认：<小柯目录>/projects
	})
	if err != nil {
		log.Fatalf("加载项目索引失败: %v", err)
	}

	// 智能体与技能（首次启动自动种子默认项）
	agentsStore, err := agents.Open(filepath.Join(cfg.DataDir, "agents.json"))
	if err != nil {
		log.Fatalf("加载智能体失败: %v", err)
	}
	if err := agentsStore.EnsureDefault(agents.Agent{
		Name: persona.Name, Description: "你的专属智能体，冷静靠谱，偶尔毒舌",
		Avatar: "/assets/avatar.png", SystemPrompt: persona.SystemPrompt, Tools: true,
	}); err != nil {
		log.Fatalf("种子默认智能体失败: %v", err)
	}
	skillsStore, err := skills.Open(filepath.Join(cfg.DataDir, "skills.json"))
	if err != nil {
		log.Fatalf("加载技能失败: %v", err)
	}
	if err := skillsStore.EnsureSeed(skills.Skill{
		Name: "金字塔写作", Description: "结论先行、以上统下的结构化表达",
		Content: `写任何汇报/文章时：
1. 第一句话给出结论或核心答案，再展开论据。
2. 论据按 MECE 分组（相互独立、完全穷尽），每组用一句话概括。
3. 层级用「总-分-总」组织：先结论，再分层论证，最后回扣结论。
4. 代码交付遵循同样原则：先给可运行的整体，再解释关键部分。`,
		Enabled: true,
	}); err != nil {
		log.Fatalf("种子示例技能失败: %v", err)
	}

	// MCP 常驻进程管理
	mcpMgr := mcp.NewManager(context.Background())
	mcpMgr.Apply(setStore.Get().MCPServers)

	// 会话存储
	st, err := store.New(filepath.Join(cfg.DataDir, "sessions"))
	if err != nil {
		log.Fatalf("初始化会话存储失败: %v", err)
	}

	// 长期记忆（全局 + 项目两级 MEMORY.md）与项目规则文件读取
	memStore, err := memory.Open(filepath.Join(cfg.DataDir, "memory"))
	if err != nil {
		log.Fatalf("初始化长期记忆失败: %v", err)
	}
	rulesStore := rules.New()

	// 自动备份：启动时立即备份一份，之后每 24 小时一次，只保留最近 14 份。
	// 备份失败只记日志，不影响服务启动。
	go autoBackup(st, filepath.Join(cfg.DataDir, "backups"))

	mux := http.NewServeMux()
	chatHandler := chat.NewHandler(cfg, st, setStore, projectsMgr, mcpMgr, agentsStore, skillsStore, memStore, rulesStore)
	chatHandler.Register(mux)
	res := &api.Resources{
		Agents: agentsStore, Skills: skillsStore,
		AvatarsDir: "avatars", AssetsDir: cfg.AssetsDir,
	}
	apiHandler := api.New(cfg, setStore, projectsMgr, mcpMgr, res)
	apiHandler.Register(mux)
	apiHandler.RegisterResources(mux)
	chatHandler.RegisterMemoryAPI(mux)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		eff := setStore.Effective()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprintf(w, `{"ok":1,"agent":%q,"mode":%q,"model":%q}`, persona.Name, eff.Mode, eff.Model)
	})

	// 灰原哀头像等静态资源（不允许目录列表）
	assets := http.StripPrefix("/assets/", http.FileServer(noDirList{http.Dir(cfg.AssetsDir)}))
	mux.Handle("GET /assets/", assets)

	// 前端构建产物（SPA 回退到 index.html）
	if info, err := os.Stat(filepath.Join(cfg.WebDist, "index.html")); err == nil && !info.IsDir() {
		mux.Handle("GET /", spaHandler(cfg.WebDist))
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "前端尚未构建：请先执行 web/ 下的 npm install && npm run build", http.StatusNotFound)
		})
	}

	addr := cfg.Server.Addr
	eff := setStore.Effective()
	log.Printf("================================================")
	log.Printf("  %s 已就绪:  http://%s", persona.Name, addr)
	log.Printf("  模式: %s   模型: %s   数据目录: %s", eff.Mode, eff.Model, cfg.DataDir)
	if n := len(setStore.Get().MCPServers); n > 0 {
		log.Printf("  MCP servers: %d 个", n)
	}
	log.Printf("================================================")
	// 读头超时防 slowloris（本地服务，风险低但属于零成本加固）；
	// 不设 Read/Write 总超时——SSE 流式回答会持续几分钟。
	srv := &http.Server{
		Addr:              addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// 优雅退出：Ctrl+C / 终止信号时先停 HTTP，再杀掉 MCP 子进程
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		log.Fatal(err)
	case <-sig:
		log.Printf("收到退出信号，正在关闭…")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	mcpMgr.Close()
}

// 自动备份保留份数、间隔，以及回收站保留天数。
const (
	backupKeep  = 14
	backupEvery = 24 * time.Hour
	trashKeep   = 30 * 24 * time.Hour
)

// autoBackup 启动时先备份一份，之后按 backupEvery 周期备份；顺带清理过期回收站。
func autoBackup(st *store.Store, dir string) {
	run := func() {
		p, err := st.Backup(dir, backupKeep)
		if err != nil {
			log.Printf("自动备份失败: %v", err)
		} else {
			log.Printf("会话已自动备份: %s", p)
		}
		if n, err := st.PurgeTrashOlderThan(trashKeep); err != nil {
			log.Printf("清理回收站失败: %v", err)
		} else if n > 0 {
			log.Printf("回收站已清理 %d 条超过 %d 天的记录", n, int(trashKeep.Hours()/24))
		}
	}
	run()
	t := time.NewTicker(backupEvery)
	defer t.Stop()
	for range t.C {
		run()
	}
}

// noDirList 包装 FileSystem，禁止目录列表（避免 /assets/ 暴露上传文件名）。
type noDirList struct{ fs http.FileSystem }

func (n noDirList) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

// securityHeaders 给所有响应加基本安全头。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; font-src 'self'; "+
				"frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// spaHandler 服务前端静态文件；未命中的路径回退到 index.html（前端路由用）。
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, filepath.Clean(strings.TrimPrefix(r.URL.Path, "/")))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}
