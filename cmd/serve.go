package cmd

import (
	"encoding/gob"
	"io"
	"reflect"
	"strconv"
	"time"

	"clevergo.tech/authmiddleware"
	"clevergo.tech/clevergo"
	"clevergo.tech/form"
	"clevergo.tech/jetpackr"
	"clevergo.tech/jetrenderer"
	"clevergo.tech/log"
	"clevergo.tech/osenv"
	"github.com/CloudyKit/jet/v5"
	"github.com/alexedwards/scs/redisstore"
	"github.com/alexedwards/scs/v2"
	"github.com/gobuffalo/packr/v2"
	"github.com/gomodule/redigo/redis"
	"github.com/justinas/nosurf"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
	"pkg.razonyang.com/gopkgs/internal/core"
	"pkg.razonyang.com/gopkgs/internal/handlers/api"
	"pkg.razonyang.com/gopkgs/internal/handlers/badge"
	"pkg.razonyang.com/gopkgs/internal/handlers/dashboard"
	"pkg.razonyang.com/gopkgs/internal/handlers/domain"
	"pkg.razonyang.com/gopkgs/internal/handlers/home"
	"pkg.razonyang.com/gopkgs/internal/handlers/pkg"
	"pkg.razonyang.com/gopkgs/internal/handlers/report"
	"pkg.razonyang.com/gopkgs/internal/handlers/trending"
	"pkg.razonyang.com/gopkgs/internal/handlers/user"
	"pkg.razonyang.com/gopkgs/internal/middleware"
	"pkg.razonyang.com/gopkgs/internal/web"

	_ "github.com/doug-martin/goqu/v9/dialect/mysql"
)

func init() {
	app.Commands = append(app.Commands, serveCmd)
}

var serveCmd = &cli.Command{
	Name:  "serve",
	Usage: "start a HTTP server",
	Action: func(c *cli.Context) error {
		startCrond()

		logger, err := provideLogger()
		if err != nil {
			return err
		}
		clevergo.SetLogger(logger)
		app := clevergo.Pure()
		if core.IsDevelopMode() {
			app.Use(clevergo.Logging())
		}

		app.Decoder = form.New()
		sessionManager := provideSessionManager()
		app.Use(
			clevergo.Recovery(),
			clevergo.ServerHeader("CleverGo"),
			clevergo.WrapHH(sessionManager.LoadAndSave),
			core.ErrorHandler,
			authmiddleware.New(core.NewSessionAuthenticator(sessionManager)),
			middleware.GoGet(db, queue),
			middleware.Host(osenv.MustGet("APP_HOST"), clevergo.PathSkipper("/assets/*", "/.well-known/*")),
			middleware.IsAuthenticated("/login", clevergo.PathSkipper(
				"/", "/callback", "/login", "/assets/*", "/.well-known/*", "/api/badges/*", "/badges/*",
				"/trending",
			)),
			clevergo.WrapHH(nosurf.NewPure),
		)
		app.Renderer = provideRenderer(sessionManager)
		app.ServeFiles("/assets", packr.New("public", "../public"))

		basicHandler := core.NewHandler(db, sessionManager)
		handlers := []web.Handler{
			&home.Handler{basicHandler},
			&dashboard.Handler{basicHandler},
			&user.Handler{basicHandler},
			&pkg.Handler{basicHandler},
			&domain.Handler{basicHandler},
			&report.Handler{basicHandler},
			&api.Handler{basicHandler},
			&badge.Handler{basicHandler},
			&trending.Handler{basicHandler},
		}
		for _, handler := range handlers {
			handler.Register(app)
		}

		return app.Run(osenv.Get("HTTP_ADDR", ":8080"))
	},
}

func provideRenderer(sessionManager *scs.SessionManager) clevergo.Renderer {
	box := packr.New("views", "../views")
	set := jet.NewHTMLSetLoader(jetpackr.New(box))
	set.SetDevelopmentMode(core.IsDevelopMode())
	renderer := jetrenderer.New(set)
	renderer.SetBeforeRender(func(w io.Writer, name string, vars jet.VarMap, data interface{}, c *clevergo.Context) error {
		ctx := c.Context()
		vars.Set("user", authmiddleware.GetIdentity(ctx))
		vars.Set("csrf", nosurf.Token(c.Request))
		vars.Set("alert", sessionManager.Pop(ctx, "alert"))
		schema := "http://"
		if c.Request.TLS != nil {
			schema = "https://"
		}
		vars.Set("siteURL", schema+osenv.MustGet("APP_HOST"))
		vars.SetFunc("date", func(args jet.Arguments) reflect.Value {
			args.RequireNumOfArguments("date", 1, 1)
			date := args.Get(0).Interface().(time.Time)
			return reflect.ValueOf(date.Format(osenv.Get("DATE_FORMAT", "2006-01-02 15:04:05")))
		})
		return nil
	})
	return renderer
}

func provideSessionManager() *scs.SessionManager {
	pool := &redis.Pool{
		MaxIdle: 10,
		Dial: func() (redis.Conn, error) {
			db, _ := strconv.Atoi(osenv.Get("REDIS_DATABASE", "0"))
			opts := []redis.DialOption{redis.DialDatabase(db)}
			if password := osenv.Get("REDIS_PASSWORD"); password != "" {
				opts = append(opts, redis.DialPassword(password))
			}
			return redis.Dial("tcp", osenv.Get("REDIS_ADDR", "localhost:6379"), opts...)
		},
	}

	_, err := pool.Get().Do("PING")
	if err != nil {
		panic(err)
	}

	gob.Register(map[string]interface{}{})
	m := scs.New()
	m.Store = redisstore.New(pool)
	m.Lifetime = 24 * time.Hour
	m.Cookie.HttpOnly = false
	return m
}

func provideLogger() (log.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = append(cfg.OutputPaths, osenv.Get("LOG_FILE", "/var/log/gopkgs.log"))
	logger, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return logger.Sugar(), nil
}
