package auth

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	dyauth "src.solsynth.dev/sosys/go/pkg/auth"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/core"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/module"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

// ModuleID is the auth module's registry ID.
const ModuleID = "app.solian.files.auth"

// Factory returns the compiled module descriptor. It is Required: DysonFS is
// useless without an identity, and the module always resolves one — either
// from Stargate or synthesized from configuration.
func Factory() module.Factory {
	return module.Factory{
		ID:             ModuleID,
		Kind:           module.KindFeature,
		DefaultEnabled: true,
		Required:       true,
		Capabilities: []string{
			"auth", "auth.token", "auth.session", "auth.synthesized",
			"auth.webdav-token", "auth.permissions",
		},
		New: func() module.Instance { return &instance{} },
	}
}

type instance struct {
	middleware *Middleware
	conns      *clientConns
}

// clientConns owns every Stargate gRPC connection the module dialed.
type clientConns struct {
	account    *grpc.ClientConn
	permission *grpc.ClientConn
}

func (c *clientConns) close() {
	if c == nil {
		return
	}
	if c.account != nil {
		_ = c.account.Close()
	}
	if c.permission != nil {
		_ = c.permission.Close()
	}
	c.account, c.permission = nil, nil
}

// Prepare builds the identity provider and returns the middleware chain. It
// runs before the HTTP engine is exposed because gin only applies middleware to
// routes registered after Use, and it must build the provider because nothing
// else runs earlier.
func (i *instance) Prepare(_ context.Context, services *core.Services) ([]gin.HandlerFunc, error) {
	cfg := services.Config

	if strings.TrimSpace(cfg.Auth.Target) == "" {
		// No Stargate endpoint: the deployment is standalone. Every request
		// belongs to one synthesized account and no permission node is
		// consulted, so file ownership and every edit is attributed to it.
		identity := &core.SynthesizedIdentity{
			ID:        synthesizedID(cfg.Auth.SyntheticAccountID),
			Name:      firstNonEmpty(cfg.Auth.SyntheticAccountName, "dyson"),
			Nick:      firstNonEmpty(cfg.Auth.SyntheticAccountNick, "Dyson"),
			Superuser: true,
		}
		i.middleware = NewSynthesized(identity)
		services.Identity.Synthesized = identity
		// No permission provider: every RequireAccountPermission call allows.
		services.Files.SetPermissionChecker(nil)
		services.SetAuthenticator(i.middleware.Handler())
		logging.Log.Info().
			Str("accountId", identity.ID).
			Bool("superuser", identity.Superuser).
			Msg("auth provider absent; every request resolves to the synthesized account")
	} else {
		authenticator, err := dyauth.NewGrpcTokenAuthenticator(dyauth.GrpcAuthDialConfig{
			Target:        cfg.Auth.Target,
			UseTLS:        cfg.Auth.UseTLS,
			TLSSkipVerify: cfg.Auth.TLSSkipVerify,
		})
		if err != nil {
			return nil, err
		}
		i.middleware = NewStargate(authenticator)
		services.SetAuthenticator(i.middleware.Handler())

		// Stargate hosts the account, auth and permission surfaces; one target
		// drives all three clients. They belong to this module because they
		// only exist when an auth provider does.
		conns, err := dialClients(cfg.Auth, services)
		if err != nil {
			conns.close()
			return nil, err
		}
		i.conns = conns
		logging.Log.Info().Str("target", cfg.Auth.Target).Bool("useTLS", cfg.Auth.UseTLS).Msg("auth client enabled")
	}

	chain := []gin.HandlerFunc{i.middleware.Handler()}
	if cfg.WebDAV.Enabled {
		// WebDAV Basic auth resolves a token to an account before the account
		// middleware runs, and always continues so a public read still works.
		chain = append(chain, WebDAVHandler(services.Files))
	}
	return chain, nil
}

// Configure has nothing left to do; Prepare owns provider construction so the
// identity exists before any route is registered.
func (i *instance) Configure(_ context.Context, _ *module.Host) error { return nil }

func (i *instance) Stop(context.Context) error {
	i.conns.close()
	return nil
}

// dialClients opens the account and permission channels and installs them on
// the services that consume them.
func dialClients(cfg config.AuthConfig, services *core.Services) (*clientConns, error) {
	conns := &clientConns{}

	accountClient, accountConn, err := service.NewAccountClient(cfg)
	if err != nil {
		return conns, err
	}
	conns.account = accountConn
	services.Quota.SetAccountClient(accountClient)

	permissionClient, permissionConn, err := service.NewPermissionClient(cfg)
	if err != nil {
		return conns, err
	}
	conns.permission = permissionConn
	services.Files.SetPermissionClient(permissionClient)

	return conns, nil
}

// Synthesized reports whether the process runs without an auth provider.
func Synthesized(services *core.Services) bool { return services.AuthDisabled() }

func synthesizedID(configured string) string {
	if id := strings.TrimSpace(configured); id != "" {
		return id
	}
	return "00000000-0000-0000-0000-000000000001"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
