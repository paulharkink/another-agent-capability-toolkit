//go:build integration

package integration

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGrafanaBrowserAdapterAgainstHTTPSLogin(t *testing.T) {
	if os.Getenv("AACT_RUN_BROWSER_DOCKER_TEST") != "1" {
		t.Skip("set AACT_RUN_BROWSER_DOCKER_TEST=1")
	}
	image := os.Getenv("AACT_BROWSER_HELPER_IMAGE")
	if image == "" {
		image = "aact/browser-authentication-helper:1.18.0-form1"
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AACT synthetic browser fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true, DNSNames: []string{"host.docker.internal", "login.example.test"}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	target := fmt.Sprintf("https://host.docker.internal:%d", port)
	login := fmt.Sprintf("https://login.example.test:%d", port)
	var submissions atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "login.example.test:") {
			if r.Method == http.MethodPost {
				submissions.Add(1)
				_ = r.ParseForm()
				if r.Form.Get("username") != "synthetic-user" || r.Form.Get("password") != "synthetic-password" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, target+"/finish", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<form method="POST"><input id="user" name="username"><input id="password" name="password" type="password"><button id="submit" type="submit">Login</button></form>`)
			return
		}
		if r.URL.Path == "/finish" {
			http.SetCookie(w, &http.Cookie{Name: "grafana_session", Value: "synthetic-session", Path: "/", Secure: true, HttpOnly: true})
			http.SetCookie(w, &http.Cookie{Name: "grafana_session_expiry", Value: "4102444800", Path: "/", Secure: true})
			http.SetCookie(w, &http.Cookie{Name: "grafana_refresh", Value: "synthetic-refresh", Path: "/", Secure: true, HttpOnly: true})
			fmt.Fprint(w, "signed in")
			return
		}
		http.Redirect(w, r, login+"/login", http.StatusFound)
	})
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	name := fmt.Sprintf("aact-test-grafana-form-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	command := exec.Command("docker", "run", "-d", "--rm", "--name", name, "--add-host", "login.example.test:host-gateway", "--add-host", "host.docker.internal:host-gateway", "--shm-size=512m", image)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start fixture browser: %v %s", err, out)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "packages", "grafana-inspector", "browser_login.py"))
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(loginOrigin, password string, pending bool) map[string]any {
		t.Helper()
		body := map[string]any{"profile_path": "/home/kasm-user/.config/chromium/Default", "origin": target, "selector": map[string]any{"kind": "none"}, "adapter": map[string]any{"source": string(source), "config": map[string]any{"url": target, "login_origin": loginOrigin, "ca_pem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), "username": "synthetic-user", "username_selector": "#user", "password_selector": "#password", "submit_selector": "#submit", "refresh_cookie_name": "grafana_refresh", "interaction_pending": pending, "__private": map[string]string{"password": password}}}}
		raw, _ := json.Marshal(body)
		cmd := exec.Command("docker", "exec", "-i", name, "browser-authentication-helper", "run-adapter")
		cmd.Stdin = bytes.NewReader(raw)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("browser adapter: %v %s", err, out)
		}
		if bytes.Contains(out, []byte(password)) {
			t.Fatal("browser adapter disclosed the password")
		}
		var result map[string]any
		if err = json.Unmarshal(out, &result); err != nil {
			t.Fatalf("invalid response: %s", out)
		}
		return result
	}
	// Wait for the browser listener; the adapter itself has a bounded startup retry.
	result := invoke("https://unexpected.example.test", "synthetic-password", false)
	if result["status"] != "interaction_required" || submissions.Load() != 0 {
		t.Fatalf("unexpected origin submitted credentials: %v", result)
	}
	result = invoke(login, "synthetic-password", false)
	if result["status"] != "refreshed" || result["grafana_session"] != "synthetic-session" || result["oauth_refresh"] != "synthetic-refresh" || submissions.Load() != 1 {
		t.Fatalf("login failed: %v, submissions=%d", result, submissions.Load())
	}
	result = invoke(login, "synthetic-wrong-password", false)
	if result["status"] != "interaction_required" || submissions.Load() != 2 {
		t.Fatalf("failed login was not exposed for interaction: %v", result)
	}
	result = invoke(login, "synthetic-wrong-password", true)
	if result["status"] != "interaction_required" || submissions.Load() != 2 {
		t.Fatalf("pending interaction resubmitted credentials: %v", result)
	}
}
