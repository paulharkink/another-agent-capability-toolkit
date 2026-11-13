package vault

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const maxAPIRequestBytes = 1 << 20

var tokenRegistryNamespace = func() string {
	sum := sha256.Sum256([]byte("AACT internal vault token registry"))
	return hex.EncodeToString(sum[:])
}()

var daemonSecretNamespace = func() string {
	sum := sha256.Sum256([]byte("AACT internal vault daemon secrets"))
	return hex.EncodeToString(sum[:])
}()

const daemonControlSecretName = "daemon-control-token"
const daemonTLSKeySecretName = "daemon-tls-private-key"
const daemonTLSCertSecretName = "daemon-tls-certificate"
const daemonTLSKeyPairSecretName = "daemon-tls-key-pair"
const privateInputEnvelopeSecretName = "aact-private-inputs-v1"

// API exposes the vault to containers through per-instance bearer tokens.
// Tokens and their container ownership are persisted only inside the encrypted
// vault so they can be revoked after an AACT process restart.
type API struct {
	mu                 sync.RWMutex
	store              *Store
	tokens             map[string]string              // bearer token -> capability-instance ID
	owners             map[string]string              // bearer token -> container ID
	privateInputScopes map[string]map[string]struct{} // bearer token -> readable private input names
}

func (a *API) RevokeTokensForInstance(namespace string) error {
	if err := validateInstanceNamespace(namespace); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var result error
	for token, boundNamespace := range a.tokens {
		if boundNamespace != namespace {
			continue
		}
		if err := a.store.Delete(tokenRegistryNamespace, token); err != nil {
			result = errors.Join(result, fmt.Errorf("remove persisted token: %w", err))
			continue
		}
		delete(a.tokens, token)
		delete(a.owners, token)
		delete(a.privateInputScopes, token)
	}
	return result
}

func (a *API) BindTokenContainer(token, containerID string) error {
	if containerID == "" || strings.Contains(containerID, "/") {
		return errors.New("invalid container ID")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	namespace, ok := a.tokens[token]
	if !ok {
		return errors.New("vault token not found")
	}
	if err := a.persistTokenLocked(token, namespace, containerID); err != nil {
		return err
	}
	a.owners[token] = containerID
	return nil
}

func (a *API) RevokeTokensForContainer(namespace, containerID string) error {
	if err := validateInstanceNamespace(namespace); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var result error
	for token, owner := range a.owners {
		if owner != containerID || a.tokens[token] != namespace {
			continue
		}
		if err := a.store.Delete(tokenRegistryNamespace, token); err != nil {
			result = errors.Join(result, fmt.Errorf("remove persisted container token: %w", err))
			continue
		}
		delete(a.tokens, token)
		delete(a.owners, token)
		delete(a.privateInputScopes, token)
	}
	return result
}

// RevokeTokensByContainerID revokes all tokens bound to one exact Docker ID.
// It supports stale-runtime cleanup when Docker metadata no longer provides
// the namespace label needed by the namespace-qualified operation.
func (a *API) RevokeTokensByContainerID(containerID string) error {
	if containerID == "" || strings.Contains(containerID, "/") {
		return errors.New("invalid container ID")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var result error
	for token, owner := range a.owners {
		if owner != containerID {
			continue
		}
		if err := a.store.Delete(tokenRegistryNamespace, token); err != nil {
			result = errors.Join(result, fmt.Errorf("remove persisted container token: %w", err))
			continue
		}
		delete(a.tokens, token)
		delete(a.owners, token)
		delete(a.privateInputScopes, token)
	}
	return result
}

// IssueToken creates an unpersisted random bearer token for one instance.
func (a *API) IssueToken(namespace string) (string, error) {
	if err := validateInstanceNamespace(namespace); err != nil {
		return "", err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.persistTokenLocked(token, namespace, ""); err != nil {
		return "", fmt.Errorf("persist container vault token: %w", err)
	}
	a.tokens[token] = namespace
	return token, nil
}

// IssuePrivateInputToken issues a read-only token that can retrieve only the
// named values from AACT's private-input envelope in one instance namespace.
func (a *API) IssuePrivateInputToken(namespace string, names []string) (string, error) {
	if err := validateInstanceNamespace(namespace); err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", errors.New("at least one private input name is required")
	}
	scope := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" || strings.ContainsAny(name, "/\\\r\n\x00") {
			return "", errors.New("invalid private input name")
		}
		scope[name] = struct{}{}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.persistPrivateInputScopeLocked(token, namespace, scope); err != nil {
		return "", fmt.Errorf("persist scoped container vault policy: %w", err)
	}
	a.tokens[token] = namespace
	a.privateInputScopes[token] = scope
	return token, nil
}

func (a *API) persistTokenLocked(token, namespace, containerID string) error {
	encoded, err := json.Marshal(struct {
		Namespace          string   `json:"namespace"`
		ContainerID        string   `json:"container_id,omitempty"`
		PrivateInputAccess []string `json:"private_input_access,omitempty"`
	}{namespace, containerID, sortedPrivateInputNames(a.privateInputScopes[token])})
	if err != nil {
		return err
	}
	return a.store.Put(tokenRegistryNamespace, token, encoded)
}

func (a *API) persistPrivateInputScopeLocked(token, namespace string, scope map[string]struct{}) error {
	encoded, err := json.Marshal(struct {
		Namespace          string   `json:"namespace"`
		PrivateInputAccess []string `json:"private_input_access"`
	}{namespace, sortedPrivateInputNames(scope)})
	if err != nil {
		return err
	}
	return a.store.Put(tokenRegistryNamespace, token, encoded)
}

func sortedPrivateInputNames(scope map[string]struct{}) []string {
	if len(scope) == 0 {
		return nil
	}
	names := make([]string, 0, len(scope))
	for name := range scope {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *API) RevokeToken(token string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.tokens[token]; !ok {
		return errors.New("vault token not found")
	}
	if err := a.store.Delete(tokenRegistryNamespace, token); err != nil {
		return fmt.Errorf("remove container vault token: %w", err)
	}
	delete(a.tokens, token)
	delete(a.owners, token)
	delete(a.privateInputScopes, token)
	return nil
}

// LoadTokens restores tokens from their encrypted internal vault namespace.
func (a *API) LoadTokens() error {
	values, err := a.store.List(tokenRegistryNamespace)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, rawNamespace := range values {
		var record struct {
			Namespace          string   `json:"namespace"`
			ContainerID        string   `json:"container_id"`
			PrivateInputAccess []string `json:"private_input_access"`
		}
		namespace := string(rawNamespace)
		if json.Unmarshal(rawNamespace, &record) == nil && record.Namespace != "" {
			namespace = record.Namespace
		}
		if err := validateInstanceNamespace(namespace); err != nil {
			return fmt.Errorf("invalid persisted vault token namespace: %w", err)
		}
		a.tokens[token] = namespace
		if len(record.PrivateInputAccess) > 0 {
			scope := make(map[string]struct{}, len(record.PrivateInputAccess))
			for _, name := range record.PrivateInputAccess {
				if name == "" || strings.ContainsAny(name, "/\\\r\n\x00") {
					return fmt.Errorf("invalid persisted private input access name")
				}
				scope[name] = struct{}{}
			}
			a.privateInputScopes[token] = scope
		}
		if record.ContainerID != "" {
			a.owners[token] = record.ContainerID
		}
	}
	return nil
}

func NewAPI(store *Store, tokens map[string]string) *API {
	copyTokens := make(map[string]string, len(tokens))
	for token, namespace := range tokens {
		copyTokens[token] = namespace
	}
	return &API{store: store, tokens: copyTokens, owners: map[string]string{}, privateInputScopes: map[string]map[string]struct{}{}}
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.store == nil {
		http.Error(w, "vault unavailable", http.StatusServiceUnavailable)
		return
	}
	namespace, token, ok := a.authenticateToken(r.Header.Get("Authorization"))
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/private-inputs/") {
		a.servePrivateInput(w, r, namespace, token)
		return
	}
	a.mu.RLock()
	_, restricted := a.privateInputScopes[token]
	a.mu.RUnlock()
	if restricted && strings.HasPrefix(r.URL.Path, "/v1/secrets") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/v1/secrets" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		values, err := a.store.List(namespace)
		if err != nil {
			http.Error(w, "vault operation failed", http.StatusInternalServerError)
			return
		}
		result := make(map[string]string, len(values))
		for key, value := range values {
			result[key] = string(value)
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	const prefix = "/v1/secrets/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, prefix)
	if name == "" || strings.Contains(name, "/") {
		http.Error(w, "invalid secret name", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, found, err := a.store.Get(namespace, name)
		if err != nil {
			http.Error(w, "vault operation failed", http.StatusBadRequest)
			return
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"value": string(value)})
	case http.MethodPut:
		reader := http.MaxBytesReader(w, r.Body, maxAPIRequestBytes)
		defer reader.Close()
		var body struct {
			Value string `json:"value"`
		}
		decoder := json.NewDecoder(reader)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := a.store.Put(namespace, name, []byte(body.Value)); err != nil {
			http.Error(w, "vault operation failed", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := a.store.Delete(namespace, name); err != nil {
			http.Error(w, "vault operation failed", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) servePrivateInput(w http.ResponseWriter, r *http.Request, namespace, token string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/v1/private-inputs/")
	if name == "" || strings.Contains(name, "/") {
		http.Error(w, "invalid private input name", http.StatusBadRequest)
		return
	}
	a.mu.RLock()
	scope, restricted := a.privateInputScopes[token]
	_, allowed := scope[name]
	a.mu.RUnlock()
	if !restricted || !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	encoded, found, err := a.store.Get(namespace, privateInputEnvelopeSecretName)
	if err != nil {
		http.Error(w, "private input unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	var record struct {
		Version int            `json:"version"`
		Values  map[string]any `json:"values"`
	}
	if err := json.Unmarshal(encoded, &record); err != nil || record.Version != 1 {
		http.Error(w, "private input unavailable", http.StatusInternalServerError)
		return
	}
	value, ok := record.Values[name].(string)
	if !ok || value == "" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": value})
}

func (a *API) authenticate(header string) (string, bool) {
	namespace, _, ok := a.authenticateToken(header)
	return namespace, ok
}

func (a *API) authenticateToken(header string) (string, string, bool) {
	if !strings.HasPrefix(header, "Bearer ") {
		return "", "", false
	}
	provided := strings.TrimPrefix(header, "Bearer ")
	if provided == "" {
		return "", "", false
	}
	matched := ""
	matchedToken := ""
	valid := 0
	a.mu.RLock()
	defer a.mu.RUnlock()
	for token, namespace := range a.tokens {
		equal := subtle.ConstantTimeCompare([]byte(provided), []byte(token))
		valid |= equal
		if equal == 1 {
			matched = namespace
			matchedToken = token
		}
	}
	if valid != 1 || validateInstanceNamespace(matched) != nil {
		return "", "", false
	}
	return matched, matchedToken, true
}

func validateInstanceNamespace(namespace string) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if namespace == tokenRegistryNamespace || namespace == daemonSecretNamespace {
		return errors.New("reserved vault namespace")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
