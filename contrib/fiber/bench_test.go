//go:build bench

// Benchmarks of the Fiber integration, excluded from normal builds by the
// bench build tag. See docs/benchmarks.md in the core module.
//
// Requests go through app.Test, which adds a fixed cost of its own; compare
// the variants with each other rather than reading absolute numbers. "route"
// is an application route on an app with the endpoints mounted, so its
// difference to "route-without-consulx" is what mounting adds to the
// application's own requests.
package fiber

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/jhonsferg/consulx"
)

func BenchmarkMount(b *testing.B) {
	c, err := consulx.New(consulx.WithServiceName("orders"), consulx.WithServicePort(3000),
		consulx.WithAutoHealth(), consulx.WithLogger(nil))
	if err != nil {
		b.Fatal(err)
	}
	route := func(ctx fiber.Ctx) error { return ctx.SendString("ok") }

	plain := fiber.New()
	plain.Get("/orders", route)
	mounted := fiber.New()
	mounted.Get("/orders", route)
	Mount(mounted, c)

	cases := []struct {
		name string
		app  *fiber.App
		path string
	}{
		{"route-without-consulx", plain, "/orders"},
		{"route", mounted, "/orders"},
		{"health-ready", mounted, consulx.DefaultReadyPath},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				resp, err := tc.app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		})
	}
}
