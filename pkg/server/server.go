package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"picotera/db/migrations"
	"picotera/pkg/artifacts"
	"picotera/pkg/auth"
	"picotera/pkg/configx"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/heapdump"
	"picotera/pkg/jsx"
	"picotera/pkg/kv"
	"picotera/pkg/llmbridge"
	"picotera/pkg/logx"
	"picotera/pkg/server/static"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"

	_ "github.com/danielgtaylor/huma/v2/formats/cbor"
)

type Server struct {
	queries                   *db.Queries
	db                        *pgxpool.Pool
	router                    *chi.Mux
	mgmtRouter                chi.Router
	api                       huma.API
	config                    *configx.Config
	proxyCache                *proxyTransportCache
	connQuarantine            *connQuarantine
	artifacts                 artifacts.Sink
	jsxEngine                 jsx.Engine
	kvStore                   kv.Store
	staticHandler             http.Handler
	endpointRouter            *endpointRouter
	projectExtractor          *projectExtractor
	llmBridge                 llmbridge.Bridge
	liveRequests              *liveRequestRegistry
	externalRequestIDHeaders  []string
	externalResponseIDHeaders []string
	httpServer                *http.Server
}

// newGatewayTransport builds an HTTP transport for upstream gateway requests
// with its own http2.ConfigureTransports call so that responseHeaderTimeout is
// bound to this exact transport (the HTTP/2 transport reads it from its bound
// *http.Transport — see the transport-cache build closure in NewServer). HTTP/2
// keepalive PINGs are enabled so dead connections — especially silently dropped
// CONNECT proxy tunnels — are detected and evicted instead of being reused,
// which otherwise surfaces as "http2: timeout awaiting response headers".
//
// The *http2.Transport handle is returned alongside the *http.Transport (nil if
// ConfigureTransports failed, or if HTTP/2 is disabled): std's
// CloseIdleConnections only reaches the h2 pool through the unexported altProto
// map, so evicting idle h2 connections (see proxyTransportCache.closeIdle)
// requires calling CloseIdleConnections on this handle directly.
//
// insecureTLS skips upstream certificate verification for every connection this
// transport makes (provider.insecure_tls). Because TLS is negotiated by the
// *http.Transport and only then handed to h2 via TLSNextProto["h2"], setting it
// here covers h2 upstreams too.
//
// With config.GatewayDisableHTTP2 the transport never gets an h2 layer at all:
// a non-nil but empty TLSNextProto is std's documented way to turn off automatic
// HTTP/2. The h2 keepalive PINGs are configured on the h2 handle, so they are
// simply absent in that mode.
func newGatewayTransport(config *configx.Config, responseHeaderTimeout time.Duration, insecureTLS bool) (*http.Transport, *http2.Transport) {
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   config.GatewayDialTimeout,
			KeepAlive: config.GatewayDialKeepAlive,
		}).DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      100,
		// std defaults to 2 idle connections per host, which would make a single
		// busy upstream host churn connections — especially with HTTP/2 off,
		// where every concurrent request needs its own connection.
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       config.GatewayIdleConnTimeout,
		TLSHandshakeTimeout:   config.GatewayTLSHandshakeTimeout,
		ExpectContinueTimeout: config.GatewayExpectContinueTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		// Emergency stop-gap: one connection per request (h2 singleUse, h1 no
		// keepalive), applied to every transport variant.
		DisableKeepAlives: config.GatewayDisableKeepAlives,
	}
	// MUST be set before http2.ConfigureTransports: that call appends "h2" and
	// "http/1.1" to t.TLSClientConfig.NextProtos, so replacing the config
	// afterwards would leave an empty ALPN list and silently downgrade every
	// HTTPS upstream to HTTP/1.1.
	if insecureTLS {
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in per provider
	}
	if config.GatewayDisableHTTP2 {
		t.ForceAttemptHTTP2 = false
		t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		return t, nil
	}
	h2, err := http2.ConfigureTransports(t)
	if err != nil {
		return t, nil
	}
	h2.ReadIdleTimeout = config.GatewayHTTP2ReadIdleTimeout
	h2.PingTimeout = config.GatewayHTTP2PingTimeout
	return t, h2
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

	// Every cache key builds its own transport through its own
	// http2.ConfigureTransports call. That is required, not merely tidy: under
	// HTTP/2 the response-header timeout is read from the *http.Transport bound
	// to the *http2.Transport at ConfigureTransports time (see x/net/http2
	// responseHeaderTimeout -> cc.t.t1.ResponseHeaderTimeout), and a Clone only
	// shallow-copies TLSNextProto, whose "h2" entry would still point at the
	// original's http2.Transport — so the clone's timeout (and its TLS policy)
	// would be ignored for h2 upstreams while its connections landed in the
	// original's pool. Both ResponseHeaderTimeout and TLSClientConfig are
	// connection-level fields that cannot be overridden per request, hence they
	// are part of the cache key instead.
	//
	// Non-streaming requests get a more lenient header timeout: the upstream may
	// buffer the whole response and return headers late, so raise the header
	// timeout to the global read timeout (a hard upper bound, not unlimited).
	proxyCache := newProxyTransportCache(func(profile transportProfile, streaming bool) (*http.Transport, *http2.Transport) {
		responseHeaderTimeout := config.GatewayResponseHeaderTimeout
		if !streaming {
			responseHeaderTimeout = config.GatewayReadTimeout
		}
		t, h2 := newGatewayTransport(config, responseHeaderTimeout, profile.InsecureTLS)
		applyProxyConfig(t, profile.ProxyURL)
		return t, h2
	})

	sink, err := artifacts.NewSink(config.S3, logx.WithContext(ctx))
	if err != nil {
		logx.WithContext(ctx).WithError(err).Warn("failed to init artifact sink, continuing without artifacts")
		sink, _ = artifacts.NewSink(configx.S3Config{}, logx.WithContext(ctx))
	}

	router := chi.NewMux()
	router.Use(decompressRequest)
	// The user-auth middleware guards only the internal management API. Instead
	// of matching a path prefix inside the middleware, we derive a sub-router
	// that carries it and register every /api/picotera route on that sub-router
	// (the Huma management operations below, plus the raw test/direct route in
	// registerEndpoints). The gateway catch-all and /api/unified stay on the
	// bare router and authenticate via API key.
	mgmtRouter := router.With(auth.Middleware(auth.NewResolver(conn, queries, config.Auth)))
	api := humachi.New(mgmtRouter, huma.DefaultConfig("PicoTera Management API", "1.0.0"))

	kvStore, err := kv.New(config.KV.Driver, kv.WithRedisURL(config.KV.RedisURL))
	if err != nil {
		return nil, fmt.Errorf("failed to create kv store: %w", err)
	}

	jsxEngine := jsx.NewEngine(jsx.Config{
		HookTimeout:      config.JSHookTimeout,
		MemoryLimit:      config.JSMemoryLimit,
		MaxTotalAttempts: config.JSMaxTotalAttempts,
		MaxDelay:         config.JSMaxDelay,
	}, queries, kvStore, newJSXHostAPI(queries))

	if config.LLMBridgePluginPath != "" {
		logx.WithContext(ctx).WithFields(logrus.Fields{
			"path":          config.LLMBridgePluginPath,
			"start_timeout": config.LLMBridgePluginStartTimeout,
		}).Info("starting llmbridge plugin")
	}
	llmBridge, err := llmbridge.New(ctx, llmbridge.Config{
		PluginPath:         config.LLMBridgePluginPath,
		PluginStartTimeout: config.LLMBridgePluginStartTimeout,
		HeapDumpDir:        config.HeapDumpDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize llmbridge: %w", err)
	}
	reqHeaders, err := parseExternalIDHeaderNames(config.GatewayExternalRequestIDHeaders)
	if err != nil {
		return nil, fmt.Errorf("invalid gateway_external_request_id_headers: %w", err)
	}
	respHeaders, err := parseExternalIDHeaderNames(config.GatewayExternalResponseIDHeaders)
	if err != nil {
		return nil, fmt.Errorf("invalid gateway_external_response_id_headers: %w", err)
	}
	server := &Server{
		config:                    config,
		queries:                   queries,
		db:                        conn,
		router:                    router,
		mgmtRouter:                mgmtRouter,
		api:                       api,
		proxyCache:                proxyCache,
		connQuarantine:            newConnQuarantine(),
		artifacts:                 sink,
		jsxEngine:                 jsxEngine,
		kvStore:                   kvStore,
		staticHandler:             static.Handler(),
		endpointRouter:            newEndpointRouter(queries),
		projectExtractor:          newProjectExtractor(queries),
		llmBridge:                 llmBridge,
		liveRequests:              newLiveRequestRegistry(),
		externalRequestIDHeaders:  reqHeaders,
		externalResponseIDHeaders: respHeaders,
	}
	server.registerOperations()
	server.registerEndpoints()
	logx.WithContext(ctx).Info("registered operations")

	return server, nil
}

func NewHuma() huma.API {
	router := chi.NewMux()
	s := &Server{api: humachi.New(router, huma.DefaultConfig("PicoTera Management API", "1.0.0"))}
	s.registerOperations()
	return s.api
}

// requireAdmin is a Huma middleware that rejects non-admin users. It runs after
// the chi-level auth middleware (which writes the resolved user onto the request
// context), so a missing user is a wiring bug rather than an unauthenticated
// request — surfaced as 500, matching handleGetMe.
func (s *Server) requireAdmin(ctx huma.Context, next func(huma.Context)) {
	u := auth.UserFromContext(ctx.Context())
	if u == nil {
		_ = huma.WriteErr(s.api, ctx, http.StatusInternalServerError, "no user in context")
		return
	}
	if !u.IsAdmin {
		_ = huma.WriteErr(s.api, ctx, http.StatusForbidden, "admin required")
		return
	}
	next(ctx)
}

func (s *Server) registerOperations() {
	mgmt := huma.NewGroup(s.api, "/api/picotera")
	admin := huma.NewGroup(s.api, "/api/picotera")
	admin.UseMiddleware(s.requireAdmin)
	s.register(mgmt, admin)
}

// register wires every management operation onto one of two groups sharing the
// /api/picotera prefix: mgmt (all authenticated users) and admin (is_admin only,
// via the requireAdmin middleware). Both registerOperations and NewHuma call it
// so the live server and the openapi generator never drift apart.
func (s *Server) register(mgmt, admin *huma.Group) {
	// --- User group: every authenticated user ---
	huma.Register(mgmt, contract.OperationGetMe, s.handleGetMe)
	huma.Register(mgmt, contract.OperationGetOverviewSummary, s.handleGetOverviewSummary)
	huma.Register(mgmt, contract.OperationGetOverviewDistribution, s.handleGetOverviewDistribution)
	huma.Register(mgmt, contract.OperationGetOverviewSeries, s.handleGetOverviewSeries)
	huma.Register(mgmt, contract.OperationGetOverviewSpeedBoxplot, s.handleGetOverviewSpeedBoxplot)
	huma.Register(mgmt, contract.OperationGetOverviewOutcomeSeries, s.handleGetOverviewOutcomeSeries)
	huma.Register(mgmt, contract.OperationListApiKeys, s.handleListApiKeys)
	huma.Register(mgmt, contract.OperationGetApiKey, s.handleGetApiKey)
	huma.Register(mgmt, contract.OperationCreateApiKey, s.handleCreateApiKey)
	huma.Register(mgmt, contract.OperationUpdateApiKey, s.handleUpdateApiKey)
	huma.Register(mgmt, contract.OperationDeleteApiKey, s.handleDeleteApiKey)
	huma.Register(mgmt, contract.OperationListRequests, s.handleListRequests)
	huma.Register(mgmt, contract.OperationListRequestTraces, s.handleListRequestTraces)
	huma.Register(mgmt, contract.OperationGetRequest, s.handleGetRequest)
	huma.Register(mgmt, contract.OperationListRequestSpans, s.handleListRequestSpans)
	huma.Register(mgmt, contract.OperationInterruptRequest, s.handleInterruptRequest)
	huma.Register(mgmt, contract.OperationGetRequestLive, s.handleGetRequestLive)
	huma.Register(mgmt, contract.OperationListProviderLabels, s.handleListProviderLabels)
	huma.Register(mgmt, contract.OperationListModelLabels, s.handleListModelLabels)
	huma.Register(mgmt, contract.OperationListEndpointLabels, s.handleListEndpointLabels)
	huma.Register(mgmt, contract.OperationListProjectLabels, s.handleListProjectLabels)
	huma.Register(mgmt, contract.OperationListUpstreamModelLabels, s.handleListUpstreamModelLabels)

	// Projects are per-user resources: every authenticated user manages their own.
	huma.Register(mgmt, contract.OperationListProjects, s.handleListProjects)
	huma.Register(mgmt, contract.OperationGetProject, s.handleGetProject)
	huma.Register(mgmt, contract.OperationUpsertProject, s.handleUpsertProject)
	huma.Register(mgmt, contract.OperationDeleteProject, s.handleDeleteProject)
	huma.Register(mgmt, contract.OperationMergeProject, s.handleMergeProject)

	// Per-user settings and runtime config.
	huma.Register(mgmt, contract.OperationListUserSettings, s.handleListUserSettings)
	huma.Register(mgmt, contract.OperationGetUserSetting, s.handleGetUserSetting)
	huma.Register(mgmt, contract.OperationUpsertUserSetting, s.handleUpsertUserSetting)
	huma.Register(mgmt, contract.OperationDeleteUserSetting, s.handleDeleteUserSetting)
	huma.Register(mgmt, contract.OperationGetConfig, s.handleGetConfig)
	// Exchange rates are read by the user-facing currency context (display
	// currency conversion across overview / requests / traces), so the list is
	// open to every authenticated user. Writes and pricing matching stay admin.
	huma.Register(mgmt, contract.OperationListExchangeRates, s.handleListExchangeRates)

	// --- Admin group: is_admin only ---
	huma.Register(admin, contract.OperationGetAdminOverviewSummary, s.handleGetAdminOverviewSummary)
	huma.Register(admin, contract.OperationGetAdminOverviewDistribution, s.handleGetAdminOverviewDistribution)
	huma.Register(admin, contract.OperationGetAdminOverviewSeries, s.handleGetAdminOverviewSeries)
	huma.Register(admin, contract.OperationGetAdminOverviewSpeedBoxplot, s.handleGetAdminOverviewSpeedBoxplot)
	huma.Register(admin, contract.OperationListProviders, s.handleListProviders)
	huma.Register(admin, contract.OperationGetProvider, s.handleGetProvider)
	huma.Register(admin, contract.OperationCreateProvider, s.handleCreateProvider)
	huma.Register(admin, contract.OperationUpsertProvider, s.handleUpsertProvider)
	huma.Register(admin, contract.OperationUpdateProviderModels, s.handleUpdateProviderModels)
	huma.Register(admin, contract.OperationDeleteProvider, s.handleDeleteProvider)
	huma.Register(admin, contract.OperationFetchModels, s.handleFetchModels)
	huma.Register(admin, contract.OperationListModels, s.handleListModels)
	huma.Register(admin, contract.OperationGetModel, s.handleGetModel)
	huma.Register(admin, contract.OperationPutModel, s.handlePutModel)
	huma.Register(admin, contract.OperationDeleteModel, s.handleDeleteModel)
	huma.Register(admin, contract.OperationListEndpoints, s.handleListEndpoints)
	huma.Register(admin, contract.OperationUpsertEndpoint, s.handleUpsertEndpoint)
	huma.Register(admin, contract.OperationDeleteEndpoint, s.handleDeleteEndpoint)
	huma.Register(admin, contract.OperationListProviderEndpoints, s.handleListProviderEndpoints)
	huma.Register(admin, contract.OperationUpsertProviderEndpoint, s.handleUpsertProviderEndpoint)
	huma.Register(admin, contract.OperationDeleteProviderEndpoint, s.handleDeleteProviderEndpoint)
	huma.Register(admin, contract.OperationListScripts, s.handleListScripts)
	huma.Register(admin, contract.OperationGetScript, s.handleGetScript)
	huma.Register(admin, contract.OperationCreateScript, s.handleCreateScript)
	huma.Register(admin, contract.OperationUpdateScript, s.handleUpdateScript)
	huma.Register(admin, contract.OperationDeleteScript, s.handleDeleteScript)
	huma.Register(admin, contract.OperationListKvEntries, s.handleListKvEntries)
	huma.Register(admin, contract.OperationGetKvEntry, s.handleGetKvEntry)
	huma.Register(admin, contract.OperationUpsertKvEntry, s.handleUpsertKvEntry)
	huma.Register(admin, contract.OperationDeleteKvEntry, s.handleDeleteKvEntry)
	huma.Register(admin, contract.OperationGetExchangeRate, s.handleGetExchangeRate)
	huma.Register(admin, contract.OperationPutExchangeRate, s.handlePutExchangeRate)
	huma.Register(admin, contract.OperationDeleteExchangeRate, s.handleDeleteExchangeRate)
	huma.Register(admin, contract.OperationMatchPricing, s.handleMatchPricing)
	huma.Register(admin, contract.OperationListUsers, s.handleListUsers)
	huma.Register(admin, contract.OperationGetUser, s.handleGetUser)
	huma.Register(admin, contract.OperationCreateUser, s.handleCreateUser)
	huma.Register(admin, contract.OperationUpdateUser, s.handleUpdateUser)
	huma.Register(admin, contract.OperationDeleteUser, s.handleDeleteUser)
	huma.Register(admin, contract.OperationListUserIdentities, s.handleListUserIdentities)
	huma.Register(admin, contract.OperationCreateUserIdentity, s.handleCreateUserIdentity)
	huma.Register(admin, contract.OperationUpdateUserIdentity, s.handleUpdateUserIdentity)
	huma.Register(admin, contract.OperationDeleteUserIdentity, s.handleDeleteUserIdentity)
}

func (s *Server) registerEndpoints() {
	// Unified generation routes. Registered BEFORE the catch-all gateway
	// mount so chi resolves them as exact-match handlers, never reaching
	// endpointRouter.Match. They route to handle_unified_gateway.go.
	// Grouped under corsMiddleware so browsers can call them cross-origin;
	// each route also registers OPTIONS so chi routes the preflight into the
	// group (the middleware answers it with 204 before reaching the handler).
	s.router.Group(func(r chi.Router) {
		r.Use(corsMiddleware)
		for _, route := range unifiedRoutes {
			h := s.handleUnifiedGenerate(route)
			r.Post(route.Path, h)
			r.Options(route.Path, h)
		}
	})

	// Short-circuit test route: forwards a caller-supplied body straight to a
	// provider's upstream, bypassing the entire gateway pipeline (no scripts,
	// no MPE, no logging). Registered before the catch-all mount like the
	// unified routes; not a Huma operation, so it never enters openapi.yaml.
	// Registered on mgmtRouter so it inherits user auth like the rest of
	// /api/picotera.
	s.mgmtRouter.Post("/api/picotera/test/direct", s.handleTestDirect)

	s.router.Mount("/", &gatewayHandler{s})
}

func (s *Server) Serve() error {
	heapdump.Install(s.config.HeapDumpDir, "host", func() {
		if err := s.llmBridge.SignalPlugin(syscall.SIGUSR1); err != nil {
			logrus.WithError(err).Warn("failed to forward SIGUSR1 to llmbridge plugin")
		}
	})
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
	logrus.WithField("host", s.config.Host).WithField("port", s.config.Port).Info("serving API")
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		}
	}
	if s.llmBridge != nil {
		if err := s.llmBridge.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("llmbridge close: %w", err))
		}
	}
	if s.artifacts != nil {
		if err := s.artifacts.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("artifact sink close: %w", err))
		}
	}
	if s.db != nil {
		s.db.Close()
	}
	return errors.Join(errs...)
}
