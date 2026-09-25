# Framework integration

ConsulX integrates through `*http.Server`, so any router that is an
`http.Handler` works without an adapter. The integration test suite runs the
same scenario (normal route, health endpoints, unknown route, registration
passing in Consul, deregistration on shutdown) for net/http, Gin, Echo and
Chi against a real agent.

Rule for all of them: **call `consulx.New` before the server starts
serving**, because the handler is wrapped in `New`.

## net/http

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /orders", listOrders)
server := &http.Server{Addr: ":8080", Handler: mux}
consul, err := consulx.New(consulx.WithServer(server), consulx.WithServiceName("orders-api"), consulx.WithAutoHealth())
```

A nil `Handler` means `http.DefaultServeMux`, as in net/http; routes
registered on it after `New` still work.

## Gin

```go
router := gin.New()
router.GET("/orders", listOrders)
server := &http.Server{Addr: ":8080", Handler: router}
consul, err := consulx.New(consulx.WithServer(server), consulx.WithServiceName("orders-api"), consulx.WithAutoHealth())
```

Use `server.ListenAndServe()` instead of `router.Run()`, which creates its
own server.

## Echo (v5)

```go
e := echo.New()
e.GET("/orders", listOrders)
server := &http.Server{Addr: ":8080", Handler: e}
consul, err := consulx.New(consulx.WithServer(server), consulx.WithServiceName("orders-api"), consulx.WithAutoHealth())
```

Start it with `server.ListenAndServe()` rather than `e.Start()`.

## Chi

```go
r := chi.NewRouter()
r.Get("/orders", listOrders)
server := &http.Server{Addr: ":8080", Handler: r}
consul, err := consulx.New(consulx.WithServer(server), consulx.WithServiceName("orders-api"), consulx.WithAutoHealth())
```

## Fiber (v3)

Fiber runs on fasthttp and has no `*http.Server`, so ConsulX cannot wrap it.
The `contrib/fiber` module mounts the health endpoints on the app; the port
is given explicitly:

```go
import consulxfiber "github.com/jhonsferg/consulx/contrib/fiber"

consul, err := consulx.New(
	consulx.WithServiceName("orders-api"),
	consulx.WithServicePort(3000),
	consulx.WithAutoHealth(),
)
app := fiber.New()
consulxfiber.Mount(app, consul)
go app.Listen(":3000")
err = consul.Run(ctx)
```

## Health endpoints on a separate port

The Consul check always targets the registered address and port. To serve
health on an internal management server while registering the public port,
mount `HealthHandler()` on the management server yourself and point the
check at it with a registration hook:

```go
consul, err := consulx.New(
	consulx.WithServer(public), // registered port
	consulx.WithServiceName("orders-api"),
	consulx.WithHealthEndpoints(consulx.HealthEndpoints{Ready: "/ready"}),
	consulx.WithRegistrationHook(func(r *api.AgentServiceRegistration) {
		r.Check.HTTP = "http://" + net.JoinHostPort(r.Address, "9090") + "/ready"
	}),
)
mgmt := &http.Server{Addr: ":9090", Handler: consul.HealthHandler()}
```

Note that `WithHealthEndpoints` also injects the paths into the public
server. This pattern is not covered by the integration suite.

## Route discovery

`http.Handler` does not expose its routes, and ConsulX does not inspect
routers by reflection. Framework-specific route metadata is not provided.
