package tui

import (
	"sync"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type tuiTestSecretBackend struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (b *tuiTestSecretBackend) Get(namespace, name string) ([]byte, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	value, ok := b.values[namespace+"/"+name]
	return append([]byte(nil), value...), ok, nil
}
func (b *tuiTestSecretBackend) Put(namespace, name string, value []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.values == nil {
		b.values = map[string][]byte{}
	}
	b.values[namespace+"/"+name] = append([]byte(nil), value...)
	return nil
}
func (b *tuiTestSecretBackend) Delete(namespace, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.values, namespace+"/"+name)
	return nil
}

func attachTestSecretBackend(store interface{ SetSecretBackend(state.SecretBackend) }) {
	store.SetSecretBackend(&tuiTestSecretBackend{values: map[string][]byte{}})
}
