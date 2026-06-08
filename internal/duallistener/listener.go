package duallistener

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"time"
)

type prefixConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type channelListener struct {
	addr  net.Addr
	base  net.Listener
	conns chan net.Conn
}

func (l *channelListener) Accept() (net.Conn, error) {
	conn, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *channelListener) Close() error {
	return l.base.Close()
}

func (l *channelListener) Addr() net.Addr {
	return l.addr
}

func Listen(addr string) (net.Listener, net.Listener, error) {
	base, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}

	httpConns := make(chan net.Conn)
	httpsConns := make(chan net.Conn)
	go splitLoop(base, httpConns, httpsConns)

	return &channelListener{addr: base.Addr(), base: base, conns: httpConns},
		&channelListener{addr: base.Addr(), base: base, conns: httpsConns},
		nil
}

func TLSConfig() (*tls.Config, error) {
	cert, err := selfSignedCertificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func splitLoop(base net.Listener, httpConns, httpsConns chan<- net.Conn) {
	defer close(httpConns)
	defer close(httpsConns)

	for {
		conn, err := base.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(conn)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		first, err := br.Peek(1)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			_ = conn.Close()
			continue
		}

		wrapped := &prefixConn{Conn: conn, reader: br}
		if first[0] == 0x16 {
			httpsConns <- wrapped
		} else {
			httpConns <- wrapped
		}
	}
}

func selfSignedCertificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "magisk-scrcpy-remote.local",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "magisk-scrcpy-remote.local"},
		IPAddresses:           localIPs(),
	}
	if len(template.IPAddresses) == 0 {
		template.IPAddresses = append(template.IPAddresses, net.ParseIP("127.0.0.1"))
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	if certPEM == nil || keyPEM == nil {
		return tls.Certificate{}, errors.New("failed to encode certificate")
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func localIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			ips = append(ips, ip4)
		} else if ip.To16() != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}
