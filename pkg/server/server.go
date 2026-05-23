package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"picotera/db/migrations"
	"picotera/pkg/artifacts"
	"picotera/pkg/auth"
	"picotera/pkg/configx"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/jsx"
	"picotera/pkg/kv"
	"picotera/pkg/llmbridge"
	"picotera/pkg/logx"
	"picotera/pkg/server/static"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"

	_ "github.com/danielgtaylor/huma/v2/formats/cbor"
)

type Server struct {
	queries          *db.Queries
	dbPool           *pgxpool.Pool
	router           *chi.Mux
	api              huma.API
	config           *configx.Config
	httpClient       *http.Client
	proxyCache       *proxyTransportCache
	artifacts        artifacts.Sink
	jsxEngine        *jsx.Engine
	kvStore          kv.Store
	sessionStore     *auth.SessionStore
	webauthn         *webauthn.WebAuthn
	staticHandler    http.Handler
	endpointRouter   *endpointRouter
	projectRouter    *projectRouter
	projectExtractor *projectExtractor
	llmBridge        llmbridge.Bridge
}

func NewServer(ctx context.Context) (*Server, error) {
	config, err := configx.Parse()
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	logx.WithContext(ctx).Info("running migrations")
	migrationResult, err := migrations.UpWithResult(config.DatabaseURL)
	if err != nil {
		logx.WithContext(ctx).WithError(err).Error("failed to run migrations")
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	logx.WithContext(ctx).Info("migrations completed")

	conn, err := pgxpool.New(ctx, config.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	queries := db.New(conn)
	if migrationResult.PreviousVersion < traceBackfillMigrationVersion && migrationResult.CurrentVersion >= traceBackfillMigrationVersion {
		logx.WithContext(ctx).Info("backfilling historical traces")
		if err := backfillTraces(ctx, queries); err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed to backfill traces: %w", err)
		}
	} else {
		logx.WithContext(ctx).WithFields(logrus.Fields{
			"previousVersion": migrationResult.PreviousVersion,
			"currentVersion":  migrationResult.CurrentVersion,
		}).Debug("skipping historical trace backfill")
	}

	logx.WithContext(ctx).Info("connected to database")

	baseTransport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 15 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: config.GatewayReadTimeout,
	}
	httpClient := &http.Client{Transport: baseTransport}
	proxyCache := newProxyTransportCache(baseTransport)

	sink, err := artifacts.NewSink(config.S3, logx.WithContext(ctx))
	if err != nil {
		logx.WithContext(ctx).WithError(err).Warn("failed to init artifact sink, continuing without artifacts")
		sink, _ = artifacts.NewSink(configx.S3Config{}, logx.WithContext(ctx))
	}

	kvStore, err := kv.New(config.KV.Driver, kv.WithRedisURL(config.KV.RedisURL))
	if err != nil {
		return nil, fmt.Errorf("failed to create kv store: %w", err)
	}
	sessionStore := auth.NewSessionStore(kvStore, config.SessionTTL)

	wa, err := auth.NewWebAuthn(config)
	if err != nil {
		return nil, fmt.Errorf("init webauthn: %w", err)
	}
	logx.WithContext(ctx).WithFields(logrus.Fields{
		"rp_id":   config.WebAuthnRPID,
		"origins": config.PublicOrigins,
	}).Info("auth ready")

	router := chi.NewMux()
	router.Use(auth.LoadSession(config, queries, sessionStore))
	api := humachi.New(router, huma.DefaultConfig("PicoTera Management API", "1.0.0"))
	registerSecurityScheme(api)

	jsxEngine := jsx.NewEngine(jsx.Config{
		HookTimeout:      config.JSHookTimeout,
		MemoryLimit:      config.JSMemoryLimit,
		MaxTotalAttempts: config.JSMaxTotalAttempts,
		MaxDelay:         config.JSMaxDelay,
	}, queries, kvStore)

	projectRouter := newProjectRouter(queries)
	if config.LLMBridgeWASMPath != "" {
		cacheDir := config.LLMBridgeWASMCacheDir
		if cacheDir == "" {
			cacheDir = llmbridge.DefaultCacheDir(config.LLMBridgeWASMPath)
		}
		logx.WithContext(ctx).WithFields(logrus.Fields{
			"path":      config.LLMBridgeWASMPath,
			"cache_dir": cacheDir,
			"runtime":   config.LLMBridgeWASMRuntime,
			"pool":      config.LLMBridgeWASMPoolSize,
		}).Info("prewarming llmbridge wasm")
	}
	llmBridge, err := llmbridge.New(ctx, llmbridge.Config{
		PoolSize:    config.LLMBridgeWASMPoolSize,
		WASMPath:    config.LLMBridgeWASMPath,
		CacheDir:    config.LLMBridgeWASMCacheDir,
		RuntimeMode: config.LLMBridgeWASMRuntime,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize llmbridge: %w", err)
	}
	server := &Server{
		config:           config,
		queries:          queries,
		dbPool:           conn,
		router:           router,
		api:              api,
		httpClient:       httpClient,
		proxyCache:       proxyCache,
		artifacts:        sink,
		jsxEngine:        jsxEngine,
		kvStore:          kvStore,
		sessionStore:     sessionStore,
		webauthn:         wa,
		staticHandler:    static.Handler(),
		endpointRouter:   newEndpointRouter(queries),
		projectRouter:    projectRouter,
		projectExtractor: newProjectExtractor(projectRouter),
		llmBridge:        llmBridge,
	}
	server.registerOperations()
	server.registerEndpoints()
	logx.WithContext(ctx).Info("registered operations")

	return server, nil
}

func NewHuma() huma.API {
	router := chi.NewMux()
	s := &Server{
		router: router,
		api:    humachi.New(router, huma.DefaultConfig("PicoTera Management API", "1.0.0")),
	}
	registerSecurityScheme(s.api)
	s.registerOperations()
	return s.api
}

// registerSecurityScheme declares the cookie-based session scheme on the
// OpenAPI document. registerOp attaches a `picoteraSession: []` requirement
// to every non-public operation, which references this scheme by name.
func registerSecurityScheme(api huma.API) {
	doc := api.OpenAPI()
	if doc.Components == nil {
		doc.Components = &huma.Components{}
	}
	if doc.Components.SecuritySchemes == nil {
		doc.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	doc.Components.SecuritySchemes["picoteraSession"] = &huma.SecurityScheme{
		Type: "apiKey",
		In:   "cookie",
		Name: auth.SessionCookieName,
	}
}

func (s *Server) registerOperations() {
	mgmt := huma.NewGroup(s.api, "/api/picotera")

	var admin = contract.AuthRequirement{Kind: contract.AuthAdmin}

	// Auth — public (status/login) or any session (logout)
	registerOp(mgmt, contract.OperationAuthStatus, s.handleAuthStatus,
		contract.AuthRequirement{Kind: contract.AuthPublic})
	registerOpHTTP(s.router, "POST", "/api/picotera/auth/login/begin",
		contract.AuthRequirement{Kind: contract.AuthPublic}, s.handleLoginBeginHTTP)
	registerOpHTTP(s.router, "POST", "/api/picotera/auth/login/complete",
		contract.AuthRequirement{Kind: contract.AuthPublic}, s.handleLoginCompleteHTTP)
	registerOpHTTP(s.router, "POST", "/api/picotera/auth/logout",
		contract.AuthRequirement{Kind: contract.AuthPublic}, s.handleLogoutHTTP)

	// Enrollment
	registerOp(mgmt, contract.OperationPreviewEnrollment, s.handlePreviewEnrollment,
		contract.AuthRequirement{Kind: contract.AuthPublic})
	registerOpHTTP(s.router, "POST", "/api/picotera/enrollments/{token}/register/begin",
		contract.AuthRequirement{Kind: contract.AuthPublic}, s.handleEnrollmentBeginHTTP)
	registerOpHTTP(s.router, "POST", "/api/picotera/enrollments/{token}/register/complete",
		contract.AuthRequirement{Kind: contract.AuthPublic}, s.handleEnrollmentCompleteHTTP)

	// Me — session-gated self-management
	sessionReq := contract.AuthRequirement{Kind: contract.AuthSession}
	registerOp(mgmt, contract.OperationGetMe, s.handleGetMe, sessionReq)
	registerOp(mgmt, contract.OperationListMyCredentials, s.handleListMyCredentials, sessionReq)
	registerOp(mgmt, contract.OperationDeleteMyCredential, s.handleDeleteMyCredential, sessionReq)
	registerOpHTTP(s.router, "POST", "/api/picotera/me/credentials/register/begin",
		sessionReq, s.handleAddCredentialBeginHTTP)
	registerOpHTTP(s.router, "POST", "/api/picotera/me/credentials/register/complete",
		sessionReq, s.handleAddCredentialCompleteHTTP)

	// Providers — all admin
	registerOp(mgmt, contract.OperationListProviders, s.handleListProviders, admin)
	registerOp(mgmt, contract.OperationGetProvider, s.handleGetProvider, admin)
	registerOp(mgmt, contract.OperationCreateProvider, s.handleCreateProvider, admin)
	registerOp(mgmt, contract.OperationUpsertProvider, s.handleUpsertProvider, admin)
	registerOp(mgmt, contract.OperationUpdateProviderModels, s.handleUpdateProviderModels, admin)
	registerOp(mgmt, contract.OperationDeleteProvider, s.handleDeleteProvider, admin)

	// Models — reads gated by view_models, writes admin
	registerOp(mgmt, contract.OperationListModels, s.handleListModels, contract.RequirePermission(contract.PermViewModels))
	registerOp(mgmt, contract.OperationGetModel, s.handleGetModel, contract.RequirePermission(contract.PermViewModels))
	registerOp(mgmt, contract.OperationPutModel, s.handlePutModel, admin)
	registerOp(mgmt, contract.OperationDeleteModel, s.handleDeleteModel, admin)

	// Endpoints — reads gated by view_models, writes admin
	registerOp(mgmt, contract.OperationListEndpoints, s.handleListEndpoints, contract.RequirePermission(contract.PermViewModels))
	registerOp(mgmt, contract.OperationUpsertEndpoint, s.handleUpsertEndpoint, admin)
	registerOp(mgmt, contract.OperationDeleteEndpoint, s.handleDeleteEndpoint, admin)

	// ProviderEndpoints — all admin (configuration surface)
	registerOp(mgmt, contract.OperationListProviderEndpoints, s.handleListProviderEndpoints, admin)
	registerOp(mgmt, contract.OperationUpsertProviderEndpoint, s.handleUpsertProviderEndpoint, admin)
	registerOp(mgmt, contract.OperationDeleteProviderEndpoint, s.handleDeleteProviderEndpoint, admin)

	// Fetch models — admin
	registerOp(mgmt, contract.OperationFetchModels, s.handleFetchModels, admin)

	// Requests
	registerOp(mgmt, contract.OperationListRequests, s.handleListRequests, contract.RequirePermission(contract.PermViewOwnUsage))
	registerOp(mgmt, contract.OperationListRequestTraces, s.handleListRequestTraces, contract.RequirePermission(contract.PermViewOwnTraces))
	registerOp(mgmt, contract.OperationGetRequest, s.handleGetRequest, contract.RequirePermission(contract.PermViewOwnUsage))
	registerOp(mgmt, contract.OperationListRequestSpans, s.handleListRequestSpans, contract.RequirePermission(contract.PermViewOwnUsage))

	// Exchange rates + pricing — admin
	registerOp(mgmt, contract.OperationListExchangeRates, s.handleListExchangeRates, admin)
	registerOp(mgmt, contract.OperationGetExchangeRate, s.handleGetExchangeRate, admin)
	registerOp(mgmt, contract.OperationPutExchangeRate, s.handlePutExchangeRate, admin)
	registerOp(mgmt, contract.OperationDeleteExchangeRate, s.handleDeleteExchangeRate, admin)
	registerOp(mgmt, contract.OperationMatchPricing, s.handleMatchPricing, admin)

	// API keys — permission-gated
	registerOp(mgmt, contract.OperationListApiKeys, s.handleListApiKeys, contract.RequirePermission(contract.PermManageOwnAPIKeys))
	registerOp(mgmt, contract.OperationGetApiKey, s.handleGetApiKey, contract.RequirePermission(contract.PermManageOwnAPIKeys))
	registerOp(mgmt, contract.OperationCreateApiKey, s.handleCreateApiKey, contract.RequirePermission(contract.PermManageOwnAPIKeys))
	registerOp(mgmt, contract.OperationUpdateApiKey, s.handleUpdateApiKey, contract.RequirePermission(contract.PermManageOwnAPIKeys))
	registerOp(mgmt, contract.OperationDeleteApiKey, s.handleDeleteApiKey, contract.RequirePermission(contract.PermManageOwnAPIKeys))

	// Overview metrics — view_own_usage
	registerOp(mgmt, contract.OperationGetOverviewSummary, s.handleGetOverviewSummary, contract.RequirePermission(contract.PermViewOwnUsage))
	registerOp(mgmt, contract.OperationGetOverviewDistribution, s.handleGetOverviewDistribution, contract.RequirePermission(contract.PermViewOwnUsage))
	registerOp(mgmt, contract.OperationGetOverviewSeries, s.handleGetOverviewSeries, contract.RequirePermission(contract.PermViewOwnUsage))

	// Projects — admin
	registerOp(mgmt, contract.OperationListProjects, s.handleListProjects, admin)
	registerOp(mgmt, contract.OperationGetProject, s.handleGetProject, admin)
	registerOp(mgmt, contract.OperationUpsertProject, s.handleUpsertProject, admin)
	registerOp(mgmt, contract.OperationDeleteProject, s.handleDeleteProject, admin)

	// Accounts — admin
	registerOp(mgmt, contract.OperationListAccounts, s.handleListAccounts, admin)
	registerOp(mgmt, contract.OperationGetAccount, s.handleGetAccount, admin)
	registerOp(mgmt, contract.OperationUpdateAccount, s.handleUpdateAccount, admin)
	registerOp(mgmt, contract.OperationDeleteAccount, s.handleDeleteAccount, admin)
	registerOp(mgmt, contract.OperationDeleteAccountCredential, s.handleDeleteAccountCredential, admin)
	registerOp(mgmt, contract.OperationRevokeAccountSessions, s.handleRevokeAccountSessions, admin)
	registerOp(mgmt, contract.OperationReissueEnrollment, s.handleReissueEnrollment, admin)
	registerOp(mgmt, contract.OperationCreateInvitation, s.handleCreateInvitation, admin)

	// Scripts — admin
	registerOp(mgmt, contract.OperationListScripts, s.handleListScripts, admin)
	registerOp(mgmt, contract.OperationGetScript, s.handleGetScript, admin)
	registerOp(mgmt, contract.OperationCreateScript, s.handleCreateScript, admin)
	registerOp(mgmt, contract.OperationUpdateScript, s.handleUpdateScript, admin)
	registerOp(mgmt, contract.OperationDeleteScript, s.handleDeleteScript, admin)

	// Simulate — admin
	registerOp(mgmt, contract.OperationSimulateDispatch, s.handleSimulateDispatch, admin)

	// KV — admin
	registerOp(mgmt, contract.OperationListKvEntries, s.handleListKvEntries, admin)
	registerOp(mgmt, contract.OperationGetKvEntry, s.handleGetKvEntry, admin)
	registerOp(mgmt, contract.OperationUpsertKvEntry, s.handleUpsertKvEntry, admin)
	registerOp(mgmt, contract.OperationDeleteKvEntry, s.handleDeleteKvEntry, admin)
}

func (s *Server) registerEndpoints() {
	// Unified generation routes. Registered BEFORE the catch-all gateway
	// mount so chi resolves them as exact-match handlers, never reaching
	// endpointRouter.Match. They route to handle_unified_gateway.go.
	s.router.Post("/api/picotera/v1/messages", s.handleUnifiedGenerate(llmbridge.FormatAnthropicMessages))
	s.router.Post("/api/picotera/v1/responses", s.handleUnifiedGenerate(llmbridge.FormatOpenAIResponses))
	s.router.Post("/api/picotera/v1/chat/completions", s.handleUnifiedGenerate(llmbridge.FormatOpenAIChatCompletions))
	s.router.Post("/api/picotera/v1beta/models/{model}:generateContent", s.handleUnifiedGenerate(llmbridge.FormatGeminiGenerateContent))
	s.router.Post("/api/picotera/v1beta/models/{model}:streamGenerateContent", s.handleUnifiedGenerate(llmbridge.FormatGeminiStreamGenerateContent))

	s.router.Mount("/", &gatewayHandler{s})
}

func (s *Server) Serve() error {
	logrus.WithField("host", s.config.Host).WithField("port", s.config.Port).Info("serving API")
	return http.ListenAndServe(fmt.Sprintf("%s:%d", s.config.Host, s.config.Port), s.router)
}
