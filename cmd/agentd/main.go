// Command agentd 是 AgentHub 的服务入口。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moying/agenthub/internal/action"
	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/agent/pi"
	"github.com/moying/agenthub/internal/aiops"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/ha"
	"github.com/moying/agenthub/internal/httpapi"
	"github.com/moying/agenthub/internal/knowledge"
	"github.com/moying/agenthub/internal/modelcfg"
	"github.com/moying/agenthub/internal/modelrouter"
	"github.com/moying/agenthub/internal/obs"
	"github.com/moying/agenthub/internal/platform/dingtalk"
	"github.com/moying/agenthub/internal/platform/feishu"
	"github.com/moying/agenthub/internal/scheduler"
	"github.com/moying/agenthub/internal/store"
	"github.com/moying/agenthub/internal/support"
	"github.com/moying/agenthub/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck())
	}
	os.Exit(run())
}

// run 执行启动流程并返回退出码。
//
// @remarks 配置错误返回 2（便于编排系统区分「配置问题」与「运行故障」），
// 其他启动失败返回 1，正常退出返回 0。
// healthcheck 供容器 HEALTHCHECK 使用：探测本机 /healthz。
func healthcheck() int {
	addr := os.Getenv("AGENTHUB_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	addr = strings.Replace(addr, "0.0.0.0", "127.0.0.1", 1)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		return 1
	}
	return 0
}

func run() int {
	envFile := os.Getenv("AGENTHUB_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if err := config.LoadDotEnv(envFile); err != nil {
		fmt.Fprintf(os.Stderr, "环境文件错误：%v\n", err)
		return 2
	}

	cfg, err := config.Load(os.Getenv("AGENTHUB_CONFIG"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误：%v\n", err)
		return 2
	}

	logger := obs.NewLogger(cfg.Log.Level, cfg.Log.Format, os.Stdout)
	if cfg.Admin.Token == "" && cfg.Env == "production" {
		logger.Warn("AGENTHUB_ADMIN_TOKEN 未设置：管理台无鉴权，请确保只在内网或网关后暴露")
	}
	logger.Info("starting", "version", version.Version, "channel", cfg.Channel, "env", cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 工作上下文与信号解耦：收到信号后先等在途运行收敛，再取消。
	workCtx, workCancel := context.WithCancel(context.Background())
	defer workCancel()
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	db, err := store.Open(ctx, cfg.DB.URL, cfg.DB.MaxConns)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		return 1
	}
	if db == nil {
		logger.Warn("database not configured; running without persistence")
	}
	defer db.Close()

	skillsDir := cfg.Agent.SkillsDir
	if skillsDir == "" {
		skillsDir = filepath.Join(cfg.Storage.Dir, "skills")
	}
	attachmentsDir := filepath.Join(cfg.Storage.Dir, "attachments")
	seedDir := os.Getenv("AGENTHUB_SEED_SKILLS_DIR")
	if seedDir == "" {
		seedDir = "skills"
	}
	if copied, err := agent.SeedSkills(seedDir, skillsDir); err != nil {
		logger.Warn("seed skills failed", "error", err)
	} else if copied > 0 {
		logger.Info("seeded skills", "count", copied, "dir", skillsDir)
	}

	var ch channel.Channel
	var dt *dingtalk.Channel
	var statusMu sync.RWMutex
	statusFn := func() string { return "off" }
	feishuStatus := func() string {
		statusMu.RLock()
		fn := statusFn
		statusMu.RUnlock()
		return fn()
	}
	setFeishuStatus := func(fn func() string) {
		statusMu.Lock()
		statusFn = fn
		statusMu.Unlock()
	}
	if cfg.Channel == config.ChannelFeishu {
		client := feishu.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
		ch = feishu.NewChannel(client)
	}
	if cfg.Channel == config.ChannelDingtalk {
		dt = dingtalk.New(dingtalk.Config{
			ClientID:       cfg.Dingtalk.ClientID,
			ClientSecret:   cfg.Dingtalk.ClientSecret,
			RequireMention: cfg.Dingtalk.RequireMention,
			AllowedUsers:   cfg.Dingtalk.AllowedUsers,
			AllowedChats:   cfg.Dingtalk.AllowedChats,
			GroupOpen:      cfg.Dingtalk.GroupOpen,
			BotName:        cfg.Dingtalk.BotName,
			AttachmentsDir: attachmentsDir,
			ModelName:      cfg.Agent.Model,
			ModelBaseURL:   cfg.Agent.BaseURL,
		}, nil, logger)
		ch = dt
	}

	profiles := store.NewProfileStore(cfg.Storage.Dir)

	modelStore := modelcfg.NewStore(filepath.Join(cfg.Storage.Dir, "settings.yaml"))
	modelConfig, err := modelStore.Load()
	if err != nil {
		logger.Error("load model config failed", "error", err)
		return 1
	}
	if len(modelConfig.Providers) == 0 {
		legacyStore := store.NewModelSettingsStore(cfg.Storage.Dir)
		if saved, exists, err := legacyStore.Load(); err == nil && exists {
			if saved.BaseURL != "" {
				cfg.Agent.BaseURL = saved.BaseURL
			}
			if saved.Model != "" {
				cfg.Agent.Model = saved.Model
			}
			if saved.APIKey != "" {
				cfg.Agent.APIKey = saved.APIKey
			}
			cfg.Agent.FallbackModel = saved.FallbackModel
			cfg.Agent.VisionModel = saved.VisionModel
		}
		modelConfig = modelcfg.Legacy(cfg.Agent.BaseURL, cfg.Agent.APIKey, cfg.Agent.Model, cfg.Agent.FallbackModel, cfg.Agent.VisionModel)
		if err := modelStore.Save(modelConfig); err != nil {
			logger.Warn("persist migrated model config failed", "error", err)
		}
	}

	metrics := obs.NewMetrics()
	routerToken := randomToken()
	router := modelrouter.New(modelConfig, routerToken, metrics, logger)
	if err := router.Start(); err != nil {
		logger.Error("model router start failed", "error", err)
		return 1
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = router.Close(ctx)
	}()

	piRoot := filepath.Join(cfg.Storage.Dir, "pi")
	piAgentDir := cfg.Pi.AgentDir
	if piAgentDir == "" {
		piAgentDir = filepath.Join(piRoot, "agent")
	}
	piSessionDir := cfg.Pi.SessionDir
	if piSessionDir == "" {
		piSessionDir = filepath.Join(piRoot, "sessions")
	}
	piWorkspaceDir := cfg.Pi.WorkspaceDir
	if piWorkspaceDir == "" {
		piWorkspaceDir = filepath.Join(piRoot, "workspaces")
	}
	opsMCPBin := cfg.Observability.OpsMCPBin
	if opsMCPBin == "" {
		if exe, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), "agenthub-ops-mcp")
			if _, err := os.Stat(candidate); err == nil {
				opsMCPBin = candidate
			}
		}
	}
	builtinMCP := []pi.MCPServerConfig{}
	if cfg.Observability.K8sEnabled && os.Getenv("AGENTHUB_KUBECTL_BIN") == "" {
		_ = os.Setenv("AGENTHUB_KUBECTL_BIN", "kubectl")
	}
	if opsMCPBin != "" && (cfg.Observability.PrometheusURL != "" || cfg.Observability.LokiURL != "" || cfg.Observability.TempoURL != "" || cfg.Observability.K8sEnabled || os.Getenv("AGENTHUB_MCP_UPSTREAMS") != "") {
		builtinMCP = append(builtinMCP, pi.MCPServerConfig{
			Name: "ops", Transport: "stdio", Command: opsMCPBin, Enabled: true,
		})
	}
	preparePi := func(current modelcfg.Config) error {
		route, ok := current.Route("main")
		if !ok {
			return fmt.Errorf("模型配置缺少 main 线路")
		}
		window, maxTokens := 0, 0
		images := false
		for _, id := range route.Candidates {
			model, ok := current.Model(id)
			if !ok {
				continue
			}
			if window == 0 || (model.ContextWindow > 0 && model.ContextWindow < window) {
				window = model.ContextWindow
			}
			if maxTokens == 0 || (model.MaxTokens > 0 && model.MaxTokens < maxTokens) {
				maxTokens = model.MaxTokens
			}
			if model.Supports("vision") {
				images = true
			}
		}
		return pi.PrepareAgentDir(pi.AgentConfig{
			Dir:            piAgentDir,
			RouterURL:      router.URL(),
			RouterToken:    router.Token(),
			RouteName:      route.Name,
			ContextWindow:  window,
			MaxTokens:      maxTokens,
			SupportsImages: images,
			SystemPrompt:   pi.SystemPrompt(),
			SkillsSrc:      skillsDir,
			MCPFile:        filepath.Join(cfg.Storage.Dir, "mcp.yaml"),
			BuiltinMCP:     builtinMCP,
		})
	}
	if err := preparePi(modelConfig); err != nil {
		logger.Error("pi agent dir 初始化失败", "error", err)
		return 1
	}
	if err := pi.SyncAgentConfig(piWorkspaceDir, piAgentDir); err != nil {
		logger.Warn("sync pi workspaces failed", "error", err)
	}
	piVersion := detectPiVersion(cfg.Pi.Bin)
	logger.Info("pi runtime", "bin", cfg.Pi.Bin, "version", piVersion)
	piPackageDir := os.Getenv("AGENTHUB_PI_PACKAGE")
	if piPackageDir == "" {
		if exe, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), "pi-package")
			if _, err := os.Stat(candidate); err == nil {
				piPackageDir = candidate
			}
		}
	}
	if piPackageDir == "" {
		if _, err := os.Stat("pi-package"); err == nil {
			piPackageDir = "pi-package"
		}
	}
	extensions := []string{}
	skillDirs := []string{}
	promptDirs := []string{}
	if piPackageDir != "" {
		for _, name := range []string{"agenthub-guard.ts", "agenthub-redact.ts", "agenthub-compaction.ts", "agenthub-subagent.ts", "agenthub-report.ts"} {
			if _, err := os.Stat(filepath.Join(piPackageDir, "extensions", name)); err == nil {
				extensions = append(extensions, filepath.Join(piPackageDir, "extensions", name))
			}
		}
		if _, err := os.Stat(filepath.Join(piPackageDir, "skills")); err == nil {
			skillDirs = append(skillDirs, filepath.Join(piPackageDir, "skills"))
		}
		if _, err := os.Stat(filepath.Join(piPackageDir, "prompts")); err == nil {
			promptDirs = append(promptDirs, filepath.Join(piPackageDir, "prompts"))
		}
	}
	runner := pi.New(pi.Config{
		Bin:          cfg.Pi.Bin,
		Tools:        cfg.Pi.Tools,
		Extensions:   extensions,
		Skills:       skillDirs,
		Prompts:      promptDirs,
		AgentDir:     piAgentDir,
		SessionDir:   piSessionDir,
		WorkspaceDir: piWorkspaceDir,
		Isolation:    cfg.Pi.Isolation,
		Timeout:      time.Duration(cfg.Pi.TimeoutSeconds) * time.Second,
		Logger:       logger,
		Provider:     "agenthub",
		Model:        "main",
	})
	engine := core.New(runner, ch, logger)
	engine.SetGenerationCleaner(func(conv string, generation int) {
		if err := runner.Cleanup(conv, generation); err != nil {
			logger.Warn("cleanup generation failed", "conversation", conv, "generation", generation, "error", err)
		}
	})
	if reactor, ok := ch.(channel.Reactor); ok {
		engine.SetReactor(reactor)
	}
	engine.SetArtifactDir(filepath.Join(cfg.Storage.Dir, "artifacts"))
	engine.SetArtifactLimits(cfg.Storage.ArtifactMaxBytes, cfg.Storage.ArtifactMaxCount, cfg.Storage.ArtifactMaxTotal)
	engine.SetConcurrency(cfg.MaxConcurrentRuns)
	engine.SetRateLimit(cfg.RateLimitPerMin, time.Minute)
	engine.SetQueueTTL(time.Duration(cfg.QueueTTLSeconds) * time.Second)
	engine.SetDeliveryLease(time.Duration(cfg.DeliveryLeaseSeconds) * time.Second)
	engine.SetOutboundQPS(cfg.OutboundQPS)
	engine.SetProfileSource(func() core.ProfileSelection {
		active := profiles.Active()
		return core.ProfileSelection{Name: active.Name, Skills: active.Skills, MCPs: active.MCPs}
	})
	if fc, ok := ch.(*feishu.Channel); ok {
		engine.SetPresenter(feishu.NewCardPresenter(fc.Client(), logger).WithStopButton(cfg.Feishu.CardActionEnabled))
	}
	if dt != nil {
		dt.SetEngine(engine)
	}

	var journal *store.Journal
	if db != nil {
		journal = store.NewJournal(db)
	}
	if db != nil {
		if err := store.Migrate(ctx, db, migrationsDir()); err != nil {
			logger.Error("migration failed", "error", err)
			return 1
		}
		engine.SetJournal(store.NewJournal(db))
		if err := engine.Restore(ctx); err != nil {
			logger.Error("restore failed", "error", err)
			return 1
		}
		logger.Info("state restored")
	}
	var taskScheduler *scheduler.Scheduler
	if journal != nil {
		taskScheduler = scheduler.New(journal, engine, logger)
		go taskScheduler.Run(workCtx)
	}

	engine.SetMetrics(metrics)
	srv := httpapi.New(cfg, logger, metrics, db, version.Version)
	var incidentStore *store.IncidentStore
	var caseStore *store.CaseStore
	var knowledgeStore *store.KnowledgeStore
	if db != nil {
		incidentStore = store.NewIncidentStore(db)
		caseStore = store.NewCaseStore(db)
		knowledgeStore = store.NewKnowledgeStore(db)
		if _, err := caseStore.EnsureDefaultEmployee(ctx, cfg.AIOps.ChatID, time.Now()); err != nil {
			logger.Warn("seed default employee failed", "error", err)
		}
		if _, err := caseStore.EnsureEmployee(ctx, store.Employee{
			ID: "tech-support", Name: "技术支持", Title: "技术支持数字同事", Role: "tech-support",
			Mission: "接住支持工单，查知识/历史工单，给出可执行步骤，无法解决时升级人工。",
			Status:  "active", ChatID: cfg.Support.ChatID,
			AllowedTools: []string{"read", "bash", "grep", "find", "ls", "tool_search", "ops_investigate", "submit_report"},
		}, time.Now()); err != nil {
			logger.Warn("seed support employee failed", "error", err)
		}
	}
	if caseStore != nil {
		engine.SetReportSink(func(ctx context.Context, caseID string, raw []byte) error {
			var report store.CaseReport
			if err := json.Unmarshal(raw, &report); err != nil {
				return err
			}
			report.CaseID = caseID
			if report.Status == "" {
				report.Status = "draft"
			}
			if report.Problem == "" {
				return fmt.Errorf("pi 报告缺少 problem")
			}
			if err := caseStore.SaveCaseReport(ctx, report, time.Now()); err != nil {
				return err
			}
			return caseStore.AddCaseEvent(ctx, caseID, "report", "pi", "pi 已提交处置报告草稿", time.Now())
		})
	}
	if knowledgeStore != nil {
		knowledgeDir := cfg.Support.KnowledgeDir
		if knowledgeDir == "" {
			knowledgeDir = filepath.Join(cfg.Storage.Dir, "knowledge")
		}
		if count, err := knowledge.IngestDir(ctx, knowledgeStore, knowledgeDir); err != nil {
			logger.Warn("ingest knowledge failed", "error", err, "dir", knowledgeDir)
		} else if count > 0 {
			logger.Info("knowledge ingested", "docs", count, "dir", knowledgeDir)
		}
	}
	var supportIngestor *support.Ingestor
	if cfg.Support.Enabled {
		if caseStore == nil {
			logger.Error("support enabled without database; webhook disabled")
		} else {
			ingestor := support.New(caseStore, engine, support.Config{ChatID: cfg.Support.ChatID, EmployeeID: cfg.Support.EmployeeID, Team: cfg.Support.Team}, logger)
			ingestor.SetKnowledge(knowledgeStore)
			supportIngestor = ingestor
			srv.SetTicketHandler(support.NewHandler(ingestor, cfg.Support.Token))
			logger.Info("support ticket webhook enabled", "chat", cfg.Support.ChatID)
		}
	}
	if cfg.AIOps.Enabled {
		if incidentStore == nil {
			logger.Error("aiops enabled without database; webhook disabled")
		} else {
			if cfg.AIOps.RunbookDir == "" {
				cfg.AIOps.RunbookDir = filepath.Join(cfg.Storage.Dir, "runbooks")
			}
			_ = os.MkdirAll(cfg.AIOps.RunbookDir, 0o755)
			ingestor := aiops.NewIngestor(incidentStore, engine, aiops.Config{
				ChatID: cfg.AIOps.ChatID, RunbookDir: cfg.AIOps.RunbookDir, MaxAlerts: cfg.AIOps.MaxAlerts,
			}, logger, metrics)
			catalogPath := cfg.AIOps.ServiceCatalog
			if catalogPath == "" {
				catalogPath = filepath.Join(cfg.Storage.Dir, "services.yaml")
			}
			if catalog, err := aiops.LoadCatalog(catalogPath); err == nil {
				ingestor.SetCatalog(catalog)
			} else {
				logger.Warn("load service catalog failed", "error", err, "path", catalogPath)
			}
			ingestor.SetCases(caseStore, "sre-oncall")
			alertHandler := aiops.NewHandler(ingestor, cfg.AIOps.Token)
			alertHandler.SetSourceTokens(cfg.AIOps.SourceTokens)
			alertHandler.SetRateLimit(cfg.AIOps.RatePerMin)
			srv.SetAlertHandler(alertHandler)
			logger.Info("aiops alert webhook enabled", "chat", cfg.AIOps.ChatID, "runbook_dir", cfg.AIOps.RunbookDir)
		}
	}
	if db != nil {
		srv.RegisterCheck("db", func() (bool, string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := db.Pool().Ping(ctx); err != nil {
				return false, err.Error()
			}
			return true, "ok"
		})
	}

	var actionExecutor func(ctx context.Context, approval *store.Approval) (string, error)
	if cfg.Observability.K8sEnabled {
		kubectl := cfg.Observability.KubectlBin
		if kubectl == "" {
			kubectl = "kubectl"
		}
		namespaces := make(map[string]bool, len(cfg.Observability.K8sNamespaces))
		for _, ns := range cfg.Observability.K8sNamespaces {
			namespaces[ns] = true
		}
		executor := &action.Executor{Kubectl: kubectl, Namespaces: namespaces}
		actionExecutor = executor.Execute
	}
	if cfg.Channel == config.ChannelFeishu {
		if cfg.HA.Enabled && db != nil {
			leader := ha.New(db, logger)
			go func() {
				err := leader.Run(ctx,
					func(leaderCtx context.Context) {
						setFeishuStatus(func() string { return "connecting" })
						fn := startFeishu(leaderCtx, cfg, logger, stop, srv, engine, ch, attachmentsDir, journal, caseStore, actionExecutor)
						setFeishuStatus(fn)
					},
					func() { setFeishuStatus(func() string { return "standby" }) },
				)
				if err != nil && ctx.Err() == nil {
					logger.Error("ha leader stopped", "error", err)
					stop()
				}
			}()
		} else {
			if cfg.HA.Enabled {
				logger.Warn("ha enabled without database; starting feishu connector locally")
			}
			setFeishuStatus(startFeishu(ctx, cfg, logger, stop, srv, engine, ch, attachmentsDir, journal, caseStore, actionExecutor))
		}
	}
	if dt != nil {
		srv.RegisterCheck("dingtalk", func() (bool, string) { return true, "stream" })
		if cfg.HA.Enabled && db != nil {
			leader := ha.New(db, logger)
			go func() {
				err := leader.Run(ctx,
					func(leaderCtx context.Context) {
						setFeishuStatus(func() string { return "connected" })
						go func() {
							if err := dt.Start(leaderCtx); err != nil && leaderCtx.Err() == nil {
								logger.Error("dingtalk stream stopped", "error", err)
								stop()
							}
						}()
					},
					func() { setFeishuStatus(func() string { return "standby" }) },
				)
				if err != nil && ctx.Err() == nil {
					logger.Error("ha leader stopped", "error", err)
					stop()
				}
			}()
		} else {
			if cfg.HA.Enabled {
				logger.Warn("ha enabled without database; starting dingtalk connector locally")
			}
			setFeishuStatus(func() string { return "connected" })
			go func() {
				if err := dt.Start(workCtx); err != nil {
					logger.Error("dingtalk stream stopped", "error", err)
					stop()
				}
			}()
		}
	}

	adminStore, err := admin.NewStoreWithSkills(cfg.Storage.Dir, filepath.Join(piAgentDir, "skills"))
	if err != nil {
		logger.Error("admin storage unavailable", "error", err)
		return 1
	}
	if list, err := profiles.ListProfiles(); err == nil && len(list) == 0 {
		// 首次启动：把当前可关联技能收进默认档案，便于管理台展示引用关系。
		var names []string
		if skills, err := adminStore.ListSkills(); err == nil {
			for _, sk := range skills {
				if sk.Status == "active" {
					names = append(names, sk.Name)
				}
			}
		}
		if err := profiles.PutProfile(store.Profile{Name: "default", Description: "默认档案", Skills: names}); err != nil {
			logger.Warn("seed default profile failed", "error", err)
		}
	}
	adminSrv := admin.New(cfg, engine, adminStore, admin.Options{
		Version:       version.Version,
		FeishuStatus:  feishuStatus,
		Agent:         runner,
		Tools:         nil,
		Runtime:       "pi",
		PiVersion:     piVersion,
		Profiles:      profiles,
		Incidents:     incidentStore,
		Cases:         caseStore,
		ExecuteAction: actionExecutor,
		CreateTicket: func(ctx context.Context, title, summary, service, environment, priority, requester string) (string, error) {
			if supportIngestor == nil {
				return "", fmt.Errorf("support disabled")
			}
			return supportIngestor.Create(ctx, support.Ticket{
				ID: fmt.Sprintf("manual-%d", time.Now().UnixNano()), Title: title, Description: summary,
				Service: service, Environment: environment, Priority: priority, Requester: requester, Source: "manual",
			})
		},
		SendReport: func(ctx context.Context, caseID, text string) error {
			fc, ok := ch.(*feishu.Channel)
			if !ok {
				return fmt.Errorf("当前通道不支持发送报告")
			}
			if caseStore == nil {
				return fmt.Errorf("案件存储未接入")
			}
			c, err := caseStore.GetCase(ctx, caseID)
			if err != nil || c == nil || c.ChatID == "" {
				return fmt.Errorf("案件没有绑定群")
			}
			_, err = fc.Client().SendText(ctx, c.ChatID, "", text)
			return err
		},
		NotifyApproval: func(ctx context.Context, approval *store.Approval) error {
			fc, ok := ch.(*feishu.Channel)
			if !ok || caseStore == nil {
				return nil
			}
			c, err := caseStore.GetCase(ctx, approval.CaseID)
			if err != nil || c == nil || c.ChatID == "" {
				return err
			}
			_, err = fc.Client().CreateCard(ctx, c.ChatID, "", feishu.ApprovalCard(approval))
			return err
		},
		History: func(ctx context.Context) ([]store.Generation, error) {
			if journal == nil {
				return nil, nil
			}
			return journal.ListGenerations(ctx, 100)
		},
		HistoryMessages: func(ctx context.Context, conv string, generation int) ([]core.Message, error) {
			if journal == nil {
				return nil, nil
			}
			return journal.ListGenerationMessages(ctx, conv, generation, 200)
		},
		AuditRecord: func(actor, action, target, detail string) {
			if journal == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := journal.SaveAudit(ctx, actor, action, target, detail); err != nil {
				logger.Debug("audit write failed", "error", err)
			}
		},
		AuditList: func(ctx context.Context, limit int) ([]store.AuditEntry, error) {
			if journal == nil {
				return nil, nil
			}
			return journal.ListAudit(ctx, limit)
		},
		Journal:   journal,
		Scheduler: taskScheduler,
		ReloadAgentConfig: func() error {
			if err := preparePi(router.Config()); err != nil {
				return err
			}
			return pi.SyncAgentConfig(piWorkspaceDir, piAgentDir)
		},
		ModelConfig: func() modelcfg.Config { return router.Config() },
		SetModelConfig: func(v modelcfg.Config) error {
			if err := modelStore.Save(v); err != nil {
				return err
			}
			router.Update(v)
			if err := preparePi(v); err != nil {
				return err
			}
			return pi.SyncAgentConfig(piWorkspaceDir, piAgentDir)
		},
		ModelHealth: router.Health,
		TestModel: func(ctx context.Context, route string) error {
			payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":8}`, route)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, router.URL()+"/chat/completions", strings.NewReader(payload))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+router.Token())
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
				return fmt.Errorf("线路测试失败（%d）：%s", resp.StatusCode, string(raw))
			}
			return nil
		},
	}, logger)

	go func() {
		if err := srv.Run(serverCtx); err != nil {
			logger.Error("http server stopped", "error", err)
			stop()
		}
	}()
	go func() {
		if err := adminSrv.Run(serverCtx); err != nil {
			logger.Error("admin server stopped", "error", err)
			stop()
		}
	}()
	go flushLoop(workCtx, engine, logger)
	go deliveryRecoveryLoop(workCtx, engine, logger)
	if db != nil {
		go purgeLoop(ctx, db, filepath.Join(cfg.Storage.Dir, "artifacts"), cfg.RetentionDays, logger)
	}
	if caseStore != nil {
		var notifySLA func(context.Context, string, string, string)
		if fc, ok := ch.(*feishu.Channel); ok {
			notifySLA = func(ctx context.Context, chatID, caseID, text string) {
				if chatID == "" {
					return
				}
				if _, err := fc.Client().SendText(ctx, chatID, "", text); err != nil {
					logger.Warn("sla notify failed", "case", caseID, "error", err)
				}
			}
		}
		go slaLoop(ctx, caseStore, notifySLA, logger)
	}
	go workspaceCleanupLoop(workCtx, piWorkspaceDir, piSessionDir, cfg.RetentionDays, logger)

	<-ctx.Done()
	logger.Info("shutting down; draining in-flight runs")

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if !engine.WaitIdle(drainCtx) {
		logger.Warn("drain timeout; interrupting in-flight runs")
	}
	drainCancel()
	workCancel()
	serverCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := waitShutdown(shutdownCtx, db); err != nil {
		logger.Error("shutdown incomplete", "error", err)
		return 1
	}
	logger.Info("stopped")
	return 0
}

func slaLoop(ctx context.Context, cases *store.CaseStore, notify func(context.Context, string, string, string), logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		ids, err := cases.EscalateOverdue(ctx, time.Now(), 50)
		if err != nil && ctx.Err() == nil {
			logger.Warn("sla escalation failed", "error", err)
		} else if len(ids) > 0 {
			logger.Warn("cases escalated past SLA", "count", len(ids), "cases", ids)
			if notify != nil {
				for _, id := range ids {
					c, _ := cases.GetCase(ctx, id)
					if c == nil {
						continue
					}
					notify(ctx, c.ChatID, c.ID, "SLA 超时，案件已升级为等待人工："+c.Title+"（"+c.ID+"）")
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func migrationsDir() string {
	if dir := os.Getenv("AGENTHUB_MIGRATIONS_DIR"); dir != "" {
		return dir
	}
	return "migrations"
}

func startFeishu(ctx context.Context, cfg *config.Config, logger *slog.Logger, stop context.CancelFunc, health *httpapi.Server, engine *core.Engine, ch channel.Channel, attachmentsDir string, journal *store.Journal, cases *store.CaseStore, executeAction func(context.Context, *store.Approval) (string, error)) func() string {
	var client *feishu.Client
	if fc, ok := ch.(*feishu.Channel); ok {
		client = fc.Client()
	}
	dispatcher := feishu.NewDispatcher(engine, feishu.Options{
		BotOpenID:        cfg.Feishu.BotOpenID,
		RequireMention:   cfg.Feishu.RequireMention,
		AllowedUsers:     cfg.Feishu.AllowedUsers,
		AllowedChats:     cfg.Feishu.AllowedChats,
		AdminOpenIDs:     cfg.Feishu.AdminUsers,
		Client:           client,
		AttachmentsDir:   attachmentsDir,
		MentionFreeChats: cfg.Feishu.MentionFreeChats,
		ModelName:        cfg.Agent.Model,
		ModelBaseURL:     cfg.Agent.BaseURL,
		OnAccepted: func(ctx context.Context, in channel.Inbound) {
			if client == nil {
				return
			}
			conv := in.Tenant + ":" + in.Chat.ID + ":" + in.Sender.OpenID
			chatName, _, err := client.ChatInfo(ctx, in.Chat.ID)
			if err != nil {
				logger.Debug("feishu chat info failed", "chat", in.Chat.ID, "error", err)
			}
			userName, err := client.UserName(ctx, in.Sender.OpenID)
			if err != nil {
				logger.Debug("feishu user name failed", "user", in.Sender.OpenID, "error", err)
			}
			if chatName == "" && userName == "" {
				return
			}
			engine.SetIdentity(conv, chatName, userName)
			if journal != nil {
				saveCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := journal.SaveIdentity(saveCtx, conv, chatName, userName); err != nil {
					logger.Debug("save identity failed", "error", err)
				}
			}
		},
	}, logger)

	if client != nil {
		// 启动时补齐历史会话展示名（飞书群名/用户名）。
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, info := range engine.SessionInfos() {
				if info.ChatName != "" || info.UserName != "" {
					continue
				}
				parts := strings.SplitN(info.ID, ":", 3)
				if len(parts) != 3 {
					continue
				}
				chatName, _, err := client.ChatInfo(ctx, parts[1])
				if err != nil {
					logger.Debug("feishu chat info failed", "chat", parts[1], "error", err)
				}
				userName, err := client.UserName(ctx, parts[2])
				if err != nil {
					logger.Debug("feishu user name failed", "user", parts[2], "error", err)
				}
				if chatName == "" && userName == "" {
					continue
				}
				engine.SetIdentity(info.ID, chatName, userName)
				if journal != nil {
					if err := journal.SaveIdentity(ctx, info.ID, chatName, userName); err != nil {
						logger.Debug("save identity failed", "error", err)
					}
				}
			}
		}()
	}

	mux := http.NewServeMux()
	serving := false
	serve := func() {
		if serving {
			return
		}
		serving = true
		go func() {
			if err := httpapi.Serve(ctx, cfg.Webhook.Addr, mux, logger); err != nil {
				logger.Error("webhook server stopped", "error", err)
				stop()
			}
		}()
	}

	if cfg.Feishu.Transport == "webhook" {
		hook := feishu.NewWebhook(dispatcher, cfg.Feishu.VerificationToken, cfg.Feishu.EncryptKey, logger)
		mux.Handle("/feishu/events", hook)
		logger.Info("feishu transport", "mode", "webhook", "addr", cfg.Webhook.Addr)
		health.RegisterCheck("feishu", func() (bool, string) { return true, "webhook" })
	}
	if cfg.Feishu.CardActionEnabled {
		cardAction := feishu.NewCardAction(engine, cfg.Feishu.VerificationToken, cfg.Feishu.EncryptKey, logger)
		cardAction.SetApprovalHandler(cases, executeAction, cfg.Feishu.AdminUsers)
		mux.Handle("/feishu/card", cardAction)
		logger.Info("card action endpoint enabled", "path", "/feishu/card")
	}
	if cfg.Feishu.Transport == "webhook" || cfg.Feishu.CardActionEnabled {
		serve()
	}
	if cfg.Feishu.Transport == "webhook" {
		return func() string { return "webhook" }
	}

	connector := feishu.NewWSConnector(cfg.Feishu.AppID, cfg.Feishu.AppSecret, cfg.Feishu.Domain, logger)
	health.RegisterCheck("feishu", func() (bool, string) {
		if connector.Connected() {
			return true, "connected"
		}
		if connector.Ready() {
			return true, "reconnecting"
		}
		return true, "connecting"
	})
	logger.Info("feishu transport", "mode", "websocket", "domain", cfg.Feishu.Domain)
	go func() {
		// 长连接断开后按指数退避重试，不让一次网络抖动带走整个服务。
		backoff := time.Second
		for ctx.Err() == nil {
			err := connector.Run(ctx, dispatcher.HandlePayload)
			if ctx.Err() != nil {
				return
			}
			logger.Error("websocket connector stopped; retrying", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
	return func() string {
		switch {
		case connector.Connected():
			return "connected"
		case connector.Ready():
			return "reconnecting"
		default:
			return "connecting"
		}
	}
}

// purgeLoop 定期清理过期历史数据。
// randomToken 生成内部路由的随机 token。
func randomToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

// detectPiVersion 读取 pi 版本，失败返回空。
func detectPiVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// workspaceCleanupLoop 定期清理超过保留期的 pi 工作区与会话文件。
func workspaceCleanupLoop(ctx context.Context, workspaces, sessions string, days int, logger *slog.Logger) {
	if days <= 0 {
		return
	}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().AddDate(0, 0, -days)
			removed, err := pi.CleanupExpired(workspaces, sessions, cutoff)
			if err != nil {
				logger.Warn("pi workspace cleanup failed", "error", err)
			} else if removed > 0 {
				logger.Info("pi workspace cleanup", "removed", removed)
			}
		}
	}
}

func purgeLoop(ctx context.Context, db *store.DB, artifactDir string, days int, logger *slog.Logger) {
	if days <= 0 {
		return
	}
	journal := store.NewJournal(db)
	run := func() {
		cutoff := time.Now().AddDate(0, 0, -days)
		deleted, err := journal.PurgeBefore(ctx, cutoff)
		if err != nil {
			logger.Error("retention purge failed", "error", err)
			return
		}
		if deleted > 0 {
			logger.Info("retention purge", "rows", deleted, "cutoff", cutoff.Format(time.RFC3339))
		}
		cleanupArtifactDirs(artifactDir, cutoff, logger)
	}
	run()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func cleanupArtifactDirs(dir string, cutoff time.Time, logger *slog.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			logger.Warn("artifact cleanup failed", "name", entry.Name(), "error", err)
		}
	}
}

func deliveryRecoveryLoop(ctx context.Context, engine *core.Engine, logger *slog.Logger) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			engine.RecoverDeliveries(ctx)
		}
	}
}

func flushLoop(ctx context.Context, engine *core.Engine, logger *slog.Logger) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			engine.Dispatch(ctx)
		}
	}
}

func waitShutdown(ctx context.Context, db *store.DB) error {
	done := make(chan struct{})
	go func() {
		db.Close()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
