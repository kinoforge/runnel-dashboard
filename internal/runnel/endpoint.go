package runnel

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type Endpoint struct {
	Base  string
	TLS   *tls.Config
	user  string
	pass  string
	basic bool
}

var urlParams = map[string]bool{"ca": true, "cert": true, "key": true}

func ParseURL(raw string) (Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return Endpoint{}, fmt.Errorf("runnel url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Endpoint{}, errors.New("runnel url: scheme must be http or https")
	}
	if u.Host == "" || u.Fragment != "" || u.Opaque != "" {
		return Endpoint{}, errors.New("runnel url: needs a host and no fragment")
	}
	q := u.Query()
	for k, v := range q {
		if !urlParams[k] || len(v) != 1 || v[0] == "" {
			return Endpoint{}, fmt.Errorf("runnel url: unknown or repeated option %q; options are ca, cert and key", k)
		}
	}
	e := Endpoint{Base: u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/")}
	if u.User != nil {
		e.basic, e.user = true, u.User.Username()
		e.pass, _ = u.User.Password()
	}
	if len(q) == 0 {
		return e, nil
	}
	if u.Scheme != "https" {
		return Endpoint{}, errors.New("runnel url: ca, cert and key need https")
	}
	e.TLS, err = clientTLS(q.Get("ca"), q.Get("cert"), q.Get("key"))
	return e, err
}

func clientTLS(ca, cert, key string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, fmt.Errorf("runnel url ca: %w", err)
		}
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("runnel url ca %s holds no PEM certificate", ca)
		}
	}
	if (cert == "") != (key == "") {
		return nil, errors.New("runnel url: cert and key go together")
	}
	if cert != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("runnel url cert: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

func (e Endpoint) HasCredentials() bool { return e.basic }

func (e Endpoint) Authorize(r *http.Request) {
	if e.basic {
		r.SetBasicAuth(e.user, e.pass)
	}
}

func (e Endpoint) transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if e.TLS != nil {
		t.TLSClientConfig = e.TLS
	}
	return t
}

func (e Endpoint) String() string { return e.Base }

func (e Endpoint) GoString() string { return e.Base }
