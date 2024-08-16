package ipparser

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/miekg/dns"

	"github.com/libp2p/go-libp2p/core/peer"
)

const pluginName = "ipparser"

func init() { plugin.Register(pluginName, setup) }

func setup(c *caddy.Controller) error {
	c.Next()

	var forgeDomain string
	if c.NextArg() {
		forgeDomain = c.Val()
	}
	if c.NextArg() {
		// If there was another token, return an error, because we don't have any configuration.
		// Any errors returned from this setup function should be wrapped with plugin.Error, so we
		// can present a slightly nicer error message to the user.
		return plugin.Error(pluginName, c.ArgErr())
	}

	// Add the Plugin to CoreDNS, so Servers can use it in their plugin chain.
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return ipParser{Next: next, ForgeDomain: forgeDomain}
	})

	return nil
}

type ipParser struct {
	Next        plugin.Handler
	ForgeDomain string
}

const ttl = 1 * time.Hour

// ServeDNS implements the plugin.Handler interface.
func (p ipParser) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	var answers []dns.RR
	for _, q := range r.Question {
		if q.Qtype != dns.TypeA && q.Qtype != dns.TypeAAAA && q.Qtype != dns.TypeANY {
			continue
		}

		subdomain := strings.TrimSuffix(q.Name, "."+p.ForgeDomain+".")
		if len(subdomain) == len(q.Name) || len(subdomain) == 0 {
			continue
		}

		domainSegments := strings.Split(subdomain, ".")
		if len(domainSegments) != 2 {
			continue
		}

		peerIDStr := domainSegments[1]
		_, err := peer.Decode(peerIDStr)
		if err != nil {
			continue
		}

		prefix := domainSegments[0]
		segments := strings.Split(prefix, "-")
		if len(segments) == 4 && (q.Qtype == dns.TypeA || q.Qtype == dns.TypeANY) {
			ipStr := strings.Join(segments, ".")
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}
			answers = append(answers, &dns.A{
				Hdr: dns.RR_Header{
					Name:   dns.Fqdn(q.Name),
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    uint32(ttl.Seconds()),
				},
				A: ip,
			})
			continue
		}

		if !(q.Qtype == dns.TypeAAAA || q.Qtype == dns.TypeANY) {
			continue
		}

		// - is not a valid first or last character https://datatracker.ietf.org/doc/html/rfc1123#section-2
		if prefix[0] == '-' || prefix[len(prefix)-1] == '-' {
			continue
		}

		prefixAsIpv6 := strings.Join(segments, ":")
		ip := net.ParseIP(prefixAsIpv6)
		if ip == nil {
			continue
		}

		answers = append(answers, &dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   dns.Fqdn(q.Name),
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    uint32(ttl.Seconds()),
			},
			AAAA: ip,
		})
	}

	if len(answers) > 0 {
		var m dns.Msg
		m.SetReply(r)
		m.Authoritative = true
		m.Answer = answers
		w.WriteMsg(&m)
		return dns.RcodeSuccess, nil
	}

	// Call next plugin (if any).
	return plugin.NextOrFailure(p.Name(), p.Next, ctx, w, r)
}

// Name implements the Handler interface.
func (p ipParser) Name() string { return pluginName }
