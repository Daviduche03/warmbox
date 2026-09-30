// Pool keeps a warm set of pre-booted microVMs so that acquiring a
// desktop skips the cold-boot cost entirely.
package desktop

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// Pool maintains PoolSize idle, ready VMs.
type Pool struct {
	mgr  *Manager
	size int
	// idleTimeout, when > 0, reclaims idle VMs once the pool has gone untouched
	// for this long. A warm VM holds RAM, so after a quiet spell it is worth
	// dropping it and paying a cold boot on the next create.
	idleTimeout time.Duration
	log         io.Writer

	mu      sync.Mutex
	idle    []*VM
	pending int
	// touched is the last time a lease was taken or released.
	touched time.Time
	// drained records that idle VMs were reclaimed for inactivity, so
	// replenishing stays off until the next Acquire.
	drained bool
}

// NewPool creates a warm pool over the given manager. An idleTimeout of 0 keeps
// the pool warm indefinitely.
func NewPool(mgr *Manager, size int, idleTimeout time.Duration, log io.Writer) *Pool {
	if log == nil {
		log = io.Discard
	}
	if size < 0 {
		size = 0
	}
	if idleTimeout < 0 {
		idleTimeout = 0
	}
	return &Pool{
		mgr:         mgr,
		size:        size,
		idleTimeout: idleTimeout,
		log:         log,
		touched:     time.Now(),
	}
}

// Run maintains the pool until ctx is cancelled. It is meant to run in a
// goroutine.
func (p *Pool) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.replenish(ctx)
		}
	}
}

func (p *Pool) replenish(ctx context.Context) {
	// Reclaim idle VMs once the pool has been quiet for a while. They exist to
	// make creates instant; holding their RAM through a long idle stretch (or
	// overnight) is the wrong trade, so drop them and boot on demand instead.
	var drained []*VM
	p.mu.Lock()
	if p.idleTimeout > 0 && !p.drained && len(p.idle) > 0 && time.Since(p.touched) > p.idleTimeout {
		drained, p.idle = p.idle, nil
		p.drained = true
	}
	need := 0
	if !p.drained {
		need = p.size - len(p.idle) - p.pending
		if need > 0 {
			p.pending += need
		}
	}
	p.mu.Unlock()

	for _, vm := range drained {
		fmt.Fprintf(p.log, "pool: draining idle vm %s (%s without a lease; reclaiming RAM)\n", vm.ID, p.idleTimeout)
		_ = p.mgr.Destroy(vm.ID)
	}

	for i := 0; i < need; i++ {
		go func() {
			vm, err := p.bootOne(ctx)
			p.mu.Lock()
			p.pending--
			if err == nil {
				p.idle = append(p.idle, vm)
				// A VM finishing its boot is the pool's last change: start
				// (or restart) the idle clock from here.
				p.touched = time.Now()
			}
			p.mu.Unlock()
			if err != nil {
				fmt.Fprintf(p.log, "pool: boot failed: %v\n", err)
			}
		}()
	}
}

func (p *Pool) bootOne(ctx context.Context) (*VM, error) {
	id := NewID()
	vm, err := p.mgr.Start(id)
	if err != nil {
		return nil, err
	}
	// Bound the cold-boot wait.
	bootCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := p.mgr.WaitReady(bootCtx, id); err != nil {
		_ = p.mgr.Destroy(id)
		return nil, err
	}
	fmt.Fprintf(p.log, "pool: warmed vm %s\n", id)
	return vm, nil
}

// Acquire returns a ready VM, booting one on demand if the pool is empty.
func (p *Pool) Acquire(ctx context.Context) (*VM, error) {
	for {
		p.mu.Lock()
		// A lease is activity: wake the pool back up.
		p.touched = time.Now()
		p.drained = false
		if n := len(p.idle); n > 0 {
			vm := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.mu.Unlock()
			vm.SetState(StateBusy)
			fmt.Fprintf(p.log, "pool: leased vm %s\n", vm.ID)
			return vm, nil
		}
		p.mu.Unlock()

		// Empty pool: boot one synchronously rather than block forever.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		vm, err := p.bootOne(ctx)
		if err == nil {
			vm.SetState(StateBusy)
			return vm, nil
		}
		// Back off and retry unless the context is done.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Release destroys a leased VM. The Run loop replenishes the pool.
func (p *Pool) Release(vm *VM) error {
	if vm == nil {
		return nil
	}
	p.mu.Lock()
	p.touched = time.Now()
	p.mu.Unlock()
	return p.mgr.Destroy(vm.ID)
}

// Stats reports current pool sizes.
func (p *Pool) Stats() (idle, pending int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle), p.pending
}
