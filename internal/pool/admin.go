package pool

import (
	"errors"
	"workbuddy2api/internal/auth"
)

// RemoveManaged requires a disabled, drained account and successful file deletion.
func (p *Pool) RemoveManaged(uid string, expected *auth.Auth) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.byUID[uid]
	if !ok || e.a != expected {
		return errors.New("account changed")
	}
	if !e.disabled || e.inFlight.Load() != 0 {
		return errors.New("account must be disabled and drained")
	}
	if err := e.a.DeleteCredential(); err != nil {
		return err
	}
	delete(p.byUID, uid)
	p.dirty.Store(true)
	p.saveLocked()
	return nil
}
