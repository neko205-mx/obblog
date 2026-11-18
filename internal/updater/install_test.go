package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixture publishes one fake binary with valid signatures so each test can
// break exactly one thing.
type fixture struct {
	server  *httptest.Server
	release *Release
	dir     string
	pub     ed25519.PublicKey
}

func newFixture(t *testing.T, mutate func(*fixture)) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// A real ELF file so the architecture check passes on amd64/arm64.
	binary := fakeELF(t)
	sum := sha256.Sum256(binary)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + AssetName() + "\n")
	signature := ed25519.Sign(priv, sums)

	f := &fixture{dir: t.TempDir(), pub: pub}

	mux := http.NewServeMux()
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { w.Write(sums) })
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) { w.Write(signature) })
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	f.release = &Release{
		Tag:          "v1.1.0",
		BinaryURL:    f.server.URL + "/binary",
		BinaryName:   AssetName(),
		ChecksumsURL: f.server.URL + "/sums",
		SignatureURL: f.server.URL + "/sig",
	}
	if mutate != nil {
		mutate(f)
	}
	return f
}

func (f *fixture) installer() *Installer {
	return &Installer{
		Dir:       f.dir,
		Client:    f.server.Client(),
		PublicKey: base64.StdEncoding.EncodeToString(f.pub),
	}
}

// fakeELF builds a minimal ELF header for the host architecture. The verifier
// only reads e_ident and e_machine, so a full binary is not required.
func fakeELF(t *testing.T) []byte {
	t.Helper()
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	binary.LittleEndian.PutUint16(header[16:], 2)  // ET_EXEC
	binary.LittleEndian.PutUint32(header[20:], 1)  // EV_CURRENT
	binary.LittleEndian.PutUint16(header[52:], 64) // e_ehsize
	binary.LittleEndian.PutUint16(header[54:], 56) // e_phentsize
	binary.LittleEndian.PutUint16(header[58:], 64) // e_shentsize
	var machine uint16
	switch runtime.GOARCH {
	case "amd64":
		machine = 0x3e // EM_X86_64
	case "arm64":
		machine = 0xb7 // EM_AARCH64
	default:
		t.Skip("unsupported test architecture")
	}
	binary.LittleEndian.PutUint16(header[18:], machine)
	return header
}

func TestInstallStagesVerifiedBinary(t *testing.T) {
	f := newFixture(t, nil)
	path, err := f.installer().Install(context.Background(), f.release)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if filepath.Base(path) != "obblog" {
		t.Errorf("unexpected path: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("staged binary must be executable")
	}
	// The temp file must not linger.
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the staged binary, found %d entries", len(entries))
	}
}

// A tampered checksum file must be rejected before any binary is written.
func TestInstallRejectsBadSignature(t *testing.T) {
	f := newFixture(t, nil)
	// Replace the checksum body after signing, so the signature no longer
	// matches what is served.
	f.release.ChecksumsURL = f.server.URL + "/sums-tampered"
	mux := http.NewServeMux()
	mux.HandleFunc("/sums-tampered", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("0", 64) + "  " + AssetName() + "\n"))
	})
	tampered := httptest.NewServer(mux)
	t.Cleanup(tampered.Close)
	f.release.ChecksumsURL = tampered.URL + "/sums-tampered"
	f.release.SignatureURL = f.server.URL + "/sig"

	if _, err := f.installer().Install(context.Background(), f.release); err == nil {
		t.Fatal("tampered checksums must be rejected")
	}
	entries, _ := os.ReadDir(f.dir)
	if len(entries) != 0 {
		t.Error("nothing should be staged after a failed verification")
	}
}

// A binary that does not match the signed digest must be rejected even when the
// signature over the checksum file is valid.
func TestInstallRejectsChecksumMismatch(t *testing.T) {
	f := newFixture(t, nil)
	// Serve a different binary than the one the checksum describes.
	f.release.BinaryURL = f.server.URL + "/binary-other"
	mux := http.NewServeMux()
	mux.HandleFunc("/binary-other", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not the signed content"))
	})
	other := httptest.NewServer(mux)
	t.Cleanup(other.Close)
	f.release.BinaryURL = other.URL + "/binary-other"

	if _, err := f.installer().Install(context.Background(), f.release); err == nil {
		t.Fatal("checksum mismatch must be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(f.dir, "obblog")); statErr == nil {
		t.Error("a mismatched binary must never be staged")
	}
}

// An empty key disables installation rather than silently skipping checks.
func TestInstallRequiresPublicKey(t *testing.T) {
	f := newFixture(t, nil)
	installer := f.installer()
	installer.PublicKey = ""
	original := PublicKey
	PublicKey = ""
	defer func() { PublicKey = original }()

	if _, err := installer.Install(context.Background(), f.release); err == nil {
		t.Fatal("install must fail without a signing key")
	} else if !strings.Contains(err.Error(), "公钥") {
		t.Errorf("error should mention the missing key: %v", err)
	}
}

func TestInstallRejectsWrongArchitecture(t *testing.T) {
	f := newFixture(t, nil)
	// A well-formed ELF header for a foreign machine.
	foreign := make([]byte, 64)
	copy(foreign, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	binary.LittleEndian.PutUint16(foreign[16:], 2)    // ET_EXEC
	binary.LittleEndian.PutUint32(foreign[20:], 1)    // EV_CURRENT
	binary.LittleEndian.PutUint16(foreign[52:], 64)   // e_ehsize
	binary.LittleEndian.PutUint16(foreign[54:], 56)   // e_phentsize
	binary.LittleEndian.PutUint16(foreign[58:], 64)   // e_shentsize
	binary.LittleEndian.PutUint16(foreign[18:], 0x08) // EM_MIPS, never our target
	sum := sha256.Sum256(foreign)

	mux := http.NewServeMux()
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { w.Write(foreign) })
	mux.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + AssetName() + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + AssetName() + "\n")
	signature := ed25519.Sign(priv, sums)
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) { w.Write(signature) })

	f.release.BinaryURL = srv.URL + "/b"
	f.release.ChecksumsURL = srv.URL + "/s"
	f.release.SignatureURL = srv.URL + "/sig"
	installer := &Installer{Dir: f.dir, Client: srv.Client(), PublicKey: base64.StdEncoding.EncodeToString(pub)}

	if _, err := installer.Install(context.Background(), f.release); err == nil {
		t.Fatal("wrong architecture must be rejected")
	} else if !strings.Contains(err.Error(), "架构") {
		t.Errorf("error should mention architecture: %v", err)
	}
}

func TestInstallRejectsNonELF(t *testing.T) {
	f := newFixture(t, nil)
	junk := []byte("#!/bin/sh\necho hi\n")
	sum := sha256.Sum256(junk)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + AssetName() + "\n")
	signature := ed25519.Sign(priv, sums)

	mux := http.NewServeMux()
	mux.HandleFunc("/j", func(w http.ResponseWriter, r *http.Request) { w.Write(junk) })
	mux.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) { w.Write(sums) })
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) { w.Write(signature) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	installer := &Installer{Dir: f.dir, Client: srv.Client(), PublicKey: base64.StdEncoding.EncodeToString(pub)}
	release := &Release{Tag: "v2.0.0", BinaryURL: srv.URL + "/j", BinaryName: AssetName(), ChecksumsURL: srv.URL + "/s", SignatureURL: srv.URL + "/sig"}
	if _, err := installer.Install(context.Background(), release); err == nil {
		t.Fatal("a script must not be accepted as a binary")
	}
}

func TestExpectedSumParsesVariants(t *testing.T) {
	digest := strings.Repeat("a", 64)
	sums := []byte(
		"# comment line\n" +
			digest + "  obblog-linux-amd64\n" +
			strings.Repeat("b", 64) + " *obblog-linux-arm64\n")
	got, err := expectedSum(sums, "obblog-linux-arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("b", 64) {
		t.Errorf("got %s", got)
	}
	if _, err := expectedSum(sums, "missing"); err == nil {
		t.Error("missing entry must error")
	}
}

func TestParsePublicKeyAcceptsFormats(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(pub)
	if _, err := parsePublicKey(b64); err != nil {
		t.Errorf("base64 key rejected: %v", err)
	}
	if _, err := parsePublicKey(""); err == nil {
		t.Error("empty key must error")
	}
	if _, err := parsePublicKey("not-base64!!"); err == nil {
		t.Error("garbage key must error")
	}
	if _, err := parsePublicKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("short key must error")
	}
}

func TestVerifyRejectsWrongSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	message := []byte("payload")
	good := ed25519.Sign(priv, message)
	if !Verify(pub, message, good) {
		t.Fatal("valid signature rejected")
	}
	if Verify(pub, []byte("other"), good) {
		t.Error("signature must not verify a different message")
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if Verify(otherPub, message, good) {
		t.Error("signature must not verify under another key")
	}
}

func TestAssetName(t *testing.T) {
	want := fmt.Sprintf("obblog-%s-%s", runtime.GOOS, runtime.GOARCH)
	if AssetName() != want {
		t.Errorf("AssetName() = %s, want %s", AssetName(), want)
	}
}
