package dns

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
)

// Provider is a store for DNS resolved addresses. It provides a way to resolve addresses and obtain them.
type Provider struct {
	sync.Mutex
	resolver Resolver
	// A map from domain name to a slice of resolved targets.
	resolved map[string][]string
	logger   log.Logger
}

var (
	dnsResolveLookupsCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "thanos_sd_dns_lookup_total",
		Help: "The number of lookups using DNS resolution",
	})
	dnsResolveFailuresCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "thanos_sd_dns_failures_total",
		Help: "The number of DNS SD lookup failures",
	})
)

func init() {
	prometheus.MustRegister(dnsResolveLookupsCount)
	prometheus.MustRegister(dnsResolveFailuresCount)
}

// NewProviderWithResolver returns a new empty provider with a default resolver.
func NewProviderWithResolver(logger log.Logger) *Provider {
	return NewProvider(nil, logger)
}

// NewProvider returns a new empty Provider. If resolver is nil, the default resolver will be used.
func NewProvider(resolver Resolver, logger log.Logger) *Provider {
	if resolver == nil {
		resolver = NewResolver(nil)
	}
	return &Provider{
		resolver: resolver,
		resolved: make(map[string][]string),
		logger:   logger,
	}
}

// Resolve stores a list of provided addresses or their DNS records if requested.
// Addresses prefixed with `dns+` or `dnssrv+` will be resolved through respective DNS lookup (A/AAAA or SRV).
// defaultPort is used for non-SRV records when a port is not supplied.
func (p *Provider) Resolve(ctx context.Context, addrs []string) error {
	p.Lock()
	defer p.Unlock()

	for _, addr := range addrs {
		var resolvedHosts []string
		qtypeAndName := strings.SplitN(addr, "+", 2)
		if len(qtypeAndName) != 2 {
			// No lookup specified. Add to results and continue to the next address.
			p.resolved[addr] = []string{addr}
			continue
		}
		qtype, name := qtypeAndName[0], qtypeAndName[1]

		resolvedHosts, err := p.resolver.Resolve(ctx, name, qtype)
		dnsResolveLookupsCount.Inc()
		if err != nil {
			// The DNS resolution failed. Continue without modifying the old records.
			dnsResolveFailuresCount.Inc()
			level.Error(p.logger).Log("msg", fmt.Sprintf("dns resolution failed for %v", addr), "err", err)
			continue
		}
		p.resolved[addr] = resolvedHosts
	}

	// Remove stored addresses that are no longer requested.
	var entriesToDelete []string
	for existingAddr := range p.resolved {
		if !contains(addrs, existingAddr) {
			entriesToDelete = append(entriesToDelete, existingAddr)
		}
	}
	for _, toDelete := range entriesToDelete {
		delete(p.resolved, toDelete)
	}

	return nil
}

// Addresses returns the latest addresses present in the Provider.
func (p *Provider) Addresses() []string {
	p.Lock()
	defer p.Unlock()
	var result []string
	for _, addrs := range p.resolved {
		result = append(result, addrs...)
	}
	return result
}

func contains(slice []string, str string) bool {
	for _, s := range slice {
		if str == s {
			return true
		}
	}
	return false
}
