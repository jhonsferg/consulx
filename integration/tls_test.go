package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jhonsferg/consulx"
)

type pki struct {
	caFile, serverCert, serverKey, clientCert, clientKey string
}

// newPKI writes a CA, a server certificate for 127.0.0.1/localhost and a
// client certificate into dir.
func newPKI(t *testing.T, dir string) pki {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "consulx test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)

	issue := func(name string, serial int64, usage x509.ExtKeyUsage, dns []string, ips []net.IP) (string, string) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
			DNSNames: dns, IPAddresses: ips,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, _ := x509.MarshalECPrivateKey(key)
		certPath, keyPath := filepath.Join(dir, name+".pem"), filepath.Join(dir, name+"-key.pem")
		writePEM(t, certPath, "CERTIFICATE", der)
		writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)
		return certPath, keyPath
	}
	p := pki{caFile: filepath.Join(dir, "ca.pem")}
	writePEM(t, p.caFile, "CERTIFICATE", caDER)
	p.serverCert, p.serverKey = issue("server", 2, x509.ExtKeyUsageServerAuth,
		[]string{"localhost", "server.dc1.consul"}, []net.IP{net.ParseIP("127.0.0.1")})
	p.clientCert, p.clientKey = issue("client", 3, x509.ExtKeyUsageClientAuth, nil, nil)
	return p
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// startTLSConsul runs a dev agent serving HTTPS with mutual TLS on 8501.
func startTLSConsul(t *testing.T, p pki) string {
	t.Helper()
	port := freePort(t)
	hcl := `ports { https = 8501 grpc_tls = 8503 } tls { defaults { ca_file = "/tls/ca.pem" cert_file = "/tls/server.pem" key_file = "/tls/server-key.pem" verify_incoming = true } }`
	files := []testcontainers.ContainerFile{
		{HostFilePath: p.caFile, ContainerFilePath: "/tls/ca.pem", FileMode: 0o644},
		{HostFilePath: p.serverCert, ContainerFilePath: "/tls/server.pem", FileMode: 0o644},
		{HostFilePath: p.serverKey, ContainerFilePath: "/tls/server-key.pem", FileMode: 0o644},
	}
	ctr, err := testcontainers.Run(t.Context(), "hashicorp/consul:"+consulVersion(),
		testcontainers.WithCmd("agent", "-dev", "-client=0.0.0.0", "-log-level=warn", "-hcl", hcl),
		testcontainers.WithFiles(files...),
		testcontainers.WithExposedPorts("8500/tcp", "8501/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{
				network.MustParsePort("8501/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(port)}},
			}
		}),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("8501/tcp").WithStartupTimeout(time.Minute)),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		if ctr != nil {
			if rc, lerr := ctr.Logs(t.Context()); lerr == nil {
				out, _ := io.ReadAll(rc)
				t.Logf("consul logs:\n%s", out)
			}
		}
		t.Fatal(err)
	}
	return fmt.Sprintf("https://127.0.0.1:%d", port)
}

func TestTLS(t *testing.T) {
	p := newPKI(t, t.TempDir())
	addr := startTLSConsul(t, p)

	info := func(opts ...consulx.Option) error {
		c, err := consulx.New(append([]consulx.Option{consulx.WithConsulAddress(addr), consulx.WithAutoRegister(false)}, opts...)...)
		if err != nil {
			return err
		}
		var lastErr error
		for range 20 { // the HTTPS listener may accept before the agent is ready
			if _, lastErr = c.AgentInfo(t.Context()); lastErr == nil {
				return nil
			}
			time.Sleep(500 * time.Millisecond)
		}
		return lastErr
	}

	if err := info(consulx.WithTLS(consulx.TLSConfig{CAFile: p.caFile, CertFile: p.clientCert, KeyFile: p.clientKey})); err != nil {
		t.Fatalf("mutual TLS with the right CA must work: %v", err)
	}
	if err := info(consulx.WithTLS(consulx.TLSConfig{CAFile: p.caFile})); err == nil {
		t.Fatal("the agent requires a client certificate; the request must fail")
	}
	other := newPKI(t, t.TempDir())
	if err := info(consulx.WithTLS(consulx.TLSConfig{CAFile: other.caFile, CertFile: p.clientCert, KeyFile: p.clientKey})); err == nil {
		t.Fatal("a server certificate from an unknown CA must be rejected")
	}
}
