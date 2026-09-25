// Package consulx integrates Go services with HashiCorp Consul.
//
// ConsulX sits on top of the official client (github.com/hashicorp/consul/api)
// and takes care of the work every service repeats by hand: resolving its
// own address, registering with health checks, heartbeats, re-registration
// after agent restarts, deregistration on shutdown, discovery, client-side
// load balancing and layered configuration from Consul KV.
//
// A minimal integration:
//
//	server := &http.Server{Addr: ":8080", Handler: router}
//
//	consul, err := consulx.New(
//		consulx.WithConsulAddress("http://localhost:8500"),
//		consulx.WithServer(server),
//		consulx.WithServiceName("orders-api"),
//		consulx.WithAutoHealth(),
//	)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//
//	go server.ListenAndServe()
//	if err := consul.Run(ctx); err != nil {
//		log.Fatal(err)
//	}
//
// ConsulX never captures OS signals and never creates or starts the HTTP
// server; the application stays in control of both.
//
// Anything ConsulX does not model is reachable through Client.Raw, which
// returns the underlying official client.
package consulx
