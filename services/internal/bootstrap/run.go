package bootstrap

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/admission"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/connectors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/contextservice"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/httpserver"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/ratelimit"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/skills"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
	"github.com/redis/go-redis/v9"
)

func Run(role string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := config.Load(role, os.Getenv)
	if err != nil {
		return err
	}
	poolConfig, err := pgxpool.ParseConfig(c.DatabaseDSN)
	if err != nil {
		return errors.New("database DSN is invalid")
	}
	poolConfig.MaxConns = 4
	poolConfig.ConnConfig.ConnectTimeout = 2 * time.Second
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	poolConfig.ConnConfig.RuntimeParams["lock_timeout"] = "1000"
	poolConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return errors.New("database pool initialization failed")
	}
	defer pool.Close()
	options := &redis.Options{Addr: c.RedisAddress, Username: c.RedisUsername, Password: c.RedisPassword, MaxRetries: -1, DialTimeout: 200 * time.Millisecond, ReadTimeout: 200 * time.Millisecond, WriteTimeout: 200 * time.Millisecond, ContextTimeoutEnabled: true, PoolSize: 4}
	if c.RedisTLS {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cache := redis.NewClient(options)
	defer cache.Close()
	limiter := ratelimit.New(options)
	defer limiter.Close()
	var execution *worker.Service
	var accessRuntime *access.Runtime
	var creationGate *admission.Gate
	if role == "gateway" || role == "api" && os.Getenv("HC_ACCESS_CONFIG_FILE") != "" {
		startup, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err = db.Check(startup, pool); err == nil {
			accessRuntime, err = access.NewRuntime(startup, pool, os.Getenv("HC_ACCESS_CONFIG_FILE"))
		}
		cancel()
		if err != nil {
			return err
		}
		defer accessRuntime.Close()
	}
	if role == "api" && accessRuntime != nil {
		creationGate, err = admission.NewGate(pool, accessRuntime, os.Getenv("HC_ACCESS_CA_FILE"))
		if err != nil {
			return err
		}
		defer creationGate.Close()
	}
	if role == "worker" {
		startup, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err = db.Check(startup, pool); err == nil {
			execution, err = worker.NewService(startup, pool, os.Getenv("HC_WORKER_CONFIG_FILE"))
		}
		cancel()
		if err != nil {
			return err
		}
	}
	probe := func(probeContext context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ctx := probeContext
		if err := db.Check(ctx, pool); err != nil {
			return err
		}
		if execution != nil {
			return execution.Ready(ctx)
		}
		if accessRuntime != nil {
			if err := accessRuntime.Ready(ctx); err != nil {
				return err
			}
		}
		redisContext, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		return cache.Ping(redisContext).Err()
	}
	handler := httpserver.New(role, probe)
	if role == "api" {
		o, err := identity.LoadOptions(os.Getenv)
		if err != nil {
			return err
		}
		if o != nil {
			if accessRuntime != nil && (o.PublicURL != accessRuntime.Rules.PortalOrigin || o.OrganizationID != accessRuntime.Options.OrganizationID || o.Environment != accessRuntime.Options.Environment || o.KeyID != accessRuntime.Options.DataKeyID) {
				return access.ErrConfiguration
			}
			authorizationServer, err := identity.NewAuthorizationServer(pool, o, limiter.Allow)
			if err != nil {
				return err
			}
			cipher, err := security.NewSecretCipherWithKeyring(o.KeyID, o.Key, o.KeyringFile)
			if err != nil {
				return err
			}
			handler.WithOIDCProvider(authorizationServer)
			handler.WithIdentity(identity.NewHandler(identity.NewService(pool, authorizationServer.Client(), cipher, o.Environment, o.OrganizationID, o.PublicURL)))
			var policy *settings.Policy
			if path := os.Getenv("HC_MODEL_ALLOWLIST_FILE"); path != "" {
				data, err := config.ReadPrivateFile(path)
				if err != nil {
					return err
				}
				policy, err = settings.ParsePolicy(data)
				if err != nil {
					return err
				}
			}
			maintenance := os.Getenv("HC_MAINTENANCE")
			if maintenance != "" && maintenance != "true" && maintenance != "false" {
				return errors.New("HC_MAINTENANCE must be true or false")
			}
			cursor, err := pagination.NewCodec(o.Key, o.Environment)
			if err != nil {
				return err
			}
			var entry httpserver.BusinessHandler
			localAccounts := identity.NewAccounts(pool, o.OrganizationID)
			configurationService := settings.NewService(pool, cipher, policy, o.Environment, maintenance == "true")
			var commandGate instances.CreationGate
			if accessRuntime != nil {
				entry = access.NewAPIHandler(accessRuntime.Service, limiter.Allow)
				configurationService.WithRuntimeChecks(creationGate)
				commandGate = creationGate
			}
			handler.WithBusiness(httpserver.BusinessMux{
				Connectors:    connectors.NewHandler(connectors.NewService(pool, cipher, o.Environment, maintenance == "true", os.Getenv("HC_CONNECTOR_ENDPOINTS_FILE"), o.PublicURL), limiter.Allow),
				Context:       contextservice.NewHandler(contextservice.NewService(pool, cipher, o.Environment, maintenance == "true", os.Getenv("HC_CONTEXT_ENDPOINTS_FILE")), limiter.Allow),
				Skills:        skills.NewHandler(skills.NewService(pool, cipher, o.Environment, maintenance == "true"), limiter.Allow),
				Management:    identity.NewManagementHandler(identity.NewManagementService(pool, cursor, o.Issuer, localAccounts, cipher, o.Environment)),
				Configuration: settings.NewHandler(configurationService, limiter.Ready, limiter.Allow),
				Resources:     instances.NewQueryHandler(instances.NewQueryService(pool, cursor)),
				Access:        entry,
				Commands:      instances.NewCommandHandler(instances.NewCommandService(pool, commandGate, maintenance == "true"), limiter.Allow),
			}, o.PublicURL)
		}
	}
	var root http.Handler = handler
	if execution != nil {
		root = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/internal/preflight" {
				handler.ServeHTTP(w, r)
				return
			}
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(host)
			if err != nil || ip == nil || !ip.IsLoopback() || r.Host != c.HTTPAddress || r.Method != "GET" || r.URL.RawQuery != "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			webapi.JSON(w, 200, execution.Admission(r.Context()))
		})
	}
	var gateway *access.Gateway
	if role == "gateway" {
		gateway = access.NewGateway(accessRuntime.Rules, accessRuntime.Options.OrganizationID, accessRuntime.Service, limiter.Allow)
		defer gateway.Close()
		root = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host == c.HTTPAddress && (r.URL.Path == "/livez" || r.URL.Path == "/readyz") {
				handler.ServeHTTP(w, r)
				return
			}
			gateway.ServeHTTP(w, r)
		})
	}
	server := &http.Server{Addr: c.HTTPAddress, Handler: root, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	if gateway != nil {
		server.WriteTimeout = 0
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	var workerCh chan error
	if execution != nil {
		workerCh = make(chan error, 1)
		go func() { workerCh <- execution.Run(ctx) }()
	}
	slog.Info("process started", "role", role, "address", c.HTTPAddress)
	serverFinished, workerFinished := false, false
	var runErr error
	select {
	case err := <-errCh:
		serverFinished = true
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case err := <-workerCh:
		workerFinished = true
		runErr = err
	case <-ctx.Done():
	}
	stop()
	if gateway != nil {
		gateway.Close()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		if runErr == nil {
			runErr = err
		}
	}
	if !serverFinished {
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) && runErr == nil {
			runErr = err
		}
	}
	if workerCh != nil && !workerFinished {
		select {
		case err := <-workerCh:
			if runErr == nil {
				runErr = err
			}
		case <-shutdown.Done():
			if runErr == nil {
				runErr = errors.New("Worker shutdown deadline exceeded")
			}
		}
	}
	return runErr
}
