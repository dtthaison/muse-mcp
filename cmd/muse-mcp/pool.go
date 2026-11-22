package main

import (
	"errors"
	"reflect"
	"sync"

	"github.com/dtthaison/muse-mcp/internal/accounts"
	"github.com/dtthaison/muse-mcp/internal/tools"
)

// errPoolClosed is what Service answers once the pool has been closed (the
// server is shutting down; a request still in flight must not start a browser
// nobody would stop).
var errPoolClosed = errors.New("muse-mcp is shutting down")

// pool is the MCP server's tools.ServiceSource: it builds one browser and
// muse service per account on first use, and keeps them until Close.
// accounts.json is read on every call, so an account added with
// "muse-mcp login --account NAME --browser SPEC" works without a restart, and
// an account whose browser was changed there gets a new service (the old one is
// closed first: both would want the same account lock).
type pool struct {
	store *accounts.Store
	// build makes the service for an account, and a func that shuts it down.
	build func(name string, acc accounts.Account) (tools.Service, func(), error)
	// fallback, if set, answers for an account whose build failed (Chrome or the
	// provider is not installed, a bad spec). It is never cached, so the next
	// call builds again and picks up a fix; without it Service returns the error.
	fallback func(name string, err error) tools.Service

	mu     sync.Mutex
	closed bool
	svcs   map[string]*poolEntry
}

// poolEntry is a built service and the account settings it was built from.
type poolEntry struct {
	svc     tools.Service
	acc     accounts.Account
	closeFn func()
}

func (e *poolEntry) close() {
	if e.closeFn != nil {
		e.closeFn()
	}
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
	if p.closed {
		return nil, errPoolClosed
	}
	if e, ok := p.svcs[name]; ok {
		if reflect.DeepEqual(e.acc, acc) {
			return e.svc, nil
		}
		e.close() // the account now uses another browser
		delete(p.svcs, name)
	}
	svc, closeFn, err := p.build(name, acc)
	if err != nil {
		if p.fallback == nil {
			return nil, err
		}
		return p.fallback(name, err), nil
	}
	if p.svcs == nil {
		p.svcs = map[string]*poolEntry{}
	}
	p.svcs[name] = &poolEntry{svc: svc, acc: acc, closeFn: closeFn}
	return svc, nil
}

// Close shuts down every service the pool built. Service fails afterwards.
func (p *pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, e := range p.svcs {
		e.close()
	}
	p.svcs = nil
}
