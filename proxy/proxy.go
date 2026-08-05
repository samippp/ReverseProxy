package proxy

import (
	"log"
	"net/http/httputil"
	"net/url"
)

func NewReverseProxy(hostname string) *httputil.ReverseProxy {
	target, err := url.Parse(hostname)
	if err != nil {
		log.Fatal("Cannot parse target for \"{}\"", hostname)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	return proxy
}
