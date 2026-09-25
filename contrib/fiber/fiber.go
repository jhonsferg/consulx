// Package fiber serves ConsulX health endpoints on a Fiber application.
//
// Fiber runs on fasthttp, not net/http, so consulx.WithServer cannot wrap
// it. Configure ConsulX without a server, give it the port explicitly and
// mount the endpoints on the app:
//
//	consul, err := consulx.New(
//		consulx.WithServiceName("orders-api"),
//		consulx.WithServicePort(3000),
//		consulx.WithAutoHealth(),
//	)
//	app := fiber.New()
//	consulxfiber.Mount(app, consul)
//	go app.Listen(":3000")
//	err = consul.Run(ctx)
package fiber

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"

	"github.com/jhonsferg/consulx"
)

// Mount registers GET and HEAD routes for every health endpoint configured
// on c (Health.Endpoints). It registers nothing when health endpoints are
// disabled. The routes serve exactly what the net/http integration serves.
func Mount(app *fiber.App, c *consulx.Client) {
	e := c.EffectiveConfig().Health.Endpoints
	h := adaptor.HTTPHandler(c.HealthHandler())
	for _, path := range []string{e.Health, e.Live, e.Ready} {
		if path == "" {
			continue
		}
		app.Get(path, h) // Fiber also answers HEAD for GET routes
	}
}
