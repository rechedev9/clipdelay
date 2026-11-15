package relay

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// upstream is a platform ingest URL split the way RTMP needs it: rtmp://host/app/streamkey.
type upstream struct {
	scheme string
	addr   string
	app    string
	tcURL  string
	key    string
}

func parseUpstream(raw string) (upstream, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return upstream{}, errors.New("upstream: not a URL")
	}
	defaultPort := ""
	switch u.Scheme {
	case "rtmp":
		defaultPort = "1935"
	case "rtmps":
		defaultPort = "443"
	default:
		return upstream{}, fmt.Errorf("upstream: scheme %q is not rtmp or rtmps", u.Scheme)
	}
	if u.Hostname() == "" {
		return upstream{}, errors.New("upstream: missing host")
	}
	path := strings.Trim(u.Path, "/")
	cut := strings.LastIndex(path, "/")
	if cut <= 0 || cut == len(path)-1 {
		return upstream{}, errors.New("upstream: want rtmp://host/app/streamkey")
	}
	port := u.Port()
	if port == "" {
		port = defaultPort
	}
	app := path[:cut]
	key := path[cut+1:]
	// Platforms put options such as ?bandwidthtest=true after the key; they belong to the stream name.
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return upstream{
		scheme: u.Scheme,
		addr:   net.JoinHostPort(u.Hostname(), port),
		app:    app,
		tcURL:  u.Scheme + "://" + u.Host + "/" + app,
		key:    key,
	}, nil
}

// String is safe to log: the stream key never leaves this type.
func (u upstream) String() string {
	return u.tcURL + "/****"
}
