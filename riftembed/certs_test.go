package riftembed_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// testCA is a throwaway certificate authority for TLS tests. Its leaves are issued separately
// from the CA itself, because a strict verifier refuses a CA certificate used as a leaf, which is
// why httptest's own self-signed certificate cannot stand in for a real chain.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	PEM  string
}

var serial atomic.Int64

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial.Add(1)),
		Subject:               pkix.Name{CommonName: "rift-go test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key, PEM: pemOf("CERTIFICATE", der)}
}

// leaf is an issued certificate in the forms the tests need.
type leaf struct {
	TLS     tls.Certificate
	CertPEM string
	KeyPEM  string
}

// serverLeaf issues a certificate valid for 127.0.0.1.
func (ca *testCA) serverLeaf(t *testing.T) leaf {
	t.Helper()
	return ca.issue(t, &x509.Certificate{
		Subject:     pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
}

// clientLeaf issues a certificate a client can present.
func (ca *testCA) clientLeaf(t *testing.T) leaf {
	t.Helper()
	return ca.issue(t, &x509.Certificate{
		Subject:     pkix.Name{CommonName: "rift-go test client"},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
}

func (ca *testCA) issue(t *testing.T, tmpl *x509.Certificate) leaf {
	t.Helper()
	key := newKey(t)
	tmpl.SerialNumber = big.NewInt(serial.Add(1))
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return leaf{
		TLS:     tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		CertPEM: pemOf("CERTIFICATE", der),
		KeyPEM:  pemOf("PRIVATE KEY", keyDER),
	}
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func pemOf(kind string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}
