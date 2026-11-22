package main

import (
	"sync"

	"github.com/dtthaison/muse-mcp/internal/accounts"
	"github.com/dtthaison/muse-mcp/internal/tools"
)

// pool is the MCP server's tools.ServiceSource: it builds one browser and
// muse service per account on first use, and keeps them until Close.
// accounts.json is read on every call, so an account added with
// "muse-mcp login --account NAME --browser SPEC" works without a restart.
type pool struct {
	store *accounts.Store
	// build makes the service for an account, and a func that shuts it down.
	build func(name string, acc accounts.Account) (tools.Service, func(), error)

	mu      sync.Mutex
	svcs    map[string]tools.Service
	closers []func()
}

func (p *pool) Service(account string) (tools.Service, error) {
	f, err := p.store.Load()
	if err != nil {
		return nil, err
	}
	name, acc, err := f.Resolve(account)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if svc, ok := p.svcs[name]; ok {
		return svc, nil
	}
	svc, closeFn, err := p.build(name, acc)
	if err != nil {
		return nil, err
	}
	if p.svcs == nil {
		p.svcs = map[string]tools.Service{}
	}
	p.svcs[name] = svc
	if closeFn != nil {
		p.closers = append(p.closers, closeFn)
	}
	return svc, nil
}

// Close shuts down every service the pool built.
func (p *pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.closers {
		c()
	}
	p.closers, p.svcs = nil, nil
}
