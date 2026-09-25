package fiber

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/jhonsferg/consulx"
	"github.com/jhonsferg/consulx/health"
)

func TestMountServesHealthNextToRoutes(t *testing.T) {
	c, err := consulx.New(consulx.WithServiceName("orders"), consulx.WithServicePort(3000),
		consulx.WithAutoHealth(), consulx.WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/orders", func(ctx fiber.Ctx) error { return ctx.SendString("orders") })
	Mount(app, c)

	check := func(method, path string, want int) string {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(method, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d, want %d (%s)", method, path, resp.StatusCode, want, body)
		}
		return string(body)
	}
	if body := check(http.MethodGet, "/orders", 200); body != "orders" {
		t.Fatalf("route %q", body)
	}
	check(http.MethodGet, "/health", 200)
	check(http.MethodHead, "/health/live", 200)
	check(http.MethodGet, "/nope", 404)

	c.Health().Set("db", health.Result{Status: health.StatusDown})
	check(http.MethodGet, "/health/ready", http.StatusServiceUnavailable)
}

func TestMountWithoutHealthEndpoints(t *testing.T) {
	c, err := consulx.New(consulx.WithServiceName("orders"), consulx.WithServicePort(3000), consulx.WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	Mount(app, c)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/health", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("no endpoints must be mounted: %d", resp.StatusCode)
	}
}
