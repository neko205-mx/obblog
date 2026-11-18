package updater

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixture publishes one fake binary with a matching checksum so each test can
// break exactly one thing.
type fixture struct {
	server  *httptest.Server
	release *Release
	dir     string
}

func newFixture(t *testing.T, mutate func(*fixture)) *fixture {
	t.Helper()
	binary := fakeELF(t)
	sum := sha256.Sum256(binary)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + AssetName() + "\n")

	f := &fixture{dir: t.TempDir()}

	mux := http.NewServeMux()
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) { w.Write(binary) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { w.Write(sums) })
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	f.release = &Release{
		Tag:          "v1.1.0",
		BinaryURL:    f.server.URL + "/binary",
		BinaryName:   AssetName(),
		ChecksumsURL: f.server.URL + "/sums",
	}
	if mutate != nil {
		mutate(f)
	}
	return f
}

func (f *fixture) installer() *Installer {
	return &Installer{Dir: f.dir, Client: f.server.Client()}
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

// A binary that does not match the published digest must be rejected and
// nothing may be staged.
func TestInstallRejectsChecksumMismatch(t *testing.T) {
	f := newFixture(t, nil)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not the signed content"))
	}))
	t.Cleanup(other.Close)
	f.release.BinaryURL = other.URL + "/binary"

	if _, err := f.installer().Install(context.Background(), f.release); err == nil {
		t.Fatal("checksum mismatch must be rejected")
	} else if !strings.Contains(err.Error(), "校验和") {
		t.Errorf("error should mention the checksum: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(f.dir, "obblog")); statErr == nil {
		t.Error("a mismatched binary must never be staged")
	}
}

// A release missing its checksum file cannot be verified at all.
func TestInstallRequiresChecksums(t *testing.T) {
	f := newFixture(t, nil)
	f.release.ChecksumsURL = ""
	if _, err := f.installer().Install(context.Background(), f.release); err == nil {
		t.Fatal("a release without checksums must be rejected")
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

	f.release.BinaryURL = srv.URL + "/b"
	f.release.ChecksumsURL = srv.URL + "/s"
	installer := &Installer{Dir: f.dir, Client: srv.Client()}

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

	mux := http.NewServeMux()
	mux.HandleFunc("/j", func(w http.ResponseWriter, r *http.Request) { w.Write(junk) })
	mux.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + AssetName() + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	installer := &Installer{Dir: f.dir, Client: srv.Client()}
	release := &Release{Tag: "v2.0.0", BinaryURL: srv.URL + "/j", BinaryName: AssetName(), ChecksumsURL: srv.URL + "/s"}
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
	if _, err := expectedSum([]byte("short  name\n"), "name"); err == nil {
		t.Error("malformed digest must error")
	}
}
