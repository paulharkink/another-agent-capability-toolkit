package vault

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	keepass "github.com/tobischo/gokeepasslib/v3"
)

func TestStoreRoundTripsKDBXAndRejectsWrongPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.kdbx")
	master := []byte("test-only-master-password")
	store, err := Open(path, master)
	if err != nil {
		t.Fatal(err)
	}
	namespace := strings.Repeat("a", 64)
	if err := store.Put(namespace, "service-token", []byte("synthetic-secret-value")); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, master)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := reopened.Get(namespace, "service-token")
	if err != nil || !found || !bytes.Equal(got, []byte("synthetic-secret-value")) {
		t.Fatalf("Get() = %q, %v, %v", got, found, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte("synthetic-secret-value")) || bytes.Contains(contents, master) {
		t.Fatal("vault file contains a plaintext secret")
	}
	if _, err := Open(path, []byte("wrong-test-password")); err == nil {
		t.Fatal("opening the vault with a wrong master password succeeded")
	}
}

func TestStoreCRUDAndPrivateFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "vault.kdbx")
	store, err := Open(path, []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	namespace := strings.Repeat("c", 64)
	if got, found, err := store.Get(namespace, "missing"); err != nil || found || got != nil {
		t.Fatalf("missing Get() = %q, %v, %v", got, found, err)
	}
	if got, err := store.List(namespace); err != nil || len(got) != 0 {
		t.Fatalf("empty List() = %#v, %v", got, err)
	}
	if err := store.Put(namespace, "token", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(namespace, "token", []byte("updated")); err != nil {
		t.Fatal(err)
	}
	values, err := store.List(namespace)
	if err != nil || len(values) != 1 || string(values["token"]) != "updated" {
		t.Fatalf("List() after update = %#v, %v", values, err)
	}
	if err := store.Delete(namespace, "token"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(namespace, "token"); err != nil || found {
		t.Fatalf("Get() after delete found=%v err=%v", found, err)
	}
	if err := store.Delete(namespace, "token"); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0600 {
		t.Fatalf("vault mode = %04o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("vault directory mode = %04o, want 0700", got)
	}
}

func TestStoreUsesKDBX41AndExplicitArgon2Parameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.kdbx")
	master := []byte("test-only-master-password")
	if _, err := Open(path, master); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db := keepass.NewDatabase()
	db.Credentials = keepass.NewPasswordCredentials(string(master))
	err = keepass.NewDecoder(file).Decode(db)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("decode generated database: decode=%v close=%v", err, closeErr)
	}
	if !db.Header.IsKdbx41() {
		t.Fatal("database header is not KDBX 4.1")
	}
	kdf := db.Header.FileHeaders.KdfParameters
	if !bytes.Equal(kdf.UUID, keepass.KdfArgon2) || kdf.Memory < 64<<20 || kdf.Iterations < 3 || kdf.Parallelism < 2 {
		t.Fatalf("KDF parameters are too weak or unexpected: %#v", kdf)
	}
}

func TestStoreRejectsInvalidNamespaceAndName(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "vault.kdbx"), []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("not-an-instance-id", "token", []byte("value")); err == nil {
		t.Fatal("invalid namespace was accepted")
	}
	if err := store.Put(strings.Repeat("g", 64), "token", []byte("value")); err == nil {
		t.Fatal("non-hex namespace was accepted")
	}
	if err := store.Put(strings.Repeat("a", 64), "", []byte("value")); err == nil {
		t.Fatal("empty secret name was accepted")
	}
}

func TestStoreIsolatesNamespaces(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	namespaceA := strings.Repeat("a", 64)
	namespaceB := strings.Repeat("b", 64)
	if err := store.Put(namespaceA, "token", []byte("a-value")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(namespaceB, "token", []byte("b-value")); err != nil {
		t.Fatal(err)
	}
	gotA, foundA, err := store.Get(namespaceA, "token")
	if err != nil || !foundA || string(gotA) != "a-value" {
		t.Fatalf("namespace A Get() = %q, %v, %v", gotA, foundA, err)
	}
	gotB, foundB, err := store.Get(namespaceB, "token")
	if err != nil || !foundB || string(gotB) != "b-value" {
		t.Fatalf("namespace B Get() = %q, %v, %v", gotB, foundB, err)
	}
	listedA, err := store.List(namespaceA)
	if err != nil || len(listedA) != 1 || string(listedA["token"]) != "a-value" {
		t.Fatalf("namespace A List() = %#v, %v", listedA, err)
	}
}

func BenchmarkStoreOpenWithArgon2(b *testing.B) {
	for i := 0; i < b.N; i++ {
		path := filepath.Join(b.TempDir(), "credentials.kdbx")
		store, err := Open(path, []byte("benchmark-only-master-password"))
		if err != nil {
			b.Fatal(err)
		}
		if err := store.Put("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "key", []byte("value")); err != nil {
			b.Fatal(err)
		}
	}
}

func TestIndependentHandlesPreserveUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.kdbx")
	first, err := Open(path, []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path, []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	ns := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := first.Put(ns, "first", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := second.Put(ns, "second", []byte("two")); err != nil {
		t.Fatal(err)
	}
	fresh, err := Open(path, []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := fresh.List(ns)
	if err != nil {
		t.Fatal(err)
	}
	if string(values["first"]) != "one" || string(values["second"]) != "two" {
		t.Fatalf("updates lost across handles: %#v", values)
	}
}

func TestNamespaceRequiresCanonicalLowercaseID(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "key", []byte("value")); err == nil {
		t.Fatal("uppercase namespace accepted")
	}
}

func TestKeePassXCOpensGeneratedKDBX(t *testing.T) {
	cli, err := exec.LookPath("keepassxc-cli")
	if err != nil {
		t.Skip("keepassxc-cli is not installed")
	}
	path := filepath.Join(t.TempDir(), "credentials.kdbx")
	store, err := Open(path, []byte("interoperability-test-password"))
	if err != nil {
		t.Fatal(err)
	}
	ns := strings.Repeat("d", 64)
	if err := store.Put(ns, "token", []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "ls", "-q", path, "/"+ns)
	cmd.Stdin = strings.NewReader("interoperability-test-password\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("KeePassXC could not open KDBX: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "token") {
		t.Fatalf("KeePassXC group listing = %q", output)
	}
}

func TestStoreFailsClosedWhenAnotherVaultOwnerHoldsFileLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.kdbx")
	store, err := Open(path, []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockVaultFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, _, err := store.Get(strings.Repeat("a", 64), "key"); err == nil || !strings.Contains(err.Error(), "another AACT process") {
		t.Fatalf("Get under conflicting owner err=%v", err)
	}
}

func TestDuplicateTitlesUseConsistentFirstValueAndNormalizeOnWrite(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	ns := strings.Repeat("e", 64)
	if err := store.Put(ns, "token", []byte("first")); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	if err := store.db.UnlockProtectedEntries(); err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	group := store.findNamespaceLocked(ns)
	duplicate := keepass.NewEntry()
	duplicate.Values = append(duplicate.Values, keepass.ValueData{Key: "Title", Value: keepass.V{Content: "token"}})
	setEntryPassword(&duplicate, "c2Vjb25k")
	group.Entries = append(group.Entries, duplicate)
	if err := store.db.LockProtectedEntries(); err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	if err := store.persistLocked(); err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	store.mu.Unlock()
	value, found, err := store.Get(ns, "token")
	if err != nil || !found || string(value) != "first" {
		t.Fatalf("Get duplicate=%q,%v,%v", value, found, err)
	}
	listed, err := store.List(ns)
	if err != nil || string(listed["token"]) != "first" {
		t.Fatalf("List duplicate=%#v,%v", listed, err)
	}
	if err := store.Put(ns, "token", []byte("updated")); err != nil {
		t.Fatal(err)
	}
	listed, err = store.List(ns)
	if err != nil || len(listed) != 1 || string(listed["token"]) != "updated" {
		t.Fatalf("List after normalization=%#v,%v", listed, err)
	}
	if err := store.Delete(ns, "token"); err != nil {
		t.Fatal(err)
	}
	listed, err = store.List(ns)
	if err != nil || len(listed) != 0 {
		t.Fatalf("List after delete=%#v,%v", listed, err)
	}
}

func TestOpenSecuresExistingParentDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "permissive")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "credentials.kdbx")
	if _, err := Open(path, []byte("test-only-master-password")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, []byte("test-only-master-password")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("vault parent mode=%04o, want 0700", got)
	}
}
